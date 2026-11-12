// Package obj is the format-neutral object file model the backends fill
// and the Mach-O, ELF and COFF writers encode.
package obj

// Section identifies where a symbol or relocation lives.
type Section int

const (
	Text      Section = iota // code
	RoData                   // read-only bytes without pointers
	DataConst                // read-only data with relocated pointers
	Data                     // writable data
	NumSections
)

// RelocKind is a relocation type. Code relocations are architecture
// specific; Abs64 is used in data.
type RelocKind uint8

const (
	Branch26  RelocKind = iota // arm64 bl/b
	Page21                     // arm64 adrp
	PageOff12                  // arm64 add (low 12 bits)
	Abs64                      // 64-bit absolute address
	Call32                     // amd64 call rel32
	PC32                       // amd64 rip-relative rel32 (lea)
)

// Reloc is a relocation at Off in section Sec against Sym.
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
	Sects  [NumSections][]byte
	Align  [NumSections]uint32 // log2
	Syms   []Symbol
	Relocs []Reloc
	Undef  map[string]bool
}

// New returns an empty object for arch.
func New(arch string) *Object {
	o := &Object{Arch: arch, Undef: map[string]bool{}}
	o.Align = [NumSections]uint32{Text: 2, RoData: 3, DataConst: 3, Data: 3}
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

// Undefined declares an external symbol.
func (o *Object) Undefined(name string) { o.Undef[name] = true }

// Reloc records a relocation.
func (o *Object) Reloc(sec Section, off uint32, kind RelocKind, sym string) {
	o.Relocs = append(o.Relocs, Reloc{Sec: sec, Off: off, Kind: kind, Sym: sym})
}
