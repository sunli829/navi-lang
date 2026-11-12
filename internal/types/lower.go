package types

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// env is the context for lowering type syntax.
type env struct {
	file   *syntax.File
	crate  *resolve.Crate
	gen    *Generics
	selfTy *Type // what Self means here (impl self type, trait Self, or nil)
	// implTrait is the trait an impl implements, for Self::Name in trait impls.
	implTrait *TraitRef
	// For trait items: the trait, so that Self::Name finds its assoc types.
	trait *resolve.Def
	// astGen lists the generics syntax in scope (innermost last), used to
	// find the bounds of a parameter for T::Name shorthand.
	astGen []*syntax.Generics

	// Where impl Trait is allowed and what it becomes.
	fnDef    *resolve.Def
	implArg  *[]*TypeParam // argument position: synthesized parameters
	implRet  *[]*Type      // return position: opaque types
	implNote string        // why impl Trait is not allowed, when it is not
}

func (e *env) with(f func(*env)) *env {
	n := *e
	f(&n)
	return &n
}

// lowerType converts type syntax into a Type.
func (c *collector) lowerType(e *env, t syntax.Type) *Type {
	ctx := c.info.Ctx
	switch t := t.(type) {
	case nil:
		return ctx.Unit
	case *syntax.PathType:
		return c.lowerPathType(e, t.Path)
	case *syntax.RefType:
		return ctx.Ref(t.Mut, c.lowerType(e, t.Elem))
	case *syntax.PtrType:
		return ctx.Ptr(t.Mut, c.lowerType(e, t.Elem))
	case *syntax.SliceType:
		return ctx.Slice(c.lowerType(e, t.Elem))
	case *syntax.ArrayType:
		elem := c.lowerType(e, t.Elem)
		n, ok := c.evalConst(e.file, t.Len)
		if !ok {
			return ctx.Err
		}
		if n < 0 {
			c.errorf(e.file, t.Len.Pos(), "array length cannot be negative")
			return ctx.Err
		}
		return ctx.Array(elem, n)
	case *syntax.TupleType:
		elems := make([]*Type, len(t.Elems))
		for i, el := range t.Elems {
			elems[i] = c.lowerType(e, el)
		}
		return ctx.Tuple(elems)
	case *syntax.FnType:
		inner := e.with(func(n *env) { n.implArg, n.implRet, n.implNote = nil, nil, "function pointer types" })
		params := make([]*Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = c.lowerType(inner, p)
		}
		ret := ctx.Unit
		if t.Result != nil {
			ret = c.lowerType(inner, t.Result)
		}
		return ctx.Fn(params, ret, t.Abi)
	case *syntax.DynType:
		var bounds []*TraitRef
		for _, b := range t.Bounds {
			if tr := c.lowerTraitRef(e, b, nil); tr != nil {
				bounds = append(bounds, tr)
			}
		}
		if len(bounds) == 0 {
			return ctx.Err
		}
		c.checkDyn(e.file, t.Pos(), bounds)
		return ctx.Dyn(bounds)
	case *syntax.ImplType:
		return c.lowerImplTrait(e, t)
	case *syntax.NeverType:
		return ctx.Never
	case *syntax.InferType:
		c.errorf(e.file, t.Pos(), "the placeholder _ is not allowed in item signatures")
		return ctx.Err
	}
	return ctx.Err
}

