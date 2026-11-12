package amd64

import (
	"navi/internal/backend/lower"
	"navi/internal/obj"
)

// Machine implements lower.Machine for x86-64. C calls follow the System
// V ABI, or the Windows x64 ABI when Win64 is set.
type Machine struct {
	a     *Asm
	Win64 bool
}

// NewMachine returns an amd64 machine.
func NewMachine(win64 bool) *Machine { return &Machine{Win64: win64} }

var regs = [lower.NumRegs]Reg{
	lower.RA: R10, lower.RM: R11, lower.V1: RAX, lower.V2: RCX, lower.T1: RDX,
	lower.BASE: RBX, lower.I1: R8, lower.I2: R9, lower.CTX: R12, lower.RET: R13,
	lower.LOC: R14, lower.FP: RBP, lower.SP: RSP, lower.F0: 0, lower.F1: 1,
}

func r(x lower.Reg) Reg { return regs[x] }

var conds = [...]Cond{
	lower.EQ: CE, lower.NE: CNE, lower.LT: CL, lower.LE: CLE, lower.GT: CG, lower.GE: CGE,
	lower.LTU: CB, lower.LEU: CBE, lower.GTU: CA, lower.GEU: CAE,
}

func (m *Machine) Begin() { m.a = &Asm{} }

func (m *Machine) Finish() ([]byte, []obj.Reloc, error) {
	if err := m.a.Finish(); err != nil {
		return nil, nil, err
	}
	var out []obj.Reloc
	for _, rl := range m.a.Relocs {
		out = append(out, obj.Reloc{Off: uint32(rl.Off), Kind: rl.Kind, Sym: rl.Sym})
	}
	return m.a.Code, out, nil
}

func (m *Machine) NewLabel() lower.Label { return lower.Label(m.a.NewLabel()) }
func (m *Machine) Bind(l lower.Label)    { m.a.Bind(Label(l)) }

func (m *Machine) Prologue(frame int64) {
	m.a.Push(RBP)
	m.a.MovRR(RBP, RSP)
	m.reserve(frame)
}

// reserve lowers rsp by frame bytes. Windows commits the stack one guard
// page at a time, so large frames touch every page on the way down.
func (m *Machine) reserve(frame int64) {
	if m.Win64 {
		for frame >= 4096 {
			m.a.SubImm(RSP, 4096)
			m.a.Load(8, R11, RSP, 0)
			frame -= 4096
		}
	}
	if frame > 0 {
		m.a.SubImm(RSP, frame)
	}
}

func (m *Machine) Return() {
	m.a.MovRR(RSP, RBP)
	m.a.Pop(RBP)
	m.a.Ret()
}

// The entry point preserves the registers that are callee-saved in the C
// ABIs and that generated code uses: rbx, rsi, rdi, r12-r15.
var saved = []Reg{RBX, RSI, RDI, R12, R13, R14, R15}

func (m *Machine) EntryPrologue(frame int64) {
	m.a.Push(RBP)
	m.a.MovRR(RBP, RSP)
	for _, s := range saved {
		m.a.Push(s)
	}
	m.reserve(8 + frame) // 7 pushes + 8 keep rsp 16-byte aligned
}

func (m *Machine) EntryReturn0() {
	m.a.Lea(RSP, RBP, -int64(8*len(saved)))
	for i := len(saved) - 1; i >= 0; i-- {
		m.a.Pop(saved[i])
	}
	m.a.MovImm(RAX, 0)
	m.a.Pop(RBP)
	m.a.Ret()
}

func (m *Machine) MovImm(x lower.Reg, v int64) { m.a.MovImm(r(x), v) }

func (m *Machine) Mov(dst, src lower.Reg) {
	if dst != src {
		m.a.MovRR(r(dst), r(src))
	}
}

func (m *Machine) Load(size int64, dst, base lower.Reg, off int64) {
	m.a.Load(size, r(dst), r(base), off)
}
func (m *Machine) Store(size int64, src, base lower.Reg, off int64) {
	m.a.Store(size, r(src), r(base), off)
}
func (m *Machine) StoreZero(size int64, base lower.Reg, off int64) {
	m.a.StoreZero(size, r(base), off)
}
func (m *Machine) AddrOf(dst, base lower.Reg, off int64) { m.a.Lea(r(dst), r(base), off) }

