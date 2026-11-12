// Package mir defines navi's mid-level IR: functions made of basic blocks
// over typed locals and places, close to Rust's MIR. Functions are generic;
// the interpreter runs them with concrete types per frame and the backend
// instantiates them per GC shape.
package mir

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// Func is a function body.
type Func struct {
	Name     string
	Def      *resolve.Def // the item (nil for closures and shims)
	Owner    *resolve.Def // whose generic parameters are in scope
	Locals   []*Local     // 0 is the return place, 1..NArgs the arguments
	NArgs    int
	Blocks   []*Block
	File     *syntax.File
	Closure  bool // argument 1 is the environment pointer
	Captures []*types.Type
	// Unwind marks locals whose defers must run at return (set by PushDefer).
	HasDefer bool
}

// Local is a local variable or temporary.
type Local struct {
	Type *types.Type
	Name string // "" for temporaries
	// Boxed locals are captured by closures: they live in a heap cell and
	// the local holds a pointer to it.
	Boxed bool
}

// Block is a basic block.
type Block struct {
	Stmts []Stmt
	Term  Term
}

// ProjKind is a place projection.
type ProjKind uint8

const (
	PDeref    ProjKind = iota // *place
	PField                    // place.N (structs, tuples, closure envs)
	PIndex                    // place[local]
	PDowncast                 // place as variant V (then fields of V)
)

// Proj is one projection step.
type Proj struct {
	Kind    ProjKind
	Field   int
	Index   int // local holding the index (PIndex)
	Variant int
}

// Place is a memory location: a local followed by projections.
type Place struct {
	Local int
	Proj  []Proj
}

// LocalPlace returns the place of a local.
func LocalPlace(l int) Place { return Place{Local: l} }

// With returns p extended by a projection.
func (p Place) With(pr Proj) Place {
	out := Place{Local: p.Local, Proj: make([]Proj, len(p.Proj), len(p.Proj)+1)}
	copy(out.Proj, p.Proj)
	out.Proj = append(out.Proj, pr)
	return out
}

// Deref returns *p.
func (p Place) Deref() Place { return p.With(Proj{Kind: PDeref}) }

// Field returns p.i.
func (p Place) Field(i int) Place { return p.With(Proj{Kind: PField, Field: i}) }

// ConstKind is the kind of a constant.
type ConstKind uint8

const (
	CInt ConstKind = iota
	CFloat
	CBool
	CChar
	CStr   // a &str literal
	CBytes // a &[u8] literal
	CUnit
	CFn     // a function value (FnRef)
	CStatic // the address of a static: &STATIC
	CZero   // the zero value of Type (unit structs, never used values)
)

// Const is a constant operand.
type Const struct {
	Kind   ConstKind
	Int    int64
	Float  float64
	Str    string
	Fn     *FnRef
	Static *resolve.Def
	Type   *types.Type
}

// FnRef names a function instance: an item with generic arguments, a trait
// method (resolved to an impl when types are concrete), a closure body or a
// compiler-generated shim.
type FnRef struct {
	Def  *resolve.Def
	Args []*types.Type // arguments for all generics in scope of Def
	// Self is the self type for trait methods.
	Self    *types.Type
	Closure *Func
	// Ctor marks struct/variant constructors used as functions.
	Ctor bool
}

// OperandKind distinguishes copies from constants.
type OperandKind uint8

const (
	OpCopy OperandKind = iota
	OpConst
)

// Operand is an input to an rvalue or call.
type Operand struct {
	Kind  OperandKind
	Place Place
	Const *Const
}

// Copy returns an operand that reads p.
func Copy(p Place) Operand { return Operand{Kind: OpCopy, Place: p} }

// ConstOp returns a constant operand.
func ConstOp(c *Const) Operand { return Operand{Kind: OpConst, Const: c} }

// BinOp is a primitive binary operation.
type BinOp uint8

const (
	Add BinOp = iota
	Sub
	Mul
	Div
	Rem
	BitAnd
	BitOr
	BitXor
	Shl
	Shr
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
	Offset // pointer + n elements
)

var binNames = [...]string{"add", "sub", "mul", "div", "rem", "and", "or", "xor", "shl", "shr", "eq", "ne", "lt", "le", "gt", "ge", "offset"}

func (op BinOp) String() string { return binNames[op] }

// UnOp is a primitive unary operation.
type UnOp uint8

