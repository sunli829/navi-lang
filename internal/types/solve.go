package types

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// obligation is a trait bound that must hold: Self: Trait<Args>.
type obligation struct {
	tr  *TraitRef
	pos syntax.Pos
	// proj, when set, makes this a projection obligation: the projection
	// must normalize to proj.target.
	proj *pendingProj
}

type pendingProj struct {
	assoc  *resolve.Def // the associated type
	target *Type        // the inference variable standing for it
}

type selKind uint8

const (
	selNone selKind = iota
	selImpl
	selParam
	selBuiltin
	selDyn
	selErr
)

// selection is how a trait bound is satisfied.
type selection struct {
	kind   selKind
	impl   *ImplInfo
	args   []*Type   // impl generic arguments
	pred   *TraitRef // the where clause or dyn/opaque bound used
	nested []*TraitRef
	// assoc gives builtin associated types (Output, Target, Item).
	assoc map[string]*Type
}

type selStatus uint8

const (
	selYes selStatus = iota
	selNo
	selAmbig
)

const maxSolveDepth = 32

// langName returns the lang name of a trait/ADT def, or "".
func (fc *fnCtx) langName(d *resolve.Def) string { return fc.c.langOf[d] }

// selectTrait finds how tr is satisfied. On success the inference
// variables are bound as needed (callers wanting a probe use snapshots).
func (fc *fnCtx) selectTrait(tr *TraitRef, depth int) (*selection, selStatus) {
	if depth > maxSolveDepth {
		return nil, selAmbig
	}
	self := fc.in.shallow(tr.Self)
	if self.Kind == KError || self.Kind == KNever {
		// ! never has a value, so any bound on it holds vacuously.
		return &selection{kind: selErr}, selYes
	}
	for _, a := range tr.Args {
		if fc.in.shallow(a).Kind == KError {
			return &selection{kind: selErr}, selYes
		}
	}
	type cand struct {
		sel *selection
	}
	var cands []cand

	// 1. Where clauses (including supertraits of them).
	for _, p := range fc.preds {
		if p.Trait != tr.Trait {
			continue
		}
		pp := p
		if fc.in.probe(func() bool { return fc.matchTraitRef(pp, tr) }) {
			cands = append(cands, cand{&selection{kind: selParam, pred: pp}})
		}
	}
	if len(cands) > 0 {
		if len(cands) > 1 && traitRefHasInfer(fc.in.resolveTraitRef(tr)) {
			return nil, selAmbig
		}
		fc.matchTraitRef(cands[0].sel.pred, tr)
		return cands[0].sel, selYes
	}

	if self.Kind == KInfer {
		kind := fc.in.vars[self.Var].kind
		if kind == varGeneral {
			return nil, selAmbig
		}
		// Integer/float literals: builtin impls exist for all their types.
		if _, ok := fc.builtin(tr, fc.in.ctx.Ints[I32], depth); ok && kind == varInt {
			return nil, selAmbig
		}
		if _, ok := fc.builtin(tr, fc.in.ctx.F64, depth); ok && kind == varFloat {
			return nil, selAmbig
		}
	}

	// 2. dyn and impl-Trait bounds.
	if self.Kind == KDyn || self.Kind == KOpaque {
		for _, b := range self.Bounds {
			bound := &TraitRef{Trait: b.Trait, Self: self, Args: b.Args, Bindings: b.Bindings}
			var hit *TraitRef
			fc.c.walkSupertraits(bound, func(s *TraitRef) {
				if hit == nil && s.Trait == tr.Trait {
					hit = s
				}
			})
			if hit != nil && fc.in.probe(func() bool { return fc.matchTraitRef(hit, tr) }) {
				fc.matchTraitRef(hit, tr)
				return &selection{kind: selDyn, pred: hit}, selYes
			}
		}
	}

	// 3. Builtin impls for primitive and structural types.
	snap := fc.in.snapshot()
	if sel, ok := fc.builtin(tr, self, depth); ok {
		if fc.nestedHold(sel.nested, depth) != selNo {
			return sel, selYes
		}
	}
	fc.in.rollback(snap)

	// 4. Impls.
	var implHits []*selection
	ambig := false
	for _, ii := range fc.info.ImplsOf[tr.Trait] {
		if ii.Trait == nil {
			continue
		}
		snap := fc.in.snapshot()
		sel := fc.matchImpl(ii, tr)
		if sel != nil {
			switch fc.nestedHold(sel.nested, depth) {
			case selYes:
				implHits = append(implHits, sel)
			case selAmbig:
				implHits = append(implHits, sel)
				ambig = true
			}
		}
		fc.in.rollback(snap)
	}
	for _, sel := range fc.derived(tr) {
		implHits = append(implHits, sel)
	}
	switch {
	case len(implHits) == 0 && self.Kind == KInfer:
		return nil, selAmbig // a literal: decide after it falls back to i32/f64
	case len(implHits) == 0:
		return nil, selNo
	case len(implHits) > 1 || ambig && traitRefHasInfer(fc.in.resolveTraitRef(tr)):
		return nil, selAmbig
	}
	sel := implHits[0]
	if sel.impl != nil {
		sel = fc.matchImpl(sel.impl, tr)
	}
	return sel, selYes
}

