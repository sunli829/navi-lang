package types

import (
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

type bindMode uint8

const (
	byValue bindMode = iota
	byRef
	byRefMut
)

// checkPat type-checks a pattern against the type of the value it matches.
func (fc *fnCtx) checkPat(p syntax.Pat, expected *Type, refutable bool) {
	fc.pat(p, expected, byValue)
	if !refutable {
		fc.checkIrrefutable(p, expected, "this")
	}
}

// peel implements match ergonomics: a non-reference pattern matching a
// reference matches the referent, and bindings inside bind by reference.
func (fc *fnCtx) peel(t *Type, mode bindMode) (*Type, bindMode) {
	for {
		s := fc.in.shallow(t)
		if s.Kind != KRef {
			return t, mode
		}
		if s.Mut && mode == byValue {
			mode = byRefMut
		} else if !s.Mut {
			mode = byRef
		}
		t = s.Elem
	}
}

func (fc *fnCtx) pat(p syntax.Pat, exp *Type, mode bindMode) {
	ctx := fc.in.ctx
	in := fc.in
	fc.body.Pats[p] = exp
	switch p := p.(type) {
	case *syntax.WildPat, *syntax.RestPat:
		return
	case *syntax.IdentPat:
		if res, ok := fc.info.Res.PatConsts[p]; ok {
			exp, _ = fc.peel(exp, mode)
			t := fc.constPatType(p.Pos(), res)
			if !in.tryUnify(t, exp) {
				fc.mismatch(p.Pos(), exp, t)
			}
			return
		}
		l := fc.info.Res.Bindings[p]
		t := exp
		switch mode {
		case byRef:
			t = ctx.Ref(false, exp)
			fc.body.RefBindings[p] = true
		case byRefMut:
			t = ctx.Ref(true, exp)
			fc.body.RefBindings[p] = true
		}
		if l != nil {
			if old := fc.body.Locals[l]; old != nil {
				// The same binding in another alternative of an or-pattern.
				if !in.tryUnify(old, t) {
					fc.mismatch(p.Pos(), old, t)
				}
			} else {
				fc.body.Locals[l] = t
			}
		}
		if p.Sub != nil {
			fc.pat(p.Sub, exp, mode)
		}
		return
	case *syntax.RefPat:
		e := in.shallow(exp)
		if e.Kind == KInfer {
			elem := in.fresh()
			in.unify(e, ctx.Ref(false, elem))
			e = in.shallow(exp)
		}
		if e.Kind != KRef {
			if e.Kind != KError {
				fc.errorf(p.Amp, "mismatched types: expected %s, found a reference pattern", in.resolve(e))
			}
			fc.pat(p.Pat, ctx.Err, byValue)
			return
		}
		fc.pat(p.Pat, e.Elem, byValue)
		return
	case *syntax.OrPat:
		for _, a := range p.Alts {
			fc.pat(a, exp, mode)
		}
		return
	case *syntax.LitPat:
		lt := fc.infer(p.X, nil)
		// String literal patterns have type &str; match ergonomics stop there.
		for {
			if in.tryUnify(lt, exp) {
				return
			}
			s := in.shallow(exp)
			if s.Kind != KRef {
				break
			}
			exp = s.Elem
		}
		fc.mismatch(p.Pos(), exp, lt)
		return
	}

	exp, mode = fc.peel(exp, mode)
	e := in.shallow(exp)
	switch p := p.(type) {
	case *syntax.RangePat:
		for _, x := range []syntax.Expr{p.Lo, p.Hi} {
			if x != nil {
				fc.check(x, exp)
			}
		}
		if e.Kind != KInfer && e.Kind != KError && !isNumT(e) && e.Kind != KChar {
			fc.errorf(p.Pos(), "range patterns can only match numbers and chars, not %s", in.resolve(e))
		}
	case *syntax.PathPat:
		res := fc.info.Res.Paths[p.Path]
		t := fc.constPatType(p.Pos(), res)
		if res.Kind == resolve.ResDef && (res.Def.Kind == resolve.DefStruct || res.Def.Kind == resolve.DefVariant) || len(res.Rest) > 0 {
			t = fc.valuePath(&syntax.PathExpr{Path: p.Path})
			if s := in.shallow(t); s.Kind == KFn {
				fc.errorf(p.Pos(), "%s is a tuple struct or variant; write %s(..)", syntax.Dump(p.Path), syntax.Dump(p.Path))
				return
			}
		}
		if !in.tryUnify(t, exp) {
			fc.mismatch(p.Pos(), exp, t)
		}
	case *syntax.TupleStructPat:
		ft := in.shallow(fc.valuePath(&syntax.PathExpr{Path: p.Path}))
		if ft.Kind == KError {
			for _, el := range p.Elems {
				fc.pat(el, ctx.Err, mode)
			}
			return
		}
		if ft.Kind != KFn {
			fc.errorf(p.Pos(), "%s is not a tuple struct or tuple variant", syntax.Dump(p.Path))
			return
		}
		if !in.tryUnify(ft.Ret, exp) {
			fc.mismatch(p.Pos(), exp, ft.Ret)
		}
		fc.tupleElems(p.Pos(), p.Elems, ft.Elems, mode)
	case *syntax.StructPat:
		adt, fields := fc.structPatTarget(p)
		if adt == nil {
			for _, f := range p.Fields {
				fc.pat(f.Pat, ctx.Err, mode)
			}
			return
		}
		if !in.tryUnify(adt, exp) {
			fc.mismatch(p.Pos(), exp, adt)
		}
		ai := fc.c.adt(in.shallow(adt).Def)
		m := paramMap(ai.Generics, in.shallow(adt).Args)
		seen := map[string]bool{}
		for _, fp := range p.Fields {
			var f *Field
			for _, x := range fields {
				if x.Name == fp.Name.Value {
					f = x
				}
			}
			if f == nil {
				fc.errorf(fp.Name.NamePos, "%s has no field named %s", syntax.Dump(p.Path), fp.Name.Value)
				fc.pat(fp.Pat, ctx.Err, mode)
				continue
			}
			seen[f.Name] = true
			if !f.Vis.AccessibleFrom(fc.module) {
				fc.errorf(fp.Name.NamePos, "field %s is private", f.Name)
			}
			fc.pat(fp.Pat, fc.normalize(Subst(ctx, f.Type, m, nil)), mode)
		}
		if !p.Rest {
			var missing []string
			for _, f := range fields {
				if !seen[f.Name] {
					missing = append(missing, f.Name)
				}
			}
			if len(missing) > 0 {
				fc.errorf(p.Pos(), "pattern does not mention field%s %s; add them or use ..", plural(len(missing)), strings.Join(missing, ", "))
			}
		}
	case *syntax.TuplePat:
		if e.Kind == KInfer {
			hasRest := false
			for _, el := range p.Elems {
				if _, ok := el.(*syntax.RestPat); ok {
					hasRest = true
				}
			}
			if !hasRest {
				vars := make([]*Type, len(p.Elems))
				for i := range vars {
					vars[i] = in.fresh()
				}
				in.unify(e, ctx.Tuple(vars))
				e = in.shallow(exp)
			}
		}
		if e.Kind != KTuple {
			if e.Kind != KError && e.Kind != KInfer {
				fc.errorf(p.Pos(), "mismatched types: expected %s, found a tuple pattern", in.resolve(e))
			}
			for _, el := range p.Elems {
				fc.pat(el, ctx.Err, mode)
			}
			return
		}
		fc.tupleElems(p.Pos(), p.Elems, e.Elems, mode)
	case *syntax.SlicePat:
		var elem *Type
		switch e.Kind {
		case KArray, KSlice:
			elem = e.Elem
		case KError, KInfer:
			elem = ctx.Err
		default:
			fc.errorf(p.Pos(), "mismatched types: expected %s, found a slice pattern", in.resolve(e))
			elem = ctx.Err
		}
		fixed := 0
		for _, el := range p.Elems {
			if isRest(el) {
				if ip, ok := el.(*syntax.IdentPat); ok {
					if l := fc.info.Res.Bindings[ip]; l != nil {
						t := ctx.Slice(elem)
						fc.body.Locals[l] = ctx.Ref(mode == byRefMut, t)
						fc.body.RefBindings[ip] = true
					}
				}
				continue
			}
			fixed++
			fc.pat(el, elem, mode)
		}
		if e.Kind == KArray && !hasRestPat(p.Elems) && int64(fixed) != e.Len {
			fc.errorf(p.Pos(), "pattern has %d elements but the array has %d", fixed, e.Len)
		}
	}
}

func isRest(p syntax.Pat) bool {
	switch p := p.(type) {
	case *syntax.RestPat:
		return true
	case *syntax.IdentPat:
		_, ok := p.Sub.(*syntax.RestPat)
		return ok
	}
	return false
}

func hasRestPat(ps []syntax.Pat) bool {
	for _, p := range ps {
		if isRest(p) {
			return true
		}
	}
	return false
}

// tupleElems matches tuple-like subpatterns (with an optional ..) against
// the element types.
func (fc *fnCtx) tupleElems(pos syntax.Pos, elems []syntax.Pat, types []*Type, mode bindMode) {
	rest := -1
	for i, el := range elems {
		if _, ok := el.(*syntax.RestPat); ok {
			if rest >= 0 {
				fc.errorf(el.Pos(), ".. can only be used once per pattern")
			}
			rest = i
		}
	}
	if rest < 0 {
		if len(elems) != len(types) {
			fc.errorf(pos, "this pattern has %d field%s, but the type has %d", len(elems), plural(len(elems)), len(types))
		}
		for i, el := range elems {
			if i < len(types) {
				fc.pat(el, types[i], mode)
			} else {
				fc.pat(el, fc.in.ctx.Err, mode)
			}
		}
		return
	}
	after := len(elems) - rest - 1
	if rest+after > len(types) {
		fc.errorf(pos, "this pattern has %d fields, but the type has %d", rest+after, len(types))
		return
	}
	for i := 0; i < rest; i++ {
		fc.pat(elems[i], types[i], mode)
	}
	for i := 0; i < after; i++ {
		fc.pat(elems[rest+1+i], types[len(types)-after+i], mode)
	}
}

// constPatType is the type of a constant or unit struct/variant used as a
// pattern.
func (fc *fnCtx) constPatType(pos syntax.Pos, res resolve.Res) *Type {
	ctx := fc.in.ctx
	if res.Kind != resolve.ResDef || len(res.Rest) > 0 {
		return ctx.Err
	}
	d := res.Def
	switch d.Kind {
	case resolve.DefConst, resolve.DefStatic, resolve.DefAssocConst:
		return fc.normalize(fc.c.constInfo(d).Type)
	case resolve.DefStruct:
		ai := fc.c.adt(d)
		_, vars := fc.instantiate(ai.Generics)
		return ctx.Adt(d, vars)
	case resolve.DefVariant:
		ai := fc.c.adt(d.Parent)
		_, vars := fc.instantiate(ai.Generics)
		return ctx.Adt(d.Parent, vars)
	}
	return ctx.Err
}

// structPatTarget resolves the path of a struct pattern to its ADT type and
// fields.
func (fc *fnCtx) structPatTarget(p *syntax.StructPat) (*Type, []*Field) {
	ctx := fc.in.ctx
	res := fc.info.Res.Paths[p.Path]
	var adt *Type
	var variant *resolve.Def
	switch {
	case res.Kind == resolve.ResSelfTy && len(res.Rest) == 0:
		adt = fc.env.selfTy
	case res.Kind == resolve.ResDef && len(res.Rest) == 0:
		switch res.Def.Kind {
		case resolve.DefStruct:
			_, vars := fc.instantiate(fc.c.adt(res.Def).Generics)
			adt = ctx.Adt(res.Def, vars)
		case resolve.DefVariant:
			_, vars := fc.instantiate(fc.c.adt(res.Def.Parent).Generics)
			adt, variant = ctx.Adt(res.Def.Parent, vars), res.Def
		}
	case len(res.Rest) == 1:
		base, _ := fc.baseType(p.Path, res)
		if b := fc.in.shallow(base); b.Kind == KAdt {
			for _, v := range fc.c.adt(b.Def).Variants {
				if v.Def.Name == res.Rest[0].Name.Value {
					adt, variant = b, v.Def
				}
			}
		}
	}
	if adt == nil || fc.in.shallow(adt).Kind != KAdt {
		if res.Kind != resolve.ResErr {
			fc.errorf(p.Pos(), "expected a struct or variant")
		}
		return nil, nil
	}
	ai := fc.c.adt(fc.in.shallow(adt).Def)
	if variant == nil {
		return adt, ai.Fields
	}
	for _, v := range ai.Variants {
		if v.Def == variant {
			return adt, v.Fields
		}
	}
	return adt, nil
}
