package types

import (
	"navi/internal/syntax"
)

// coerceTo checks that a value of type got (produced by x) can be used where
// want is expected, recording implicit conversions; it reports a mismatch at
// pos otherwise.
func (fc *fnCtx) coerceTo(x syntax.Expr, got, want *Type, pos syntax.Pos) bool {
	if fc.coerce(x, got, want) {
		return true
	}
	fc.mismatch(pos, want, got)
	return false
}

// coerce implements the implicit conversions: ! to any type, &mut T to &T,
// &[T; N] to &[T], &T to &dyn Trait, deref coercion (&String to &str) and
// &T to *const T.
func (fc *fnCtx) coerce(x syntax.Expr, got, want *Type) bool {
	in := fc.in
	g, w := in.shallow(got), in.shallow(want)
	if g.Kind == KNever || g.Kind == KError || w.Kind == KError {
		return true
	}
	if g.Kind == KInfer || w.Kind == KInfer {
		return in.tryUnify(g, w)
	}
	if w.Kind == KRef && g.Kind == KRef && (g.Mut || !w.Mut) {
		if in.tryUnify(g.Elem, w.Elem) {
			return true
		}
		ge, we := in.shallow(g.Elem), in.shallow(w.Elem)
		if ge.Kind == KArray && we.Kind == KSlice && in.tryUnify(ge.Elem, we.Elem) {
			fc.adjust(x).Unsize = true
			return true
		}
		if we.Kind == KDyn && ge.Kind != KDyn {
			for _, b := range we.Bounds {
				fc.require(&TraitRef{Trait: b.Trait, Self: ge, Args: b.Args, Bindings: b.Bindings}, x.Pos())
			}
			fc.adjust(x).ToDyn = w
			return true
		}
		if we.Kind != KInfer {
			steps := fc.autoderef(ge, w.Mut)
			for i := 1; i < len(steps); i++ {
				if in.tryUnify(steps[i], we) {
					a := fc.adjust(x)
					a.Derefs = i + 1 // through the reference, then i derefs
					a.Autoref = 1
					if w.Mut {
						a.Autoref = 2
					}
					return true
				}
			}
		}
		return false
	}
	if w.Kind == KPtr && g.Kind == KRef && (g.Mut || !w.Mut) {
		return in.tryUnify(g.Elem, w.Elem)
	}
	if w.Kind == KPtr && g.Kind == KPtr && g.Mut && !w.Mut {
		return in.tryUnify(g.Elem, w.Elem)
	}
	return in.tryUnify(g, w)
}

func (fc *fnCtx) adjust(x syntax.Expr) *Adjust {
	if x == nil {
		return &Adjust{}
	}
	a := fc.body.Adjust[x]
	if a == nil {
		a = &Adjust{}
		fc.body.Adjust[x] = a
	}
	return a
}

// autoderef returns t followed by the types reached by dereferencing it:
// through references, raw pointers are not followed, and through Deref
// (DerefMut when mut) impls.
func (fc *fnCtx) autoderef(t *Type, mut bool) []*Type {
	steps := []*Type{t}
	cur := t
	for i := 0; i < 16; i++ {
		c := fc.in.shallow(cur)
		var next *Type
		switch c.Kind {
		case KRef:
			next = c.Elem
		case KInfer, KError, KNever:
		default:
			next = fc.overloadedDeref(c)
		}
		if next == nil {
			break
		}
		steps = append(steps, next)
		cur = next
	}
	return steps
}

// overloadedDeref returns <t as Deref>::Target if t implements Deref.
func (fc *fnCtx) overloadedDeref(t *Type) *Type {
	deref := fc.info.Lang["Deref"]
	if deref == nil {
		return nil
	}
	tr := &TraitRef{Trait: deref, Self: t}
	if fc.holds(tr) != selYes {
		return nil
	}
	target := fc.c.findOwnAssocType(deref, "Target")
	if target == nil {
		return nil
	}
	r := fc.normalizeProjNow(target, tr)
	if r == nil || r.Kind == KError {
		return nil
	}
	return r
}

// lub computes the common type of two branches (if/else, match arms).
func (fc *fnCtx) lub(x syntax.Expr, a, b *Type, pos syntax.Pos) *Type {
	sa, sb := fc.in.shallow(a), fc.in.shallow(b)
	switch {
	case sa.Kind == KNever:
		return b
	case sb.Kind == KNever:
		return a
	}
	if fc.coerce(x, b, a) {
		return a
	}
	fc.mismatch(pos, a, b)
	return a
}
