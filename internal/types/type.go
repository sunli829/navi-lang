// Package types represents navi types, lowers type syntax to them, collects
// item signatures and type-checks function bodies.
package types

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"navi/internal/resolve"
)

// Kind is the kind of a type.
type Kind uint8

const (
	KError Kind = iota // a type that could not be computed (already reported)
	KBool
	KChar
	KStr // the unsized str; string slices are &str
	KInt
	KFloat
	KNever
	KTuple // () is the empty tuple
	KRef
	KPtr
	KSlice // the unsized [T]; slices are &[T]
	KArray
	KFn
	KAdt    // struct or enum
	KParam  // type parameter, or Self inside a trait
	KProj   // <Self as Trait>::Name
	KDyn    // dyn Trait
	KOpaque // impl Trait in return position
	KInfer  // inference variable (not interned)
)

// IntKind identifies an integer type.
type IntKind uint8

const (
	I8 IntKind = iota
	I16
	I32
	I64
	Isize
	U8
	U16
	U32
	U64
	Usize
)

var intNames = [...]string{"i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize"}

func (k IntKind) String() string { return intNames[k] }

// Signed reports whether k is a signed integer kind.
func (k IntKind) Signed() bool { return k <= Isize }

// Type is an interned type: two types are equal iff their pointers are
// equal (inference variables excepted).
type Type struct {
	Kind  Kind
	Int   IntKind // KInt
	F64   bool    // KFloat: f64 (else f32)
	Mut   bool    // KRef, KPtr
	Elem  *Type   // KRef, KPtr, KSlice, KArray
	Len   int64   // KArray
	Elems []*Type // KTuple; parameters of KFn
	Ret   *Type   // KFn result
	Abi   string  // KFn: "" for navi functions and closures, "C" for C function pointers

	Def  *resolve.Def // KAdt: struct/enum; KParam: the parameter (or the trait for Self); KProj: assoc type; KOpaque: function
	Args []*Type      // KAdt generic arguments
	Self bool         // KParam: this is Self of trait Def

	Trait  *TraitRef   // KProj: the trait (with self type)
	Bounds []*TraitRef // KDyn: traits (Self is nil); KOpaque: bounds
	Index  int         // KOpaque: which impl Trait in the signature
	Var    int         // KInfer: variable number

	id  int
	key string
}

// TraitRef is a reference to a trait with its self type and generic
// arguments: `Self: Trait<Args>`. Bindings carry `Item = T` constraints
// from bounds and dyn types.
type TraitRef struct {
	Trait    *resolve.Def
	Self     *Type // nil inside dyn types
	Args     []*Type
	Bindings []AssocBinding
}

// AssocBinding is Name = Type inside a bound.
type AssocBinding struct {
	Name string
	Type *Type
}

// Ctx interns types. It is safe for concurrent use.
type Ctx struct {
	mu    sync.Mutex
	types map[string]*Type
	next  int

	Bool, Char, Str, Never, Unit, Err *Type
	F32, F64                          *Type
	Ints                              [10]*Type
}

// NewCtx returns a context with the basic types created.
func NewCtx() *Ctx {
	c := &Ctx{types: map[string]*Type{}}
	c.Err = c.intern(&Type{Kind: KError})
	c.Bool = c.intern(&Type{Kind: KBool})
	c.Char = c.intern(&Type{Kind: KChar})
	c.Str = c.intern(&Type{Kind: KStr})
	c.Never = c.intern(&Type{Kind: KNever})
	c.Unit = c.intern(&Type{Kind: KTuple})
	c.F32 = c.intern(&Type{Kind: KFloat})
	c.F64 = c.intern(&Type{Kind: KFloat, F64: true})
	for k := range c.Ints {
		c.Ints[k] = c.intern(&Type{Kind: KInt, Int: IntKind(k)})
	}
	return c
}

func defID(d *resolve.Def) string { return fmt.Sprintf("%p", d) }

func typesKey(ts []*Type) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(t.id))
	}
	return b.String()
}

