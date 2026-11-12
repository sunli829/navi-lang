package arm64

import (
	"navi/internal/backend/lower"
	"navi/internal/obj"
)

// Machine implements lower.Machine for AArch64 (AAPCS64 for C calls).
type Machine struct{ a *Asm }

// NewMachine returns an arm64 machine.
func NewMachine() *Machine { return &Machine{} }

var regs = [lower.NumRegs]Reg{
	lower.RA: X10, lower.RM: X11, lower.V1: X12, lower.V2: X13, lower.T1: X14,
	lower.BASE: X15, lower.I1: X16, lower.I2: X17, lower.CTX: X0, lower.RET: X8,
	lower.LOC: X9, lower.FP: FP, lower.SP: SP, lower.F0: 0, lower.F1: 1,
}

func r(x lower.Reg) Reg { return regs[x] }

var conds = [...]Cond{
	lower.EQ: EQ, lower.NE: NE, lower.LT: LT, lower.LE: LE, lower.GT: GT, lower.GE: GE,
	lower.LTU: LO, lower.LEU: LS, lower.GTU: HI, lower.GEU: HS,
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
	return m.a.Bytes(), out, nil
}

func (m *Machine) NewLabel() lower.Label { return lower.Label(m.a.NewLabel()) }
func (m *Machine) Bind(l lower.Label)    { m.a.Bind(Label(l)) }

func (m *Machine) Prologue(frame int64) {
	a := m.a
	a.StpPre(FP, LR, SP, -16)
	a.MovSP(FP, SP)
	if frame < 4096 {
		a.SubImm(SP, SP, frame)
	} else {
		a.MovImm(X16, frame)
		a.SubExt(SP, SP, X16)
	}
}

func (m *Machine) Return() {
	m.a.MovSP(SP, FP)
	m.a.LdpPost(FP, LR, SP, 16)
	m.a.Ret()
}

// Generated code only uses x0-x17 and d0-d1, so the entry point has no
// C callee-saved registers to preserve.
func (m *Machine) EntryPrologue(frame int64) { m.Prologue(frame) }

func (m *Machine) EntryReturn0() {
	m.a.MovImm(X0, 0)
	m.Return()
}

func (m *Machine) MovImm(x lower.Reg, v int64) { m.a.MovImm(r(x), v) }

func (m *Machine) Mov(dst, src lower.Reg) {
	if dst == lower.SP || src == lower.SP {
		m.a.MovSP(r(dst), r(src))
		return
	}
	m.a.Mov(r(dst), r(src))
}

func (m *Machine) Load(size int64, dst, base lower.Reg, off int64) {
	m.a.Ldr(size, r(dst), r(base), off)
}
func (m *Machine) Store(size int64, src, base lower.Reg, off int64) {
	m.a.Str(size, r(src), r(base), off)
}
func (m *Machine) StoreZero(size int64, base lower.Reg, off int64) {
	m.a.Str(size, ZR, r(base), off)
}
func (m *Machine) AddrOf(dst, base lower.Reg, off int64) { m.a.AddrOf(r(dst), r(base), off) }

func (m *Machine) AddImm(x lower.Reg, v int64) {
	switch {
	case v >= 0 && v < 4096:
		m.a.AddImm(r(x), r(x), v)
	case v < 0 && v > -4096:
		m.a.SubImm(r(x), r(x), -v)
	default:
		m.a.MovImm(X17, v)
		m.a.Add(r(x), r(x), X17)
	}
}

