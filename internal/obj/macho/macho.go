// Package macho writes Mach-O relocatable object files (MH_OBJECT) for
// arm64 and x86_64 macOS.
package macho

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"navi/internal/obj"
)

const numSections = obj.NumSections

type Section = obj.Section

var sectNames = [...][2]string{
	obj.Text:      {"__TEXT", "__text"},
	obj.RoData:    {"__TEXT", "__const"},
	obj.DataConst: {"__DATA", "__const"},
	obj.Data:      {"__DATA", "__data"},
}

const (
	mhMagic64        = 0xfeedfacf
	cpuTypeARM64     = 0x0100000c
	cpuTypeX86_64    = 0x01000007
	cpuSubtypeAll    = 0
	cpuSubtypeX86All = 3
	mhObject         = 1
	lcSegment64      = 0x19
	lcSymtab         = 0x2
	lcDysymtab       = 0xb
	lcBuildVersion   = 0x32
	platformMacOS    = 1
	nSect            = 0xe
	nExt             = 0x1
	nUndf            = 0x0
)

// Write encodes o as a Mach-O object file (MH_OBJECT). Sections are not
// split at symbols (no MH_SUBSECTIONS_VIA_SYMBOLS), so the linker keeps
// their contents in order.
func Write(o *obj.Object) ([]byte, error) {
	le := binary.LittleEndian
	// Section addresses (object files lay sections out from 0).
	var addr [numSections]uint64
	var cur uint64
	for s := Section(0); s < numSections; s++ {
		a := uint64(1) << o.Align[s]
		cur = (cur + a - 1) / a * a
		addr[s] = cur
		cur += uint64(len(o.Sects[s]))
	}
	vmSize := cur

	// Symbols: locals, then external definitions, then undefined.
	var locals, globals []obj.Symbol
	for _, s := range o.Syms {
		if s.Global {
			globals = append(globals, s)
		} else {
			locals = append(locals, s)
		}
	}
	var undef []string
	for name := range o.Undef {
		undef = append(undef, name)
	}
	sort.Strings(undef)
	symIndex := map[string]int{}
	var strtab bytes.Buffer
	strtab.WriteByte(0)
	strtab.WriteByte(0)
	type nlist struct {
		strx  uint32
		typ   uint8
		sect  uint8
		desc  uint16
		value uint64
	}
	var syms []nlist
	add := func(name string, typ, sect uint8, value uint64) {
		symIndex[name] = len(syms)
		syms = append(syms, nlist{strx: uint32(strtab.Len()), typ: typ, sect: sect, value: value})
		strtab.WriteString(name)
		strtab.WriteByte(0)
	}
	for _, s := range locals {
		add(s.Name, nSect, uint8(s.Sec)+1, addr[s.Sec]+s.Off)
	}
	for _, s := range globals {
		add(s.Name, nSect|nExt, uint8(s.Sec)+1, addr[s.Sec]+s.Off)
	}
	for _, name := range undef {
		add(name, nUndf|nExt, 0, 0)
	}

	// Relocations per section.
	var relocs [numSections][]uint32
	for _, r := range o.Relocs {
		idx, ok := symIndex[r.Sym]
		if !ok {
			return nil, fmt.Errorf("macho: relocation against unknown symbol %s", r.Sym)
		}
		var typ, length, pcrel uint32
		switch r.Kind {
		case obj.Branch26:
			typ, length, pcrel = 2, 2, 1
		case obj.Page21:
			typ, length, pcrel = 3, 2, 1
		case obj.PageOff12:
			typ, length, pcrel = 4, 2, 0
		case obj.Abs64:
			typ, length, pcrel = 0, 3, 0
		case obj.Call32:
			typ, length, pcrel = 2, 2, 1 // X86_64_RELOC_BRANCH
		case obj.PC32:
			typ, length, pcrel = 1, 2, 1 // X86_64_RELOC_SIGNED
		}
		info := uint32(idx) | pcrel<<24 | length<<25 | 1<<27 | typ<<28
		relocs[r.Sec] = append(relocs[r.Sec], r.Off, info)
	}

	// Load commands.
	nsects := uint32(numSections)
	segCmdSize := uint32(72 + 80*nsects)
	cmdsSize := segCmdSize + 24 /*build*/ + 24 /*symtab*/ + 80 /*dysymtab*/
	headerSize := uint32(32) + cmdsSize
	// Section contents follow the load commands, at the same relative
	// offsets as their addresses.
	base := (headerSize + 15) / 16 * 16
	var fileOff [numSections]uint32
	for s := Section(0); s < numSections; s++ {
		fileOff[s] = base + uint32(addr[s])
	}
	contentEnd := base + uint32(vmSize)
	relOff := (contentEnd + 7) / 8 * 8
	var relFile [numSections]uint32
	pos := relOff
	for s := Section(0); s < numSections; s++ {
		relFile[s] = pos
		pos += uint32(len(relocs[s]) * 4)
	}
	symOff := (pos + 7) / 8 * 8
	strOff := symOff + uint32(len(syms)*16)

	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, le, v) }
	cpu, sub := uint32(cpuTypeARM64), uint32(cpuSubtypeAll)
	if o.Arch == "amd64" {
		cpu, sub = cpuTypeX86_64, cpuSubtypeX86All
	}
	w(uint32(mhMagic64))
	w(cpu)
	w(sub)
	w(uint32(mhObject))
	w(uint32(4)) // ncmds
	w(cmdsSize)
	w(uint32(0)) // flags
	w(uint32(0))

	name16 := func(s string) [16]byte {
		var n [16]byte
		copy(n[:], s)
		return n
	}
	// LC_SEGMENT_64
	w(uint32(lcSegment64))
	w(segCmdSize)
	w(name16(""))
	w(uint64(0))
	w(vmSize)
	w(uint64(base))
	w(vmSize)
	w(uint32(7)) // maxprot rwx
	w(uint32(7))
	w(nsects)
	w(uint32(0))
	for s := Section(0); s < numSections; s++ {
		w(name16(sectNames[s][1]))
		w(name16(sectNames[s][0]))
		w(addr[s])
		w(uint64(len(o.Sects[s])))
		w(fileOff[s])
		w(o.Align[s])
		if len(relocs[s]) > 0 {
			w(relFile[s])
		} else {
			w(uint32(0))
		}
		w(uint32(len(relocs[s]) / 2))
		flags := uint32(0)
		if s == obj.Text {
			flags = 0x80000400 // pure instructions, some instructions
		}
		w(flags)
		w(uint32(0))
		w(uint32(0))
		w(uint32(0))
	}
	// LC_BUILD_VERSION: macOS 11.0
	w(uint32(lcBuildVersion))
	w(uint32(24))
	w(uint32(platformMacOS))
	w(uint32(11 << 16))
	w(uint32(0))
	w(uint32(0))
	// LC_SYMTAB
	w(uint32(lcSymtab))
	w(uint32(24))
	w(symOff)
	w(uint32(len(syms)))
	w(strOff)
	w(uint32(strtab.Len()))
	// LC_DYSYMTAB
	w(uint32(lcDysymtab))
	w(uint32(80))
	w(uint32(0))
	w(uint32(len(locals)))
	w(uint32(len(locals)))
	w(uint32(len(globals)))
	w(uint32(len(locals) + len(globals)))
	w(uint32(len(undef)))
	for i := 0; i < 12; i++ {
		w(uint32(0))
	}

	// Section contents.
	for s := Section(0); s < numSections; s++ {
		for uint32(b.Len()) < fileOff[s] {
			b.WriteByte(0)
		}
		b.Write(o.Sects[s])
	}
	for uint32(b.Len()) < relOff {
		b.WriteByte(0)
	}
	for s := Section(0); s < numSections; s++ {
		for _, v := range relocs[s] {
			w(v)
		}
	}
	for uint32(b.Len()) < symOff {
		b.WriteByte(0)
	}
	for _, s := range syms {
		w(s.strx)
		w(s.typ)
		w(s.sect)
		w(s.desc)
		w(s.value)
	}
	b.Write(strtab.Bytes())
	return b.Bytes(), nil
}
