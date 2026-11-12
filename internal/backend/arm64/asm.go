// Package arm64 generates AArch64 machine code from the code generation
// plan: an instruction encoder with labels and relocations (this file) and
// the MIR lowering (gen.go).
package arm64

import (
	"fmt"

	"navi/internal/obj/macho"
)

// Reg is a general purpose or floating point register number. 31 is SP or
// XZR depending on the instruction.
type Reg uint8

const (
	X0  Reg = 0
	X1  Reg = 1
	X8  Reg = 8
	X9  Reg = 9
	X10 Reg = 10
	X11 Reg = 11
	X12 Reg = 12
	X13 Reg = 13
	X14 Reg = 14
	X15 Reg = 15
	X16 Reg = 16
	X17 Reg = 17
	FP  Reg = 29
	LR  Reg = 30
	SP  Reg = 31
	ZR  Reg = 31
)

// Cond is a condition code.
type Cond uint8

const (
	EQ Cond = iota
	NE
	HS
	LO
	MI
	PL
	VS
	VC
	HI
	LS
	GE
	LT
	GT
	LE
	AL
)

func (c Cond) Invert() Cond { return c ^ 1 }

// Label is a code position resolved when bound.
type Label int

type fixKind uint8

const (
	fixB fixKind = iota
	fixBCond
	fixCB
	fixADR
)

type fixup struct {
	at    int
	label Label
	kind  fixKind
}

// Reloc is a relocation against a symbol at a byte offset of the code.
type Reloc struct {
	Off  int
	Kind macho.RelocKind
	Sym  string
}

// Asm accumulates the instructions of one function.
type Asm struct {
	Code   []uint32
	labels []int
	fixups []fixup
	Relocs []Reloc
}

func (a *Asm) emit(w uint32) { a.Code = append(a.Code, w) }

// NewLabel returns an unbound label.
func (a *Asm) NewLabel() Label {
	a.labels = append(a.labels, -1)
	return Label(len(a.labels) - 1)
}

// Bind binds l to the next instruction.
func (a *Asm) Bind(l Label) { a.labels[l] = len(a.Code) }

// Finish resolves label references.
func (a *Asm) Finish() error {
	for _, f := range a.fixups {
		to := a.labels[f.label]
		if to < 0 {
			return fmt.Errorf("arm64: unbound label")
		}
		d := int32(to - f.at)
		w := a.Code[f.at]
		switch f.kind {
		case fixB:
			w |= uint32(d) & 0x3ffffff
		case fixBCond, fixCB:
			if d >= 1<<18 || d < -(1<<18) {
				return fmt.Errorf("arm64: conditional branch out of range")
			}
			w |= (uint32(d) & 0x7ffff) << 5
		case fixADR:
			bd := uint32(d * 4)
			w |= (bd&3)<<29 | ((bd>>2)&0x7ffff)<<5
		}
		a.Code[f.at] = w
	}
	return nil
}

// Bytes returns the little-endian encoding.
func (a *Asm) Bytes() []byte {
	out := make([]byte, 4*len(a.Code))
	for i, w := range a.Code {
		out[4*i] = byte(w)
		out[4*i+1] = byte(w >> 8)
		out[4*i+2] = byte(w >> 16)
		out[4*i+3] = byte(w >> 24)
	}
	return out
}

func r3(base uint32, rd, rn, rm Reg) uint32 {
	return base | uint32(rm&31)<<16 | uint32(rn&31)<<5 | uint32(rd&31)
}

// --- Arithmetic ---------------------------------------------------------

// AddImm is add rd, rn, #imm (rd/rn may be SP); imm < 4096 or a multiple
// of 4096 below 2^24.
func (a *Asm) AddImm(rd, rn Reg, imm int64) { a.addSubImm(0x91000000, rd, rn, imm) }

// SubImm is sub rd, rn, #imm.
func (a *Asm) SubImm(rd, rn Reg, imm int64) { a.addSubImm(0xD1000000, rd, rn, imm) }