func (m *Machine) AdrSym(dst lower.Reg, sym string)         { m.a.AdrSym(r(dst), sym) }
func (m *Machine) SignExt(dst, src lower.Reg, size int64)   { m.a.SignExt(r(dst), r(src), size) }
func (m *Machine) ZeroExt(dst, src lower.Reg, size int64)   { m.a.ZeroExt(r(dst), r(src), size) }
func (m *Machine) MulAdd(dst, x, y, c lower.Reg)            { m.a.Madd(r(dst), r(x), r(y), r(c)) }
func (m *Machine) Neg(dst, src lower.Reg)                   { m.a.Sub(r(dst), ZR, r(src)) }
func (m *Machine) Not(dst, src lower.Reg)                   { m.a.Mvn(r(dst), r(src)) }
func (m *Machine) Cmp(x, y lower.Reg)                       { m.a.Cmp(r(x), r(y)) }
func (m *Machine) SetCond(dst lower.Reg, c lower.Cond)      { m.a.Cset(r(dst), conds[c]) }
func (m *Machine) BranchIf(c lower.Cond, l lower.Label)     { m.a.BCond(conds[c], Label(l)) }
func (m *Machine) Select(dst, x, y lower.Reg, c lower.Cond) { m.a.Csel(r(dst), r(x), r(y), conds[c]) }
func (m *Machine) Jump(l lower.Label)                       { m.a.B(Label(l)) }
func (m *Machine) BranchZero(x lower.Reg, l lower.Label)    { m.a.Cbz(r(x), Label(l)) }
func (m *Machine) BranchNonZero(x lower.Reg, l lower.Label) { m.a.Cbnz(r(x), Label(l)) }
func (m *Machine) FMovTo(f, x lower.Reg, f64 bool)          { m.a.FmovToF(r(f), r(x), f64) }
func (m *Machine) FMovFrom(x, f lower.Reg, f64 bool)        { m.a.FmovFromF(r(x), r(f), f64) }
func (m *Machine) FNeg(dst, src lower.Reg, f64 bool)        { m.a.Fneg(r(dst), r(src), f64) }
func (m *Machine) Call(sym string)                          { m.a.Bl(sym) }
func (m *Machine) CallReg(x lower.Reg)                      { m.a.Blr(r(x)) }
func (m *Machine) Trap()                                    { m.a.Brk(1) }
func (m *Machine) IntToFloat(f, x lower.Reg, signed, f64 bool) {
	if signed {
		m.a.Scvtf(r(f), r(x), f64)
	} else {
		m.a.Ucvtf(r(f), r(x), f64)
	}
}

// FloatToInt: fcvtzs/fcvtzu already saturate and map NaN to 0.
func (m *Machine) FloatToInt(x, f lower.Reg, signed, f64 bool) {
	if signed {
		m.a.Fcvtzs(r(x), r(f), f64)
	} else {
		m.a.Fcvtzu(r(x), r(f), f64)
	}
}

func (m *Machine) FloatConv(dst, src lower.Reg, toF64 bool) {
	if toF64 {
		m.a.FcvtDS(r(dst), r(src))
	} else {
		m.a.FcvtSD(r(dst), r(src))
	}
}

func (m *Machine) CmpImm(x lower.Reg, v int64) {
	switch {
	case v >= 0 && v < 4096:
		m.a.CmpImm(r(x), v)
	case v < 0 && v > -4096:
		m.a.CmnImm(r(x), -v)
	default:
		m.a.MovImm(X17, v)
		m.a.Cmp(r(x), X17)
	}
}

func (m *Machine) Arith(op lower.Op, dst, x, y lower.Reg) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	switch op {
	case lower.Add:
		a.Add(d, n, k)
	case lower.Sub:
		a.Sub(d, n, k)
	case lower.Mul:
		a.Mul(d, n, k)
	case lower.And:
		a.And(d, n, k)
	case lower.Or:
		a.Orr(d, n, k)
	case lower.Xor:
		a.Eor(d, n, k)
	case lower.Shl:
		a.Lslv(d, n, k)
	case lower.ShrU:
		a.Lsrv(d, n, k)
	case lower.ShrS:
		a.Asrv(d, n, k)
	case lower.DivS:
		a.Sdiv(d, n, k)
	case lower.DivU:
		a.Udiv(d, n, k)
	case lower.RemS:
		a.Sdiv(X17, n, k)
		a.Msub(d, X17, k, n)
	case lower.RemU:
		a.Udiv(X17, n, k)
		a.Msub(d, X17, k, n)
	}
}

func (m *Machine) ArithOvf(op lower.Op, signed bool, dst, x, y lower.Reg, ovf lower.Label) {
	a := m.a
	d, n, k := r(dst), r(x), r(y)
	switch op {
	case lower.Add:
		a.Adds(d, n, k)
		if signed {
			a.BCond(VS, Label(ovf))
		} else {
			a.BCond(HS, Label(ovf))
		}
	case lower.Sub:
		a.Subs(d, n, k)
		if signed {
			a.BCond(VS, Label(ovf))
		} else {
			a.BCond(LO, Label(ovf))
		}
	case lower.Mul:
		a.Mul(X17, n, k)
		if signed {
			a.Smulh(X14, n, k)
			a.CmpAsr63(X14, X17)
			a.BCond(NE, Label(ovf))
		} else {
			a.Umulh(X14, n, k)
			a.Cbnz(X14, Label(ovf))
		}
		a.Mov(d, X17)
	}
}