func (c *collector) lowerImplTrait(e *env, t *syntax.ImplType) *Type {
	ctx := c.info.Ctx
	switch {
	case e.implArg != nil:
		// Argument position: an anonymous type parameter with these bounds.
		def := &resolve.Def{Kind: resolve.DefTypeParam, Name: "impl " + syntax.Dump(t)[6:len(syntax.Dump(t))-1], Crate: e.crate, File: e.file, Pos: t.Pos()}
		p := &TypeParam{Def: def, Type: ctx.Param(def)}
		*e.implArg = append(*e.implArg, p)
		c.paramOwner[p] = e.gen
		for _, b := range t.Bounds {
			if tr := c.lowerTraitRef(e, b, p.Type); tr != nil {
				e.gen.Preds = append(e.gen.Preds, &Pred{Trait: tr})
			}
		}
		return p.Type
	case e.implRet != nil:
		var bounds []*TraitRef
		for _, b := range t.Bounds {
			if tr := c.lowerTraitRef(e, b, nil); tr != nil {
				bounds = append(bounds, tr)
			}
		}
		o := ctx.Opaque(e.fnDef, len(*e.implRet), bounds)
		*e.implRet = append(*e.implRet, o)
		return o
	}
	where := e.implNote
	if where == "" {
		where = "this position"
	}
	c.errorf(e.file, t.Pos(), "impl Trait is only allowed in function parameter and return types, not in %s", where)
	return ctx.Err
}

// lowerPathType lowers a named type.
func (c *collector) lowerPathType(e *env, p *syntax.Path) *Type {
	ctx := c.info.Ctx
	res, ok := c.info.Res.Paths[p]
	if !ok || res.Kind == resolve.ResErr {
		return ctx.Err
	}
	last := p.Segments[len(p.Segments)-1-len(res.Rest)]
	var base *Type
	switch res.Kind {
	case resolve.ResPrim:
		base = c.prim(res.Prim)
		if last.Args != nil {
			c.errorf(e.file, last.Args.Lt, "primitive type %s takes no generic arguments", res.Prim)
		}
	case resolve.ResSelfTy:
		if e.selfTy == nil {
			c.errorf(e.file, p.Pos(), "Self is not available here")
			return ctx.Err
		}
		base = e.selfTy
	case resolve.ResQualified:
		return c.lowerQualified(e, p)
	case resolve.ResDef:
		d := res.Def
		switch d.Kind {
		case resolve.DefStruct, resolve.DefEnum:
			base = ctx.Adt(d, c.lowerGenericArgs(e, d, c.genericsOf(d), last.Args, nil, p.Pos()))
		case resolve.DefTypeAlias:
			ai := c.alias(d)
			if ai == nil {
				return ctx.Err
			}
			args := c.lowerGenericArgs(e, d, ai.Generics, last.Args, nil, p.Pos())
			base = c.subst(ai.Type, paramMap(ai.Generics, args), nil)
		case resolve.DefTypeParam:
			base = ctx.Param(d)
		default:
			c.errorf(e.file, p.Pos(), "expected type, found %s %s", d.Kind, d.Name)
			return ctx.Err
		}
	default:
		c.errorf(e.file, p.Pos(), "expected type")
		return ctx.Err
	}
	if len(res.Rest) == 0 {
		return base
	}
	if len(res.Rest) > 1 {
		c.errorf(e.file, res.Rest[1].Name.NamePos, "nested associated type paths are not supported")
		return ctx.Err
	}
	return c.assocShorthand(e, base, res.Rest[0], p.Pos())
}

// lowerQualified lowers <T as Trait>::Name.
func (c *collector) lowerQualified(e *env, p *syntax.Path) *Type {
	ctx := c.info.Ctx
	if len(p.Segments) != 1 {
		c.errorf(e.file, p.Pos(), "expected a single associated type name after <T as Trait>")
		return ctx.Err
	}
	self := c.lowerType(e, p.QSelf.Type)
	if p.QSelf.Trait == nil {
		c.errorf(e.file, p.Pos(), "ambiguous associated type; write <%s as Trait>::%s", self, p.Segments[0].Name.Value)
		return ctx.Err
	}
	tr := c.lowerTraitRef(e, p.QSelf.Trait, self)
	if tr == nil {
		return ctx.Err
	}
	name := p.Segments[0].Name.Value
	assoc := c.findAssocType(tr.Trait, name)
	if assoc == nil {
		c.errorf(e.file, p.Segments[0].Name.NamePos, "trait %s has no associated type %s", tr.Trait.Name, name)
		return ctx.Err
	}
	return ctx.Proj(assoc, c.traitRefFor(tr, assoc.Parent))
}