// CmpImm is cmp rn, #imm.
func (a *Asm) CmpImm(rn Reg, imm int64) { a.addSubImm(0xF1000000, ZR, rn, imm) }

func (a *Asm) addSubImm(base uint32, rd, rn Reg, imm int64) {
	if imm < 0 || imm >= 1<<24 || imm >= 4096 && imm&0xfff != 0 {
		panic(fmt.Sprintf("arm64: immediate %d out of range", imm))
	}
	sh := uint32(0)
	if imm >= 4096 {
		sh, imm = 1, imm>>12
	}
	a.emit(base | sh<<22 | uint32(imm)<<10 | uint32(rn&31)<<5 | uint32(rd&31))
}

func (a *Asm) Add(rd, rn, rm Reg)  { a.emit(r3(0x8B000000, rd, rn, rm)) }
func (a *Asm) Sub(rd, rn, rm Reg)  { a.emit(r3(0xCB000000, rd, rn, rm)) }
func (a *Asm) Adds(rd, rn, rm Reg) { a.emit(r3(0xAB000000, rd, rn, rm)) }
func (a *Asm) Subs(rd, rn, rm Reg) { a.emit(r3(0xEB000000, rd, rn, rm)) }
func (a *Asm) Cmp(rn, rm Reg)      { a.Subs(ZR, rn, rm) }

// CmpAsr63 is cmp rn, rm, asr #63.
func (a *Asm) CmpAsr63(rn, rm Reg) { a.emit(r3(0xEB000000, ZR, rn, rm) | 2<<22 | 63<<10) }

// AddExt is add rd, rn, rm (uxtx), usable with SP as rd/rn.
func (a *Asm) AddExt(rd, rn, rm Reg) { a.emit(r3(0x8B200000, rd, rn, rm) | 3<<13) }

// SubExt is sub rd, rn, rm (uxtx), usable with SP as rd/rn.
func (a *Asm) SubExt(rd, rn, rm Reg) { a.emit(r3(0xCB200000, rd, rn, rm) | 3<<13) }

func (a *Asm) Mul(rd, rn, rm Reg) { a.emit(r3(0x9B000000, rd, rn, rm) | 31<<10) }

// Madd is rd = ra + rn*rm.
func (a *Asm) Madd(rd, rn, rm, ra Reg) { a.emit(r3(0x9B000000, rd, rn, rm) | uint32(ra)<<10) }

// Msub is rd = ra - rn*rm.
func (a *Asm) Msub(rd, rn, rm, ra Reg) { a.emit(r3(0x9B008000, rd, rn, rm) | uint32(ra)<<10) }

func (a *Asm) Smulh(rd, rn, rm Reg) { a.emit(r3(0x9B400000, rd, rn, rm) | 31<<10) }
func (a *Asm) Umulh(rd, rn, rm Reg) { a.emit(r3(0x9BC00000, rd, rn, rm) | 31<<10) }
func (a *Asm) Sdiv(rd, rn, rm Reg)  { a.emit(r3(0x9AC00C00, rd, rn, rm)) }
func (a *Asm) Udiv(rd, rn, rm Reg)  { a.emit(r3(0x9AC00800, rd, rn, rm)) }
func (a *Asm) Lslv(rd, rn, rm Reg)  { a.emit(r3(0x9AC02000, rd, rn, rm)) }
func (a *Asm) Lsrv(rd, rn, rm Reg)  { a.emit(r3(0x9AC02400, rd, rn, rm)) }
func (a *Asm) Asrv(rd, rn, rm Reg)  { a.emit(r3(0x9AC02800, rd, rn, rm)) }
func (a *Asm) And(rd, rn, rm Reg)   { a.emit(r3(0x8A000000, rd, rn, rm)) }
func (a *Asm) Orr(rd, rn, rm Reg)   { a.emit(r3(0xAA000000, rd, rn, rm)) }
func (a *Asm) Eor(rd, rn, rm Reg)   { a.emit(r3(0xCA000000, rd, rn, rm)) }
func (a *Asm) Mvn(rd, rm Reg)       { a.emit(r3(0xAA200000, rd, ZR, rm)) }

