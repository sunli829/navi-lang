package types

import (
	"sort"
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

// checkAdt reports duplicate fields and types of infinite size.
func (c *collector) checkAdt(d *resolve.Def) {
	ai := c.adt(d)
	checkFields := func(fields []*Field) {
		seen := map[string]bool{}
		for _, f := range fields {
			if seen[f.Name] {
				c.errorf(d.File, f.Pos, "field %s is declared more than once", f.Name)
			}
			seen[f.Name] = true
		}
	}
	checkFields(ai.Fields)
	for _, v := range ai.Variants {
		checkFields(v.Fields)
	}
	if c.containsByValue(ai, d, map[*resolve.Def]bool{}, 0) {
		c.errorf(d.File, d.Pos, "recursive type %s has infinite size; put the recursive field behind a reference (&%s)", d.Name, d.Name)
	}
}

// containsByValue reports whether the fields of ai contain target by value
// (not behind a reference, pointer or slice).
func (c *collector) containsByValue(ai *AdtInfo, target *resolve.Def, onStack map[*resolve.Def]bool, depth int) bool {
	if depth > 64 || onStack[ai.Def] {
		return false
	}
	onStack[ai.Def] = true
	defer delete(onStack, ai.Def)
	var fields []*Field
	fields = append(fields, ai.Fields...)
	for _, v := range ai.Variants {
		fields = append(fields, v.Fields...)
	}
	for _, f := range fields {
		if c.typeContains(f.Type, target, onStack, depth) {
			return true
		}
	}
	return false
}

func (c *collector) typeContains(t *Type, target *resolve.Def, onStack map[*resolve.Def]bool, depth int) bool {
	switch t.Kind {
	case KAdt:
		if t.Def == target {
			return true
		}
		inner := c.adt(t.Def)
		// Check the definition with these arguments substituted.
		m := paramMap(inner.Generics, t.Args)
		sub := &AdtInfo{Def: inner.Def}
		for _, f := range inner.Fields {
			sub.Fields = append(sub.Fields, &Field{Type: c.subst(f.Type, m, nil)})
		}
		for _, v := range inner.Variants {
			nv := &VariantInfo{}
			for _, f := range v.Fields {
				nv.Fields = append(nv.Fields, &Field{Type: c.subst(f.Type, m, nil)})
			}
			sub.Variants = append(sub.Variants, nv)
		}
		return c.containsByValue(sub, target, onStack, depth+1)
	case KTuple:
		for _, e := range t.Elems {
			if c.typeContains(e, target, onStack, depth) {
				return true
			}
		}
	case KArray:
		return t.Len > 0 && c.typeContains(t.Elem, target, onStack, depth)
	}
	return false
}

// isLocal reports whether t counts as defined in crate cr for the orphan
// rule: a struct or enum of the crate, possibly behind & or &mut, or a dyn
// type of one of its traits.
func isLocal(t *Type, cr *resolve.Crate) bool {
	switch t.Kind {
	case KAdt:
		return t.Def.Crate == cr
	case KRef:
		return isLocal(t.Elem, cr)
	case KDyn:
		return len(t.Bounds) > 0 && t.Bounds[0].Trait.Crate == cr
	}
	return false
}

func (c *collector) checkImpl(d *resolve.Def) {
	ii := c.impl(d)
	n := d.Node.(*syntax.ImplDecl)
	if n.Trait == nil {
		c.checkInherentImpl(d, ii)
		return
	}
	if ii.Trait == nil {
		return // the trait path was already reported
	}
	ti := c.trait(ii.Trait.Trait)
	if ti == nil {
		return
	}
	tn := ti.Def.Node.(*syntax.TraitDecl)
	switch {
	case tn.Unsafe && !n.Unsafe:
		c.errorf(d.File, d.Pos, "implementing the unsafe trait %s requires an unsafe impl", ti.Def.Name)
	case !tn.Unsafe && n.Unsafe:
		c.errorf(d.File, d.Pos, "trait %s is not unsafe, so it cannot have an unsafe impl", ti.Def.Name)
	}

	if !d.Crate.IsStd && ti.Def.Crate != d.Crate && !isLocal(ii.SelfTy, d.Crate) {
		local := false
		for _, a := range ii.Trait.Args {
			if isLocal(a, d.Crate) {
				local = true
			}
		}
		if !local {
			c.errorf(d.File, d.Pos, "only traits defined in this crate can be implemented for types defined outside of it (impl %s for %s)", ii.Trait, ii.SelfTy)
		}
	}

	for name, item := range ii.Items {
		ti := ti.Items[name]
		if ti == nil {
			c.errorf(item.File, item.Pos, "%s is not a member of trait %s", name, ii.Trait.Trait.Name)
			continue
		}
		if ti.Kind != item.Kind {
			c.errorf(item.File, item.Pos, "%s is an %s in trait %s, but an %s here", name, ti.Kind, ii.Trait.Trait.Name, item.Kind)
			continue
		}
		if item.Kind == resolve.DefAssocFn {
			c.checkMethodShape(item, ti, ii.Trait.Trait)
		}
	}
	var missing []string
	for _, it := range ti.Order {
		if ii.Items[it.Name] != nil || c.hasDefault(it) {
			continue
		}
		missing = append(missing, it.Name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		c.errorf(d.File, d.Pos, "not all items of trait %s are implemented; missing: %s", ii.Trait.Trait.Name, strings.Join(missing, ", "))
	}
}

// hasDefault reports whether a trait item has a default (body, value or type).
func (c *collector) hasDefault(it *resolve.Def) bool {
	switch n := it.Node.(type) {
	case *syntax.FnDecl:
		return n.Body != nil
	case *syntax.ConstDecl:
		return n.Value != nil
	case *syntax.TypeAlias:
		return n.Type != nil
	}
	return false
}

// checkMethodShape compares the receiver and parameter count of an impl
// method with the trait declaration; full signature comparison happens
// during type checking.
func (c *collector) checkMethodShape(impl, decl *resolve.Def, trait *resolve.Def) {
	is, ds := impl.Node.(*syntax.FnDecl), decl.Node.(*syntax.FnDecl)
	switch {
	case is.Self == nil && ds.Self != nil:
		c.errorf(impl.File, impl.Pos, "method %s has a self parameter in trait %s but not here", impl.Name, trait.Name)
	case is.Self != nil && ds.Self == nil:
		c.errorf(impl.File, impl.Pos, "method %s has a self parameter here but not in trait %s", impl.Name, trait.Name)
	case is.Self != nil && (is.Self.Ref != ds.Self.Ref || is.Self.Ref && is.Self.Mut != ds.Self.Mut):
		c.errorf(impl.File, impl.Pos, "method %s takes %s but the declaration in trait %s takes %s", impl.Name, selfDesc(is.Self), trait.Name, selfDesc(ds.Self))
	case len(is.Params) != len(ds.Params):
		c.errorf(impl.File, impl.Pos, "method %s has %d parameter%s but the declaration in trait %s has %d", impl.Name, len(is.Params), plural(len(is.Params)), trait.Name, len(ds.Params))
	}
}

func selfDesc(s *syntax.SelfParam) string {
	switch {
	case s.Ref && s.Mut:
		return "&mut self"
	case s.Ref:
		return "&self"
	}
	return "self"
}

func (c *collector) checkInherentImpl(d *resolve.Def, ii *ImplInfo) {
	t := ii.SelfTy
	switch {
	case t.Kind == KError:
	case t.Kind == KAdt:
		if t.Def.Crate != d.Crate {
			c.errorf(d.File, d.Pos, "cannot define an inherent impl for %s, which is defined in crate %s; use a trait instead", t, t.Def.Crate.Name)
		}
	case t.Kind == KParam || t.Kind == KProj || t.Kind == KOpaque:
		c.errorf(d.File, d.Pos, "cannot define an inherent impl for the type parameter %s", t)
	case !d.Crate.IsStd:
		c.errorf(d.File, d.Pos, "inherent impls for %s can only be defined in std; use a trait instead", t)
	}
	for _, it := range ii.Items {
		if it.Kind == resolve.DefAssocType {
			c.errorf(it.File, it.Pos, "associated types are only allowed in trait impls")
		}
	}
}

func (c *collector) checkTrait(d *resolve.Def) {
	ti := c.trait(d)
	if ti == nil {
		return
	}
	// Supertrait cycles: walkSupertraits stops at repeats, so look for d
	// among its own supertraits.
	cycle := false
	for _, s := range ti.Supers {
		c.walkSupertraits(s, func(t *TraitRef) {
			if t.Trait == d {
				cycle = true
			}
		})
	}
	if cycle {
		c.errorf(d.File, d.Pos, "trait %s is its own supertrait (through a cycle)", d.Name)
	}
}
