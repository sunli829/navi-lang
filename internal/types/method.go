package types

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// methodPick is the result of method lookup.
type methodPick struct {
	def     *resolve.Def
	impl    *ImplInfo // inherent or trait impl owning def (nil for trait methods)
	trait   *TraitRef // for trait methods
	step    *Type     // receiver type after autoderef
	derefs  int
	autoref int // 0, 1 (&), 2 (&mut)
	unsize  bool
}

// lookupMethod finds the method name for a receiver of type recv, following
// Rust's probe order: for each autoderef step, by value, then &, then &mut;
// inherent methods before trait methods.
func (fc *fnCtx) lookupMethod(recv *Type, name string, pos syntax.Pos) *methodPick {
	steps := fc.autoderef(recv, false)
	last := fc.in.shallow(steps[len(steps)-1])
	unsized := -1
	if last.Kind == KArray {
		steps = append(steps, fc.in.ctx.Slice(last.Elem))
		unsized = len(steps) - 1
	}
	for i, s := range steps {
		s = fc.in.shallow(s)
		if s.Kind == KInfer && fc.in.vars[s.Var].kind == varGeneral {
			return nil
		}
		for autoref := 0; autoref <= 2; autoref++ {
			adjusted := s
			switch autoref {
			case 1:
				adjusted = fc.in.ctx.Ref(false, s)
			case 2:
				adjusted = fc.in.ctx.Ref(true, s)
			}
			if p := fc.methodAt(adjusted, s, name, pos); p != nil {
				p.step = s
				p.derefs = i
				p.autoref = autoref
				if i == unsized {
					p.derefs = i - 1
					p.unsize = true
				}
				return p
			}
		}
	}
	return nil
}

// methodAt finds a method whose receiver type matches adjusted. base is the
// step type without autoref (used to find inherent impls and traits).
func (fc *fnCtx) methodAt(adjusted, base *Type, name string, pos syntax.Pos) *methodPick {
	ctx := fc.in.ctx
	// The type that owns the method is the receiver without references.
	owner := base
	for {
		o := fc.in.shallow(owner)
		if o.Kind != KRef {
			owner = o
			break
		}
		owner = o.Elem
	}
	tryRecv := func(fs *FnSig, m map[*resolve.Def]*Type, self *Type) bool {
		if fs.Self == nil {
			return false
		}
		r := fc.normalize(Subst(ctx, fs.Self, m, self))
		return fc.in.unify(r, adjusted)
	}
	for _, cand := range []*Type{owner, base} {
		for _, ii := range fc.c.inherentImpls(cand) {
			it := ii.Items[name]
			if it == nil || it.Kind != resolve.DefAssocFn {
				continue
			}
			fs := fc.c.fnSig(it)
			snap := fc.in.snapshot()
			m, _ := fc.instantiate(ii.Generics)
			self := fc.normalize(Subst(ctx, ii.SelfTy, m, nil))
			ok := tryRecv(fs, m, self)
			fc.in.rollback(snap)
			if ok {
				return &methodPick{def: it, impl: ii}
			}
		}
	}
	var picks []*methodPick
	for _, td := range fc.candidateTraits(owner) {
		ti := fc.c.trait(td)
		it := ti.Items[name]
		if it == nil || it.Kind != resolve.DefAssocFn {
			continue
		}
		fs := fc.c.fnSig(it)
		snap := fc.in.snapshot()
		self := fc.in.fresh()
		m, vars := fc.instantiate(&Generics{Params: ti.Generics.Params})
		tr := &TraitRef{Trait: td, Self: self, Args: vars}
		ok := tryRecv(fs, m, self) && fc.holds(tr) != selNo
		fc.in.rollback(snap)
		if ok {
			picks = append(picks, &methodPick{def: it, trait: &TraitRef{Trait: td}})
		}
	}
	if len(picks) > 1 {
		fc.errorf(pos, "multiple applicable methods named %s (from traits %s and %s); call it as Trait::%s(...)", name, picks[0].trait.Trait.Name, picks[1].trait.Trait.Name, name)
	}
	if len(picks) > 0 {
		return picks[0]
	}
	return nil
}

