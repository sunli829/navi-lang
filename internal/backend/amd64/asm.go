// Package amd64 generates x86-64 machine code: an instruction encoder
// with labels and relocations (this file) and the lower.Machine
// implementation (machine.go).
package amd64

import (
	"encoding/binary"
	"fmt"

	"navi/internal/obj"
)

// Reg is a general purpose register (0-15) or an XMM register.
type Reg uint8

const (
	RAX Reg = iota
	RCX
	RDX
	RBX
	RSP
	RBP
	RSI
	RDI
	R8
	R9
	R10
	R11
	R12
	R13
	R14
	R15
)

// Cond is an x86 condition code.
type Cond uint8

const (
	CO  Cond = 0x0
	CNO Cond = 0x1
	CB  Cond = 0x2 // below (unsigned <), carry
	CAE Cond = 0x3
	CE  Cond = 0x4
	CNE Cond = 0x5
	CBE Cond = 0x6
	CA  Cond = 0x7
	CS  Cond = 0x8
	CNS Cond = 0x9
	CP  Cond = 0xA
	CNP Cond = 0xB
	CL  Cond = 0xC
	CGE Cond = 0xD
	CLE Cond = 0xE
	CG  Cond = 0xF
)

// Label is a code position resolved when bound.
type Label int

type fixup struct {
	at    int // offset of the rel32 field
	label Label
}

// Reloc is a relocation of a rel32 field at Off.
type Reloc struct {
	Off  int
	Kind obj.RelocKind
	Sym  string
}

// Asm accumulates the code of one function.
type Asm struct {
	Code   []byte
	labels []int
	fixups []fixup
	Relocs []Reloc
}

func (a *Asm) b(bs ...byte) { a.Code = append(a.Code, bs...) }
func (a *Asm) u32(v uint32) { a.Code = binary.LittleEndian.AppendUint32(a.Code, v) }
func (a *Asm) u64(v uint64) { a.Code = binary.LittleEndian.AppendUint64(a.Code, v) }

func (a *Asm) NewLabel() Label {
	a.labels = append(a.labels, -1)
	return Label(len(a.labels) - 1)
}

func (a *Asm) Bind(l Label) { a.labels[l] = len(a.Code) }

// Finish resolves label references.
func (a *Asm) Finish() error {
	for _, f := range a.fixups {
		to := a.labels[f.label]
		if to < 0 {
			return fmt.Errorf("amd64: unbound label")
		}
		binary.LittleEndian.PutUint32(a.Code[f.at:], uint32(int32(to-(f.at+4))))
	}
	return nil
}

// rex emits a REX prefix if needed (or forced for byte registers).
func (a *Asm) rex(w bool, reg, index, base Reg, force bool) {
	v := byte(0x40)
	if w {
		v |= 8
	}
	if reg&8 != 0 {
		v |= 4
	}
	if index&8 != 0 {
		v |= 2
	}
	if base&8 != 0 {
		v |= 1
	}
	if v != 0x40 || force {
		a.b(v)
	}
}

// modrmReg encodes a register-direct operand.
func (a *Asm) modrmReg(reg, rm Reg) { a.b(0xC0 | byte(reg&7)<<3 | byte(rm&7)) }

// modrmMem encodes [base + disp] with reg in the reg field.
func (a *Asm) modrmMem(reg, base Reg, disp int64) {
	r := byte(reg&7) << 3
	b := byte(base & 7)
	switch {
	case disp == 0 && b != 5:
		a.b(0x00 | r | b)
		if b == 4 {
			a.b(0x24)
		}
	case disp >= -128 && disp < 128:
		a.b(0x40 | r | b)
		if b == 4 {
			a.b(0x24)
		}
		a.b(byte(int8(disp)))
	default:
		a.b(0x80 | r | b)
		if b == 4 {
			a.b(0x24)
		}
		a.u32(uint32(int32(disp)))
	}
}

// opRR emits "op rm, reg" for a 64-bit (w) register-register form.
func (a *Asm) opRR(w bool, op []byte, reg, rm Reg) {
	a.rex(w, reg, 0, rm, false)
	a.b(op...)
	a.modrmReg(reg, rm)
}

// opRM emits "op [base+disp], reg" (or the reverse direction per op).
func (a *Asm) opRM(w bool, op []byte, reg, base Reg, disp int64, forceRex bool) {
	a.rex(w, reg, 0, base, forceRex)
	a.b(op...)
	a.modrmMem(reg, base, disp)
}