// assocShorthand resolves Base::Name where Base is a type parameter or
// Self: the trait is found among the bounds of the parameter.
func (c *collector) assocShorthand(e *env, base *Type, seg *syntax.PathSegment, pos syntax.Pos) *Type {
	ctx := c.info.Ctx
	name := seg.Name.Value
	if seg.Args != nil {
		c.errorf(e.file, seg.Args.Lt, "associated types take no generic arguments")
	}
	if base.Kind == KError {
		return base
	}
	var candidates []*TraitRef
	switch {
	case base.Kind == KParam && base.Self:
		// Self inside a trait: the trait itself (and its supertraits).
		candidates = append(candidates, c.selfTraitRef(base.Def))
	case base.Kind == KParam:
		candidates = c.paramBounds(e, base)
	case e.implTrait != nil && base == e.selfTy:
		candidates = append(candidates, e.implTrait)
	default:
		c.errorf(e.file, seg.Name.NamePos, "ambiguous associated type %s::%s; write <%s as Trait>::%s", base, name, base, name)
		return ctx.Err
	}
	var hits []*Type
	seen := map[*resolve.Def]bool{}
	for _, tr := range candidates {
		c.walkSupertraits(tr, func(sup *TraitRef) {
			if seen[sup.Trait] {
				return
			}
			seen[sup.Trait] = true
			if a := c.findOwnAssocType(sup.Trait, name); a != nil {
				hits = append(hits, ctx.Proj(a, sup))
			}
		})
	}
	switch len(hits) {
	case 0:
		c.errorf(e.file, seg.Name.NamePos, "associated type %s not found for %s", name, base)
		return ctx.Err
	case 1:
		return hits[0]
	}
	c.errorf(e.file, seg.Name.NamePos, "ambiguous associated type %s for %s: it is defined in more than one bound", name, base)
	return ctx.Err
}

// paramBounds returns the trait bounds of a type parameter declared in the
// generics syntax in scope (bounds on the parameter and where clauses). Only
// the traits' own arguments are lowered.
func (c *collector) paramBounds(e *env, param *Type) []*TraitRef {
	var out []*TraitRef
	paths := c.info.Res.Paths
	for _, g := range e.astGen {
		if g == nil {
			continue
		}
		for _, gp := range g.Params {
			if c.info.Res.DefOf[gp] == param.Def {
				for _, b := range gp.Bounds {
					if tr := c.lowerTraitRefQuiet(e, b, param); tr != nil {
						out = append(out, tr)
					}
				}
			}
		}
		for _, wp := range g.Where {
			pt, ok := wp.Type.(*syntax.PathType)
			if !ok || len(pt.Path.Segments) != 1 {
				continue
			}
			if res := paths[pt.Path]; res.Kind == resolve.ResDef && res.Def == param.Def && len(res.Rest) == 0 {
				for _, b := range wp.Bounds {
					if tr := c.lowerTraitRefQuiet(e, b, param); tr != nil {
						out = append(out, tr)
					}
				}
			}
		}
	}
	return out
}

// lowerTraitRefQuiet lowers a bound without reporting errors (they are
// reported when the predicate itself is lowered).
func (c *collector) lowerTraitRefQuiet(e *env, p *syntax.Path, self *Type) *TraitRef {
	c.quiet++
	defer func() { c.quiet-- }()
	return c.lowerTraitRef(e, p, self)
}