const (
	Neg UnOp = iota
	Not
)

// CastKind classifies casts.
type CastKind uint8

const (
	CastNumeric CastKind = iota // between integer/float/char/bool types
	CastPtr                     // pointer to pointer, ref to pointer
	CastPtrInt                  // pointer to integer
	CastIntPtr                  // integer to pointer
	CastEnum                    // fieldless enum to integer
	CastFnPtr                   // extern "C" fn to pointer/integer
)

// Rvalue computes a value.
type Rvalue interface{ rvalue() }

type (
	// Use is a plain operand.
	Use struct{ X Operand }
	// Ref takes the address of a place (&, &mut and raw pointers).
	Ref struct {
		Mut   bool
		Place Place
	}
	// Bin is a primitive binary operation on X and Y of type Type.
	Bin struct {
		Op   BinOp
		X, Y Operand
		Type *types.Type // operand type
	}
	// Un is a primitive unary operation.
	Un struct {
		Op   UnOp
		X    Operand
		Type *types.Type
	}
	// Cast converts X to To.
	Cast struct {
		Kind CastKind
		X    Operand
		From *types.Type
		To   *types.Type
	}
	// Aggregate builds a tuple, struct, enum variant, array or closure.
	Aggregate struct {
		Kind    AggKind
		Type    *types.Type
		Variant int // for enums
		Ops     []Operand
		Closure *Func
	}
	// Discr reads the variant index of an enum place.
	Discr struct{ Place Place }
	// Len reads the length of a slice or array place.
	Len struct{ Place Place }
	// Unsize converts a pointer to an array into a slice pointer.
	Unsize struct {
		X   Operand
		Len int64
		To  *types.Type
	}
	// MakeDyn converts &T into &dyn Trait (pointer plus vtable for T).
	MakeDyn struct {
		X        Operand
		Concrete *types.Type // T
		To       *types.Type // the &dyn type
	}
)

// AggKind is the kind of aggregate.
type AggKind uint8

const (
	AggTuple AggKind = iota
	AggAdt
	AggArray
	AggClosure
)

func (Use) rvalue()       {}
func (Ref) rvalue()       {}
func (Bin) rvalue()       {}
func (Un) rvalue()        {}
func (Cast) rvalue()      {}
func (Aggregate) rvalue() {}
func (Discr) rvalue()     {}
func (Len) rvalue()       {}
func (Unsize) rvalue()    {}
func (MakeDyn) rvalue()   {}

// Stmt is a statement.
type Stmt interface{ stmt() }

type (
	// Assign stores an rvalue into a place.
	Assign struct {
		Place Place
		Rv    Rvalue
	}
	// SetDiscr sets the variant of an enum place (after its fields are written).
	SetDiscr struct {
		Place   Place
		Variant int
	}
	// Box allocates a heap cell for a captured local and stores its address
	// in the local.
	Box struct {
		Local int
		Type  *types.Type
	}
	// PushDefer registers a call to run when the function returns or unwinds.
	PushDefer struct {
		Fn   Operand
		Args []Operand
	}
)

func (Assign) stmt()    {}
func (SetDiscr) stmt()  {}
func (Box) stmt()       {}
func (PushDefer) stmt() {}

// Term ends a basic block.
type Term interface{ term() }

type (
	// Goto jumps to Target.
	Goto struct{ Target int }
	// Switch jumps on an integer (or bool/char) value.
	Switch struct {
		X       Operand
		Type    *types.Type
		Vals    []int64
		Targets []int
		Else    int
	}
	// Return returns from the function (after running defers).
	Return struct{}
	// Unreachable marks code that cannot be reached.
	Unreachable struct{}
	// Call calls Fn and stores the result in Dest, then continues at
	// Target (-1 if the callee never returns).
	Call struct {
		Fn     Operand
		Args   []Operand
		Dest   Place
		Target int
		Pos    syntax.Pos
	}
	// Assert panics with Msg unless Cond is true, else continues at Target.
	// For bounds checks Len and Index are set and the message names them.
	Assert struct {
		Cond       Operand
		Msg        string
		Target     int
		Pos        syntax.Pos
		Bounds     bool
		Len, Index Operand
	}
)

func (Goto) term()        {}
func (Switch) term()      {}
func (Return) term()      {}
func (Unreachable) term() {}
func (Call) term()        {}
func (Assert) term()      {}