// --- Moves and memory -------------------------------------------------------

// MovRR is mov dst, src (64-bit).
func (a *Asm) MovRR(dst, src Reg) { a.opRR(true, []byte{0x89}, src, dst) }

// MovImm loads a 64-bit constant.
func (a *Asm) MovImm(dst Reg, v int64) {
	switch {
	case v == 0:
		a.opRR(false, []byte{0x31}, dst, dst) // xor r32, r32
	case v > 0 && v <= 0xffffffff:
		a.rex(false, 0, 0, dst, false)
		a.b(0xB8 + byte(dst&7))
		a.u32(uint32(v))
	case v >= -1<<31 && v < 0:
		a.rex(true, 0, 0, dst, false)
		a.b(0xC7)
		a.modrmReg(0, dst)
		a.u32(uint32(int32(v)))
	default:
		a.rex(true, 0, 0, dst, false)
		a.b(0xB8 + byte(dst&7))
		a.u64(uint64(v))
	}
}

// Load reads size bytes zero-extended into dst.
func (a *Asm) Load(size int64, dst, base Reg, disp int64) {
	switch size {
	case 1:
		a.opRM(true, []byte{0x0F, 0xB6}, dst, base, disp, false)
	case 2:
		a.opRM(true, []byte{0x0F, 0xB7}, dst, base, disp, false)
	case 4:
		a.opRM(false, []byte{0x8B}, dst, base, disp, false)
	default:
		a.opRM(true, []byte{0x8B}, dst, base, disp, false)
	}
}

// Store writes the low size bytes of src.
func (a *Asm) Store(size int64, src, base Reg, disp int64) {
	switch size {
	case 1:
		a.opRM(false, []byte{0x88}, src, base, disp, src >= 4)
	case 2:
		a.b(0x66)
		a.opRM(false, []byte{0x89}, src, base, disp, false)
	case 4:
		a.opRM(false, []byte{0x89}, src, base, disp, false)
	default:
		a.opRM(true, []byte{0x89}, src, base, disp, false)
	}
}

// StoreZero writes size zero bytes.
func (a *Asm) StoreZero(size int64, base Reg, disp int64) {
	switch size {
	case 1:
		a.opRM(false, []byte{0xC6}, 0, base, disp, false)
		a.b(0)
	case 2:
		a.b(0x66)
		a.opRM(false, []byte{0xC7}, 0, base, disp, false)
		a.b(0, 0)
	case 4:
		a.opRM(false, []byte{0xC7}, 0, base, disp, false)
		a.u32(0)
	default:
		a.opRM(true, []byte{0xC7}, 0, base, disp, false)
		a.u32(0)
	}
}

// Lea is lea dst, [base + disp].
func (a *Asm) Lea(dst, base Reg, disp int64) { a.opRM(true, []byte{0x8D}, dst, base, disp, false) }

// LeaSym is lea dst, [rip + sym].
func (a *Asm) LeaSym(dst Reg, sym string) {
	a.rex(true, dst, 0, 0, false)
	a.b(0x8D, byte(dst&7)<<3|5)
	a.Relocs = append(a.Relocs, Reloc{len(a.Code), obj.PC32, sym})
	a.u32(0)
}

// LoadMem64 is mov dst, [base+disp] (alias of Load 8).
func (a *Asm) LoadMem64(dst, base Reg, disp int64) { a.Load(8, dst, base, disp) }

// --- Arithmetic --------------------------------------------------------------

// ALU register-register operations: op dst, src.
func (a *Asm) Add(dst, src Reg) { a.opRR(true, []byte{0x01}, src, dst) }
func (a *Asm) Sub(dst, src Reg) { a.opRR(true, []byte{0x29}, src, dst) }
func (a *Asm) And(dst, src Reg) { a.opRR(true, []byte{0x21}, src, dst) }
func (a *Asm) Or(dst, src Reg)  { a.opRR(true, []byte{0x09}, src, dst) }
func (a *Asm) Xor(dst, src Reg) { a.opRR(true, []byte{0x31}, src, dst) }
func (a *Asm) Cmp(x, y Reg)     { a.opRR(true, []byte{0x39}, y, x) }
func (a *Asm) Test(x, y Reg)    { a.opRR(true, []byte{0x85}, y, x) }

// Test32 is test x32, y32.
func (a *Asm) Test32(x, y Reg) { a.opRR(false, []byte{0x85}, y, x) }