// Mov copies a register (not SP).
func (a *Asm) Mov(rd, rm Reg) { a.Orr(rd, ZR, rm) }

// MovSP is mov rd, rn where either may be SP.
func (a *Asm) MovSP(rd, rn Reg) { a.AddImm(rd, rn, 0) }

// Csel is rd = cond ? rn : rm.
func (a *Asm) Csel(rd, rn, rm Reg, c Cond) { a.emit(r3(0x9A800000, rd, rn, rm) | uint32(c)<<12) }

// Cset is rd = cond ? 1 : 0.
func (a *Asm) Cset(rd Reg, c Cond) { a.emit(r3(0x9A800400, rd, ZR, ZR) | uint32(c.Invert())<<12) }

// Sbfm and Ubfm are the 64-bit bitfield moves.
func (a *Asm) Sbfm(rd, rn Reg, immr, imms uint32) {
	a.emit(0x93400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
}
func (a *Asm) Ubfm(rd, rn Reg, immr, imms uint32) {
	a.emit(0xD3400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
}

// SignExt sign-extends the low size bytes of rn into rd.
func (a *Asm) SignExt(rd, rn Reg, size int64) {
	if size >= 8 {
		if rd != rn {
			a.Mov(rd, rn)
		}
		return
	}
	a.Sbfm(rd, rn, 0, uint32(size*8-1))
}

// ZeroExt zero-extends the low size bytes of rn into rd.
func (a *Asm) ZeroExt(rd, rn Reg, size int64) {
	if size >= 8 {
		if rd != rn {
			a.Mov(rd, rn)
		}
		return
	}
	a.Ubfm(rd, rn, 0, uint32(size*8-1))
}

// LsrImm is lsr rd, rn, #s.
func (a *Asm) LsrImm(rd, rn Reg, s uint32) { a.Ubfm(rd, rn, s, 63) }

// LslImm is lsl rd, rn, #s.
func (a *Asm) LslImm(rd, rn Reg, s uint32) { a.Ubfm(rd, rn, (64-s)%64, 63-s) }

// MovImm loads a 64-bit constant.
func (a *Asm) MovImm(rd Reg, v int64) {
	u := uint64(v)
	if u == 0 {
		a.emit(0xD2800000 | uint32(rd))
		return
	}
	// movn for values with mostly ones.
	if ^u < 1<<16 {
		a.emit(0x92800000 | uint32(^u&0xffff)<<5 | uint32(rd))
		return
	}
	first := true
	for hw := uint32(0); hw < 4; hw++ {
		part := uint32(u>>(16*hw)) & 0xffff
		if part == 0 {
			continue
		}
		if first {
			a.emit(0xD2800000 | hw<<21 | part<<5 | uint32(rd))
			first = false
		} else {
			a.emit(0xF2800000 | hw<<21 | part<<5 | uint32(rd))
		}
	}
}

// --- Memory -------------------------------------------------------------

var ldrScaled = map[int64][2]uint32{ // size -> {load, store}, unsigned offset
	1: {0x39400000, 0x39000000},
	2: {0x79400000, 0x79000000},
	4: {0xB9400000, 0xB9000000},
	8: {0xF9400000, 0xF9000000},
}

var ldrUnscaled = map[int64][2]uint32{
	1: {0x38400000, 0x38000000},
	2: {0x78400000, 0x78000000},
	4: {0xB8400000, 0xB8000000},
	8: {0xF8400000, 0xF8000000},
}

// Ldr loads size bytes (zero-extended) from [rn + off]; Str stores them.
// Offsets that do not fit an instruction use X17 as a temporary.
func (a *Asm) Ldr(size int64, rt, rn Reg, off int64) { a.ldst(size, rt, rn, off, 0) }
func (a *Asm) Str(size int64, rt, rn Reg, off int64) { a.ldst(size, rt, rn, off, 1) }

func (a *Asm) ldst(size int64, rt, rn Reg, off int64, store int) {
	switch {
	case off >= 0 && off%size == 0 && off/size < 4096:
		a.emit(ldrScaled[size][store] | uint32(off/size)<<10 | uint32(rn)<<5 | uint32(rt))
	case off >= -256 && off < 256:
		a.emit(ldrUnscaled[size][store] | (uint32(off)&0x1ff)<<12 | uint32(rn)<<5 | uint32(rt))
	default:
		if rn == X17 || rt == X17 {
			panic("arm64: X17 used with a large offset")
		}
		a.AddrOf(X17, rn, off)
		a.emit(ldrScaled[size][store] | uint32(X17)<<5 | uint32(rt))
	}
}

// LdrPost is ldr rt, [rn], #off (64-bit); StrPost is str rt, [rn], #off.
func (a *Asm) LdrPost(rt, rn Reg, off int64) {
	a.emit(0xF8400400 | (uint32(off)&0x1ff)<<12 | uint32(rn)<<5 | uint32(rt))
}
func (a *Asm) StrPost(rt, rn Reg, off int64) {
	a.emit(0xF8000400 | (uint32(off)&0x1ff)<<12 | uint32(rn)<<5 | uint32(rt))
}

// SubsImm is subs rd, rn, #imm; CmnImm is cmn rn, #imm.
func (a *Asm) SubsImm(rd, rn Reg, imm int64) { a.addSubImm(0xF1000000, rd, rn, imm) }
func (a *Asm) CmnImm(rn Reg, imm int64)      { a.addSubImm(0xB1000000, ZR, rn, imm) }

// AddrOf computes rd = rn + off for any offset (rn may be SP).
func (a *Asm) AddrOf(rd, rn Reg, off int64) {
	switch {
	case off >= 0 && off < 4096:
		a.AddImm(rd, rn, off)
	case off < 0 && off > -4096:
		a.SubImm(rd, rn, -off)
	default:
		if rd == rn {
			panic("arm64: AddrOf with rd == rn and a large offset")
		}
		a.MovImm(rd, off)
		a.AddExt(rd, rn, rd)
	}
}

// StpPre is stp rt, rt2, [rn, #off]!; LdpPost is ldp rt, rt2, [rn], #off.
func (a *Asm) StpPre(rt, rt2, rn Reg, off int64) {
	a.emit(0xA9800000 | (uint32(off/8)&0x7f)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt))
}
func (a *Asm) LdpPost(rt, rt2, rn Reg, off int64) {
	a.emit(0xA8C00000 | (uint32(off/8)&0x7f)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt))
}