func (m *Machine) AddImm(x lower.Reg, v int64) {
	if v >= -1<<31 && v < 1<<31 {
		m.a.AddImm(r(x), v)
		return
	}
	m.a.MovImm(R9, v)
	m.a.Add(r(x), R9)
}

func (m *Machine) AdrSym(dst lower.Reg, sym string)       { m.a.LeaSym(r(dst), sym) }
func (m *Machine) SignExt(dst, src lower.Reg, size int64) { m.a.Movsx(r(dst), r(src), size) }
func (m *Machine) ZeroExt(dst, src lower.Reg, size int64) { m.a.Movzx(r(dst), r(src), size) }

func (m *Machine) Neg(dst, src lower.Reg) {
	m.Mov(dst, src)
	m.a.Neg(r(dst))
}

func (m *Machine) Not(dst, src lower.Reg) {
	m.Mov(dst, src)
	m.a.Not(r(dst))
}

// binary computes dst = x op y with a two-operand instruction emit(dst,
// src), using R9 when dst aliases y.
func (m *Machine) binary(dst, x, y Reg, emit func(d, s Reg), commutative bool) {
	switch {
	case dst == x:
		emit(dst, y)
	case dst == y && commutative:
		emit(dst, x)
	case dst == y:
		m.a.MovRR(R9, y)
		m.a.MovRR(dst, x)
		emit(dst, R9)
	default:
		m.a.MovRR(dst, x)
		emit(dst, y)
	}
}

func (m *Machine) Arith(op lower.Op, dst, x, y lower.Reg) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	switch op {
	case lower.Add:
		m.binary(d, n, k, a.Add, true)
	case lower.Sub:
		m.binary(d, n, k, a.Sub, false)
	case lower.Mul:
		m.binary(d, n, k, a.Imul, true)
	case lower.And:
		m.binary(d, n, k, a.And, true)
	case lower.Or:
		m.binary(d, n, k, a.Or, true)
	case lower.Xor:
		m.binary(d, n, k, a.Xor, true)
	case lower.Shl, lower.ShrU, lower.ShrS:
		// The count must be in cl.
		if k != RCX {
			a.MovRR(R9, k)
			if n == RCX {
				a.MovRR(RCX, R9)
				a.MovRR(R9, n)
				n = R9
			} else {
				a.MovRR(RCX, R9)
			}
		}
		if d != n {
			if d == RCX {
				a.MovRR(R9, n)
				n = R9
				m.shift(op, R9)
				a.MovRR(d, R9)
				return
			}
			a.MovRR(d, n)
		}
		m.shift(op, d)
	case lower.DivS, lower.DivU, lower.RemS, lower.RemU:
		// rdx:rax / divisor; the divisor must not be rax or rdx.
		div := k
		if div == RAX || div == RDX {
			a.MovRR(R9, div)
			div = R9
		}
		if n != RAX {
			a.MovRR(RAX, n)
		}
		if op == lower.DivS || op == lower.RemS {
			a.Cqo()
			a.IdivRAX(div)
		} else {
			a.MovImm(RDX, 0)
			a.DivRAX(div)
		}
		res := RAX
		if op == lower.RemS || op == lower.RemU {
			res = RDX
		}
		if d != res {
			a.MovRR(d, res)
		}
	}
}

func (m *Machine) shift(op lower.Op, d Reg) {
	switch op {
	case lower.Shl:
		m.a.ShlCL(d)
	case lower.ShrU:
		m.a.ShrCL(d)
	default:
		m.a.SarCL(d)
	}
}

func (m *Machine) ArithOvf(op lower.Op, signed bool, dst, x, y lower.Reg, ovf lower.Label) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	jump := func() {
		if signed {
			a.Jcc(CO, Label(ovf))
		} else {
			a.Jcc(CB, Label(ovf))
		}
	}
	switch op {
	case lower.Add:
		m.binary(d, n, k, a.Add, true)
		jump()
	case lower.Sub:
		m.binary(d, n, k, a.Sub, false)
		jump()
	case lower.Mul:
		if signed {
			m.binary(d, n, k, a.Imul, true)
			a.Jcc(CO, Label(ovf))
			return
		}
		mul := k
		if mul == RAX || mul == RDX {
			a.MovRR(R9, mul)
			mul = R9
		}
		if n != RAX {
			a.MovRR(RAX, n)
		}
		a.MulRAX(mul)
		a.Jcc(CO, Label(ovf))
		if d != RAX {
			a.MovRR(d, RAX)
		}
	}
}