// nestedHold checks nested obligations in a probe.
func (fc *fnCtx) nestedHold(nested []*TraitRef, depth int) selStatus {
	status := selYes
	for _, n := range nested {
		snap := fc.in.snapshot()
		_, st := fc.selectTrait(n, depth+1)
		fc.in.rollback(snap)
		if st == selNo {
			return selNo
		}
		if st == selAmbig {
			status = selAmbig
		}
	}
	return status
}

// matchTraitRef unifies a known bound with the wanted trait ref.
func (fc *fnCtx) matchTraitRef(have, want *TraitRef) bool {
	if have.Trait != want.Trait || len(have.Args) != len(want.Args) {
		return false
	}
	if !fc.in.unify(have.Self, want.Self) {
		return false
	}
	for i := range have.Args {
		if !fc.in.unify(have.Args[i], want.Args[i]) {
			return false
		}
	}
	return true
}

// instantiate creates fresh inference variables for the parameters of g
// (and its parents) and returns the substitution.
func (fc *fnCtx) instantiate(g *Generics) (map[*resolve.Def]*Type, []*Type) {
	m := map[*resolve.Def]*Type{}
	var vars []*Type
	for _, p := range g.All() {
		v := fc.in.fresh()
		fc.varOrigin[v.Var] = p
		m[p.Def] = v
		vars = append(vars, v)
	}
	return m, vars
}

// matchImpl tries to use impl ii for tr, returning the selection with its
// nested obligations, or nil.
func (fc *fnCtx) matchImpl(ii *ImplInfo, tr *TraitRef) *selection {
	m, vars := fc.instantiate(ii.Generics)
	ctx := fc.in.ctx
	itr := SubstTraitRef(ctx, ii.Trait, m, nil)
	itr.Self = fc.normalize(itr.Self)
	if !fc.matchTraitRef(itr, tr) {
		return nil
	}
	sel := &selection{kind: selImpl, impl: ii, args: vars}
	for _, p := range ii.Generics.AllPreds() {
		sel.nested = append(sel.nested, SubstTraitRef(ctx, p.Trait, m, nil))
	}
	return sel
}

