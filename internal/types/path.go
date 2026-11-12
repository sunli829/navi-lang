package types

import (
	"fmt"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

// implKey groups types for inherent impl lookup: the ADT definition or the
// kind of primitive.
func implKey(t *Type) string {
	switch t.Kind {
	case KAdt:
		return fmt.Sprintf("adt%p", t.Def)
	case KInt:
		return t.Int.String()
	case KFloat:
		if t.F64 {
			return "f64"
		}
		return "f32"
	case KBool:
		return "bool"
	case KChar:
		return "char"
	case KStr:
		return "str"
	case KSlice:
		return "slice"
	case KArray:
		return "array"
	case KPtr:
		return "ptr"
	case KDyn:
		if len(t.Bounds) > 0 {
			return fmt.Sprintf("dyn%p", t.Bounds[0].Trait)
		}
	}
	return ""
}

// inherentImpls returns the inherent impls that may apply to t.
func (c *collector) inherentImpls(t *Type) []*ImplInfo {
	if c.inherent == nil {
		c.inherent = map[string][]*ImplInfo{}
		for _, ii := range c.info.ImplsOf[nil] {
			if k := implKey(ii.SelfTy); k != "" {
				c.inherent[k] = append(c.inherent[k], ii)
			}
		}
	}
	return c.inherent[implKey(t)]
}

// fnType builds the function type of a signature after substitution.
func (fc *fnCtx) fnType(fs *FnSig, m map[*resolve.Def]*Type, self *Type) *Type {
	ctx := fc.in.ctx
	var params []*Type
	if fs.Self != nil {
		params = append(params, fc.normalize(Subst(ctx, fs.Self, m, self)))
	}
	for _, p := range fs.Params {
		params = append(params, fc.normalize(Subst(ctx, p, m, self)))
	}
	ret := fc.normalize(Subst(ctx, fs.Ret, m, self))
	return ctx.Fn(params, ret, fs.Abi)
}

// requirePreds registers the where clauses of g (and parents) under m.
func (fc *fnCtx) requirePreds(g *Generics, m map[*resolve.Def]*Type, self *Type, pos syntax.Pos) {
	for _, p := range g.AllPreds() {
		tr := SubstTraitRef(fc.in.ctx, p.Trait, m, self)
		tr.Self = fc.normalize(tr.Self)
		for i, a := range tr.Args {
			tr.Args[i] = fc.normalize(a)
		}
		fc.require(tr, pos)
	}
}

// explicitArgs lowers turbofish arguments for params (the item's own
// parameters), unifying them with the fresh variables in m.
func (fc *fnCtx) explicitArgs(params []*TypeParam, args *syntax.GenericArgs, m map[*resolve.Def]*Type, what string) {
	if args == nil {
		return
	}
	if len(args.Args) > len(params) {
		fc.errorf(args.Lt, "%s takes %d generic argument%s but %d were supplied", what, len(params), plural(len(params)), len(args.Args))
		return
	}
	for i, a := range args.Args {
		t := fc.lowerOrInfer(a)
		fc.in.unify(m[params[i].Def], t)
	}
}

// valuePath types a path used as an expression.
func (fc *fnCtx) valuePath(x *syntax.PathExpr) *Type {
	ctx := fc.in.ctx
	res, ok := fc.info.Res.Paths[x.Path]
	if !ok || res.Kind == resolve.ResErr {
		return ctx.Err
	}
	segs := x.Path.Segments
	if len(res.Rest) > 0 || res.Kind == resolve.ResQualified {
		return fc.assocPath(x, res)
	}
	switch res.Kind {
	case resolve.ResLocal:
		if t := fc.body.Locals[res.Local]; t != nil {
			return t
		}
		return ctx.Err
	case resolve.ResSelfTy:
		if self := fc.env.selfTy; self != nil && self.Kind == KAdt && !fc.c.adt(self.Def).IsEnum {
			return fc.ctorType(fc.c.adt(self.Def).Def, self, x)
		}
		fc.errorf(x.Pos(), "Self is not a value here")
		return ctx.Err
	case resolve.ResDef:
	default:
		return ctx.Err
	}
	d := res.Def
	last := segs[len(segs)-1]
	switch d.Kind {
	case resolve.DefFn, resolve.DefExternFn:
		fs := fc.c.fnSig(d)
		m, vars := fc.instantiate(fs.Generics)
		fc.explicitArgs(fs.Generics.Params, last.Args, m, "function "+d.Name)
		fc.requirePreds(fs.Generics, m, nil, x.Pos())
		fc.body.Callees[x] = &Callee{Def: d, Args: vars}
		if fs.Unsafe || d.Kind == resolve.DefExternFn {
			fc.requireUnsafe(x.Pos(), "call to unsafe function "+d.Name)
		}
		return fc.fnType(fs, m, nil)
	case resolve.DefConst, resolve.DefStatic, resolve.DefExternStatic:
		ci := fc.c.constInfo(d)
		if d.Kind == resolve.DefExternStatic {
			fc.requireUnsafe(x.Pos(), "use of extern static "+d.Name)
		}
		fc.body.Callees[x] = &Callee{Def: d}
		return fc.normalize(ci.Type)
	case resolve.DefStruct:
		ai := fc.c.adt(d)
		m, vars := fc.instantiate(ai.Generics)
		fc.explicitArgs(ai.Generics.Params, last.Args, m, "struct "+d.Name)
		return fc.ctorType(d, ctx.Adt(d, vars), x)
	case resolve.DefVariant:
		enum := d.Parent
		ai := fc.c.adt(enum)
		m, vars := fc.instantiate(ai.Generics)
		// Option::<i32>::None or None::<i32>
		if len(segs) >= 2 && segs[len(segs)-2].Args != nil {
			fc.explicitArgs(ai.Generics.Params, segs[len(segs)-2].Args, m, "enum "+enum.Name)
		}
		fc.explicitArgs(ai.Generics.Params, last.Args, m, "enum "+enum.Name)
		return fc.ctorType(d, ctx.Adt(enum, vars), x)
	}
	fc.errorf(x.Pos(), "expected value, found %s %s", d.Kind, d.Name)
	return ctx.Err
}

// ctorType returns the type of a struct or variant constructor used as a
// value: the ADT itself for unit forms, a function for tuple forms.
func (fc *fnCtx) ctorType(d *resolve.Def, adt *Type, x syntax.Expr) *Type {
	ai := fc.c.adt(adt.Def)
	m := paramMap(ai.Generics, adt.Args)
	kind, fields := ai.Kind, ai.Fields
	if d.Kind == resolve.DefVariant {
		for _, v := range ai.Variants {
			if v.Def == d {
				kind, fields = v.Kind, v.Fields
			}
		}
	}
	fc.body.Callees[x] = &Callee{Def: d, Args: adt.Args}
	switch kind {
	case syntax.StructUnit:
		return adt
	case syntax.StructTuple:
		params := make([]*Type, len(fields))
		for i, f := range fields {
			params[i] = fc.normalize(Subst(fc.in.ctx, f.Type, m, nil))
		}
		return fc.in.ctx.Fn(params, adt, "")
	}
	fc.errorf(x.Pos(), "%s %s has named fields; use %s { ... }", d.Kind, d.Name, d.Name)
	return fc.in.ctx.Err
}

// baseType computes the type named by a path with associated-item
// segments, before the first such segment. Missing generic arguments
// become inference variables. ok is false for trait bases (Trait::item),
// in which case trait is set.
func (fc *fnCtx) baseType(p *syntax.Path, res resolve.Res) (t *Type, trait *TraitRef) {
	ctx := fc.in.ctx
	if res.Kind == resolve.ResQualified {
		self := fc.lower(p.QSelf.Type)
		if p.QSelf.Trait == nil {
			return self, nil
		}
		tr := fc.c.lowerTraitRef(fc.env, p.QSelf.Trait, self)
		return self, tr
	}
	seg := p.Segments[len(p.Segments)-1-len(res.Rest)]
	switch res.Kind {
	case resolve.ResPrim:
		return fc.c.prim(res.Prim), nil
	case resolve.ResSelfTy:
		if fc.env.selfTy == nil {
			return ctx.Err, nil
		}
		return fc.env.selfTy, nil
	case resolve.ResDef:
		d := res.Def
		switch d.Kind {
		case resolve.DefStruct, resolve.DefEnum:
			ai := fc.c.adt(d)
			m, vars := fc.instantiate(ai.Generics)
			fc.explicitArgs(ai.Generics.Params, seg.Args, m, d.Kind.String()+" "+d.Name)
			return ctx.Adt(d, vars), nil
		case resolve.DefTypeAlias:
			ai := fc.c.alias(d)
			if ai == nil {
				return ctx.Err, nil
			}
			m, _ := fc.instantiate(ai.Generics)
			fc.explicitArgs(ai.Generics.Params, seg.Args, m, "type alias "+d.Name)
			return fc.normalize(Subst(ctx, ai.Type, m, nil)), nil
		case resolve.DefTypeParam:
			return ctx.Param(d), nil
		case resolve.DefTrait:
			ti := fc.c.trait(d)
			self := fc.in.fresh()
			tr := &TraitRef{Trait: d, Self: self}
			m, vars := fc.instantiate(&Generics{Params: ti.Generics.Params})
			fc.explicitArgs(ti.Generics.Params, seg.Args, m, "trait "+d.Name)
			tr.Args = vars
			return self, tr
		}
	}
	return ctx.Err, nil
}

// assocPath types Base::item: an enum variant through an alias or Self, an
// associated function or constant of an inherent impl or of a trait.
func (fc *fnCtx) assocPath(x *syntax.PathExpr, res resolve.Res) *Type {
	ctx := fc.in.ctx
	if len(res.Rest) > 1 {
		fc.errorf(res.Rest[1].Name.NamePos, "nested associated item paths are not supported")
		return ctx.Err
	}
	var seg *syntax.PathSegment
	if res.Kind == resolve.ResQualified {
		seg = x.Path.Segments[0]
	} else {
		seg = res.Rest[0]
	}
	name := seg.Name.Value
	base, trait := fc.baseType(x.Path, res)
	base = fc.in.shallow(base)
	if base.Kind == KError {
		return ctx.Err
	}
	// Variants through Self or an alias: Self::Circle.
	if trait == nil && base.Kind == KAdt {
		ai := fc.c.adt(base.Def)
		for _, v := range ai.Variants {
			if v.Def.Name == name {
				return fc.ctorType(v.Def, base, x)
			}
		}
	}
	item, owner, self, tr := fc.findAssoc(base, trait, name, x.Pos())
	if item == nil {
		fc.errorf(seg.Name.NamePos, "no associated item named %s found for %s", name, fc.in.resolve(base))
		return ctx.Err
	}
	if !item.Vis.AccessibleFrom(fc.module) {
		fc.errorf(seg.Name.NamePos, "%s %s is private", item.Kind, name)
	}
	m := map[*resolve.Def]*Type{}
	var args []*Type
	if owner != nil { // inherent or trait impl: instantiate and match its self type
		om, vars := fc.instantiate(owner.Generics)
		for k, v := range om {
			m[k] = v
		}
		args = append(args, vars...)
		fc.in.unify(fc.normalize(Subst(ctx, owner.SelfTy, om, nil)), base)
		fc.requirePreds(&Generics{Preds: owner.Generics.AllPreds()}, om, nil, x.Pos())
	} else if tr != nil { // trait item: Self and trait parameters
		ti := fc.c.trait(tr.Trait)
		for i, p := range ti.Generics.Params {
			if i < len(tr.Args) {
				m[p.Def] = tr.Args[i]
			}
		}
		args = append(args, tr.Args...)
		fc.require(tr, x.Pos())
	}
	switch item.Kind {
	case resolve.DefAssocFn:
		fs := fc.c.fnSig(item)
		own, vars := fc.instantiate(&Generics{Params: fs.Generics.Params})
		for k, v := range own {
			m[k] = v
		}
		args = append(args, vars...)
		fc.explicitArgs(fs.Generics.Params, seg.Args, m, "function "+name)
		fc.requirePreds(&Generics{Preds: fs.Generics.Preds}, m, self, x.Pos())
		fc.body.Callees[x] = &Callee{Def: item, Args: args, Self: self}
		if fs.Unsafe {
			fc.requireUnsafe(x.Pos(), "call to unsafe function "+name)
		}
		return fc.fnType(fs, m, self)
	case resolve.DefAssocConst:
		ci := fc.c.constInfo(item)
		fc.body.Callees[x] = &Callee{Def: item, Args: args, Self: self}
		return fc.normalize(Subst(ctx, ci.Type, m, self))
	}
	fc.errorf(seg.Name.NamePos, "%s is an associated type, not a value", name)
	return ctx.Err
}

// findAssoc finds an associated fn or const named name for base: inherent
// impls first, then traits (the given one, bounds of parameters, or traits
// in scope that base implements). It returns the item and either the impl
// that owns it or the trait reference it comes from, plus the Self type.
func (fc *fnCtx) findAssoc(base *Type, trait *TraitRef, name string, pos syntax.Pos) (*resolve.Def, *ImplInfo, *Type, *TraitRef) {
	if trait != nil {
		var hit *resolve.Def
		var owner *TraitRef
		fc.c.walkSupertraits(trait, func(t *TraitRef) {
			if hit != nil {
				return
			}
			if it := fc.c.trait(t.Trait).Items[name]; it != nil && it.Kind != resolve.DefAssocType {
				hit, owner = it, t
			}
		})
		return hit, nil, trait.Self, owner
	}
	for _, ii := range fc.c.inherentImpls(base) {
		it := ii.Items[name]
		if it == nil || it.Kind == resolve.DefAssocType {
			continue
		}
		snap := fc.in.snapshot()
		om, _ := fc.instantiate(ii.Generics)
		ok := fc.in.unify(fc.normalize(Subst(fc.in.ctx, ii.SelfTy, om, nil)), base)
		fc.in.rollback(snap)
		if ok {
			return it, ii, base, nil
		}
	}
	var hits []*TraitRef
	var items []*resolve.Def
	for _, td := range fc.candidateTraits(base) {
		ti := fc.c.trait(td)
		it := ti.Items[name]
		if it == nil || it.Kind == resolve.DefAssocType {
			continue
		}
		tr := &TraitRef{Trait: td, Self: base}
		_, vars := fc.instantiate(&Generics{Params: ti.Generics.Params})
		tr.Args = vars
		if fc.holds(tr) != selNo {
			hits = append(hits, tr)
			items = append(items, it)
		}
	}
	if len(hits) > 1 {
		fc.errorf(pos, "multiple applicable items named %s for %s (from traits %s and %s)", name, fc.in.resolve(base), hits[0].Trait.Name, hits[1].Trait.Name)
	}
	if len(hits) > 0 {
		return items[0], nil, base, hits[0]
	}
	return nil, nil, nil, nil
}

// candidateTraits lists traits whose items may apply to t: traits in scope,
// plus the bounds of parameters, dyn and impl Trait types.
func (fc *fnCtx) candidateTraits(t *Type) []*resolve.Def {
	seen := map[*resolve.Def]bool{}
	var out []*resolve.Def
	add := func(d *resolve.Def) {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	s := fc.in.shallow(t)
	for _, p := range fc.preds {
		if fc.in.shallow(p.Self) == s {
			add(p.Trait)
		}
	}
	if s.Kind == KDyn || s.Kind == KOpaque {
		for _, b := range s.Bounds {
			fc.c.walkSupertraits(&TraitRef{Trait: b.Trait, Self: s, Args: b.Args}, func(tr *TraitRef) { add(tr.Trait) })
		}
	}
	for _, d := range fc.traits {
		add(d)
	}
	return out
}

func (fc *fnCtx) requireUnsafe(pos syntax.Pos, what string) {
	if fc.unsafe == 0 {
		fc.errorf(pos, "%s requires an unsafe block or function", what)
	}
}