// lowerTraitRef lowers a trait bound or dyn component with the given self
// type (nil for dyn).
func (c *collector) lowerTraitRef(e *env, p *syntax.Path, self *Type) *TraitRef {
	res, ok := c.info.Res.Paths[p]
	if !ok || res.Kind != resolve.ResDef || res.Def.Kind != resolve.DefTrait || len(res.Rest) > 0 {
		return nil
	}
	trait := res.Def
	ti := c.trait(trait)
	if ti == nil {
		return nil
	}
	last := p.Segments[len(p.Segments)-1]
	tr := &TraitRef{Trait: trait, Self: self}
	tr.Args = c.lowerGenericArgs(e, trait, ti.Generics, last.Args, self, p.Pos())
	if last.Args != nil {
		for _, b := range last.Args.Bindings {
			ty := c.lowerType(e.with(func(n *env) { n.implArg, n.implRet, n.implNote = nil, nil, "associated type bindings" }), b.Type)
			if c.findAssocTypeInSupers(tr, b.Name.Value) == nil {
				c.errorf(e.file, b.Name.NamePos, "trait %s has no associated type %s", trait.Name, b.Name.Value)
				continue
			}
			tr.Bindings = append(tr.Bindings, AssocBinding{Name: b.Name.Value, Type: ty})
		}
	}
	return tr
}

// lowerGenericArgs lowers the generic arguments for an item with generics
// g, filling in defaults. self is the Self type for trait defaults.
func (c *collector) lowerGenericArgs(e *env, d *resolve.Def, g *Generics, args *syntax.GenericArgs, self *Type, pos syntax.Pos) []*Type {
	ctx := c.info.Ctx
	var params []*TypeParam
	if g != nil {
		params = g.Params
	}
	var given []*Type
	if args != nil {
		inner := e.with(func(n *env) { n.implArg, n.implRet, n.implNote = nil, nil, "generic arguments" })
		for _, a := range args.Args {
			given = append(given, c.lowerType(inner, a))
		}
		if d.Kind != resolve.DefTrait && len(args.Bindings) > 0 {
			c.errorf(e.file, args.Bindings[0].Name.NamePos, "associated type bindings are only allowed on traits")
		}
	}
	if len(given) > len(params) {
		c.errorf(e.file, pos, "%s %s takes %d generic argument%s but %d were supplied", d.Kind, d.Name, len(params), plural(len(params)), len(given))
		return padErr(ctx, len(params))
	}
	out := make([]*Type, len(params))
	copy(out, given)
	m := map[*resolve.Def]*Type{}
	for i, p := range params {
		if i < len(given) {
			m[p.Def] = given[i]
			continue
		}
		if p.Default == nil {
			c.errorf(e.file, pos, "missing generic argument%s for %s %s (expected %d, found %d)", plural(len(params)-len(given)), d.Kind, d.Name, len(params), len(given))
			return padErr(ctx, len(params))
		}
		if self == nil && mentionsSelf(p.Default) {
			c.errorf(e.file, pos, "the type parameter %s of %s must be specified explicitly here (its default refers to Self)", p.Def.Name, d.Name)
			out[i] = ctx.Err
			continue
		}
		out[i] = c.subst(p.Default, m, self)
		m[p.Def] = out[i]
	}
	return out
}