// --- Control flow -------------------------------------------------------

func (a *Asm) B(l Label) {
	a.fixups = append(a.fixups, fixup{len(a.Code), l, fixB})
	a.emit(0x14000000)
}

func (a *Asm) BCond(c Cond, l Label) {
	a.fixups = append(a.fixups, fixup{len(a.Code), l, fixBCond})
	a.emit(0x54000000 | uint32(c))
}

func (a *Asm) Cbz(rt Reg, l Label) {
	a.fixups = append(a.fixups, fixup{len(a.Code), l, fixCB})
	a.emit(0xB4000000 | uint32(rt))
}

func (a *Asm) Cbnz(rt Reg, l Label) {
	a.fixups = append(a.fixups, fixup{len(a.Code), l, fixCB})
	a.emit(0xB5000000 | uint32(rt))
}

// Adr loads the address of a label in this function.
func (a *Asm) Adr(rd Reg, l Label) {
	a.fixups = append(a.fixups, fixup{len(a.Code), l, fixADR})
	a.emit(0x10000000 | uint32(rd))
}

// Bl calls a symbol.
func (a *Asm) Bl(sym string) {
	a.Relocs = append(a.Relocs, Reloc{4 * len(a.Code), macho.Branch26, sym})
	a.emit(0x94000000)
}

// BSym jumps to a symbol (tail call).
func (a *Asm) BSym(sym string) {
	a.Relocs = append(a.Relocs, Reloc{4 * len(a.Code), macho.Branch26, sym})
	a.emit(0x14000000)
}

