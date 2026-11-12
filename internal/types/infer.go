package types

// Inference variables and unification, local to one function body.

type varKind uint8

const (
	varGeneral varKind = iota
	varInt             // from an integer literal: only integer types
	varFloat           // from a float literal: only float types
)

type varInfo struct {
	t     *Type // the KInfer type object
	kind  varKind
	bound *Type // nil while unresolved
}

type infer struct {
	ctx  *Ctx
	vars []*varInfo
	undo []int // variables bound since the start, for rollback
}

func (in *infer) newVar(kind varKind) *Type {
	t := &Type{Kind: KInfer, Var: len(in.vars)}
	in.ctx.mu.Lock()
	in.ctx.next++
	t.id = in.ctx.next
	in.ctx.mu.Unlock()
	in.vars = append(in.vars, &varInfo{t: t, kind: kind})
	return t
}

func (in *infer) fresh() *Type { return in.newVar(varGeneral) }

// shallow follows bound variables at the top of t.
func (in *infer) shallow(t *Type) *Type {
	for t != nil && t.Kind == KInfer {
		v := in.vars[t.Var]
		if v.bound == nil {
			return t
		}
		t = v.bound
	}
	return t
}

func (in *infer) snapshot() int { return len(in.undo) }

func (in *infer) rollback(n int) {
	for len(in.undo) > n {
		i := in.undo[len(in.undo)-1]
		in.undo = in.undo[:len(in.undo)-1]
		in.vars[i].bound = nil
	}
}

func (in *infer) bind(v *varInfo, t *Type) {
	v.bound = t
	in.undo = append(in.undo, v.t.Var)
}

// occurs reports whether variable v appears in t.
func (in *infer) occurs(v int, t *Type) bool {
	found := false
	visit(in.resolve(t), func(x *Type) {
		if x.Kind == KInfer && x.Var == v {
			found = true
		}
	})
	return found
}

// resolve replaces bound variables throughout t.
func (in *infer) resolve(t *Type) *Type {
	if t == nil {
		return nil
	}
	t = in.shallow(t)
	if !hasInfer(t) {
		return t
	}
	ctx := in.ctx
	sl := func(ts []*Type) []*Type {
		out := make([]*Type, len(ts))
		for i, x := range ts {
			out[i] = in.resolve(x)
		}
		return out
	}
	switch t.Kind {
	case KRef:
		return ctx.Ref(t.Mut, in.resolve(t.Elem))
	case KPtr:
		return ctx.Ptr(t.Mut, in.resolve(t.Elem))
	case KSlice:
		return ctx.Slice(in.resolve(t.Elem))
	case KArray:
		return ctx.Array(in.resolve(t.Elem), t.Len)
	case KTuple:
		return ctx.Tuple(sl(t.Elems))
	case KFn:
		return ctx.Fn(sl(t.Elems), in.resolve(t.Ret), t.Abi)
	case KAdt:
		return ctx.Adt(t.Def, sl(t.Args))
	case KProj:
		return ctx.Proj(t.Def, in.resolveTraitRef(t.Trait))
	case KDyn:
		bs := make([]*TraitRef, len(t.Bounds))
		for i, b := range t.Bounds {
			bs[i] = in.resolveTraitRef(b)
		}
		return ctx.Dyn(bs)
	case KOpaque:
		bs := make([]*TraitRef, len(t.Bounds))
		for i, b := range t.Bounds {
			bs[i] = in.resolveTraitRef(b)
		}
		return ctx.Opaque(t.Def, t.Index, bs)
	}
	return t
}

func (in *infer) resolveTraitRef(tr *TraitRef) *TraitRef {
	if tr == nil {
		return nil
	}
	out := &TraitRef{Trait: tr.Trait, Self: in.resolve(tr.Self)}
	for _, a := range tr.Args {
		out.Args = append(out.Args, in.resolve(a))
	}
	for _, b := range tr.Bindings {
		out.Bindings = append(out.Bindings, AssocBinding{Name: b.Name, Type: in.resolve(b.Type)})
	}
	return out
}

// hasInfer reports whether t mentions an inference variable.
func hasInfer(t *Type) bool {
	found := false
	visit(t, func(x *Type) {
		if x.Kind == KInfer {
			found = true
		}
	})
	return found
}

