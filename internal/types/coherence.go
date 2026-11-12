package types

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// Item checks that need the trait solver: impl method signatures, overlap
// between impls, and dyn-compatibility of trait methods.

// scratchCtx returns a function context for solving outside any body.
func (c *collector) scratchCtx(d *resolve.Def) *fnCtx {
	fc := &fnCtx{
		c:         c,
		info:      c.info,
		in:        &infer{ctx: c.info.Ctx},
		file:      d.File,
		owner:     d,
		varOrigin: map[int]*TypeParam{},
		opaques:   map[*Type]*Type{},
	}
	return fc
}

// checkImplSigs compares each method of a trait impl with the trait's
// declaration (after substituting the impl's types and normalizing
// associated types through the solver).
func (c *collector) checkImplSigs(d *resolve.Def) {
	ii := c.impl(d)
	if ii.Trait == nil {
		return
	}
	ti := c.trait(ii.Trait.Trait)
	if ti == nil {
		return
	}
	ctx := c.info.Ctx
	tm := paramMap(ti.Generics, ii.Trait.Args)
	for name, item := range ii.Items {
		decl := ti.Items[name]
		if decl == nil || item.Kind != resolve.DefAssocFn || decl.Kind != resolve.DefAssocFn {
			continue
		}
		is, ds := c.fnSig(item), c.fnSig(decl)
		if len(is.Generics.Params) != len(ds.Generics.Params) {
			c.errorf(item.File, item.Pos, "method %s has %d type parameter%s but its declaration in trait %s has %d", name, len(is.Generics.Params), plural(len(is.Generics.Params)), ti.Def.Name, len(ds.Generics.Params))
			continue
		}
		if (is.Self == nil) != (ds.Self == nil) || len(is.Params) != len(ds.Params) ||
			is.Self != nil && (is.Self.Kind != ds.Self.Kind || is.Self.Mut != ds.Self.Mut) {
			continue // reported by checkMethodShape
		}
		m := map[*resolve.Def]*Type{}
		for k, v := range tm {
			m[k] = v
		}
		for i, p := range ds.Generics.Params {
			m[p.Def] = is.Generics.Params[i].Type
		}
		fc := c.newFnCtx(item)
		norm := func(t *Type) *Type { return fc.in.resolve(fc.normalize(t)) }
		conv := func(t *Type) *Type { return norm(Subst(ctx, t, m, ii.SelfTy)) }
		want := ctx.Fn(append(selfList(ds.Self, conv), mapTypes(ds.Params, conv)...), conv(ds.Ret), "")
		got := ctx.Fn(append(selfList(is.Self, norm), mapTypes(is.Params, norm)...), norm(is.Ret), "")
		if want != got && !hasInfer(want) && !hasInfer(got) {
			c.errorf(item.File, item.Pos, "method %s has an incompatible signature for trait %s: expected %s, found %s", name, ti.Def.Name, want, got)
		}
	}
}

func selfList(t *Type, f func(*Type) *Type) []*Type {
	if t == nil {
		return nil
	}
	return []*Type{f(t)}
}

func mapTypes(ts []*Type, f func(*Type) *Type) []*Type {
	out := make([]*Type, len(ts))
	for i, t := range ts {
		out[i] = f(t)
	}
	return out
}

// checkOverlap reports pairs of impls of the same trait that could apply to
// the same type.
func (c *collector) checkOverlap() {
	for trait, impls := range c.info.ImplsOf {
		if trait == nil {
			continue
		}
		for i := 0; i < len(impls); i++ {
			for j := i + 1; j < len(impls); j++ {
				a, b := impls[i], impls[j]
				if a.Trait == nil || b.Trait == nil {
					continue
				}
				if c.implsOverlap(a, b) {
					c.errorf(b.Def.File, b.Def.Pos, "conflicting implementations of trait %s: this impl for %s overlaps with the impl for %s", trait.Name, b.SelfTy, a.SelfTy)
				}
			}
		}
	}
}

func (c *collector) implsOverlap(a, b *ImplInfo) bool {
	fc := c.scratchCtx(b.Def)
	ctx := c.info.Ctx
	ma, _ := fc.instantiate(a.Generics)
	mb, _ := fc.instantiate(b.Generics)
	ta := SubstTraitRef(ctx, a.Trait, ma, nil)
	tb := SubstTraitRef(ctx, b.Trait, mb, nil)
	if !fc.matchTraitRef(ta, tb) {
		return false
	}
	// Both impls' where clauses must be satisfiable for a real overlap.
	var nested []*TraitRef
	for _, p := range a.Generics.AllPreds() {
		nested = append(nested, SubstTraitRef(ctx, p.Trait, ma, nil))
	}
	for _, p := range b.Generics.AllPreds() {
		nested = append(nested, SubstTraitRef(ctx, p.Trait, mb, nil))
	}
	for _, n := range nested {
		if fc.holds(n) == selNo {
			return false
		}
	}
	return true
}

// dynCallable reports whether a trait method can be called through dyn:
// it takes &self or &mut self, has no type parameters of its own and does
// not mention Self elsewhere. Other methods are left out of the vtable
// (as if they had Rust's `where Self: Sized`).
func (c *collector) dynCallable(m *resolve.Def) bool {
	fs := c.fnSig(m)
	if fs.Self == nil || fs.Self.Kind != KRef || len(fs.Generics.Params) > 0 {
		return false
	}
	for _, p := range fs.Params {
		if mentionsSelf(p) {
			return false
		}
	}
	return !mentionsSelf(fs.Ret)
}

// checkDyn verifies a dyn type: every associated type must be given.
func (c *collector) checkDyn(file *syntax.File, pos syntax.Pos, bounds []*TraitRef) {
	for _, b := range bounds {
		given := map[string]bool{}
		for _, bd := range b.Bindings {
			given[bd.Name] = true
		}
		c.walkSupertraits(&TraitRef{Trait: b.Trait, Self: c.info.Ctx.SelfParam(b.Trait), Args: b.Args}, func(t *TraitRef) {
			for _, ch := range c.children[t.Trait] {
				if ch.Kind == resolve.DefAssocType && !given[ch.Name] {
					c.errorf(file, pos, "the associated type %s of trait %s must be specified in dyn %s (write dyn %s<%s = ...>)", ch.Name, t.Trait.Name, b.Trait.Name, b.Trait.Name, ch.Name)
				}
			}
		})
	}
}