// Imul is imul dst, src (64-bit, signed, sets OF).
func (a *Asm) Imul(dst, src Reg) { a.opRR(true, []byte{0x0F, 0xAF}, dst, src) }

// group3 emits F7 /ext on a register: mul(4) imul(5) div(6) idiv(7) neg(3) not(2).
func (a *Asm) group3(ext Reg, r Reg) { a.opRR(true, []byte{0xF7}, ext, r) }

func (a *Asm) MulRAX(src Reg)  { a.group3(4, src) }
func (a *Asm) DivRAX(src Reg)  { a.group3(6, src) }
func (a *Asm) IdivRAX(src Reg) { a.group3(7, src) }
func (a *Asm) Neg(r Reg)       { a.group3(3, r) }
func (a *Asm) Not(r Reg)       { a.group3(2, r) }

// Cqo sign-extends rax into rdx.
func (a *Asm) Cqo() { a.b(0x48, 0x99) }

// Shifts by cl: shl(4) shr(5) sar(7).
func (a *Asm) ShlCL(r Reg) { a.opRR(true, []byte{0xD3}, 4, r) }
func (a *Asm) ShrCL(r Reg) { a.opRR(true, []byte{0xD3}, 5, r) }
func (a *Asm) SarCL(r Reg) { a.opRR(true, []byte{0xD3}, 7, r) }

// ShrImm and ShlImm shift by a constant.
func (a *Asm) ShrImm(r Reg, n byte) { a.opRR(true, []byte{0xC1}, 5, r); a.b(n) }
func (a *Asm) ShlImm(r Reg, n byte) { a.opRR(true, []byte{0xC1}, 4, r); a.b(n) }

// Btc complements bit n of r.
func (a *Asm) Btc(r Reg, n byte) { a.opRR(true, []byte{0x0F, 0xBA}, 7, r); a.b(n) }

// imm81 emits group-1 "op r, imm" (add 0, or 1, and 4, sub 5, xor 6, cmp 7).
func (a *Asm) imm81(ext Reg, r Reg, v int64) {
	if v >= -128 && v < 128 {
		a.opRR(true, []byte{0x83}, ext, r)
		a.b(byte(int8(v)))
		return
	}
	a.opRR(true, []byte{0x81}, ext, r)
	a.u32(uint32(int32(v)))
}

func (a *Asm) AddImm(r Reg, v int64) { a.imm81(0, r, v) }
func (a *Asm) SubImm(r Reg, v int64) { a.imm81(5, r, v) }
func (a *Asm) AndImm(r Reg, v int64) { a.imm81(4, r, v) }
func (a *Asm) CmpImm(r Reg, v int64) { a.imm81(7, r, v) }

// Movsx sign-extends the low size bytes of src into dst.
func (a *Asm) Movsx(dst, src Reg, size int64) {
	switch size {
	case 1:
		a.rex(true, dst, 0, src, false)
		a.b(0x0F, 0xBE)
		a.modrmReg(dst, src)
	case 2:
		a.opRR(true, []byte{0x0F, 0xBF}, dst, src)
	case 4:
		a.opRR(true, []byte{0x63}, dst, src)
	default:
		if dst != src {
			a.MovRR(dst, src)
		}
	}
}

// Movzx zero-extends the low size bytes of src into dst.
func (a *Asm) Movzx(dst, src Reg, size int64) {
	switch size {
	case 1:
		a.rex(true, dst, 0, src, false)
		a.b(0x0F, 0xB6)
		a.modrmReg(dst, src)
	case 2:
		a.opRR(true, []byte{0x0F, 0xB7}, dst, src)
	case 4:
		a.opRR(false, []byte{0x89}, src, dst) // mov r32, r32
	default:
		if dst != src {
			a.MovRR(dst, src)
		}
	}
}

// Setcc sets the low byte of r to the condition and zero-extends it.
func (a *Asm) Setcc(c Cond, r Reg) {
	a.rex(false, 0, 0, r, r >= 4)
	a.b(0x0F, 0x90+byte(c))
	a.modrmReg(0, r)
	a.Movzx(r, r, 1)
}

// SetccByte sets only the low byte.
func (a *Asm) SetccByte(c Cond, r Reg) {
	a.rex(false, 0, 0, r, r >= 4)
	a.b(0x0F, 0x90+byte(c))
	a.modrmReg(0, r)
}

