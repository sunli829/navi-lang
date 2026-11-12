package mir

import (
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// bindPattern matches an irrefutable pattern against the value at sp.
func (b *builder) bindPattern(p syntax.Pat, sp Place) {
	fail := b.newBlock()
	b.testPat(p, sp, fail)
	saved := b.cur
	b.cur = fail
	b.terminate(Unreachable{})
	b.cur = saved
}

func (b *builder) letStmt(st *syntax.LetStmt) {
	t := b.body.Pats[st.Pat]
	if st.Init == nil {
		syntax.Inspect(st.Pat, func(n syntax.Node) bool {
			if ip, ok := n.(*syntax.IdentPat); ok {
				if l := b.res.Bindings[ip]; l != nil {
					b.declare(l, b.body.Locals[l])
				}
			}
			return true
		})
		return
	}
	if st.Else != nil {
		sp := b.placeOrTemp(st.Init)
		elseBlk := b.newBlock()
		b.testPat(st.Pat, sp, elseBlk)
		saved := b.cur
		b.cur = elseBlk
		b.exprInto(st.Else, b.temp(b.ctx().Never))
		if b.cur >= 0 {
			b.terminate(Unreachable{})
		}
		b.cur = saved
		return
	}
	if ip, ok := st.Pat.(*syntax.IdentPat); ok && ip.Sub == nil && !b.body.RefBindings[ip] {
		if l := b.res.Bindings[ip]; l != nil {
			dst := b.declare(l, b.body.Locals[l])
			b.exprInto(st.Init, dst)
			return
		}
	}
	tmp := b.temp(t)
	b.exprInto(st.Init, tmp)
	b.bindPattern(st.Pat, tmp)
}

func (b *builder) match(x *syntax.MatchExpr, dest Place) {
	ctx := b.ctx()
	sp := b.placeOrTemp(x.X)
	join := b.newBlock()
	for _, arm := range x.Arms {
		if b.cur < 0 {
			break
		}
		next := b.newBlock()
		b.testPat(arm.Pat, sp, next)
		if arm.Guard != nil {
			c := b.operand(arm.Guard)
			body := b.newBlock()
			b.terminate(Switch{X: c, Type: ctx.Bool, Vals: []int64{0}, Targets: []int{next}, Else: body})
			b.cur = body
		}
		b.exprInto(arm.Body, dest)
		if b.cur >= 0 {
			b.terminate(Goto{Target: join})
		}
		b.cur = next
	}
	b.terminate(Unreachable{})
	b.cur = join
}

// testPat emits code that checks whether the value at sp matches p,
// binding its variables, and jumps to fail if not. The current block
// continues on success.
func (b *builder) testPat(p syntax.Pat, sp Place, fail int) {
	ctx := b.ctx()
	if b.cur < 0 {
		return
	}
	t := b.body.Pats[p]
	switch p := p.(type) {
	case *syntax.WildPat, *syntax.RestPat:
		return
	case *syntax.IdentPat:
		if res, ok := b.res.PatConsts[p]; ok {
			for t.Kind == types.KRef {
				sp, t = sp.Deref(), t.Elem
			}
			b.constTest(res.Def, sp, t, fail, p.Pos())
			return
		}
		if p.Sub != nil {
			b.testPat(p.Sub, sp, fail)
		}
		l := b.res.Bindings[p]
		if l == nil {
			return
		}
		lt := b.body.Locals[l]
		dst, ok := b.orBound[l]
		if !ok {
			dst = b.declare(l, lt)
		}
		if b.body.RefBindings[p] {
			b.assign(dst, Ref{Mut: lt.Kind == types.KRef && lt.Mut, Place: sp})
		} else {
			b.assign(dst, Use{X: Copy(sp)})
		}
		return
	case *syntax.RefPat:
		b.testPat(p.Pat, sp.Deref(), fail)
		return
	case *syntax.OrPat:
		// Every alternative binds the same variables: declare them once,
		// before branching, and let each alternative assign them.
		outer := b.orBound
		if outer == nil {
			b.orBound = map[*resolve.Local]Place{}
			syntax.Inspect(p, func(n syntax.Node) bool {
				if ip, ok := n.(*syntax.IdentPat); ok {
					if _, isConst := b.res.PatConsts[ip]; !isConst {
						if l := b.res.Bindings[ip]; l != nil {
							if _, done := b.orBound[l]; !done {
								b.orBound[l] = b.declare(l, b.body.Locals[l])
							}
						}
					}
				}
				return true
			})
			defer func() { b.orBound = nil }()
		}
		success := b.newBlock()
		for i, alt := range p.Alts {
			next := fail
			if i < len(p.Alts)-1 {
				next = b.newBlock()
			}
			b.testPat(alt, sp, next)
			if b.cur >= 0 {
				b.terminate(Goto{Target: success})
			}
			b.cur = next
			if next == fail {
				break
			}
		}
		b.cur = success
		return
	case *syntax.LitPat:
		lt := b.typeOf(p.X)
		for t != lt && t.Kind == types.KRef {
			sp, t = sp.Deref(), t.Elem
		}
		lit := b.operand(p.X)
		ok := b.temp(ctx.Bool)
		if isPrim(lt) {
			b.assign(ok, Bin{Op: Eq, X: Copy(sp), Y: lit, Type: lt})
		} else {
			b.call(b.fnConst(b.p.LangFn("str_eq"), nil), []Operand{Copy(sp), lit}, ok, p.Pos())
		}
		b.branch(ok, fail)
		return
	}
	// Non-reference patterns match through references (match ergonomics).
	for t.Kind == types.KRef {
		sp, t = sp.Deref(), t.Elem
	}
	switch p := p.(type) {
	case *syntax.RangePat:
		if p.Lo != nil {
			ok := b.temp(ctx.Bool)
			b.assign(ok, Bin{Op: Le, X: b.operand(p.Lo), Y: Copy(sp), Type: t})
			b.branch(ok, fail)
		}
		if p.Hi != nil {
			op := Le
			if !p.Inclusive {
				op = Lt
			}
			ok := b.temp(ctx.Bool)
			b.assign(ok, Bin{Op: op, X: Copy(sp), Y: b.operand(p.Hi), Type: t})
			b.branch(ok, fail)
		}
	case *syntax.PathPat:
		res := b.res.Paths[p.Path]
		def := res.Def
		if len(res.Rest) == 1 && t.Kind == types.KAdt {
			def = b.variantByName(t, res.Rest[0].Name.Value)
		}
		if def != nil {
			b.constTest(def, sp, t, fail, p.Pos())
		}
	case *syntax.TupleStructPat:
		res := b.res.Paths[p.Path]
		def := res.Def
		if len(res.Rest) == 1 && t.Kind == types.KAdt {
			def = b.variantByName(t, res.Rest[0].Name.Value)
		}
		fp := sp
		n := 0
		if def != nil && def.Kind == resolve.DefVariant {
			vi := variantIndex(b.info, def)
			b.discrTest(sp, t, vi, fail)
			fp = sp.With(Proj{Kind: PDowncast, Variant: vi})
			n = len(b.info.Adts[t.Def].Variants[vi].Fields)
		} else if t.Kind == types.KAdt {
			n = len(b.info.Adts[t.Def].Fields)
		}
		b.positional(p.Elems, fp, n, fail)
	case *syntax.StructPat:
		res := b.res.Paths[p.Path]
		fp := sp
		var fields []*types.Field
		def := res.Def
		if len(res.Rest) == 1 && t.Kind == types.KAdt {
			def = b.variantByName(t, res.Rest[0].Name.Value)
		}
		if t.Kind != types.KAdt {
			return
		}
		ai := b.info.Adts[t.Def]
		if def != nil && def.Kind == resolve.DefVariant {
			vi := variantIndex(b.info, def)
			b.discrTest(sp, t, vi, fail)
			fp = sp.With(Proj{Kind: PDowncast, Variant: vi})
			fields = ai.Variants[vi].Fields
		} else {
			fields = ai.Fields
		}
		for _, f := range p.Fields {
			for i, fd := range fields {
				if fd.Name == f.Name.Value {
					b.testPat(f.Pat, fp.Field(i), fail)
				}
			}
		}
	case *syntax.TuplePat:
		n := len(t.Elems)
		b.positional(p.Elems, sp, n, fail)
	case *syntax.SlicePat:
		b.slicePat(p, sp, t, fail)
	}
}

func (b *builder) variantByName(t *types.Type, name string) *resolve.Def {
	for _, v := range b.info.Adts[t.Def].Variants {
		if v.Def.Name == name {
			return v.Def
		}
	}
	return nil
}

// branch continues if cond is true and jumps to fail otherwise.
func (b *builder) branch(cond Place, fail int) {
	next := b.newBlock()
	b.terminate(Switch{X: Copy(cond), Type: b.ctx().Bool, Vals: []int64{0}, Targets: []int{fail}, Else: next})
	b.cur = next
}

func (b *builder) discrTest(sp Place, t *types.Type, variant int, fail int) {
	ctx := b.ctx()
	if len(b.info.Adts[t.Def].Variants) == 1 {
		return
	}
	d := b.temp(ctx.Ints[types.Isize])
	b.assign(d, Discr{Place: sp})
	next := b.newBlock()
	b.terminate(Switch{X: Copy(d), Type: ctx.Ints[types.Isize], Vals: []int64{int64(variant)}, Targets: []int{next}, Else: fail})
	b.cur = next
}

// constTest compares the value at sp with a unit struct/variant or constant.
func (b *builder) constTest(def *resolve.Def, sp Place, t *types.Type, fail int, pos syntax.Pos) {
	ctx := b.ctx()
	switch def.Kind {
	case resolve.DefStruct:
		return
	case resolve.DefVariant:
		b.discrTest(sp, t, variantIndex(b.info, def), fail)
		return
	}
	v := b.temp(t)
	if def.Kind == resolve.DefConst || def.Kind == resolve.DefStatic {
		b.assign(v, Use{X: Copy(b.staticPlace(def))})
	} else {
		b.call(ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: def}}), nil, v, pos)
	}
	ok := b.temp(ctx.Bool)
	if isPrim(t) {
		b.assign(ok, Bin{Op: Eq, X: Copy(sp), Y: Copy(v), Type: t})
	} else {
		eq := b.info.TraitItem("PartialEq", "eq")
		b.call(b.traitFn(eq, t, []*types.Type{t}), []Operand{b.refTo(Copy(sp), t), b.refTo(Copy(v), t)}, ok, pos)
	}
	b.branch(ok, fail)
}

