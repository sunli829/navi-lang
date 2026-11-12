// Package coff writes COFF object files for x86-64 Windows.
package coff

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"navi/internal/obj"
)

var sectNames = [...]string{
	obj.Text:      ".text",
	obj.RoData:    ".rdata",
	obj.DataConst: ".data$r", // has relocations; merged into .data
	obj.Data:      ".data",
}

const (
	cntCode       = 0x20
	cntInitData   = 0x40
	memExecute    = 0x20000000
	memRead       = 0x40000000
	memWrite      = 0x80000000
	align1        = 0x00100000
	align8        = 0x00400000
	align16       = 0x00500000
	relAMD64Addr  = 0x1 // IMAGE_REL_AMD64_ADDR64
	relAMD64Rel32 = 0x4 // IMAGE_REL_AMD64_REL32
)

// Write encodes o (amd64) as a COFF object.
func Write(o *obj.Object) ([]byte, error) {
	if o.Arch != "amd64" {
		return nil, fmt.Errorf("coff: only amd64 is supported")
	}
	le := binary.LittleEndian
	nsec := int(obj.NumSections)
	// Symbols: one per defined symbol, then undefined ones.
	var strtab bytes.Buffer
	var symtab bytes.Buffer
	symIndex := map[string]int{}
	n := 0
	addSym := func(name string, value uint32, sect int16, typ uint16, class byte) {
		symIndex[name] = n
		n++
		var e [18]byte
		if len(name) <= 8 {
			copy(e[:8], name)
		} else {
			le.PutUint32(e[4:], uint32(4+strtab.Len()))
			strtab.WriteString(name)
			strtab.WriteByte(0)
		}
		le.PutUint32(e[8:], value)
		le.PutUint16(e[12:], uint16(sect))
		le.PutUint16(e[14:], typ)
		e[16] = class
		symtab.Write(e[:])
	}
	for _, s := range o.Syms {
		class := byte(3) // IMAGE_SYM_CLASS_STATIC
		if s.Global {
			class = 2 // EXTERNAL
		}
		typ := uint16(0)
		if s.Sec == obj.Text {
			typ = 0x20
		}
		addSym(s.Name, uint32(s.Off), int16(s.Sec)+1, typ, class)
	}
	var undef []string
	for name := range o.Undef {
		if _, ok := symIndex[name]; !ok {
			undef = append(undef, name)
		}
	}
	sort.Strings(undef)
	for _, name := range undef {
		addSym(name, 0, 0, 0x20, 2)
	}
	// Relocations per section.
	var relocs [obj.NumSections]bytes.Buffer
	var nrel [obj.NumSections]int
	for _, r := range o.Relocs {
		idx, ok := symIndex[r.Sym]
		if !ok {
			return nil, fmt.Errorf("coff: relocation against unknown symbol %s", r.Sym)
		}
		var typ uint16
		switch r.Kind {
		case obj.Call32, obj.PC32:
			typ = relAMD64Rel32
		case obj.Abs64:
			typ = relAMD64Addr
		default:
			return nil, fmt.Errorf("coff: relocation kind %d not supported", r.Kind)
		}
		var e [10]byte
		le.PutUint32(e[0:], r.Off)
		le.PutUint32(e[4:], uint32(idx))
		le.PutUint16(e[8:], typ)
		relocs[r.Sec].Write(e[:])
		nrel[r.Sec]++
	}
	// Layout: header (20), section headers (40 each), raw data,
	// relocations, symbol table, string table.
	off := uint32(20 + 40*nsec)
	var rawOff, relOff [obj.NumSections]uint32
	for s := obj.Section(0); s < obj.NumSections; s++ {
		off = (off + 15) &^ 15
		rawOff[s] = off
		off += uint32(len(o.Sects[s]))
	}
	for s := obj.Section(0); s < obj.NumSections; s++ {
		relOff[s] = off
		off += uint32(relocs[s].Len())
	}
	symOff := off

	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, le, v) }
	w(uint16(0x8664))
	w(uint16(nsec))
	w(uint32(0))
	w(symOff)
	w(uint32(n))
	w(uint16(0))
	w(uint16(0))
	for s := obj.Section(0); s < obj.NumSections; s++ {
		var name [8]byte
		copy(name[:], sectNames[s])
		w(name)
		w(uint32(0))
		w(uint32(0))
		w(uint32(len(o.Sects[s])))
		w(rawOff[s])
		if nrel[s] > 0 {
			w(relOff[s])
		} else {
			w(uint32(0))
		}
		w(uint32(0))
		w(uint16(nrel[s]))
		w(uint16(0))
		var ch uint32
		switch s {
		case obj.Text:
			ch = cntCode | memExecute | memRead | align16
		case obj.RoData:
			ch = cntInitData | memRead | align8
		default:
			ch = cntInitData | memRead | memWrite | align8
		}
		w(ch)
	}
	for s := obj.Section(0); s < obj.NumSections; s++ {
		for uint32(b.Len()) < rawOff[s] {
			b.WriteByte(0)
		}
		b.Write(o.Sects[s])
	}
	for s := obj.Section(0); s < obj.NumSections; s++ {
		b.Write(relocs[s].Bytes())
	}
	b.Write(symtab.Bytes())
	w(uint32(4 + strtab.Len()))
	b.Write(strtab.Bytes())
	return b.Bytes(), nil
}