// solvePending tries to discharge pending obligations; errors are reported
// for those that cannot hold. With final set, ambiguity is reported too.
func (fc *fnCtx) solvePending(final bool) {
	for progress := true; progress; {
		progress = false
		cur := fc.pending
		fc.pending = nil
		for _, ob := range cur {
			if ob.proj != nil {
				t := fc.normalizeProjNow(ob.proj.assoc, ob.tr)
				if t == nil {
					fc.pending = append(fc.pending, ob)
					continue
				}
				if !fc.in.unify(ob.proj.target, t) {
					fc.errorf(ob.pos, "type mismatch: expected %s, found %s", fc.in.resolve(ob.proj.target), fc.in.resolve(t))
				}
				progress = true
				continue
			}
			sel, st := fc.selectTrait(ob.tr, 0)
			switch st {
			case selYes:
				for _, n := range sel.nested {
					fc.pending = append(fc.pending, &obligation{tr: n, pos: ob.pos})
				}
				fc.applyBindings(ob.tr, sel, ob.pos)
				progress = true
			case selNo:
				fc.unsatisfied(ob)
				progress = true
			case selAmbig:
				fc.pending = append(fc.pending, ob)
			}
		}
	}
	if final {
		for _, ob := range fc.pending {
			if ob.proj != nil {
				continue
			}
			fc.errorf(ob.pos, "type annotations needed: cannot decide whether %s holds", fc.describeBound(ob.tr))
		}
		fc.pending = nil
	}
}

// applyBindings unifies Item = T bindings of a bound with the selected
// impl's associated types.
func (fc *fnCtx) applyBindings(tr *TraitRef, sel *selection, pos syntax.Pos) {
	for _, b := range tr.Bindings {
		assoc := fc.c.findAssocTypeInSupers(&TraitRef{Trait: tr.Trait, Self: tr.Self}, b.Name)
		if assoc == nil {
			continue
		}
		got := fc.normalizeProjNow(assoc, fc.c.traitRefFor(&TraitRef{Trait: tr.Trait, Self: tr.Self, Args: tr.Args}, assoc.Parent))
		if got == nil {
			v := fc.in.fresh()
			fc.pending = append(fc.pending, &obligation{tr: fc.c.traitRefFor(&TraitRef{Trait: tr.Trait, Self: tr.Self, Args: tr.Args}, assoc.Parent), pos: pos, proj: &pendingProj{assoc: assoc, target: v}})
			got = v
		}
		if !fc.in.unify(got, b.Type) {
			fc.errorf(pos, "type mismatch resolving %s::%s: expected %s, found %s", fc.in.resolve(tr.Self), b.Name, fc.in.resolve(b.Type), fc.in.resolve(got))
		}
	}
}

func (fc *fnCtx) unsatisfied(ob *obligation) {
	fc.errorf(ob.pos, "the trait bound %s is not satisfied", fc.describeBound(ob.tr))
}

func (fc *fnCtx) describeBound(tr *TraitRef) string {
	r := fc.in.resolveTraitRef(tr)
	self := "?"
	if r.Self != nil {
		self = fc.show(r.Self)
	}
	return self + ": " + r.String()
}

// require registers an obligation.
func (fc *fnCtx) require(tr *TraitRef, pos syntax.Pos) {
	fc.pending = append(fc.pending, &obligation{tr: tr, pos: pos})
}

// holds checks a bound immediately (in a probe), for method lookup.
func (fc *fnCtx) holds(tr *TraitRef) selStatus {
	snap := fc.in.snapshot()
	_, st := fc.selectTrait(tr, 0)
	fc.in.rollback(snap)
	return st
}

// ---------------------------------------------------------------------------
// Normalization

