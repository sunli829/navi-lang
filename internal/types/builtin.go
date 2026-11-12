package types

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// Builtin impls: std lang traits implemented by the compiler for primitive
// and structural types (Rust writes these with macros in core; navi has no
// macros, so the compiler provides them).

func isIntT(t *Type) bool   { return t.Kind == KInt }
func isFloatT(t *Type) bool { return t.Kind == KFloat }
func isNumT(t *Type) bool   { return t.Kind == KInt || t.Kind == KFloat }
func isScalarT(t *Type) bool {
	return isNumT(t) || t.Kind == KBool || t.Kind == KChar
}

var assignOps = map[string]string{
	"AddAssign": "Add", "SubAssign": "Sub", "MulAssign": "Mul", "DivAssign": "Div", "RemAssign": "Rem",
	"BitAndAssign": "BitAnd", "BitOrAssign": "BitOr", "BitXorAssign": "BitXor",
	"ShlAssign": "Shl", "ShrAssign": "Shr",
}

// builtin reports whether tr has a builtin impl for self. The selection's
// nested obligations must also hold.
func (fc *fnCtx) builtin(tr *TraitRef, self *Type, depth int) (*selection, bool) {
	name := fc.langName(tr.Trait)
	if name == "" {
		return nil, false
	}
	in := fc.in
	ctx := in.ctx
	self = in.shallow(self)
	sel := &selection{kind: selBuiltin, assoc: map[string]*Type{}}
	rhs := self
	if len(tr.Args) > 0 {
		rhs = tr.Args[0]
	}
	same := func() bool { return in.unify(rhs, self) }
	elemwise := func(trait *resolve.Def, elems []*Type) {
		for _, e := range elems {
			sel.nested = append(sel.nested, &TraitRef{Trait: trait, Self: e, Args: argsLike(tr, e)})
		}
	}
	structural := func(trait *resolve.Def) bool {
		switch self.Kind {
		case KTuple:
			if !same() {
				return false
			}
			elemwise(trait, self.Elems)
			return true
		case KArray, KSlice:
			if !same() {
				return false
			}
			elemwise(trait, []*Type{self.Elem})
			return true
		}
		return false
	}

	if base, ok := assignOps[name]; ok {
		_ = base
		switch {
		case name == "ShlAssign" || name == "ShrAssign":
			r := in.shallow(rhs)
			return sel, isIntT(self) && (isIntT(r) || r.Kind == KInfer && in.vars[r.Var].kind == varInt)
		case name == "BitAndAssign" || name == "BitOrAssign" || name == "BitXorAssign":
			return sel, (isIntT(self) || self.Kind == KBool) && same()
		default:
			return sel, isNumT(self) && same()
		}
	}

	switch name {
	case "Clone":
		switch {
		case isScalarT(self), self.Kind == KPtr, self.Kind == KFn, self.Kind == KNever:
			return sel, true
		case self.Kind == KRef && !self.Mut:
			return sel, true
		case self.Kind == KTuple:
			elemwise(tr.Trait, self.Elems)
			return sel, true
		case self.Kind == KArray:
			elemwise(tr.Trait, []*Type{self.Elem})
			return sel, true
		}
	case "Default":
		switch {
		case isScalarT(self):
			return sel, true
		case self.Kind == KRef && !self.Mut && in.shallow(self.Elem).Kind == KStr:
			return sel, true
		case self.Kind == KTuple:
			elemwise(tr.Trait, self.Elems)
			return sel, true
		case self.Kind == KArray:
			elemwise(tr.Trait, []*Type{self.Elem})
			return sel, true
		}
	case "PartialEq", "PartialOrd":
		switch {
		case isScalarT(self), self.Kind == KStr, self.Kind == KPtr, self.Kind == KFn:
			return sel, same()
		case self.Kind == KRef:
			r := in.shallow(rhs)
			if r.Kind == KInfer {
				r2 := ctx.Ref(self.Mut, in.fresh())
				if !in.unify(r, r2) {
					return nil, false
				}
				r = in.shallow(rhs)
			}
			if r.Kind != KRef {
				return nil, false
			}
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem, Args: []*Type{r.Elem}})
			return sel, true
		}
		return sel, structural(tr.Trait)
	case "Eq", "Ord":
		switch {
		case isIntT(self), self.Kind == KBool, self.Kind == KChar, self.Kind == KStr, self.Kind == KPtr, self.Kind == KNever:
			return sel, true
		case self.Kind == KRef:
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem})
			return sel, true
		}
		return sel, structural(tr.Trait)
	case "Hash":
		switch {
		case isIntT(self), self.Kind == KBool, self.Kind == KChar, self.Kind == KStr, self.Kind == KPtr:
			return sel, true
		case self.Kind == KRef:
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem})
			return sel, true
		}
		return sel, structural(tr.Trait)
	case "Debug":
		switch {
		case isScalarT(self), self.Kind == KStr, self.Kind == KPtr, self.Kind == KNever, self.Kind == KFn:
			return sel, true
		case self.Kind == KRef:
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem})
			return sel, true
		case self.Kind == KTuple:
			elemwise(tr.Trait, self.Elems)
			return sel, true
		case self.Kind == KArray, self.Kind == KSlice:
			elemwise(tr.Trait, []*Type{self.Elem})
			return sel, true
		}
	case "Display":
		switch {
		case isScalarT(self), self.Kind == KStr, self.Kind == KNever:
			return sel, true
		case self.Kind == KRef:
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem})
			return sel, true
		}
	case "LowerHex", "UpperHex", "Octal", "Binary":
		switch {
		case isIntT(self):
			return sel, true
		case self.Kind == KRef:
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem})
			return sel, true
		}
	case "LowerExp", "UpperExp":
		switch {
		case isFloatT(self):
			return sel, true
		case self.Kind == KRef:
			sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: self.Elem})
			return sel, true
		}
	case "Add", "Sub", "Mul", "Div", "Rem":
		if isNumT(self) && same() {
			sel.assoc["Output"] = self
			return sel, true
		}
	case "BitAnd", "BitOr", "BitXor":
		if (isIntT(self) || self.Kind == KBool) && same() {
			sel.assoc["Output"] = self
			return sel, true
		}
	case "Shl", "Shr":
		r := in.shallow(rhs)
		if isIntT(self) && (isIntT(r) || r.Kind == KInfer && in.vars[r.Var].kind == varInt) {
			sel.assoc["Output"] = self
			return sel, true
		}
	case "Neg":
		if isFloatT(self) || isIntT(self) && self.Int.Signed() {
			sel.assoc["Output"] = self
			return sel, true
		}
	case "Not":
		if isIntT(self) || self.Kind == KBool {
			sel.assoc["Output"] = self
			return sel, true
		}
	case "Step":
		if isIntT(self) || self.Kind == KChar {
			return sel, true
		}
	case "Deref":
		if self.Kind == KRef {
			sel.assoc["Target"] = self.Elem
			return sel, true
		}
	case "DerefMut":
		if self.Kind == KRef && self.Mut {
			sel.assoc["Target"] = self.Elem
			return sel, true
		}
	case "Index", "IndexMut":
		var elem *Type
		switch self.Kind {
		case KArray, KSlice:
			elem = self.Elem
		case KStr:
			elem = nil
		default:
			return nil, false
		}
		idx := in.shallow(rhs)
		if elem != nil && (idx.Kind == KInt && idx.Int == Usize || idx.Kind == KInfer && in.vars[idx.Var].kind == varInt) {
			in.unify(idx, ctx.Ints[Usize])
			sel.assoc["Output"] = elem
			return sel, true
		}
		if idx.Kind == KAdt && fc.isRangeDef(idx.Def) {
			for _, a := range idx.Args {
				if !in.unify(a, ctx.Ints[Usize]) {
					return nil, false
				}
			}
			if elem != nil {
				sel.assoc["Output"] = ctx.Slice(elem)
			} else {
				sel.assoc["Output"] = ctx.Str
			}
			return sel, true
		}
	}
	return nil, false
}