func (a *Asm) Blr(rn Reg) { a.emit(0xD63F0000 | uint32(rn)<<5) }
func (a *Asm) Br(rn Reg)  { a.emit(0xD61F0000 | uint32(rn)<<5) }
func (a *Asm) Ret()       { a.emit(0xD65F03C0) }
func (a *Asm) Brk(n uint32) {
	a.emit(0xD4200000 | (n&0xffff)<<5)
}

// AdrSym loads the address of a symbol (adrp + add).
func (a *Asm) AdrSym(rd Reg, sym string) {
	a.Relocs = append(a.Relocs, Reloc{4 * len(a.Code), macho.Page21, sym})
	a.emit(0x90000000 | uint32(rd))
	a.Relocs = append(a.Relocs, Reloc{4 * len(a.Code), macho.PageOff12, sym})
	a.emit(0x91000000 | uint32(rd)<<5 | uint32(rd))
}

// --- Floating point -----------------------------------------------------

// FmovToF moves rn's bits into fp register rd (double if f64, else single
// from the low 32 bits).
func (a *Asm) FmovToF(rd, rn Reg, f64 bool) {
	if f64 {
		a.emit(0x9E670000 | uint32(rn)<<5 | uint32(rd))
	} else {
		a.emit(0x1E270000 | uint32(rn)<<5 | uint32(rd))
	}
}

// FmovFromF moves fp register rn's bits into rd.
func (a *Asm) FmovFromF(rd, rn Reg, f64 bool) {
	if f64 {
		a.emit(0x9E660000 | uint32(rn)<<5 | uint32(rd))
	} else {
		a.emit(0x1E260000 | uint32(rn)<<5 | uint32(rd))
	}
}

func ftype(f64 bool) uint32 {
	if f64 {
		return 1 << 22
	}
	return 0
}

func (a *Asm) Fadd(rd, rn, rm Reg, f64 bool) { a.emit(r3(0x1E202800|ftype(f64), rd, rn, rm)) }
func (a *Asm) Fsub(rd, rn, rm Reg, f64 bool) { a.emit(r3(0x1E203800|ftype(f64), rd, rn, rm)) }
func (a *Asm) Fmul(rd, rn, rm Reg, f64 bool) { a.emit(r3(0x1E200800|ftype(f64), rd, rn, rm)) }
func (a *Asm) Fdiv(rd, rn, rm Reg, f64 bool) { a.emit(r3(0x1E201800|ftype(f64), rd, rn, rm)) }
func (a *Asm) Fneg(rd, rn Reg, f64 bool)     { a.emit(r3(0x1E214000|ftype(f64), rd, rn, 0)) }
func (a *Asm) Fcmp(rn, rm Reg, f64 bool)     { a.emit(r3(0x1E202000|ftype(f64), 0, rn, rm)) }

// FcvtDS converts single rn to double rd; FcvtSD the reverse.
func (a *Asm) FcvtDS(rd, rn Reg) { a.emit(0x1E22C000 | uint32(rn)<<5 | uint32(rd)) }
func (a *Asm) FcvtSD(rd, rn Reg) { a.emit(0x1E624000 | uint32(rn)<<5 | uint32(rd)) }

// Scvtf/Ucvtf convert a 64-bit integer to float; Fcvtzs/Fcvtzu convert
// a float to a 64-bit integer (saturating, NaN to 0).
func (a *Asm) Scvtf(rd, rn Reg, f64 bool) {
	a.emit(0x9E220000 | ftype(f64) | uint32(rn)<<5 | uint32(rd))
}
func (a *Asm) Ucvtf(rd, rn Reg, f64 bool) {
	a.emit(0x9E230000 | ftype(f64) | uint32(rn)<<5 | uint32(rd))
}
func (a *Asm) Fcvtzs(rd, rn Reg, f64 bool) {
	a.emit(0x9E380000 | ftype(f64) | uint32(rn)<<5 | uint32(rd))
}
func (a *Asm) Fcvtzu(rd, rn Reg, f64 bool) {
	a.emit(0x9E390000 | ftype(f64) | uint32(rn)<<5 | uint32(rd))
}