func traitKey(tr *TraitRef) string {
	if tr == nil {
		return "-"
	}
	self := "-"
	if tr.Self != nil {
		self = strconv.Itoa(tr.Self.id)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%s<%s>", defID(tr.Trait), self, typesKey(tr.Args))
	for _, bd := range tr.Bindings {
		fmt.Fprintf(&b, "{%s=%d}", bd.Name, bd.Type.id)
	}
	return b.String()
}

func (t *Type) computeKey() string {
	switch t.Kind {
	case KInt:
		return "i" + strconv.Itoa(int(t.Int))
	case KFloat:
		if t.F64 {
			return "f64"
		}
		return "f32"
	case KRef, KPtr:
		return fmt.Sprintf("%d:%v:%d", t.Kind, t.Mut, t.Elem.id)
	case KSlice:
		return fmt.Sprintf("%d:%d", t.Kind, t.Elem.id)
	case KArray:
		return fmt.Sprintf("%d:%d;%d", t.Kind, t.Elem.id, t.Len)
	case KTuple:
		return "(" + typesKey(t.Elems) + ")"
	case KFn:
		ret := 0
		if t.Ret != nil {
			ret = t.Ret.id
		}
		return fmt.Sprintf("fn%s(%s)%d", t.Abi, typesKey(t.Elems), ret)
	case KAdt:
		return "adt" + defID(t.Def) + "<" + typesKey(t.Args) + ">"
	case KParam:
		return fmt.Sprintf("param%s:%v", defID(t.Def), t.Self)
	case KProj:
		return "proj" + defID(t.Def) + traitKey(t.Trait)
	case KDyn, KOpaque:
		var b strings.Builder
		fmt.Fprintf(&b, "%d:%d:", t.Kind, t.Index)
		if t.Def != nil {
			b.WriteString(defID(t.Def))
		}
		for _, tr := range t.Bounds {
			b.WriteString(traitKey(tr))
			b.WriteByte('+')
		}
		return b.String()
	}
	return strconv.Itoa(int(t.Kind))
}

func (c *Ctx) intern(t *Type) *Type {
	key := t.computeKey()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.types[key]; old != nil {
		return old
	}
	c.next++
	t.id = c.next
	t.key = key
	c.types[key] = t
	return t
}

// Constructors.

func (c *Ctx) Int(k IntKind) *Type { return c.Ints[k] }

func (c *Ctx) Ref(mut bool, elem *Type) *Type {
	return c.intern(&Type{Kind: KRef, Mut: mut, Elem: elem})
}

func (c *Ctx) Ptr(mut bool, elem *Type) *Type {
	return c.intern(&Type{Kind: KPtr, Mut: mut, Elem: elem})
}

func (c *Ctx) Slice(elem *Type) *Type { return c.intern(&Type{Kind: KSlice, Elem: elem}) }

func (c *Ctx) Array(elem *Type, n int64) *Type {
	return c.intern(&Type{Kind: KArray, Elem: elem, Len: n})
}

func (c *Ctx) Tuple(elems []*Type) *Type {
	if len(elems) == 0 {
		return c.Unit
	}
	return c.intern(&Type{Kind: KTuple, Elems: elems})
}

func (c *Ctx) Fn(params []*Type, ret *Type, abi string) *Type {
	return c.intern(&Type{Kind: KFn, Elems: params, Ret: ret, Abi: abi})
}

func (c *Ctx) Adt(def *resolve.Def, args []*Type) *Type {
	return c.intern(&Type{Kind: KAdt, Def: def, Args: args})
}

func (c *Ctx) Param(def *resolve.Def) *Type { return c.intern(&Type{Kind: KParam, Def: def}) }

// SelfParam is the Self type inside trait def.
func (c *Ctx) SelfParam(trait *resolve.Def) *Type {
	return c.intern(&Type{Kind: KParam, Def: trait, Self: true})
}

func (c *Ctx) Proj(assoc *resolve.Def, tr *TraitRef) *Type {
	return c.intern(&Type{Kind: KProj, Def: assoc, Trait: tr})
}

func (c *Ctx) Dyn(bounds []*TraitRef) *Type { return c.intern(&Type{Kind: KDyn, Bounds: bounds}) }

func (c *Ctx) Opaque(fn *resolve.Def, index int, bounds []*TraitRef) *Type {
	return c.intern(&Type{Kind: KOpaque, Def: fn, Index: index, Bounds: bounds})
}

// ---------------------------------------------------------------------------
// Printing

func (t *Type) String() string {
	var b strings.Builder
	writeType(&b, t)
	return b.String()
}

func writeTypes(b *strings.Builder, ts []*Type) {
	for i, t := range ts {
		if i > 0 {
			b.WriteString(", ")
		}
		writeType(b, t)
	}
}

func writeType(b *strings.Builder, t *Type) {
	switch t.Kind {
	case KError:
		b.WriteString("{error}")
	case KBool:
		b.WriteString("bool")
	case KChar:
		b.WriteString("char")
	case KStr:
		b.WriteString("str")
	case KInt:
		b.WriteString(t.Int.String())
	case KFloat:
		if t.F64 {
			b.WriteString("f64")
		} else {
			b.WriteString("f32")
		}
	case KNever:
		b.WriteString("!")
	case KTuple:
		b.WriteByte('(')
		writeTypes(b, t.Elems)
		if len(t.Elems) == 1 {
			b.WriteByte(',')
		}
		b.WriteByte(')')
	case KRef:
		b.WriteByte('&')
		if t.Mut {
			b.WriteString("mut ")
		}
		writeType(b, t.Elem)
	case KPtr:
		if t.Mut {
			b.WriteString("*mut ")
		} else {
			b.WriteString("*const ")
		}
		writeType(b, t.Elem)
	case KSlice:
		b.WriteByte('[')
		writeType(b, t.Elem)
		b.WriteByte(']')
	case KArray:
		b.WriteByte('[')
		writeType(b, t.Elem)
		fmt.Fprintf(b, "; %d]", t.Len)
	case KFn:
		if t.Abi != "" {
			fmt.Fprintf(b, "extern %q ", t.Abi)
		}
		b.WriteString("fn(")
		writeTypes(b, t.Elems)
		b.WriteByte(')')
		if t.Ret != nil && !(t.Ret.Kind == KTuple && len(t.Ret.Elems) == 0) {
			b.WriteString(" -> ")
			writeType(b, t.Ret)
		}
	case KAdt:
		b.WriteString(t.Def.Name)
		if len(t.Args) > 0 {
			b.WriteByte('<')
			writeTypes(b, t.Args)
			b.WriteByte('>')
		}
	case KParam:
		if t.Self {
			b.WriteString("Self")
		} else {
			b.WriteString(t.Def.Name)
		}
	case KProj:
		if t.Trait.Self != nil && t.Trait.Self.Kind == KParam && !t.Trait.Self.Self {
			writeType(b, t.Trait.Self)
		} else {
			b.WriteByte('<')
			if t.Trait.Self != nil {
				writeType(b, t.Trait.Self)
			}
			b.WriteString(" as ")
			b.WriteString(t.Trait.String())
			b.WriteByte('>')
		}
		b.WriteString("::")
		b.WriteString(t.Def.Name)
	case KDyn:
		b.WriteString("dyn ")
		writeBounds(b, t.Bounds)
	case KOpaque:
		b.WriteString("impl ")
		writeBounds(b, t.Bounds)
	case KInfer:
		fmt.Fprintf(b, "?%d", t.Var)
	}
}

func writeBounds(b *strings.Builder, bounds []*TraitRef) {
	for i, tr := range bounds {
		if i > 0 {
			b.WriteString(" + ")
		}
		b.WriteString(tr.String())
	}
}

// String renders the trait part (without Self): Iterator<Item = i32>.
func (tr *TraitRef) String() string {
	var b strings.Builder
	b.WriteString(tr.Trait.Name)
	if len(tr.Args) > 0 || len(tr.Bindings) > 0 {
		b.WriteByte('<')
		writeTypes(&b, tr.Args)
		for i, bd := range tr.Bindings {
			if i > 0 || len(tr.Args) > 0 {
				b.WriteString(", ")
			}
			b.WriteString(bd.Name)
			b.WriteString(" = ")
			writeType(&b, bd.Type)
		}
		b.WriteByte('>')
	}
	return b.String()
}