// MulAdd sets dst = c + x*y, computing the product in rdx or r9.
func (m *Machine) MulAdd(dst, x, y, c lower.Reg) {
	a := m.a
	d, n, k, cc := r(dst), r(x), r(y), r(c)
	tmp := RDX
	if n == RDX || k == RDX || cc == RDX {
		tmp = R9
	}
	a.MovRR(tmp, n)
	a.Imul(tmp, k)
	if d != cc {
		a.MovRR(d, cc)
	}
	a.Add(d, tmp)
}

func (m *Machine) Cmp(x, y lower.Reg) { m.a.Cmp(r(x), r(y)) }

func (m *Machine) CmpImm(x lower.Reg, v int64) {
	if v >= -1<<31 && v < 1<<31 {
		m.a.CmpImm(r(x), v)
		return
	}
	m.a.MovImm(R9, v)
	m.a.Cmp(r(x), R9)
}

func (m *Machine) SetCond(dst lower.Reg, c lower.Cond) { m.a.Setcc(conds[c], r(dst)) }
func (m *Machine) BranchIf(c lower.Cond, l lower.Label) {
	m.a.Jcc(conds[c], Label(l))
}

func (m *Machine) Select(dst, x, y lower.Reg, c lower.Cond) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	cc := conds[c]
	switch {
	case d == n && d == k:
	case d == n:
		a.Cmov(cc^1, d, k)
	default:
		if d != k {
			a.MovRR(d, k)
		}
		a.Cmov(cc, d, n)
	}
}

func (m *Machine) Jump(l lower.Label) { m.a.Jmp(Label(l)) }

func (m *Machine) BranchZero(x lower.Reg, l lower.Label) {
	m.a.Test(r(x), r(x))
	m.a.Jcc(CE, Label(l))
}

func (m *Machine) BranchNonZero(x lower.Reg, l lower.Label) {
	m.a.Test(r(x), r(x))
	m.a.Jcc(CNE, Label(l))
}

func (m *Machine) FMovTo(f, x lower.Reg, f64 bool)   { m.a.MovqToX(r(f), r(x), f64) }
func (m *Machine) FMovFrom(x, f lower.Reg, f64 bool) { m.a.MovqFromX(r(x), r(f), f64) }

func (m *Machine) FArith(op lower.Op, dst, x, y lower.Reg, f64 bool) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	if d != n {
		// Only F0 = F0 op F1 is used.
		panic("amd64: FArith with dst != x")
	}
	switch op {
	case lower.Add:
		a.Addsd(d, k, f64)
	case lower.Sub:
		a.Subsd(d, k, f64)
	case lower.Mul:
		a.Mulsd(d, k, f64)
	case lower.FDiv:
		a.Divsd(d, k, f64)
	}
}

// FNeg flips the sign bit through r9.
func (m *Machine) FNeg(dst, src lower.Reg, f64 bool) {
	a := m.a
	a.MovqFromX(R9, r(src), f64)
	if f64 {
		a.Btc(R9, 63)
	} else {
		a.Btc(R9, 31)
	}
	a.MovqToX(r(dst), R9, f64)
}

// FCompare uses ucomisd, whose unordered result sets ZF, PF and CF:
// a > b is "above", a >= b "above or equal" (both false for NaN), < and
// <= swap the operands, == needs ZF and not PF, != ZF clear or PF.
func (m *Machine) FCompare(dst lower.Reg, c lower.Cond, x, y lower.Reg, f64 bool) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	switch c {
	case lower.GT:
		a.Ucomisd(n, k, f64)
		a.Setcc(CA, d)
	case lower.GE:
		a.Ucomisd(n, k, f64)
		a.Setcc(CAE, d)
	case lower.LT:
		a.Ucomisd(k, n, f64)
		a.Setcc(CA, d)
	case lower.LE:
		a.Ucomisd(k, n, f64)
		a.Setcc(CAE, d)
	case lower.EQ:
		a.Ucomisd(n, k, f64)
		a.Setcc(CE, d)
		a.Setcc(CNP, R9)
		a.And(d, R9)
	case lower.NE:
		a.Ucomisd(n, k, f64)
		a.Setcc(CNE, d)
		a.Setcc(CP, R9)
		a.Or(d, R9)
	}
}