// normalize replaces projections in t by the types they stand for, where
// known. Unknown projections on inference variables become fresh variables
// with pending projection obligations.
func (fc *fnCtx) normalize(t *Type) *Type {
	if t == nil {
		return nil
	}
	hasProj := false
	visit(t, func(x *Type) {
		if x.Kind == KProj {
			hasProj = true
		}
	})
	if !hasProj {
		return t
	}
	ctx := fc.in.ctx
	sl := func(ts []*Type) []*Type {
		out := make([]*Type, len(ts))
		for i, x := range ts {
			out[i] = fc.normalize(x)
		}
		return out
	}
	switch t.Kind {
	case KProj:
		tr := &TraitRef{Trait: t.Trait.Trait, Self: fc.normalize(t.Trait.Self), Args: sl(t.Trait.Args)}
		if r := fc.normalizeProjNow(t.Def, tr); r != nil {
			return r
		}
		self := fc.in.shallow(tr.Self)
		if self.Kind != KInfer {
			return ctx.Proj(t.Def, tr) // rigid
		}
		v := fc.in.fresh()
		fc.pending = append(fc.pending, &obligation{tr: tr, pos: fc.curPos, proj: &pendingProj{assoc: t.Def, target: v}})
		return v
	case KRef:
		return ctx.Ref(t.Mut, fc.normalize(t.Elem))
	case KPtr:
		return ctx.Ptr(t.Mut, fc.normalize(t.Elem))
	case KSlice:
		return ctx.Slice(fc.normalize(t.Elem))
	case KArray:
		return ctx.Array(fc.normalize(t.Elem), t.Len)
	case KTuple:
		return ctx.Tuple(sl(t.Elems))
	case KFn:
		return ctx.Fn(sl(t.Elems), fc.normalize(t.Ret), t.Abi)
	case KAdt:
		return ctx.Adt(t.Def, sl(t.Args))
	}
	return t
}

// normalizeProjNow tries to compute <tr.Self as tr.Trait>::assoc. It
// returns nil if the self type is not known well enough yet, or the
// projection itself if it is rigid (a parameter without a binding).
func (fc *fnCtx) normalizeProjNow(assoc *resolve.Def, tr *TraitRef) *Type {
	self := fc.in.shallow(tr.Self)
	if self.Kind == KInfer && fc.in.vars[self.Var].kind == varGeneral {
		return nil
	}
	// Where clauses with a binding for this associated type.
	for _, p := range fc.preds {
		for _, b := range p.Bindings {
			if b.Name != assoc.Name {
				continue
			}
			owner := fc.c.traitRefFor(p, assoc.Parent)
			if owner.Trait == assoc.Parent && fc.in.probe(func() bool { return fc.matchTraitRef(owner, tr) }) {
				fc.matchTraitRef(owner, tr)
				return fc.normalize(b.Type)
			}
		}
	}
	snap := fc.in.snapshot()
	sel, st := fc.selectTrait(tr, 0)
	switch st {
	case selAmbig:
		fc.in.rollback(snap)
		return nil
	case selNo:
		fc.in.rollback(snap)
		fc.errorf(fc.curPos, "the trait bound %s is not satisfied", fc.describeBound(tr))
		return fc.in.ctx.Err
	}
	switch sel.kind {
	case selErr:
		return fc.in.ctx.Err
	case selImpl:
		item := sel.impl.Items[assoc.Name]
		var val *Type
		if item != nil {
			if ai := fc.c.assocType(item); ai != nil {
				val = ai.Value
			}
		} else if ai := fc.c.assocType(assoc); ai != nil {
			val = ai.Value // trait default
		}
		if val == nil {
			return fc.in.ctx.Err
		}
		m := map[*resolve.Def]*Type{}
		for i, p := range sel.impl.Generics.All() {
			m[p.Def] = sel.args[i]
		}
		return fc.normalize(Subst(fc.in.ctx, val, m, sel.impl.SelfTy))
	case selBuiltin:
		if t, ok := sel.assoc[assoc.Name]; ok {
			return t
		}
		return fc.in.ctx.Err
	case selDyn, selParam:
		for _, b := range sel.pred.Bindings {
			if b.Name == assoc.Name {
				return fc.normalize(b.Type)
			}
		}
		// Rigid projection on a parameter (or dyn without a binding).
		return fc.in.ctx.Proj(assoc, fc.in.resolveTraitRef(tr))
	}
	return fc.in.ctx.Err
}

func traitRefHasInfer(tr *TraitRef) bool {
	if tr.Self != nil && hasInfer(tr.Self) {
		return true
	}
	for _, a := range tr.Args {
		if hasInfer(a) {
			return true
		}
	}
	return false
}
