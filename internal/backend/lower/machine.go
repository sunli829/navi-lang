// Package lower turns the code generation plan into machine code and data
// for any architecture: it lowers MIR to the abstract operations of a
// Machine (implemented per architecture) and lays out dictionaries,
// vtables, locations, statics and strings in a format-neutral object.
package lower

import "navi/internal/obj"

// Reg is an abstract register. Each architecture maps them to physical
// registers; the lowering only relies on the roles below.
type Reg uint8

const (
	RA   Reg = iota // address of the place being accessed
	RM              // metadata (length or vtable) of an unsized place
	V1              // first value
	V2              // second value
	T1              // temporary (copies)
	BASE            // base address kept while operands are loaded
	I1              // index and offset computations
	I2              // index computations; Load/Store may clobber it
	CTX             // call context: dictionary or closure environment
	RET             // address of the result slot
	LOC             // caller location for #[track_caller]
	FP              // frame pointer
	SP              // stack pointer
	F0              // floating point
	F1
	NumRegs
)

// Op is an integer or floating point operation.
type Op uint8

const (
	Add Op = iota
	Sub
	Mul
	And
	Or
	Xor
	Shl
	ShrU
	ShrS
	DivS
	DivU
	RemS
	RemU
	FDiv
)

// Cond is a comparison result, after Cmp(a, b).
type Cond uint8

const (
	EQ Cond = iota
	NE
	LT // signed
	LE
	GT
	GE
	LTU // unsigned
	LEU
	GTU
	GEU
)

// Label is a code position within the current function.
type Label int

// CArg describes one argument of a C call; its value is in the outgoing
// argument area at sp+8*i (integers zero- or sign-extended to 8 bytes,
// floats as their bits).
type CArg struct {
	Float bool
	F64   bool
}

// Machine emits the code of one function at a time. Operations may
// clobber I2 (large offsets) and T1 (multiplication, division); calls
// clobber every register.
type Machine interface {
	// Begin starts a new function.
	Begin()
	// Finish ends the function and returns its code and relocations
	// (offsets relative to the function start; Sec is ignored).
	Finish() ([]byte, []obj.Reloc, error)

	NewLabel() Label
	Bind(l Label)

	// Prologue saves the frame pointer and return address, sets FP and
	// reserves frame bytes (a multiple of 16) below FP. Incoming stack
	// arguments are at FP+16. Return undoes it and returns.
	Prologue(frame int64)
	Return()
	// EntryPrologue and EntryReturn0 are the same for the C entry point,
	// which also preserves the C callee-saved registers and returns 0.
	EntryPrologue(frame int64)
	EntryReturn0()

	MovImm(r Reg, v int64)
	Mov(dst, src Reg)
	// Load reads size bytes (1, 2, 4 or 8), zero-extended; Store writes
	// them. base may be FP or SP.
	Load(size int64, dst, base Reg, off int64)
	Store(size int64, src, base Reg, off int64)
	StoreZero(size int64, base Reg, off int64)
	// AddrOf sets dst = base + off; AddImm adds a constant to r.
	AddrOf(dst, base Reg, off int64)
	AddImm(r Reg, v int64)
	// AdrSym loads the address of a symbol.
	AdrSym(dst Reg, sym string)
	SignExt(dst, src Reg, size int64)
	ZeroExt(dst, src Reg, size int64)

	// Arith sets dst = a op b on 64-bit values. Division by zero must
	// have been checked.
	Arith(op Op, dst, a, b Reg)
	// ArithOvf is Add, Sub or Mul on 64-bit values that jumps to ovf when
	// the result overflows (signed or unsigned).
	ArithOvf(op Op, signed bool, dst, a, b Reg, ovf Label)
	// MulAdd sets dst = c + a*b.
	MulAdd(dst, a, b, c Reg)
	Neg(dst, src Reg)
	Not(dst, src Reg)

	Cmp(a, b Reg)
	CmpImm(a Reg, v int64)
	SetCond(dst Reg, c Cond)
	BranchIf(c Cond, l Label)
	Select(dst, a, b Reg, c Cond) // dst = c ? a : b
	Jump(l Label)
	BranchZero(r Reg, l Label)
	BranchNonZero(r Reg, l Label)

	// Floating point: F0 and F1 are the operands.
	FMovTo(f, r Reg, f64 bool)
	FMovFrom(r, f Reg, f64 bool)
	FArith(op Op, dst, a, b Reg, f64 bool) // Add, Sub, Mul, FDiv
	FNeg(dst, src Reg, f64 bool)
	// FCompare sets dst to a c b (EQ, NE, LT, LE, GT, GE); comparisons
	// with NaN are false except NE.
	FCompare(dst Reg, c Cond, a, b Reg, f64 bool)
	IntToFloat(f, r Reg, signed, f64 bool)
	// FloatToInt converts to a 64-bit integer with Rust's saturating
	// semantics (NaN becomes 0).
	FloatToInt(r, f Reg, signed, f64 bool)
	FloatConv(dst, src Reg, toF64 bool)

	// Call calls a symbol, CallReg the code address in a register.
	Call(sym string)
	CallReg(r Reg)
	// CallC calls a C function (sym, or the address in fn if sym is "")
	// with the arguments in the outgoing area; an integer result is left
	// in V1, a float result's bits too.
	CallC(sym string, fn Reg, args []CArg, floatRet, f64 bool)
	Trap()

	// CopyWords copies n 8-byte words from [src] to [dst] (both
	// advanced); clobbers T1, V2, I1, I2.
	CopyWords(dst, src Reg, n int64)
	// RunDefers emits the body of the deferred call runner (see lower.go).
	RunDefers()
	// SwapContext emits the body of the context switch routine: it is
	// called with the C convention with two arguments, save and load, each
	// pointing at three words (sp, fp, pc); it saves the caller's context
	// in save and resumes the one in load.
	SwapContext()
}