// IntToFloat: unsigned values with the top bit set are halved (keeping
// the low bit for rounding), converted and doubled.
func (m *Machine) IntToFloat(f, x lower.Reg, signed, f64 bool) {
	a := m.a
	fr, n := r(f), r(x)
	if signed {
		a.Cvtsi2sd(fr, n, f64)
		return
	}
	big, done := a.NewLabel(), a.NewLabel()
	a.Test(n, n)
	a.Jcc(CS, big)
	a.Cvtsi2sd(fr, n, f64)
	a.Jmp(done)
	a.Bind(big)
	a.MovRR(R9, n)
	a.ShrImm(R9, 1)
	a.MovRR(RDX, n)
	a.AndImm(RDX, 1)
	a.Or(R9, RDX)
	a.Cvtsi2sd(fr, R9, f64)
	a.Addsd(fr, fr, f64)
	a.Bind(done)
}

// FloatToInt implements Rust's saturating float to integer conversion on
// top of cvttsd2si, which yields 0x8000000000000000 for NaN and values
// out of range. Clobbers r9 and xmm1.
func (m *Machine) FloatToInt(x, f lower.Reg, signed, f64 bool) {
	a := m.a
	d, fr := r(x), r(f)
	done, zero := a.NewLabel(), a.NewLabel()
	if signed {
		a.Cvttsd2si(d, fr, f64)
		a.MovImm(R9, -1<<63)
		a.Cmp(d, R9)
		a.Jcc(CNE, done)
		a.Ucomisd(fr, fr, f64)
		a.Jcc(CP, zero)
		a.MovqFromX(R9, fr, f64)
		if f64 {
			a.Test(R9, R9)
		} else {
			a.Test32(R9, R9)
		}
		a.Jcc(CS, done) // negative: the minimum is right
		a.MovImm(d, 1<<63-1)
		a.Jmp(done)
		a.Bind(zero)
		a.MovImm(d, 0)
		a.Bind(done)
		return
	}
	big, max := a.NewLabel(), a.NewLabel()
	a.Ucomisd(fr, fr, f64)
	a.Jcc(CP, zero)
	// xmm1 = 2^63
	if f64 {
		a.MovImm(R9, 0x43E0000000000000)
	} else {
		a.MovImm(R9, 0x5F000000)
	}
	a.MovqToX(1, R9, f64)
	a.Ucomisd(fr, 1, f64)
	a.Jcc(CAE, big)
	a.Cvttsd2si(d, fr, f64)
	a.Test(d, d)
	a.Jcc(CNS, done)
	a.Jmp(zero)
	a.Bind(big)
	a.Subsd(fr, 1, f64)
	a.Cvttsd2si(d, fr, f64)
	a.MovImm(R9, -1<<63)
	a.Cmp(d, R9)
	a.Jcc(CE, max)
	a.Btc(d, 63)
	a.Jmp(done)
	a.Bind(max)
	a.MovImm(d, -1)
	a.Jmp(done)
	a.Bind(zero)
	a.MovImm(d, 0)
	a.Bind(done)
}

func (m *Machine) FloatConv(dst, src lower.Reg, toF64 bool) {
	if toF64 {
		m.a.Cvtss2sd(r(dst), r(src))
	} else {
		m.a.Cvtsd2ss(r(dst), r(src))
	}
}

func (m *Machine) Call(sym string)     { m.a.Call(sym) }
func (m *Machine) CallReg(x lower.Reg) { m.a.CallR(r(x)) }
func (m *Machine) Trap()               { m.a.Ud2() }

