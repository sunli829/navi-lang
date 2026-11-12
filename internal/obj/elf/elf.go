// Package elf writes ELF64 relocatable object files for x86-64 and
// AArch64 Linux.
package elf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"navi/internal/obj"
)

var sectNames = [...]string{
	obj.Text:      ".text",
	obj.RoData:    ".rodata",
	obj.DataConst: ".data.rel.ro",
	obj.Data:      ".data",
}

const (
	shtProgbits = 1
	shtSymtab   = 2
	shtStrtab   = 3
	shtRela     = 4
	shfWrite    = 1
	shfAlloc    = 2
	shfExec     = 4
	shfInfoLink = 0x40
)

type section struct {
	name           string
	typ            uint32
	flags          uint64
	data           []byte
	link, info     uint32
	align, entsize uint64
	nameOff        uint32
	offset         uint64
}

// relocType maps a relocation to the ELF type and addend.
func relocType(arch string, k obj.RelocKind) (uint32, int64, error) {
	if arch == "amd64" {
		switch k {
		case obj.Call32:
			return 4, -4, nil // R_X86_64_PLT32
		case obj.PC32:
			return 2, -4, nil // R_X86_64_PC32
		case obj.Abs64:
			return 1, 0, nil // R_X86_64_64
		}
	} else {
		switch k {
		case obj.Branch26:
			return 283, 0, nil // R_AARCH64_CALL26
		case obj.Page21:
			return 275, 0, nil // R_AARCH64_ADR_PREL_PG_HI21
		case obj.PageOff12:
			return 277, 0, nil // R_AARCH64_ADD_ABS_LO12_NC
		case obj.Abs64:
			return 257, 0, nil // R_AARCH64_ABS64
		}
	}
	return 0, 0, fmt.Errorf("elf: relocation kind %d not supported on %s", k, arch)
}

// Write encodes o as an ELF64 relocatable object.
func Write(o *obj.Object) ([]byte, error) {
	le := binary.LittleEndian
	machine := uint16(62) // EM_X86_64
	if o.Arch == "arm64" {
		machine = 183 // EM_AARCH64
	}
	var secs []*section
	secs = append(secs, &section{}) // null section
	contentIdx := map[obj.Section]int{}
	for s := obj.Section(0); s < obj.NumSections; s++ {
		flags := uint64(shfAlloc)
		switch s {
		case obj.Text:
			flags |= shfExec
		case obj.DataConst, obj.Data:
			flags |= shfWrite
		}
		contentIdx[s] = len(secs)
		secs = append(secs, &section{name: sectNames[s], typ: shtProgbits, flags: flags,
			data: o.Sects[s], align: uint64(1) << o.Align[s]})
	}
	// Symbols: null, locals, then globals and undefined.
	var strtab bytes.Buffer
	strtab.WriteByte(0)
	var symtab bytes.Buffer
	symtab.Write(make([]byte, 24))
	symIndex := map[string]int{}
	n := 1
	addSym := func(name string, bind, typ byte, shndx uint16, value uint64) {
		symIndex[name] = n
		n++
		var e [24]byte
		le.PutUint32(e[0:], uint32(strtab.Len()))
		strtab.WriteString(name)
		strtab.WriteByte(0)
		e[4] = bind<<4 | typ
		le.PutUint16(e[6:], shndx)
		le.PutUint64(e[8:], value)
		symtab.Write(e[:])
	}
	symType := func(s obj.Section) byte {
		if s == obj.Text {
			return 2 // STT_FUNC
		}
		return 1 // STT_OBJECT
	}
	for _, s := range o.Syms {
		if !s.Global {
			addSym(s.Name, 0, symType(s.Sec), uint16(contentIdx[s.Sec]), s.Off)
		}
	}
	firstGlobal := n
	for _, s := range o.Syms {
		if s.Global {
			addSym(s.Name, 1, symType(s.Sec), uint16(contentIdx[s.Sec]), s.Off)
		}
	}
	var undef []string
	for name := range o.Undef {
		if _, ok := symIndex[name]; !ok {
			undef = append(undef, name)
		}
	}
	sort.Strings(undef)
	for _, name := range undef {
		addSym(name, 1, 0, 0, 0)
	}
	symtabIdx := len(secs)
	secs = append(secs, &section{name: ".symtab", typ: shtSymtab, data: symtab.Bytes(),
		info: uint32(firstGlobal), align: 8, entsize: 24})
	strtabIdx := len(secs)
	secs = append(secs, &section{name: ".strtab", typ: shtStrtab, data: strtab.Bytes(), align: 1})
	secs[symtabIdx].link = uint32(strtabIdx)
	// Relocation sections.
	var rela [obj.NumSections]bytes.Buffer
	for _, r := range o.Relocs {
		idx, ok := symIndex[r.Sym]
		if !ok {
			return nil, fmt.Errorf("elf: relocation against unknown symbol %s", r.Sym)
		}
		typ, addend, err := relocType(o.Arch, r.Kind)
		if err != nil {
			return nil, err
		}
		var e [24]byte
		le.PutUint64(e[0:], uint64(r.Off))
		le.PutUint64(e[8:], uint64(idx)<<32|uint64(typ))
		le.PutUint64(e[16:], uint64(addend))
		rela[r.Sec].Write(e[:])
	}
	for s := obj.Section(0); s < obj.NumSections; s++ {
		if rela[s].Len() == 0 {
			continue
		}
		secs = append(secs, &section{name: ".rela" + sectNames[s], typ: shtRela, flags: shfInfoLink,
			data: rela[s].Bytes(), link: uint32(symtabIdx), info: uint32(contentIdx[s]), align: 8, entsize: 24})
	}
	// A non-executable stack.
	secs = append(secs, &section{name: ".note.GNU-stack", typ: shtProgbits, align: 1})
	shstrIdx := len(secs)
	secs = append(secs, &section{name: ".shstrtab", typ: shtStrtab, align: 1})
	var shstr bytes.Buffer
	shstr.WriteByte(0)
	for _, s := range secs[1:] {
		s.nameOff = uint32(shstr.Len())
		shstr.WriteString(s.name)
		shstr.WriteByte(0)
	}
	secs[shstrIdx].data = shstr.Bytes()

	// Layout: header, section contents, section headers.
	off := uint64(64)
	for _, s := range secs[1:] {
		a := s.align
		if a == 0 {
			a = 1
		}
		off = (off + a - 1) / a * a
		s.offset = off
		off += uint64(len(s.data))
	}
	shoff := (off + 7) / 8 * 8

	var b bytes.Buffer
	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	b.Write(make([]byte, 8))
	w := func(v any) { binary.Write(&b, le, v) }
	w(uint16(1)) // ET_REL
	w(machine)
	w(uint32(1))
	w(uint64(0)) // entry
	w(uint64(0)) // phoff
	w(shoff)
	w(uint32(0)) // flags
	w(uint16(64))
	w(uint16(0))
	w(uint16(0))
	w(uint16(64))
	w(uint16(len(secs)))
	w(uint16(shstrIdx))
	for _, s := range secs[1:] {
		for uint64(b.Len()) < s.offset {
			b.WriteByte(0)
		}
		b.Write(s.data)
	}
	for uint64(b.Len()) < shoff {
		b.WriteByte(0)
	}
	for _, s := range secs {
		w(s.nameOff)
		w(s.typ)
		w(s.flags)
		w(uint64(0)) // addr
		w(s.offset)
		w(uint64(len(s.data)))
		w(s.link)
		w(s.info)
		w(s.align)
		w(s.entsize)
	}
	return b.Bytes(), nil
}
