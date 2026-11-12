// Package macho writes Mach-O relocatable object files (MH_OBJECT) for
// arm64 and x86_64 macOS.
package macho

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// Section identifies where a symbol or relocation lives.
type Section int

const (
	Text      Section = iota // __TEXT,__text: code
	RoData                   // __TEXT,__const: bytes without pointers
	DataConst                // __DATA,__const: read-only data with pointers
	Data                     // __DATA,__data: writable data
	numSections
)

var sectNames = [...][2]string{
	Text:      {"__TEXT", "__text"},
	RoData:    {"__TEXT", "__const"},
	DataConst: {"__DATA", "__const"},
	Data:      {"__DATA", "__data"},
}

// RelocKind is a relocation type.
type RelocKind uint8

const (
	Branch26  RelocKind = iota // bl/b to a symbol (arm64)
	Page21                     // adrp (arm64)
	PageOff12                  // add/ldr low 12 bits (arm64)
	Abs64                      // 64-bit absolute address in data
	Rel32Call                  // x86_64 call rel32
	Rel32                      // x86_64 rip-relative rel32 (lea)
)

// Reloc is a relocation in a section.
type Reloc struct {
	Sec  Section
	Off  uint32
	Kind RelocKind
	Sym  string
}

// Symbol is a symbol defined in the object.
type Symbol struct {
	Name   string
	Sec    Section
	Off    uint64
	Global bool
}

// Object accumulates the contents of an object file.
type Object struct {
	Arch   string // "arm64" or "amd64"
	Sects  [numSections][]byte
	Align  [numSections]uint32 // log2
	Syms   []Symbol
	Relocs []Reloc
	undef  map[string]bool
}

// New returns an empty object for arch.
func New(arch string) *Object {
	o := &Object{Arch: arch, undef: map[string]bool{}}
	o.Align = [numSections]uint32{Text: 2, RoData: 3, DataConst: 3, Data: 3}
	if arch == "amd64" {
		o.Align[Text] = 4
	}
	return o
}

// Define adds a symbol at the current end of sec (after aligning it).
func (o *Object) Define(name string, sec Section, align int, global bool) uint64 {
	for len(o.Sects[sec])%align != 0 {
		o.Sects[sec] = append(o.Sects[sec], 0)
	}
	off := uint64(len(o.Sects[sec]))
	o.Syms = append(o.Syms, Symbol{Name: name, Sec: sec, Off: off, Global: global})
	return off
}

// Undefined declares an external symbol (from a library).
func (o *Object) Undefined(name string) { o.undef[name] = true }

// Reloc records a relocation.
func (o *Object) Reloc(sec Section, off uint32, kind RelocKind, sym string) {
	o.Relocs = append(o.Relocs, Reloc{Sec: sec, Off: off, Kind: kind, Sym: sym})
}

const (
	mhMagic64        = 0xfeedfacf
	cpuTypeARM64     = 0x0100000c
	cpuTypeX86_64    = 0x01000007
	cpuSubtypeAll    = 0
	cpuSubtypeX86All = 3
	mhObject         = 1
	mhSubsections    = 0x2000
	lcSegment64      = 0x19
	lcSymtab         = 0x2
	lcDysymtab       = 0xb
	lcBuildVersion   = 0x32
	platformMacOS    = 1
	nSect            = 0xe
	nExt             = 0x1
	nUndf            = 0x0
)

// Bytes encodes the object file.
func (o *Object) Bytes() ([]byte, error) {
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
	var locals, globals []Symbol
	for _, s := range o.Syms {
		if s.Global {
			globals = append(globals, s)
		} else {
			locals = append(locals, s)
		}
	}
	var undef []string
	for name := range o.undef {
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
		case Branch26:
			typ, length, pcrel = 2, 2, 1
		case Page21:
			typ, length, pcrel = 3, 2, 1
		case PageOff12:
			typ, length, pcrel = 4, 2, 0
		case Abs64:
			typ, length, pcrel = 0, 3, 0
		case Rel32Call:
			typ, length, pcrel = 2, 2, 1 // X86_64_RELOC_BRANCH
		case Rel32:
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
	// File offsets of section contents.
	var fileOff [numSections]uint32
	off := headerSize
	for s := Section(0); s < numSections; s++ {
		a := uint32(1) << o.Align[s]
		off = (off + a - 1) / a * a
		// Keep file offsets congruent with addresses.
		fileOff[s] = off + uint32(addr[s]) - uint32(addr[0])
		if s == 0 {
			fileOff[s] = off
		}
	}
	contentEnd := fileOff[numSections-1] + uint32(len(o.Sects[numSections-1]))
	// Recompute section offsets so that they follow addresses exactly.
	base := fileOff[0]
	for s := Section(0); s < numSections; s++ {
		fileOff[s] = base + uint32(addr[s])
	}
	contentEnd = base + uint32(vmSize)
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
	w(uint32(mhSubsections))
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
		if s == Text {
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