var sysvInts = []Reg{RDI, RSI, RDX, RCX, R8, R9}
var winInts = []Reg{RCX, RDX, R8, R9}

func (m *Machine) CallC(sym string, fn lower.Reg, args []lower.CArg, floatRet, f64 bool) {
	a := m.a
	ints, floats := 0, 0
	for i, arg := range args {
		off := int64(8 * i)
		if m.Win64 {
			// Positional: argument i uses rcx/rdx/r8/r9 or xmm i.
			if i >= 4 {
				panic("amd64: more than 4 C arguments on Windows")
			}
			if arg.Float {
				a.Load(8, R11, RSP, off)
				a.MovqToX(Reg(i), R11, arg.F64)
			} else {
				a.Load(8, winInts[i], RSP, off)
			}
			continue
		}
		if arg.Float {
			a.Load(8, R11, RSP, off)
			a.MovqToX(Reg(floats), R11, arg.F64)
			floats++
		} else {
			if ints >= len(sysvInts) {
				panic("amd64: more than 6 integer C arguments")
			}
			a.Load(8, sysvInts[ints], RSP, off)
			ints++
		}
	}
	if m.Win64 {
		a.SubImm(RSP, 32) // shadow space
	}
	if sym != "" {
		a.Call(sym)
	} else {
		a.CallR(r(fn))
	}
	if m.Win64 {
		a.AddImm(RSP, 32)
	}
	if floatRet {
		a.MovqFromX(RAX, 0, f64)
	}
}

// CopyWords copies with rep movsq (rcx is V2; rsi/rdi are not used by
// the lowering).
func (m *Machine) CopyWords(dst, src lower.Reg, n int64) {
	a := m.a
	a.MovRR(RDI, r(dst))
	a.MovRR(RSI, r(src))
	a.MovImm(RCX, n)
	a.RepMovsq()
	a.MovRR(r(dst), RDI)
	a.MovRR(r(src), RSI)
}

// RunDefers: r12 = list head. The current record is kept in the frame
// (rbp-40) across calls; rbp-32 is the frame's (empty) defer list.
func (m *Machine) RunDefers() {
	a := m.a
	a.Push(RBP)
	a.MovRR(RBP, RSP)
	a.SubImm(RSP, 48)
	a.StoreZero(8, RBP, -32)
	a.Store(8, R12, RBP, -40)
	loop, done := a.NewLabel(), a.NewLabel()
	a.Bind(loop)
	a.Load(8, RBX, RBP, -40)
	a.Test(RBX, RBX)
	a.Jcc(CE, done)
	a.Load(8, R10, RBX, 24) // argsize
	a.Load(8, R11, RBX, 32) // retsize
	a.MovRR(RAX, R10)
	a.Add(RAX, R11)
	a.AddImm(RAX, 15)
	a.AndImm(RAX, -16)
	a.Sub(RSP, RAX)
	a.MovRR(RDI, RSP)
	a.Lea(RSI, RBX, 48)
	a.MovRR(RCX, R10)
	a.ShrImm(RCX, 3)
	a.RepMovsq()
	a.MovRR(R13, RSP)
	a.Add(R13, R10)
	a.Load(8, R14, RBX, 40)
	a.Load(8, R12, RBX, 16)
	a.Load(8, RAX, RBX, 0)
	a.Store(8, RAX, RBP, -40)
	a.Load(8, RAX, RBX, 8)
	a.CallR(RAX)
	a.Lea(RSP, RBP, -48)
	a.Jmp(loop)
	a.Bind(done)
	a.MovRR(RSP, RBP)
	a.Pop(RBP)
	a.Ret()
}

// SwapContext: the first two C arguments are save and load (sp, fp, pc).
// The return address is popped so that the saved sp is the caller's.
func (m *Machine) SwapContext() {
	a := m.a
	save, load := RDI, RSI
	if m.Win64 {
		save, load = RCX, RDX
	}
	a.Pop(RAX)
	a.Store(8, RSP, save, 0)
	a.Store(8, RBP, save, 8)
	a.Store(8, RAX, save, 16)
	a.Load(8, RSP, load, 0)
	a.Load(8, RBP, load, 8)
	a.Load(8, RAX, load, 16)
	a.JmpR(RAX)
}