func padErr(ctx *Ctx, n int) []*Type {
	out := make([]*Type, n)
	for i := range out {
		out[i] = ctx.Err
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (c *collector) prim(name string) *Type {
	ctx := c.info.Ctx
	switch name {
	case "bool":
		return ctx.Bool
	case "char":
		return ctx.Char
	case "str":
		return ctx.Str
	case "f32":
		return ctx.F32
	case "f64":
		return ctx.F64
	}
	for k, n := range intNames {
		if n == name {
			return ctx.Ints[k]
		}
	}
	return ctx.Err
}

// ---------------------------------------------------------------------------
// Substitution

// paramMap maps the parameters of g to args.
func paramMap(g *Generics, args []*Type) map[*resolve.Def]*Type {
	m := map[*resolve.Def]*Type{}
	if g == nil {
		return m
	}
	for i, p := range g.Params {
		if i < len(args) {
			m[p.Def] = args[i]
		}
	}
	return m
}

// subst replaces type parameters according to m, and trait Self by self
// (when non-nil).
func (c *collector) subst(t *Type, m map[*resolve.Def]*Type, self *Type) *Type {
	return Subst(c.info.Ctx, t, m, self)
}

// Subst replaces type parameters in t according to m, and the Self
// parameter of traits by self (when non-nil).
func Subst(ctx *Ctx, t *Type, m map[*resolve.Def]*Type, self *Type) *Type {
	if t == nil {
		return nil
	}
	if len(m) == 0 && self == nil {
		return t
	}
	sl := func(ts []*Type) []*Type {
		if ts == nil {
			return nil
		}
		out := make([]*Type, len(ts))
		for i, x := range ts {
			out[i] = Subst(ctx, x, m, self)
		}
		return out
	}
	switch t.Kind {
	case KParam:
		if t.Self {
			if self != nil {
				return self
			}
			return t
		}
		if r, ok := m[t.Def]; ok {
			return r
		}
		return t
	case KRef:
		return ctx.Ref(t.Mut, Subst(ctx, t.Elem, m, self))
	case KPtr:
		return ctx.Ptr(t.Mut, Subst(ctx, t.Elem, m, self))
	case KSlice:
		return ctx.Slice(Subst(ctx, t.Elem, m, self))
	case KArray:
		return ctx.Array(Subst(ctx, t.Elem, m, self), t.Len)
	case KTuple:
		return ctx.Tuple(sl(t.Elems))
	case KFn:
		return ctx.Fn(sl(t.Elems), Subst(ctx, t.Ret, m, self), t.Abi)
	case KAdt:
		return ctx.Adt(t.Def, sl(t.Args))
	case KProj:
		return ctx.Proj(t.Def, SubstTraitRef(ctx, t.Trait, m, self))
	case KDyn:
		bs := make([]*TraitRef, len(t.Bounds))
		for i, b := range t.Bounds {
			bs[i] = SubstTraitRef(ctx, b, m, self)
		}
		return ctx.Dyn(bs)
	case KOpaque:
		bs := make([]*TraitRef, len(t.Bounds))
		for i, b := range t.Bounds {
			bs[i] = SubstTraitRef(ctx, b, m, self)
		}
		return ctx.Opaque(t.Def, t.Index, bs)
	}
	return t
}

// SubstTraitRef substitutes inside a trait reference.
func SubstTraitRef(ctx *Ctx, tr *TraitRef, m map[*resolve.Def]*Type, self *Type) *TraitRef {
	if tr == nil {
		return nil
	}
	out := &TraitRef{Trait: tr.Trait, Self: Subst(ctx, tr.Self, m, self)}
	for _, a := range tr.Args {
		out.Args = append(out.Args, Subst(ctx, a, m, self))
	}
	for _, b := range tr.Bindings {
		out.Bindings = append(out.Bindings, AssocBinding{Name: b.Name, Type: Subst(ctx, b.Type, m, self)})
	}
	return out
}

// mentionsSelf reports whether t contains the Self parameter of a trait.
func mentionsSelf(t *Type) bool {
	found := false
	visit(t, func(x *Type) {
		if x.Kind == KParam && x.Self {
			found = true
		}
	})
	return found
}

// visit calls f for t and every type nested in it.
func visit(t *Type, f func(*Type)) {
	if t == nil {
		return
	}
	f(t)
	switch t.Kind {
	case KRef, KPtr, KSlice, KArray:
		visit(t.Elem, f)
	case KTuple:
		for _, e := range t.Elems {
			visit(e, f)
		}
	case KFn:
		for _, e := range t.Elems {
			visit(e, f)
		}
		visit(t.Ret, f)
	case KAdt:
		for _, a := range t.Args {
			visit(a, f)
		}
	case KProj:
		visit(t.Trait.Self, f)
		for _, a := range t.Trait.Args {
			visit(a, f)
		}
	case KDyn, KOpaque:
		for _, b := range t.Bounds {
			for _, a := range b.Args {
				visit(a, f)
			}
			for _, bd := range b.Bindings {
				visit(bd.Type, f)
			}
		}
	}
}
