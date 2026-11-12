package types

import (
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

// check type-checks x expecting want (with coercion).
func (fc *fnCtx) check(x syntax.Expr, want *Type) *Type {
	got := fc.infer(x, want)
	fc.coerceTo(x, got, want, x.Pos())
	return got
}

// infer computes the type of x; hint guides literals, closures and
// templates but is not enforced.
func (fc *fnCtx) infer(x syntax.Expr, hint *Type) *Type {
	t := fc.inferInner(x, hint)
	return fc.record(x, t)
}

func (fc *fnCtx) lang(name string) *resolve.Def { return fc.info.Lang[name] }

// langAdt builds a std ADT type by lang name.
func (fc *fnCtx) langAdt(name string, args ...*Type) *Type {
	d := fc.lang(name)
	if d == nil {
		return fc.in.ctx.Err
	}
	return fc.in.ctx.Adt(d, args)
}

func (fc *fnCtx) inferInner(x syntax.Expr, hint *Type) *Type {
	ctx := fc.in.ctx
	in := fc.in
	switch x := x.(type) {
	case *syntax.LitExpr:
		return fc.literal(x, hint)
	case *syntax.TemplateExpr:
		return fc.template(x, hint)
	case *syntax.PathExpr:
		return fc.valuePath(x)
	case *syntax.ParenExpr:
		return fc.infer(x.X, hint)
	case *syntax.UnaryExpr:
		return fc.unary(x, hint)
	case *syntax.RefExpr:
		var elemHint *Type
		if h := in.shallow(hint); h != nil && h.Kind == KRef {
			elemHint = h.Elem
		}
		t := fc.infer(x.X, elemHint)
		if x.Mut {
			fc.checkMutPlace(x.X, 0, "borrow as mutable", x.Amp)
		}
		return ctx.Ref(x.Mut, t)
	case *syntax.BinaryExpr:
		return fc.binary(x, hint)
	case *syntax.AssignExpr:
		return fc.assign(x)
	case *syntax.CastExpr:
		from := fc.infer(x.X, nil)
		to := fc.lower(x.Type)
		fc.checkCast(x, from, to)
		return to
	case *syntax.RangeExpr:
		return fc.rangeExpr(x, hint)
	case *syntax.CallExpr:
		return fc.call(x, hint)
	case *syntax.MethodCallExpr:
		return fc.methodCall(x, hint)
	case *syntax.FieldExpr:
		return fc.field(x)
	case *syntax.IndexExpr:
		return fc.index(x)
	case *syntax.TryExpr:
		return fc.tryExpr(x)
	case *syntax.StructLit:
		return fc.structLit(x, hint)
	case *syntax.TupleExpr:
		var hints []*Type
		if h := in.shallow(hint); h != nil && h.Kind == KTuple && len(h.Elems) == len(x.Elems) {
			hints = h.Elems
		}
		elems := make([]*Type, len(x.Elems))
		for i, e := range x.Elems {
			var eh *Type
			if hints != nil {
				eh = hints[i]
			}
			if eh != nil {
				fc.check(e, eh)
				elems[i] = eh
			} else {
				elems[i] = fc.infer(e, nil)
			}
		}
		return ctx.Tuple(elems)
	case *syntax.ArrayExpr:
		var elem *Type
		if h := in.shallow(hint); h != nil && (h.Kind == KArray || h.Kind == KSlice) {
			elem = h.Elem
		}
		if elem == nil {
			elem = in.fresh()
		}
		for _, e := range x.Elems {
			fc.check(e, elem)
		}
		return ctx.Array(elem, int64(len(x.Elems)))
	case *syntax.ArrayRepeatExpr:
		var elem *Type
		if h := in.shallow(hint); h != nil && h.Kind == KArray {
			elem = h.Elem
		}
		if elem == nil {
			elem = in.fresh()
		}
		fc.check(x.Value, elem)
		n, ok := fc.c.evalConst(fc.file, x.Count)
		if !ok {
			return ctx.Err
		}
		fc.check(x.Count, ctx.Ints[Usize])
		return ctx.Array(elem, n)
	case *syntax.BlockExpr:
		if x.Unsafe {
			fc.unsafe++
			defer func() { fc.unsafe-- }()
		}
		return fc.checkBlock(x, hint)
	case *syntax.IfExpr:
		return fc.ifExpr(x, hint)
	case *syntax.LetExpr:
		fc.errorf(x.Let, "let is only allowed as the condition of if and while")
		return ctx.Err
	case *syntax.MatchExpr:
		return fc.match(x, hint)
	case *syntax.LoopExpr:
		lc := &loopCtx{node: x, isLoop: true, breakTy: in.fresh()}
		fc.loops = append(fc.loops, lc)
		fc.checkBlock(x.Body, ctx.Unit)
		fc.loops = fc.loops[:len(fc.loops)-1]
		if !lc.hasBreak {
			return ctx.Never
		}
		return lc.breakTy
	case *syntax.WhileExpr:
		fc.cond(x.Cond)
		fc.loops = append(fc.loops, &loopCtx{node: x})
		fc.checkBlock(x.Body, ctx.Unit)
		fc.loops = fc.loops[:len(fc.loops)-1]
		return ctx.Unit
	case *syntax.ForExpr:
		fc.forExpr(x)
		return ctx.Unit
	case *syntax.BreakExpr:
		lc := fc.loopFor(x.Label)
		if lc == nil {
			if x.Value != nil {
				fc.infer(x.Value, nil)
			}
			return ctx.Never
		}
		lc.hasBreak = true
		if x.Value != nil {
			if !lc.isLoop {
				fc.errorf(x.Break, "break with a value is only allowed in loop")
				fc.infer(x.Value, nil)
			} else {
				fc.check(x.Value, lc.breakTy)
			}
		} else if lc.isLoop {
			fc.in.unify(lc.breakTy, ctx.Unit)
		}
		return ctx.Never
	case *syntax.ContinueExpr:
		return ctx.Never
	case *syntax.ReturnExpr:
		if x.Value != nil {
			fc.check(x.Value, fc.ret)
		} else if !in.tryUnify(fc.ret, ctx.Unit) {
			fc.errorf(x.Return, "return without a value in a function returning %s", in.resolve(fc.ret))
		}
		return ctx.Never
	case *syntax.ClosureExpr:
		return fc.closure(x, hint)
	case *syntax.SelectExpr:
		return fc.selectExpr(x, hint)
	case *syntax.BadExpr:
		return ctx.Err
	}
	return ctx.Err
}

func (fc *fnCtx) loopFor(label *syntax.Name) *loopCtx {
	if label != nil {
		node := fc.info.Res.Labels[label]
		for i := len(fc.loops) - 1; i >= 0; i-- {
			if fc.loops[i].node == node {
				return fc.loops[i]
			}
		}
		return nil
	}
	if len(fc.loops) == 0 {
		return nil
	}
	return fc.loops[len(fc.loops)-1]
}

// ---------------------------------------------------------------------------
// Literals and templates

func (fc *fnCtx) literal(x *syntax.LitExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	h := fc.in.shallow(hint)
	switch x.Kind {
	case syntax.LitInt:
		for k, n := range intNames {
			if strings.HasSuffix(x.Value, n) && !(strings.HasPrefix(x.Value, "0x") && strings.ContainsAny(n, "abcdef")) {
				return ctx.Ints[k]
			}
		}
		if strings.HasSuffix(x.Value, "f32") && !strings.HasPrefix(x.Value, "0x") {
			return ctx.F32
		}
		if strings.HasSuffix(x.Value, "f64") && !strings.HasPrefix(x.Value, "0x") {
			return ctx.F64
		}
		if h != nil && h.Kind == KInt {
			return h
		}
		return fc.in.newVar(varInt)
	case syntax.LitFloat:
		switch {
		case strings.HasSuffix(x.Value, "f32"):
			return ctx.F32
		case strings.HasSuffix(x.Value, "f64"):
			return ctx.F64
		}
		if h != nil && h.Kind == KFloat {
			return h
		}
		return fc.in.newVar(varFloat)
	case syntax.LitChar:
		return ctx.Char
	case syntax.LitByte:
		return ctx.Ints[U8]
	case syntax.LitString:
		return ctx.Ref(false, ctx.Str)
	case syntax.LitByteString:
		return ctx.Ref(false, ctx.Slice(ctx.Ints[U8]))
	case syntax.LitBool:
		return ctx.Bool
	}
	return ctx.Err
}

var specTraits = map[byte]string{'?': "Debug", 'x': "LowerHex", 'X': "UpperHex", 'o': "Octal", 'b': "Binary", 'e': "LowerExp", 'E': "UpperExp"}

func (fc *fnCtx) template(x *syntax.TemplateExpr, hint *Type) *Type {
	for _, p := range x.Parts {
		if p.X == nil {
			continue
		}
		t := fc.infer(p.X, nil)
		trait := "Display"
		if n := len(p.Spec); n > 0 {
			if name, ok := specTraits[p.Spec[n-1]]; ok {
				trait = name
			}
		}
		if d := fc.lang(trait); d != nil {
			fc.require(&TraitRef{Trait: d, Self: t}, p.X.Pos())
		}
	}
	// A template is a lazy fmt::Arguments where the context wants
	// Arguments or something bounded by Display; otherwise a String.
	t := fc.langAdt("String")
	if fc.wantsArguments(hint) {
		t = fc.langAdt("Arguments")
	}
	fc.body.Templates[x] = t
	return t
}

func (fc *fnCtx) wantsArguments(hint *Type) bool {
	h := fc.in.shallow(hint)
	if h == nil {
		return false
	}
	if h.Kind == KAdt && h.Def == fc.lang("Arguments") {
		return true
	}
	if h.Kind != KInfer {
		return false
	}
	origin := fc.varOrigin[h.Var]
	if origin == nil {
		return false
	}
	display := fc.lang("Display")
	if g := fc.c.paramOwner[origin]; g != nil {
		for _, p := range g.Preds {
			if p.Trait.Trait == display && p.Trait.Self == origin.Type {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Operators

func (fc *fnCtx) unary(x *syntax.UnaryExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	switch x.Op {
	case syntax.Star:
		t := fc.in.shallow(fc.infer(x.X, nil))
		switch t.Kind {
		case KRef:
			return t.Elem
		case KPtr:
			fc.requireUnsafe(x.OpPos, "dereference of raw pointer")
			return t.Elem
		case KError:
			return ctx.Err
		}
		if target := fc.overloadedDeref(t); target != nil {
			fc.adjust(x).Derefs = 1
			return target
		}
		fc.errorf(x.OpPos, "type %s cannot be dereferenced", fc.show(t))
		return ctx.Err
	case syntax.Minus, syntax.Not:
		t := fc.in.shallow(fc.infer(x.X, hint))
		if k, ok := fc.in.varKindOf(t); ok && k != varGeneral {
			if x.Op == syntax.Not && k == varFloat {
				fc.errorf(x.OpPos, "cannot apply ! to a float")
			}
			return t
		}
		if x.Op == syntax.Minus && (isFloatT(t) || isIntT(t) && t.Int.Signed()) {
			return t
		}
		if x.Op == syntax.Minus && isIntT(t) {
			fc.errorf(x.OpPos, "cannot negate unsigned integer type %s", t)
			return t
		}
		if x.Op == syntax.Not && (isIntT(t) || t.Kind == KBool) {
			return t
		}
		name := "Neg"
		if x.Op == syntax.Not {
			name = "Not"
		}
		return fc.opTrait(name, t, nil, x.OpPos)
	}
	return ctx.Err
}

// opTrait types an overloaded operator: <Self as Trait<Rhs>>::Output.
func (fc *fnCtx) opTrait(name string, self, rhs *Type, pos syntax.Pos) *Type {
	d := fc.lang(name)
	if d == nil {
		return fc.in.ctx.Err
	}
	tr := &TraitRef{Trait: d, Self: self}
	if rhs != nil {
		tr.Args = []*Type{rhs}
	}
	if r := fc.in.resolve(self); r.Kind != KError && !hasInfer(r) && fc.holds(tr) == selNo {
		op := map[string]string{"Add": "+", "Sub": "-", "Mul": "*", "Div": "/", "Rem": "%", "BitAnd": "&", "BitOr": "|", "BitXor": "^", "Shl": "<<", "Shr": ">>", "Neg": "-", "Not": "!", "PartialEq": "==", "PartialOrd": "<"}[name]
		if rhs != nil {
			fc.errorf(pos, "cannot apply %s to %s and %s", op, fc.show(r), fc.show(rhs))
		} else {
			fc.errorf(pos, "cannot apply %s to %s", op, fc.show(r))
		}
		return fc.in.ctx.Err
	}
	fc.require(tr, pos)
	out := fc.c.findOwnAssocType(d, "Output")
	if out == nil {
		return fc.in.ctx.Bool
	}
	return fc.normalize(fc.in.ctx.Proj(out, tr))
}

var binTraits = map[syntax.Kind]string{
	syntax.Plus: "Add", syntax.Minus: "Sub", syntax.Star: "Mul", syntax.Slash: "Div", syntax.Percent: "Rem",
	syntax.And: "BitAnd", syntax.Or: "BitOr", syntax.Caret: "BitXor", syntax.Shl: "Shl", syntax.Shr: "Shr",
}

func (fc *fnCtx) binary(x *syntax.BinaryExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	in := fc.in
	switch x.Op {
	case syntax.AndAnd, syntax.OrOr:
		fc.check(x.X, ctx.Bool)
		fc.check(x.Y, ctx.Bool)
		return ctx.Bool
	case syntax.EqEq, syntax.Ne, syntax.Lt, syntax.Gt, syntax.Le, syntax.Ge:
		l := fc.infer(x.X, nil)
		r := fc.infer(x.Y, l)
		ls, rs := in.shallow(l), in.shallow(r)
		if fc.scalarLike(ls) && fc.scalarLike(rs) && in.tryUnify(ls, rs) {
			if ls := in.shallow(l); ls.Kind != KInfer || in.vars[ls.Var].kind != varGeneral {
				return ctx.Bool
			}
		}
		name := "PartialEq"
		if x.Op != syntax.EqEq && x.Op != syntax.Ne {
			name = "PartialOrd"
		}
		fc.opTrait(name, l, r, x.OpPos)
		return ctx.Bool
	}
	l := fc.infer(x.X, hint)
	ls := in.shallow(l)
	if x.Op == syntax.Shl || x.Op == syntax.Shr {
		r := in.shallow(fc.infer(x.Y, nil))
		if fc.intLike(ls) && fc.intLike(r) {
			return l
		}
		return fc.opTrait(binTraits[x.Op], l, r, x.OpPos)
	}
	r := fc.infer(x.Y, l)
	rs := in.shallow(r)
	numeric := fc.numLike(ls) && fc.numLike(rs)
	if x.Op == syntax.And || x.Op == syntax.Or || x.Op == syntax.Caret {
		numeric = (fc.intLike(ls) || ls.Kind == KBool) && (fc.intLike(rs) || rs.Kind == KBool)
	}
	if numeric && in.tryUnify(ls, rs) {
		return l
	}
	if k, isVar := in.varKindOf(ls); isVar && k != varGeneral {
		if rr := in.resolve(rs); !hasInfer(rr) && rr.Kind != KError {
			op := map[syntax.Kind]string{syntax.Plus: "+", syntax.Minus: "-", syntax.Star: "*", syntax.Slash: "/", syntax.Percent: "%", syntax.And: "&", syntax.Or: "|", syntax.Caret: "^"}[x.Op]
			fc.errorf(x.OpPos, "cannot apply %s to %s and %s", op, fc.show(ls), fc.show(rr))
			return ctx.Err
		}
	}
	return fc.opTrait(binTraits[x.Op], l, r, x.OpPos)
}

func (fc *fnCtx) intLike(t *Type) bool {
	t = fc.in.shallow(t)
	k, isVar := fc.in.varKindOf(t)
	return isIntT(t) || isVar && k == varInt
}

func (fc *fnCtx) numLike(t *Type) bool {
	t = fc.in.shallow(t)
	k, isVar := fc.in.varKindOf(t)
	return isNumT(t) || isVar && k != varGeneral
}

func (fc *fnCtx) scalarLike(t *Type) bool {
	t = fc.in.shallow(t)
	return fc.numLike(t) || t.Kind == KBool || t.Kind == KChar
}

func (fc *fnCtx) assign(x *syntax.AssignExpr) *Type {
	ctx := fc.in.ctx
	in := fc.in
	l := fc.infer(x.X, nil)
	fc.checkAssignable(x.X, x.OpPos)
	if x.Op == syntax.Eq {
		fc.check(x.Y, l)
		return ctx.Unit
	}
	ls := in.shallow(l)
	r := fc.infer(x.Y, l)
	rs := in.shallow(r)
	base := map[syntax.Kind]syntax.Kind{
		syntax.PlusEq: syntax.Plus, syntax.MinusEq: syntax.Minus, syntax.StarEq: syntax.Star,
		syntax.SlashEq: syntax.Slash, syntax.PercentEq: syntax.Percent, syntax.AndEq: syntax.And,
		syntax.OrEq: syntax.Or, syntax.CaretEq: syntax.Caret, syntax.ShlEq: syntax.Shl, syntax.ShrEq: syntax.Shr,
	}[x.Op]
	switch base {
	case syntax.Shl, syntax.Shr:
		if fc.intLike(ls) && fc.intLike(rs) {
			return ctx.Unit
		}
	case syntax.And, syntax.Or, syntax.Caret:
		if (fc.intLike(ls) || ls.Kind == KBool) && in.tryUnify(ls, rs) {
			return ctx.Unit
		}
	default:
		if fc.numLike(ls) && fc.numLike(rs) && in.tryUnify(ls, rs) {
			return ctx.Unit
		}
	}
	name := binTraits[base] + "Assign"
	if d := fc.lang(name); d != nil {
		tr := &TraitRef{Trait: d, Self: l, Args: []*Type{r}}
		if res := in.resolve(l); !hasInfer(res) && res.Kind != KError && fc.holds(tr) == selNo {
			fc.errorf(x.OpPos, "cannot apply %s to %s and %s", x.Op, res, in.resolve(r))
			return ctx.Unit
		}
		fc.require(tr, x.OpPos)
	}
	return ctx.Unit
}

func (fc *fnCtx) checkCast(x *syntax.CastExpr, from, to *Type) {
	f, t := fc.in.shallow(from), fc.in.shallow(to)
	if f.Kind == KError || t.Kind == KError {
		return
	}
	if k, ok := fc.in.varKindOf(f); ok && k != varGeneral {
		if isNumT(t) || k == varInt && (t.Kind == KChar || t.Kind == KPtr) {
			return
		}
	}
	ok := false
	switch {
	case isNumT(f) && isNumT(t):
		ok = true
	case f.Kind == KBool && isIntT(t), f.Kind == KChar && isIntT(t):
		ok = true
	case isIntT(f) && f.Int == U8 && t.Kind == KChar:
		ok = true
	case f.Kind == KRef && t.Kind == KPtr:
		ok = fc.in.tryUnify(f.Elem, t.Elem) && (f.Mut || !t.Mut)
	case f.Kind == KPtr && t.Kind == KPtr, f.Kind == KPtr && isIntT(t), isIntT(f) && t.Kind == KPtr:
		ok = true
	case f.Kind == KAdt && isIntT(t):
		if ai := fc.c.adt(f.Def); ai.IsEnum {
			ok = true
			for _, v := range ai.Variants {
				if v.Kind != syntax.StructUnit {
					ok = false
				}
			}
		}
	case f.Kind == KFn && (t.Kind == KPtr || isIntT(t)) && f.Abi == "C":
		ok = true
	default:
		ok = fc.in.tryUnify(f, t)
	}
	if !ok {
		fc.errorf(x.Type.Pos(), "invalid cast from %s to %s", fc.in.resolve(f), fc.in.resolve(t))
	}
}

func (fc *fnCtx) rangeExpr(x *syntax.RangeExpr, hint *Type) *Type {
	var elem *Type
	if h := fc.in.shallow(hint); h != nil && h.Kind == KAdt && len(h.Args) == 1 {
		elem = h.Args[0]
	}
	if elem == nil {
		elem = fc.in.fresh()
	}
	if x.Lo != nil {
		fc.check(x.Lo, elem)
	}
	if x.Hi != nil {
		fc.check(x.Hi, elem)
	}
	switch {
	case x.Lo == nil && x.Hi == nil:
		return fc.langAdt("RangeFull")
	case x.Lo == nil && x.Inclusive:
		return fc.langAdt("RangeToInclusive", elem)
	case x.Lo == nil:
		return fc.langAdt("RangeTo", elem)
	case x.Hi == nil:
		return fc.langAdt("RangeFrom", elem)
	case x.Inclusive:
		return fc.langAdt("RangeInclusive", elem)
	}
	return fc.langAdt("Range", elem)
}

// ---------------------------------------------------------------------------
// Calls, fields, indexing

func (fc *fnCtx) call(x *syntax.CallExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	ft := fc.in.shallow(fc.infer(x.Fun, nil))
	if ft.Kind == KInfer {
		fc.solvePending(false)
		ft = fc.in.shallow(ft)
	}
	if ft.Kind == KError {
		for _, a := range x.Args {
			fc.infer(a, nil)
		}
		return ctx.Err
	}
	if ft.Kind != KFn {
		fc.errorf(x.Lparen, "expected function, found %s", fc.in.resolve(ft))
		for _, a := range x.Args {
			fc.infer(a, nil)
		}
		return ctx.Err
	}
	if hint != nil && fc.in.shallow(ft.Ret).Kind != KNever {
		// Let the expected result guide inference of generic parameters.
		fc.in.tryUnify(ft.Ret, hint)
	}
	if len(x.Args) != len(ft.Elems) {
		fc.errorf(x.Lparen, "this function takes %d argument%s but %d were supplied", len(ft.Elems), plural(len(ft.Elems)), len(x.Args))
	}
	for i, a := range x.Args {
		if i < len(ft.Elems) {
			fc.check(a, ft.Elems[i])
		} else {
			fc.infer(a, nil)
		}
	}
	return ft.Ret
}

func (fc *fnCtx) field(x *syntax.FieldExpr) *Type {
	ctx := fc.in.ctx
	base := fc.infer(x.X, nil)
	if fc.in.shallow(base).Kind == KInfer {
		fc.solvePending(false)
	}
	steps := fc.autoderef(base, false)
	viaShared := false
	for i, s := range steps {
		s = fc.in.shallow(s)
		if i > 0 {
			prev := fc.in.shallow(steps[i-1])
			if prev.Kind == KRef && !prev.Mut {
				viaShared = true
			}
		}
		var ft *Type
		idx := -1
		switch s.Kind {
		case KAdt:
			ai := fc.c.adt(s.Def)
			if ai.IsEnum {
				continue
			}
			m := paramMap(ai.Generics, s.Args)
			for j, f := range ai.Fields {
				if f.Name == x.Name.Value {
					if !f.Vis.AccessibleFrom(fc.module) {
						fc.errorf(x.Name.NamePos, "field %s of %s is private", f.Name, s.Def.Name)
					}
					ft, idx = fc.normalize(Subst(ctx, f.Type, m, nil)), j
				}
			}
		case KTuple:
			for j := range s.Elems {
				if itoa(j) == x.Name.Value {
					ft, idx = s.Elems[j], j
				}
			}
		}
		if ft == nil {
			continue
		}
		fc.body.Fields[x] = idx
		if i > 0 {
			fc.adjust(x.X).Derefs = i
		}
		// &T is read-only transitively: a &mut U field read through a shared
		// reference becomes &U.
		if r := fc.in.shallow(ft); viaShared && r.Kind == KRef && r.Mut {
			ft = ctx.Ref(false, r.Elem)
		}
		return ft
	}
	r := fc.in.resolve(base)
	if r.Kind != KError {
		if hasInfer(r) {
			fc.errorf(x.Name.NamePos, "type annotations needed: the type of the value must be known to access field %s", x.Name.Value)
		} else {
			fc.errorf(x.Name.NamePos, "no field %s on type %s", x.Name.Value, r)
		}
	}
	return ctx.Err
}

func (fc *fnCtx) index(x *syntax.IndexExpr) *Type {
	ctx := fc.in.ctx
	base := fc.infer(x.X, nil)
	idx := fc.infer(x.Index, nil)
	idxTrait := fc.lang("Index")
	if idxTrait == nil {
		return ctx.Err
	}
	for i, s := range fc.autoderef(base, false) {
		s = fc.in.shallow(s)
		if s.Kind == KInfer {
			break
		}
		tr := &TraitRef{Trait: idxTrait, Self: s, Args: []*Type{idx}}
		if fc.holds(tr) == selNo {
			continue
		}
		if i > 0 {
			fc.adjust(x.X).Derefs = i
		}
		fc.require(tr, x.Lbrack)
		out := fc.c.findOwnAssocType(idxTrait, "Output")
		return fc.normalize(ctx.Proj(out, tr))
	}
	r := fc.in.resolve(base)
	if r.Kind != KError {
		fc.errorf(x.Lbrack, "cannot index into a value of type %s with %s", r, fc.in.resolve(idx))
	}
	return ctx.Err
}

func (fc *fnCtx) tryExpr(x *syntax.TryExpr) *Type {
	ctx := fc.in.ctx
	t := fc.in.shallow(fc.infer(x.X, nil))
	ret := fc.in.shallow(fc.ret)
	optDef, resDef := fc.lang("Option"), fc.lang("Result")
	if t.Kind == KError {
		return ctx.Err
	}
	if t.Kind != KAdt || (t.Def != optDef && t.Def != resDef) {
		fc.errorf(x.Question, "the ? operator can only be applied to Result or Option, not %s", fc.in.resolve(t))
		return ctx.Err
	}
	if t.Def == optDef {
		if !(ret.Kind == KAdt && ret.Def == optDef) && !fc.in.tryUnify(ret, ctx.Adt(optDef, []*Type{fc.in.fresh()})) {
			fc.errorf(x.Question, "the ? operator on an Option can only be used in a function that returns Option")
		}
		return t.Args[0]
	}
	if ret.Kind == KInfer {
		fc.in.unify(ret, ctx.Adt(resDef, []*Type{fc.in.fresh(), fc.in.fresh()}))
		ret = fc.in.shallow(ret)
	}
	if !(ret.Kind == KAdt && ret.Def == resDef) {
		fc.errorf(x.Question, "the ? operator on a Result can only be used in a function that returns Result")
		return t.Args[0]
	}
	errFrom, errTo := t.Args[1], ret.Args[1]
	if !fc.in.probe(func() bool { return fc.in.unify(errFrom, errTo) }) {
		if from := fc.lang("From"); from != nil {
			fc.require(&TraitRef{Trait: from, Self: errTo, Args: []*Type{errFrom}}, x.Question)
		}
	} else {
		fc.in.unify(errFrom, errTo)
	}
	return t.Args[0]
}

func (fc *fnCtx) structLit(x *syntax.StructLit, hint *Type) *Type {
	ctx := fc.in.ctx
	res := fc.info.Res.Paths[x.Path]
	var adt *Type
	var variant *resolve.Def
	switch {
	case res.Kind == resolve.ResSelfTy && len(res.Rest) == 0:
		adt = fc.env.selfTy
	case res.Kind == resolve.ResDef && len(res.Rest) == 0:
		switch res.Def.Kind {
		case resolve.DefStruct:
			ai := fc.c.adt(res.Def)
			m, vars := fc.instantiate(ai.Generics)
			fc.explicitArgs(ai.Generics.Params, x.Path.Segments[len(x.Path.Segments)-1].Args, m, "struct "+res.Def.Name)
			adt = ctx.Adt(res.Def, vars)
		case resolve.DefVariant:
			ai := fc.c.adt(res.Def.Parent)
			_, vars := fc.instantiate(ai.Generics)
			adt = ctx.Adt(res.Def.Parent, vars)
			variant = res.Def
		case resolve.DefTypeAlias:
			ai := fc.c.alias(res.Def)
			if ai != nil {
				m, _ := fc.instantiate(ai.Generics)
				adt = fc.normalize(Subst(ctx, ai.Type, m, nil))
			}
		}
	case len(res.Rest) == 1:
		base, _ := fc.baseType(x.Path, res)
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
			fc.errorf(x.Path.Pos(), "expected a struct or variant with named fields")
		}
		for _, f := range x.Fields {
			fc.infer(f.Value, nil)
		}
		return ctx.Err
	}
	adt = fc.in.shallow(adt)
	if hint != nil {
		fc.in.tryUnify(adt, hint)
	}
	ai := fc.c.adt(adt.Def)
	fields, kind := ai.Fields, ai.Kind
	if variant != nil {
		for _, v := range ai.Variants {
			if v.Def == variant {
				fields, kind = v.Fields, v.Kind
			}
		}
	} else if ai.IsEnum {
		fc.errorf(x.Path.Pos(), "expected a struct or variant, found enum %s", adt.Def.Name)
		return ctx.Err
	}
	m := paramMap(ai.Generics, adt.Args)
	seen := map[string]bool{}
	byName := map[string]*Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	for _, fi := range x.Fields {
		f := byName[fi.Name.Value]
		if f == nil {
			fc.errorf(fi.Name.NamePos, "%s has no field named %s", adt.Def.Name, fi.Name.Value)
			continue
		}
		if seen[f.Name] {
			fc.errorf(fi.Name.NamePos, "field %s is specified more than once", f.Name)
		}
		seen[f.Name] = true
		if !f.Vis.AccessibleFrom(fc.module) {
			fc.errorf(fi.Name.NamePos, "field %s of %s is private", f.Name, adt.Def.Name)
		}
		fc.check(fi.Value, fc.normalize(Subst(ctx, f.Type, m, nil)))
	}
	if x.Base != nil {
		fc.check(x.Base, adt)
	} else if kind != syntax.StructUnit || len(fields) > 0 {
		var missing []string
		for _, f := range fields {
			if !seen[f.Name] {
				missing = append(missing, f.Name)
			}
		}
		if len(missing) > 0 {
			fc.errorf(x.Path.Pos(), "missing field%s %s in initializer of %s", plural(len(missing)), strings.Join(missing, ", "), adt.Def.Name)
		}
	}
	return adt
}

// ---------------------------------------------------------------------------
// Control flow

func (fc *fnCtx) cond(c syntax.Expr) {
	if l, ok := c.(*syntax.LetExpr); ok {
		t := fc.infer(l.X, nil)
		fc.checkPat(l.Pat, t, true)
		return
	}
	fc.check(c, fc.in.ctx.Bool)
}

func (fc *fnCtx) ifExpr(x *syntax.IfExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	fc.cond(x.Cond)
	if x.Else == nil {
		t := fc.checkBlock(x.Then, ctx.Unit)
		fc.coerceTo(x.Then, t, ctx.Unit, x.Then.Rbrace)
		return ctx.Unit
	}
	then := fc.checkBlock(x.Then, hint)
	// A diverging then-branch says nothing about the else-branch's type.
	elseHint := then
	if fc.in.shallow(then).Kind == KNever {
		elseHint = hint
	}
	els := fc.infer(x.Else, elseHint)
	if hint != nil && fc.in.shallow(hint).Kind != KInfer {
		fc.coerceTo(x.Then, then, hint, x.Then.Rbrace)
		fc.coerceTo(x.Else, els, hint, x.Else.Pos())
		return hint
	}
	return fc.lub(x.Else, then, els, x.Else.Pos())
}

func (fc *fnCtx) match(x *syntax.MatchExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	scrut := fc.infer(x.X, nil)
	var result *Type
	if hint != nil && fc.in.shallow(hint).Kind != KInfer {
		result = hint
	}
	for _, arm := range x.Arms {
		fc.checkPat(arm.Pat, scrut, true)
		if arm.Guard != nil {
			fc.check(arm.Guard, ctx.Bool)
		}
		t := fc.infer(arm.Body, result)
		switch {
		case result == nil:
			if fc.in.shallow(t).Kind != KNever {
				result = t
			}
		default:
			if !fc.coerce(arm.Body, t, result) {
				fc.mismatch(arm.Body.Pos(), result, t)
			}
		}
	}
	fc.checkExhaustive(x, scrut)
	if result == nil {
		return ctx.Never
	}
	return result
}

func (fc *fnCtx) forExpr(x *syntax.ForExpr) {
	ctx := fc.in.ctx
	iter := fc.infer(x.Iter, nil)
	// Arrays iterate as slices (impls for [T; N] need const generics).
	if it := fc.in.shallow(iter); it.Kind == KRef {
		if e := fc.in.shallow(it.Elem); e.Kind == KArray {
			iter = ctx.Ref(it.Mut, ctx.Slice(e.Elem))
			fc.adjust(x.Iter).Unsize = true
		}
	}
	into := fc.lang("IntoIterator")
	var item *Type = ctx.Err
	if into != nil {
		tr := &TraitRef{Trait: into, Self: iter}
		if r := fc.in.resolve(iter); r.Kind != KError && !hasInfer(r) && fc.holds(tr) == selNo {
			fc.errorf(x.Iter.Pos(), "%s is not an iterator (it does not implement IntoIterator)", r)
		} else {
			fc.require(tr, x.Iter.Pos())
			if a := fc.c.findOwnAssocType(into, "Item"); a != nil {
				item = fc.normalize(ctx.Proj(a, tr))
			}
		}
	}
	fc.checkPat(x.Pat, item, false)
	fc.loops = append(fc.loops, &loopCtx{node: x})
	fc.checkBlock(x.Body, ctx.Unit)
	fc.loops = fc.loops[:len(fc.loops)-1]
}

func (fc *fnCtx) closure(x *syntax.ClosureExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	h := fc.in.shallow(hint)
	params := make([]*Type, len(x.Params))
	for i, p := range x.Params {
		var t *Type
		if p.Type != nil {
			t = fc.lower(p.Type)
		}
		if h != nil && h.Kind == KFn && len(h.Elems) == len(x.Params) {
			if t == nil {
				t = h.Elems[i]
			} else {
				fc.in.unify(t, h.Elems[i])
			}
		}
		if t == nil {
			t = fc.in.fresh()
		}
		params[i] = t
		fc.checkPat(p.Pat, t, false)
	}
	var ret *Type
	if x.Result != nil {
		ret = fc.lower(x.Result)
	} else if h != nil && h.Kind == KFn {
		ret = h.Ret
	} else {
		ret = fc.in.fresh()
	}
	oldRet, oldLoops := fc.ret, fc.loops
	fc.ret, fc.loops = ret, nil
	body := fc.infer(x.Body, ret)
	fc.coerceTo(x.Body, body, ret, x.Body.Pos())
	fc.ret, fc.loops = oldRet, oldLoops
	return ctx.Fn(params, ret, "")
}

func (fc *fnCtx) selectExpr(x *syntax.SelectExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	var result *Type
	if hint != nil && fc.in.shallow(hint).Kind != KInfer {
		result = hint
	}
	defaults := 0
	for _, arm := range x.Arms {
		if arm.Default {
			defaults++
		} else {
			t := fc.infer(arm.Op, nil)
			if mc, ok := arm.Op.(*syntax.MethodCallExpr); ok && mc.Name.Value != "recv" && mc.Name.Value != "send" {
				fc.errorf(mc.Name.NamePos, "select cases must call recv() or send() on a channel")
			}
			if arm.Pat != nil {
				fc.checkPat(arm.Pat, t, false)
			}
		}
		t := fc.infer(arm.Body, result)
		if result == nil {
			if fc.in.shallow(t).Kind != KNever {
				result = t
			}
		} else if !fc.coerce(arm.Body, t, result) {
			fc.mismatch(arm.Body.Pos(), result, t)
		}
	}
	if defaults > 1 {
		fc.errorf(x.Select, "select can have at most one default case")
	}
	if result == nil {
		return ctx.Never
	}
	return result
}

// ---------------------------------------------------------------------------
// Blocks and statements

func (fc *fnCtx) checkBlock(b *syntax.BlockExpr, hint *Type) *Type {
	ctx := fc.in.ctx
	diverges := false
	for _, st := range b.Stmts {
		switch st := st.(type) {
		case *syntax.LetStmt:
			fc.curPos = st.Let
			t := fc.lowerOrInfer(st.Type)
			if st.Init != nil {
				it := fc.check(st.Init, t)
				if fc.in.shallow(it).Kind == KNever {
					diverges = true
				}
			}
			if st.Else != nil {
				et := fc.checkBlock(st.Else, nil)
				if fc.in.shallow(et).Kind != KNever {
					fc.errorf(st.Else.Lbrace, "the else block of let-else must diverge (return, break, continue or panic)")
				}
				fc.checkPat(st.Pat, t, true)
			} else {
				fc.checkPat(st.Pat, t, false)
			}
		case *syntax.ExprStmt:
			fc.curPos = st.X.Pos()
			var t *Type
			if st.Semi {
				t = fc.infer(st.X, nil)
			} else {
				t = fc.infer(st.X, ctx.Unit)
				if s := fc.in.shallow(t); s.Kind != KNever && s.Kind != KError && !fc.in.tryUnify(s, ctx.Unit) {
					fc.errorf(st.X.Pos(), "expected (), found %s; add a semicolon to discard the value", fc.in.resolve(s))
				}
			}
			if fc.in.shallow(t).Kind == KNever {
				diverges = true
			}
		case *syntax.DeferStmt:
			fc.curPos = st.Defer
			fc.infer(st.X, nil)
		}
	}
	if b.Tail != nil {
		fc.curPos = b.Tail.Pos()
		t := fc.infer(b.Tail, hint)
		if diverges && fc.in.shallow(t).Kind != KNever {
			return t
		}
		return t
	}
	if diverges {
		return ctx.Never
	}
	return ctx.Unit
}

// ---------------------------------------------------------------------------
// Places and mutability

// checkMutPlace reports an error unless x (after derefs dereferences) is a
// place that may be mutated or mutably borrowed.
func (fc *fnCtx) checkMutPlace(x syntax.Expr, derefs int, what string, pos syntax.Pos) {
	if t := fc.in.shallow(fc.body.Types[x]); derefs > 0 && t != nil {
		switch {
		case t.Kind == KRef && !t.Mut:
			fc.errorf(pos, "cannot %s data behind a & reference", what)
			return
		case t.Kind == KRef:
			return
		case t.Kind == KPtr:
			if !t.Mut {
				fc.errorf(pos, "cannot %s data behind a *const pointer", what)
			}
			return
		}
		// Overloaded deref (Vec, String): the place itself must be mutable.
	}
	if ok, why := fc.isMutPlace(x); !ok {
		fc.errorf(pos, "cannot %s %s", what, why)
	}
}

func (fc *fnCtx) checkAssignable(x syntax.Expr, pos syntax.Pos) {
	if ok, why := fc.isMutPlace(x); !ok {
		fc.errorf(pos, "cannot assign to %s", why)
	}
}

// isMutPlace reports whether x denotes a mutable place, and why not.
func (fc *fnCtx) isMutPlace(x syntax.Expr) (bool, string) {
	switch x := x.(type) {
	case *syntax.ParenExpr:
		return fc.isMutPlace(x.X)
	case *syntax.PathExpr:
		res := fc.info.Res.Paths[x.Path]
		switch {
		case res.Kind == resolve.ResLocal:
			if res.Local.Mut {
				return true, ""
			}
			return false, "immutable variable " + res.Local.Name + " (declare it with let mut)"
		case res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefStatic:
			if ci := fc.c.constInfo(res.Def); ci.Mut {
				return true, ""
			}
			return false, "immutable static " + res.Def.Name
		case res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefExternStatic:
			return true, ""
		}
		return false, "this expression"
	case *syntax.FieldExpr, *syntax.IndexExpr:
		var base syntax.Expr
		if f, ok := x.(*syntax.FieldExpr); ok {
			base = f.X
		} else {
			base = x.(*syntax.IndexExpr).X
		}
		// Through a reference, mutability is the reference's.
		t := fc.in.shallow(fc.body.Types[base])
		if t != nil && t.Kind == KRef {
			if !t.Mut {
				return false, "data behind a & reference"
			}
			return true, ""
		}
		if t != nil && t.Kind == KPtr {
			return t.Mut, "data behind a *const pointer"
		}
		if a := fc.body.Adjust[base]; a != nil && a.Derefs > 0 && t != nil && t.Kind == KAdt {
			// Overloaded deref (Vec, String): needs DerefMut on a mutable place.
			return fc.isMutPlace(base)
		}
		return fc.isMutPlace(base)
	case *syntax.UnaryExpr:
		if x.Op == syntax.Star {
			t := fc.in.shallow(fc.body.Types[x.X])
			switch {
			case t == nil:
				return true, ""
			case t.Kind == KRef && !t.Mut:
				return false, "data behind a & reference"
			case t.Kind == KPtr && !t.Mut:
				return false, "data behind a *const pointer"
			case t.Kind == KAdt:
				return fc.isMutPlace(x.X)
			}
			return true, ""
		}
	}
	// Temporaries can be mutated.
	return true, ""
}