// unify makes a and b equal, binding variables. It returns false (leaving
// partial bindings; callers that probe use snapshots) if they cannot be.
func (in *infer) unify(a, b *Type) bool {
	a, b = in.shallow(a), in.shallow(b)
	if a == b {
		return true
	}
	if a.Kind == KError || b.Kind == KError {
		return true
	}
	if a.Kind == KInfer {
		return in.bindVar(a, b)
	}
	if b.Kind == KInfer {
		return in.bindVar(b, a)
	}
	if a.Kind != b.Kind {
		return false
	}
	all := func(xs, ys []*Type) bool {
		if len(xs) != len(ys) {
			return false
		}
		for i := range xs {
			if !in.unify(xs[i], ys[i]) {
				return false
			}
		}
		return true
	}
	switch a.Kind {
	case KInt:
		return a.Int == b.Int
	case KFloat:
		return a.F64 == b.F64
	case KRef, KPtr:
		return a.Mut == b.Mut && in.unify(a.Elem, b.Elem)
	case KSlice:
		return in.unify(a.Elem, b.Elem)
	case KArray:
		return a.Len == b.Len && in.unify(a.Elem, b.Elem)
	case KTuple:
		return all(a.Elems, b.Elems)
	case KFn:
		return a.Abi == b.Abi && all(a.Elems, b.Elems) && in.unify(a.Ret, b.Ret)
	case KAdt:
		return a.Def == b.Def && all(a.Args, b.Args)
	case KProj:
		return a.Def == b.Def && in.unifyTraitRef(a.Trait, b.Trait)
	case KDyn:
		if len(a.Bounds) != len(b.Bounds) {
			return false
		}
		for i := range a.Bounds {
			if !in.unifyTraitRef(a.Bounds[i], b.Bounds[i]) {
				return false
			}
		}
		return true
	case KOpaque:
		return a.Def == b.Def && a.Index == b.Index
	}
	return false // distinct params, or other mismatches
}

func (in *infer) unifyTraitRef(a, b *TraitRef) bool {
	if a.Trait != b.Trait || len(a.Args) != len(b.Args) {
		return false
	}
	if (a.Self == nil) != (b.Self == nil) {
		return false
	}
	if a.Self != nil && !in.unify(a.Self, b.Self) {
		return false
	}
	for i := range a.Args {
		if !in.unify(a.Args[i], b.Args[i]) {
			return false
		}
	}
	if len(a.Bindings) != len(b.Bindings) {
		return false
	}
	for i := range a.Bindings {
		if a.Bindings[i].Name != b.Bindings[i].Name || !in.unify(a.Bindings[i].Type, b.Bindings[i].Type) {
			return false
		}
	}
	return true
}

func (in *infer) bindVar(v, t *Type) bool {
	vi := in.vars[v.Var]
	if t.Kind == KInfer {
		ti := in.vars[t.Var]
		// Merge literal kinds: the more specific kind wins.
		switch {
		case vi.kind == varGeneral:
			in.bind(vi, t)
			return true
		case ti.kind == varGeneral:
			in.bind(ti, v)
			return true
		case vi.kind == ti.kind:
			in.bind(vi, t)
			return true
		}
		return false
	}
	switch vi.kind {
	case varInt:
		if t.Kind != KInt {
			return false
		}
	case varFloat:
		if t.Kind != KFloat {
			return false
		}
	}
	if in.occurs(v.Var, t) {
		return false
	}
	in.bind(vi, t)
	return true
}

// probe runs f and rolls back any bindings it made, returning f's result.
func (in *infer) probe(f func() bool) bool {
	snap := in.snapshot()
	ok := f()
	in.rollback(snap)
	return ok
}

// tryUnify unifies a and b, rolling back on failure.
func (in *infer) tryUnify(a, b *Type) bool {
	snap := in.snapshot()
	if in.unify(a, b) {
		return true
	}
	in.rollback(snap)
	return false
}

// isLiteralVar reports the literal kind of an unresolved variable.
func (in *infer) varKindOf(t *Type) (varKind, bool) {
	t = in.shallow(t)
	if t.Kind != KInfer {
		return 0, false
	}
	return in.vars[t.Var].kind, true
}