// positional matches tuple-like subpatterns (with an optional ..) against
// the n fields of the place.
func (b *builder) positional(elems []syntax.Pat, sp Place, n int, fail int) {
	rest := -1
	for i, el := range elems {
		if _, ok := el.(*syntax.RestPat); ok {
			rest = i
		}
	}
	if rest < 0 {
		for i, el := range elems {
			b.testPat(el, sp.Field(i), fail)
		}
		return
	}
	for i := 0; i < rest; i++ {
		b.testPat(elems[i], sp.Field(i), fail)
	}
	after := len(elems) - rest - 1
	for i := 0; i < after; i++ {
		b.testPat(elems[rest+1+i], sp.Field(n-after+i), fail)
	}
}

func isRestPat(p syntax.Pat) bool {
	switch p := p.(type) {
	case *syntax.RestPat:
		return true
	case *syntax.IdentPat:
		_, ok := p.Sub.(*syntax.RestPat)
		return ok
	}
	return false
}

func (b *builder) slicePat(p *syntax.SlicePat, sp Place, t *types.Type, fail int) {
	ctx := b.ctx()
	usize := ctx.Ints[types.Usize]
	rest := -1
	var fixed []syntax.Pat
	for i, el := range p.Elems {
		if isRestPat(el) {
			rest = i
			continue
		}
		fixed = append(fixed, el)
	}
	n := b.temp(usize)
	b.assign(n, Len{Place: sp})
	if t.Kind == types.KSlice {
		ok := b.temp(ctx.Bool)
		op := Eq
		if rest >= 0 {
			op = Ge
		}
		b.assign(ok, Bin{Op: op, X: Copy(n), Y: ConstOp(&Const{Kind: CInt, Int: int64(len(fixed)), Type: usize}), Type: usize})
		b.branch(ok, fail)
	}
	index := func(i int64, fromEnd bool) Place {
		idx := b.temp(usize)
		if fromEnd {
			b.assign(idx, Bin{Op: Sub, X: Copy(n), Y: ConstOp(&Const{Kind: CInt, Int: i, Type: usize}), Type: usize})
		} else {
			b.assign(idx, Use{X: ConstOp(&Const{Kind: CInt, Int: i, Type: usize})})
		}
		return sp.With(Proj{Kind: PIndex, Index: idx.Local})
	}
	if rest < 0 {
		for i, el := range p.Elems {
			b.testPat(el, index(int64(i), false), fail)
		}
		return
	}
	for i := 0; i < rest; i++ {
		b.testPat(p.Elems[i], index(int64(i), false), fail)
	}
	after := len(p.Elems) - rest - 1
	for i := 0; i < after; i++ {
		b.testPat(p.Elems[rest+1+i], index(int64(after-i), true), fail)
	}
}