// argsLike returns the trait arguments for an elementwise nested bound:
// PartialEq<Rhs> on elements uses the element type as Rhs.
func argsLike(tr *TraitRef, elem *Type) []*Type {
	if len(tr.Args) == 0 {
		return nil
	}
	return []*Type{elem}
}

func (fc *fnCtx) isRangeDef(d *resolve.Def) bool {
	switch fc.langName(d) {
	case "Range", "RangeInclusive", "RangeFrom", "RangeTo", "RangeToInclusive", "RangeFull":
		return true
	}
	return false
}

// derivable lists the traits #[derive] supports.
var derivable = map[string]bool{
	"Debug": true, "Clone": true, "PartialEq": true, "Eq": true,
	"PartialOrd": true, "Ord": true, "Hash": true, "Default": true,
}

// derives returns the derived trait names of an ADT.
func (c *collector) derives(d *resolve.Def) map[string]bool {
	if ds, ok := c.deriveCache[d]; ok {
		return ds
	}
	ds := map[string]bool{}
	var attrs []*syntax.Attr
	switch n := d.Node.(type) {
	case *syntax.StructDecl:
		attrs = n.Attrs
	case *syntax.EnumDecl:
		attrs = n.Attrs
	}
	for _, a := range attrs {
		if len(a.Path.Segments) != 1 || a.Path.Segments[0].Name.Value != "derive" {
			continue
		}
		for _, t := range a.Args {
			switch t.Kind {
			case syntax.Ident:
				if !derivable[t.Text] {
					c.errorf(d.File, t.Pos, "cannot derive %s; derivable traits are Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash and Default", t.Text)
					continue
				}
				ds[t.Text] = true
			case syntax.Comma:
			default:
				c.errorf(d.File, t.Pos, "malformed derive attribute")
			}
		}
	}
	if ds["Eq"] && !ds["PartialEq"] {
		c.errorf(d.File, d.Pos, "deriving Eq requires deriving PartialEq too")
	}
	if ds["Ord"] && !(ds["PartialOrd"] && ds["Eq"]) {
		c.errorf(d.File, d.Pos, "deriving Ord requires deriving PartialOrd and Eq too")
	}
	if ds["PartialOrd"] && !ds["PartialEq"] {
		c.errorf(d.File, d.Pos, "deriving PartialOrd requires deriving PartialEq too")
	}
	if ds["Default"] && d.Kind == resolve.DefEnum {
		c.errorf(d.File, d.Pos, "Default cannot be derived for enums")
		delete(ds, "Default")
	}
	c.deriveCache[d] = ds
	return ds
}

// derived returns the selection for a derived impl of tr, if any. Like
// Rust's derive, each type parameter must implement the trait too.
func (fc *fnCtx) derived(tr *TraitRef) []*selection {
	self := fc.in.shallow(tr.Self)
	if self.Kind != KAdt {
		return nil
	}
	name := fc.langName(tr.Trait)
	if name == "" || !fc.c.derives(self.Def)[name] {
		return nil
	}
	if len(tr.Args) > 0 && !fc.in.unify(tr.Args[0], self) {
		return nil
	}
	sel := &selection{kind: selBuiltin, assoc: map[string]*Type{}}
	for _, a := range self.Args {
		sel.nested = append(sel.nested, &TraitRef{Trait: tr.Trait, Self: a, Args: argsLike(tr, a)})
	}
	return []*selection{sel}
}
