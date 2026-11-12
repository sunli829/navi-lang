package types

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// Generics are the type parameters of an item. Items nested in an impl or
// trait (associated items) have the impl/trait generics as Parent.
type Generics struct {
	Parent *Generics
	Params []*TypeParam
	Preds  []*Pred // bounds and where clauses declared on this item
	// SelfTrait is set for trait generics: Self is a parameter bounded by
	// the trait itself.
	SelfTrait *resolve.Def
}

// TypeParam is one generic parameter.
type TypeParam struct {
	Def     *resolve.Def
	Type    *Type // the KParam type
	Default *Type // nil if none; may mention earlier parameters and Self
}

// Pred is a where-clause predicate: a trait bound `Type: Trait<...>`, which
// may carry associated type bindings (`I: Iterator<Item = u8>`).
type Pred struct {
	Trait *TraitRef
}

// All returns the parameters of g and its parents, outermost first.
func (g *Generics) All() []*TypeParam {
	if g == nil {
		return nil
	}
	return append(g.Parent.All(), g.Params...)
}

// AllPreds returns the predicates of g and its parents.
func (g *Generics) AllPreds() []*Pred {
	if g == nil {
		return nil
	}
	return append(g.Parent.AllPreds(), g.Preds...)
}

// Field is a struct or variant field.
type Field struct {
	Name string // "0", "1", ... for tuple fields
	Type *Type
	Vis  resolve.Visibility
	Pos  syntax.Pos
}

// AdtInfo describes a struct or enum.
type AdtInfo struct {
	Def      *resolve.Def
	Generics *Generics
	IsEnum   bool
	Kind     syntax.StructKind // for structs
	Fields   []*Field          // for structs
	Variants []*VariantInfo    // for enums
}

// VariantInfo describes an enum variant.
type VariantInfo struct {
	Def    *resolve.Def
	Kind   syntax.StructKind
	Fields []*Field
	Discr  int64
}

// FnSig is the signature of a function or method.
type FnSig struct {
	Def      *resolve.Def
	Generics *Generics
	// Self is the receiver: nil for functions without self, otherwise the
	// receiver type (Self, &Self or &mut Self).
	Self     *Type
	Params   []*Type
	Ret      *Type
	Abi      string // "C" for extern "C" functions
	Const    bool
	Unsafe   bool
	HasBody  bool
	Opaques  []*Type // impl Trait types in the return type
	Variadic bool
}

// TraitInfo describes a trait.
type TraitInfo struct {
	Def      *resolve.Def
	Generics *Generics // includes Self (SelfTrait) and the trait parameters
	Supers   []*TraitRef
	Items    map[string]*resolve.Def // associated fns, consts and types
	Order    []*resolve.Def          // items in declaration order
}

// ImplInfo describes an impl block.
type ImplInfo struct {
	Def      *resolve.Def
	Generics *Generics
	Trait    *TraitRef // nil for inherent impls; Self is the impl's self type
	SelfTy   *Type
	Items    map[string]*resolve.Def
}

// AssocTypeInfo describes an associated type: a declaration in a trait
// (Bounds, optional Default) or a definition in an impl (Value).
type AssocTypeInfo struct {
	Def    *resolve.Def
	Bounds []*TraitRef
	Value  *Type // the type in impls, the default in traits (nil if none)
}

// ConstInfo describes a const, static or associated const.
type ConstInfo struct {
	Def  *resolve.Def
	Type *Type
	Mut  bool // static mut
}

// AliasInfo describes a type alias.
type AliasInfo struct {
	Def      *resolve.Def
	Generics *Generics
	Type     *Type
}

// Info holds the results of type collection (and later, type checking).
type Info struct {
	Ctx     *Ctx
	Res     *resolve.Info
	Adts    map[*resolve.Def]*AdtInfo
	Fns     map[*resolve.Def]*FnSig
	Traits  map[*resolve.Def]*TraitInfo
	Impls   map[*resolve.Def]*ImplInfo
	Assoc   map[*resolve.Def]*AssocTypeInfo
	Consts  map[*resolve.Def]*ConstInfo
	Aliases map[*resolve.Def]*AliasInfo

	// ImplsOf lists impls by the trait they implement (nil key: inherent).
	ImplsOf map[*resolve.Def][]*ImplInfo
	// Lang holds well-known std items by name (Option, Iterator, Deref, ...).
	Lang map[string]*resolve.Def

	// Bodies holds the type-checked bodies of functions, consts and statics.
	Bodies map[*resolve.Def]*BodyInfo
	// Hidden maps each opaque return type to the concrete type its function
	// returns.
	Hidden map[*Type]*Type
}