// methodCall type-checks recv.name::<T>(args).
func (fc *fnCtx) methodCall(x *syntax.MethodCallExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	recv := fc.infer(x.Recv, nil)
	fc.solvePending(false)
	p := fc.lookupMethod(recv, x.Name.Value, x.Name.NamePos)
	if p == nil {
		r := fc.in.resolve(recv)
		if r.Kind != KError {
			if hasInfer(r) {
				fc.errorf(x.Name.NamePos, "type annotations needed: the type of the receiver of %s must be known", x.Name.Value)
			} else if fc.hasField(r, x.Name.Value) {
				fc.errorf(x.Name.NamePos, "%s is a field, not a method; to call a function stored in a field write (expr.%s)(...)", x.Name.Value, x.Name.Value)
			} else {
				fc.errorf(x.Name.NamePos, "no method named %s found for %s", x.Name.Value, r)
			}
		}
		for _, a := range x.Args {
			fc.infer(a, nil)
		}
		return ctx.Err
	}
	if !p.def.Vis.AccessibleFrom(fc.module) {
		fc.errorf(x.Name.NamePos, "method %s is private", x.Name.Value)
	}
	if st := fc.in.shallow(p.step); st.Kind == KDyn && p.trait != nil && !fc.c.dynCallable(p.def) {
		fc.errorf(x.Name.NamePos, "method %s cannot be called on dyn %s: it is generic, takes self by value or mentions Self", x.Name.Value, st.Bounds[0].Trait.Name)
	}
	fs := fc.c.fnSig(p.def)
	m := map[*resolve.Def]*Type{}
	var args []*Type
	var self *Type
	if p.impl != nil {
		om, vars := fc.instantiate(p.impl.Generics)
		for k, v := range om {
			m[k] = v
		}
		args = append(args, vars...)
		self = fc.normalize(Subst(ctx, p.impl.SelfTy, om, nil))
		fc.requirePreds(&Generics{Preds: p.impl.Generics.AllPreds()}, om, nil, x.Pos())
	} else {
		ti := fc.c.trait(p.trait.Trait)
		self = fc.in.fresh()
		tm, vars := fc.instantiate(&Generics{Params: ti.Generics.Params})
		for k, v := range tm {
			m[k] = v
		}
		args = append(args, vars...)
		fc.require(&TraitRef{Trait: p.trait.Trait, Self: self, Args: vars}, x.Pos())
	}
	own, vars := fc.instantiate(&Generics{Params: fs.Generics.Params})
	for k, v := range own {
		m[k] = v
	}
	args = append(args, vars...)
	fc.explicitArgs(fs.Generics.Params, x.TypeArgs, m, "method "+x.Name.Value)
	fc.requirePreds(&Generics{Preds: fs.Generics.Preds}, m, self, x.Pos())

	recvTy := fc.normalize(Subst(ctx, fs.Self, m, self))
	adjusted := p.step
	switch p.autoref {
	case 1:
		adjusted = ctx.Ref(false, p.step)
	case 2:
		adjusted = ctx.Ref(true, p.step)
		fc.checkMutPlace(x.Recv, p.derefs, "borrow as mutable", x.Name.NamePos)
	}
	if !fc.in.unify(recvTy, adjusted) {
		fc.mismatch(x.Recv.Pos(), recvTy, adjusted)
	}
	adj := &Adjust{Derefs: p.derefs, Autoref: p.autoref, Unsize: p.unsize}
	fc.body.Callees[x] = &Callee{Def: p.def, Args: args, Self: self, Recv: adj}
	if fs.Unsafe {
		fc.requireUnsafe(x.Pos(), "call to unsafe method "+x.Name.Value)
	}
	if len(x.Args) != len(fs.Params) {
		fc.errorf(x.Name.NamePos, "method %s takes %d argument%s but %d were supplied", x.Name.Value, len(fs.Params), plural(len(fs.Params)), len(x.Args))
	}
	for i, a := range x.Args {
		if i < len(fs.Params) {
			fc.check(a, fc.normalize(Subst(ctx, fs.Params[i], m, self)))
		} else {
			fc.infer(a, nil)
		}
	}
	return fc.normalize(Subst(ctx, fs.Ret, m, self))
}

// hasField reports whether t (after autoderef) has a field named name.
func (fc *fnCtx) hasField(t *Type, name string) bool {
	for _, s := range fc.autoderef(t, false) {
		s = fc.in.shallow(s)
		if s.Kind == KAdt {
			ai := fc.c.adt(s.Def)
			for _, f := range ai.Fields {
				if f.Name == name {
					return true
				}
			}
		}
	}
	return false
}