func (m *Machine) FArith(op lower.Op, dst, x, y lower.Reg, f64 bool) {
	switch op {
	case lower.Add:
		m.a.Fadd(r(dst), r(x), r(y), f64)
	case lower.Sub:
		m.a.Fsub(r(dst), r(x), r(y), f64)
	case lower.Mul:
		m.a.Fmul(r(dst), r(x), r(y), f64)
	case lower.FDiv:
		m.a.Fdiv(r(dst), r(x), r(y), f64)
	}
}

// FCompare: after fcmp an unordered result sets C and V, so mi, ls, gt and
// ge are false for NaN, and ne is true.
func (m *Machine) FCompare(dst lower.Reg, c lower.Cond, x, y lower.Reg, f64 bool) {
	m.a.Fcmp(r(x), r(y), f64)
	fc := map[lower.Cond]Cond{lower.EQ: EQ, lower.NE: NE, lower.LT: MI, lower.LE: LS, lower.GT: GT, lower.GE: GE}[c]
	m.a.Cset(r(dst), fc)
}

func (m *Machine) CallC(sym string, fn lower.Reg, args []lower.CArg, floatRet, f64 bool) {
	a := m.a
	ints, floats := 0, 0
	for i, arg := range args {
		if arg.Float {
			a.Ldr(8, X17, SP, int64(8*i))
			a.FmovToF(Reg(floats), X17, arg.F64)
			floats++
		} else {
			a.Ldr(8, Reg(ints), SP, int64(8*i))
			ints++
		}
	}
	if sym != "" {
		a.Bl(sym)
	} else {
		a.Blr(r(fn))
	}
	if floatRet {
		a.FmovFromF(X12, 0, f64)
	} else {
		a.Mov(X12, X0)
	}
}

func (m *Machine) CopyWords(dst, src lower.Reg, n int64) {
	a := m.a
	a.MovImm(X13, n)
	loop := a.NewLabel()
	a.Bind(loop)
	a.LdrPost(X14, r(src), 8)
	a.StrPost(X14, r(dst), 8)
	a.SubsImm(X13, X13, 1)
	a.BCond(NE, loop)
}

// RunDefers: x0 = list head. Uses x19/x20 (saved) to survive the calls.
// The frame has the standard fixed slots, with an empty defer list at
// x29-32 so unwinding can walk through it.
func (m *Machine) RunDefers() {
	a := m.a
	a.StpPre(FP, LR, SP, -16)
	a.MovSP(FP, SP)
	a.SubImm(SP, SP, 48)
	a.Str(8, 19, FP, -8)
	a.Str(8, 20, FP, -16)
	a.Str(8, ZR, FP, -32)
	a.Mov(19, X0)
	loop, done, copyLoop, copied := a.NewLabel(), a.NewLabel(), a.NewLabel(), a.NewLabel()
	a.Bind(loop)
	a.Cbz(19, done)
	a.Ldr(8, X10, 19, 24)
	a.Ldr(8, X11, 19, 32)
	a.Add(X12, X10, X11)
	a.AddImm(X12, X12, 15)
	a.LsrImm(X12, X12, 4)
	a.LslImm(X12, X12, 4)
	a.SubExt(SP, SP, X12)
	a.MovSP(X17, SP)
	a.AddImm(X16, 19, 48)
	a.LsrImm(X13, X10, 3)
	a.Cbz(X13, copied)
	a.Bind(copyLoop)
	a.LdrPost(X14, X16, 8)
	a.StrPost(X14, X17, 8)
	a.SubsImm(X13, X13, 1)
	a.BCond(NE, copyLoop)
	a.Bind(copied)
	a.AddExt(X8, SP, X10)
	a.Ldr(8, X9, 19, 40)
	a.Ldr(8, X0, 19, 16)
	a.Ldr(8, X16, 19, 8)
	a.Ldr(8, 20, 19, 0)
	a.Blr(X16)
	a.SubImm(SP, FP, 48)
	a.Mov(19, 20)
	a.B(loop)
	a.Bind(done)
	a.Ldr(8, 19, FP, -8)
	a.Ldr(8, 20, FP, -16)
	a.MovSP(SP, FP)
	a.LdpPost(FP, LR, SP, 16)
	a.Ret()
}