// Cmov is cmovcc dst, src.
func (a *Asm) Cmov(c Cond, dst, src Reg) { a.opRR(true, []byte{0x0F, 0x40 + byte(c)}, dst, src) }

// --- Control flow -------------------------------------------------------------

func (a *Asm) Jmp(l Label) {
	a.b(0xE9)
	a.fixups = append(a.fixups, fixup{len(a.Code), l})
	a.u32(0)
}

func (a *Asm) Jcc(c Cond, l Label) {
	a.b(0x0F, 0x80+byte(c))
	a.fixups = append(a.fixups, fixup{len(a.Code), l})
	a.u32(0)
}

// Call calls a symbol (rel32).
func (a *Asm) Call(sym string) {
	a.b(0xE8)
	a.Relocs = append(a.Relocs, Reloc{len(a.Code), obj.Call32, sym})
	a.u32(0)
}

// CallR calls the address in r.
func (a *Asm) CallR(r Reg) {
	a.rex(false, 0, 0, r, false)
	a.b(0xFF)
	a.modrmReg(2, r)
}

// JmpR jumps to the address in r.
func (a *Asm) JmpR(r Reg) {
	a.rex(false, 0, 0, r, false)
	a.b(0xFF)
	a.modrmReg(4, r)
}

func (a *Asm) Ret() { a.b(0xC3) }
func (a *Asm) Ud2() { a.b(0x0F, 0x0B) }
func (a *Asm) Push(r Reg) {
	a.rex(false, 0, 0, r, false)
	a.b(0x50 + byte(r&7))
}
func (a *Asm) Pop(r Reg) {
	a.rex(false, 0, 0, r, false)
	a.b(0x58 + byte(r&7))
}

// RepMovsq copies rcx words from [rsi] to [rdi].
func (a *Asm) RepMovsq() { a.b(0xF3, 0x48, 0xA5) }

// --- SSE ----------------------------------------------------------------------

// MovqToX moves r's bits into xmm x (movq, or movd for 32 bits).
func (a *Asm) MovqToX(x, r Reg, f64 bool) {
	a.b(0x66)
	a.rex(f64, x, 0, r, false)
	a.b(0x0F, 0x6E)
	a.modrmReg(x, r)
}

// MovqFromX moves xmm x's bits into r.
func (a *Asm) MovqFromX(r, x Reg, f64 bool) {
	a.b(0x66)
	a.rex(f64, x, 0, r, false)
	a.b(0x0F, 0x7E)
	a.modrmReg(x, r)
}

func (a *Asm) sse(prefix byte, op byte, w bool, reg, rm Reg) {
	a.b(prefix)
	a.rex(w, reg, 0, rm, false)
	a.b(0x0F, op)
	a.modrmReg(reg, rm)
}

func fp(f64 bool) byte {
	if f64 {
		return 0xF2
	}
	return 0xF3
}

func (a *Asm) Addsd(d, s Reg, f64 bool) { a.sse(fp(f64), 0x58, false, d, s) }
func (a *Asm) Subsd(d, s Reg, f64 bool) { a.sse(fp(f64), 0x5C, false, d, s) }
func (a *Asm) Mulsd(d, s Reg, f64 bool) { a.sse(fp(f64), 0x59, false, d, s) }
func (a *Asm) Divsd(d, s Reg, f64 bool) { a.sse(fp(f64), 0x5E, false, d, s) }

// Ucomisd compares x with y (ucomiss for f32).
func (a *Asm) Ucomisd(x, y Reg, f64 bool) {
	if f64 {
		a.b(0x66)
	}
	a.rex(false, x, 0, y, false)
	a.b(0x0F, 0x2E)
	a.modrmReg(x, y)
}

// Cvtsi2sd converts a 64-bit integer to float.
func (a *Asm) Cvtsi2sd(x, r Reg, f64 bool) { a.sse(fp(f64), 0x2A, true, x, r) }

// Cvttsd2si converts a float to a 64-bit integer (truncating).
func (a *Asm) Cvttsd2si(r, x Reg, f64 bool) { a.sse(fp(f64), 0x2C, true, r, x) }

// Cvtss2sd widens single to double; Cvtsd2ss narrows.
func (a *Asm) Cvtss2sd(d, s Reg) { a.sse(0xF3, 0x5A, false, d, s) }
func (a *Asm) Cvtsd2ss(d, s Reg) { a.sse(0xF2, 0x5A, false, d, s) }
