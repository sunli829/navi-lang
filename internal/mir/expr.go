package mir

import (
	"strconv"
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

func (b *builder) ctx() *types.Ctx { return b.info.Ctx }

// operand lowers x to a value, applying recorded coercions.
func (b *builder) operand(x syntax.Expr) Operand {
	op := b.rawOperand(x)
	return b.coerce(x, op)
}

// coerce applies the implicit conversion recorded for x.
func (b *builder) coerce(x syntax.Expr, op Operand) Operand {
	a := b.body.Adjust[x]
	if a == nil || a.Autoref == 0 && !a.Unsize && a.ToDyn == nil {
		return op
	}
	ctx := b.ctx()
	t := b.typeOf(x)
	if a.Autoref > 0 && a.Derefs > 0 {
		// Deref coercion: &T -> &U through Deref.
		tmp := b.temp(t)
		b.assign(tmp, Use{X: op})
		p, pt := tmp.Deref(), t.Elem
		for i := 1; i < a.Derefs; i++ {
			p, pt = b.derefPlace(p, pt, a.Autoref == 2, x.Pos())
		}
		out := b.temp(ctx.Ref(a.Autoref == 2, pt))
		b.assign(out, Ref{Mut: a.Autoref == 2, Place: p})
		return Copy(out)
	}
	if a.Unsize {
		if t.Kind == types.KRef && t.Elem.Kind == types.KArray {
			to := ctx.Ref(t.Mut, ctx.Slice(t.Elem.Elem))
			out := b.temp(to)
			b.assign(out, Unsize{X: op, Len: t.Elem.Len, To: to})
			return Copy(out)
		}
		return op
	}
	if a.ToDyn != nil {
		out := b.temp(a.ToDyn)
		b.assign(out, MakeDyn{X: op, Concrete: t.Elem, To: a.ToDyn})
		return Copy(out)
	}
	return op
}

// derefPlace dereferences p of type t once: through a reference or raw
// pointer, or by calling Deref::deref (DerefMut::deref_mut if mut).
func (b *builder) derefPlace(p Place, t *types.Type, mut bool, pos syntax.Pos) (Place, *types.Type) {
	ctx := b.ctx()
	if t.Kind == types.KRef || t.Kind == types.KPtr {
		return p.Deref(), t.Elem
	}
	target := b.info.DerefTarget(b.body.Owner, t)
	if target == nil {
		target = ctx.Err
	}
	trait, method := "Deref", "deref"
	if mut {
		trait, method = "DerefMut", "deref_mut"
	}
	m := b.info.TraitItem(trait, method)
	ref := b.temp(ctx.Ref(mut, t))
	b.assign(ref, Ref{Mut: mut, Place: p})
	out := b.temp(ctx.Ref(mut, target))
	b.call(b.traitFn(m, t, nil), []Operand{Copy(ref)}, out, pos)
	return out.Deref(), target
}

func (b *builder) traitFn(method *resolve.Def, self *types.Type, args []*types.Type) Operand {
	return ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: method, Self: self, Args: args}})
}

func (b *builder) fnConst(def *resolve.Def, args []*types.Type) Operand {
	return ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: def, Args: args}})
}

func unitConst(ctx *types.Ctx) Operand {
	return ConstOp(&Const{Kind: CUnit, Type: ctx.Unit})
}

// rawOperand lowers x without coercion.
func (b *builder) rawOperand(x syntax.Expr) Operand {
	ctx := b.ctx()
	switch x := x.(type) {
	case *syntax.LitExpr:
		return b.literal(x)
	case *syntax.ParenExpr:
		return b.rawOperand(x.X)
	case *syntax.PathExpr:
		res := b.res.Paths[x.Path]
		cl := b.body.Callees[x]
		switch {
		case res.Kind == resolve.ResLocal:
			return Copy(b.localPlace(res.Local))
		case cl != nil && cl.Def != nil:
			d := cl.Def
			switch d.Kind {
			case resolve.DefFn, resolve.DefExternFn, resolve.DefAssocFn:
				return ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: d, Args: cl.Args, Self: cl.Self}, Type: b.typeOf(x)})
			case resolve.DefConst, resolve.DefAssocConst:
				out := b.temp(b.typeOf(x))
				b.call(ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: d, Args: cl.Args, Self: cl.Self}}), nil, out, x.Pos())
				return Copy(out)
			case resolve.DefStatic, resolve.DefExternStatic:
				return Copy(b.staticPlace(d))
			case resolve.DefStruct, resolve.DefVariant:
				t := b.typeOf(x)
				if t.Kind == types.KFn {
					return ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: d, Args: cl.Args, Ctor: true}, Type: t})
				}
				out := b.temp(t)
				b.aggregateCtor(d, t, nil, out)
				return Copy(out)
			}
		}
		if t := b.typeOf(x); t.Kind == types.KAdt {
			// Self as a unit struct value.
			out := b.temp(t)
			b.assign(out, Aggregate{Kind: AggAdt, Type: t})
			return Copy(out)
		}
		return ConstOp(&Const{Kind: CZero, Type: b.typeOf(x)})
	}
	t := b.typeOf(x)
	if t.Kind == types.KNever {
		b.exprIntoRaw(x, b.temp(t))
		return ConstOp(&Const{Kind: CZero, Type: t})
	}
	tmp := b.temp(t)
	b.exprIntoRaw(x, tmp)
	_ = ctx
	return Copy(tmp)
}

func (b *builder) staticPlace(d *resolve.Def) Place {
	ctx := b.ctx()
	t := b.info.Consts[d].Type
	ptr := b.temp(ctx.Ref(true, t))
	b.assign(ptr, Use{X: ConstOp(&Const{Kind: CStatic, Static: d, Type: ctx.Ref(true, t)})})
	return ptr.Deref()
}

func (b *builder) literal(x *syntax.LitExpr) Operand {
	t := b.typeOf(x)
	switch x.Kind {
	case syntax.LitInt:
		v, _ := parseInt(x.Value)
		if t.Kind == types.KFloat {
			return ConstOp(&Const{Kind: CFloat, Float: float64(v), Type: t})
		}
		return ConstOp(&Const{Kind: CInt, Int: v, Type: t})
	case syntax.LitFloat:
		s := strings.TrimSuffix(strings.TrimSuffix(strings.ReplaceAll(x.Value, "_", ""), "f32"), "f64")
		f, _ := strconv.ParseFloat(s, 64)
		return ConstOp(&Const{Kind: CFloat, Float: f, Type: t})
	case syntax.LitBool:
		v := int64(0)
		if x.Value == "true" {
			v = 1
		}
		return ConstOp(&Const{Kind: CBool, Int: v, Type: t})
	case syntax.LitChar:
		v, _ := charLit(x.Value)
		return ConstOp(&Const{Kind: CChar, Int: v, Type: t})
	case syntax.LitByte:
		v, _ := charLit(x.Value[1:])
		return ConstOp(&Const{Kind: CInt, Int: v, Type: t})
	case syntax.LitString:
		return ConstOp(&Const{Kind: CStr, Str: unquote(x.Value), Type: t})
	case syntax.LitByteString:
		return ConstOp(&Const{Kind: CBytes, Str: unquote(x.Value[1:]), Type: t})
	}
	return ConstOp(&Const{Kind: CZero, Type: t})
}

// place lowers a place expression. mut requests a place that will be
// mutated or mutably borrowed (selecting DerefMut/IndexMut).
func (b *builder) place(x syntax.Expr, mut bool) Place {
	ctx := b.ctx()
	switch x := x.(type) {
	case *syntax.ParenExpr:
		return b.place(x.X, mut)
	case *syntax.PathExpr:
		res := b.res.Paths[x.Path]
		if res.Kind == resolve.ResLocal {
			return b.localPlace(res.Local)
		}
		if cl := b.body.Callees[x]; cl != nil && cl.Def != nil && (cl.Def.Kind == resolve.DefStatic || cl.Def.Kind == resolve.DefExternStatic) {
			return b.staticPlace(cl.Def)
		}
	case *syntax.FieldExpr:
		base, bt := b.autoderefBase(x.X, mut)
		idx := b.body.Fields[x]
		_ = bt
		return base.Field(idx)
	case *syntax.IndexExpr:
		return b.indexPlace(x, mut)
	case *syntax.UnaryExpr:
		if x.Op == syntax.Star {
			if a := b.body.Adjust[x]; a != nil && a.Derefs == 1 {
				inner := b.place(x.X, mut)
				p, _ := b.derefPlace(inner, b.typeOf(x.X), mut, x.OpPos)
				return p
			}
			t := b.typeOf(x.X)
			if t.Kind == types.KRef || t.Kind == types.KPtr {
				return b.placeOrTemp(x.X).Deref()
			}
		}
	}
	t := b.typeOf(x)
	tmp := b.temp(t)
	b.exprInto(x, tmp)
	_ = ctx
	return tmp
}

// placeOrTemp returns the place of x if it is a place expression, else a
// temporary holding its value.
func (b *builder) placeOrTemp(x syntax.Expr) Place {
	switch x.(type) {
	case *syntax.PathExpr, *syntax.FieldExpr, *syntax.IndexExpr, *syntax.ParenExpr, *syntax.UnaryExpr:
		return b.place(x, false)
	}
	tmp := b.temp(b.typeOf(x))
	b.exprInto(x, tmp)
	return tmp
}

// autoderefBase lowers the base of a field or index expression, applying
// the autoderef steps recorded by the type checker.
func (b *builder) autoderefBase(x syntax.Expr, mut bool) (Place, *types.Type) {
	p := b.placeOrTempMut(x, mut)
	t := b.typeOf(x)
	if a := b.body.Adjust[x]; a != nil && a.Autoref == 0 {
		for i := 0; i < a.Derefs; i++ {
			p, t = b.derefPlace(p, t, mut, x.Pos())
		}
	}
	return p, t
}

func (b *builder) placeOrTempMut(x syntax.Expr, mut bool) Place {
	switch x.(type) {
	case *syntax.PathExpr, *syntax.FieldExpr, *syntax.IndexExpr, *syntax.ParenExpr, *syntax.UnaryExpr:
		return b.place(x, mut)
	}
	tmp := b.temp(b.typeOf(x))
	b.exprInto(x, tmp)
	return tmp
}

func (b *builder) indexPlace(x *syntax.IndexExpr, mut bool) Place {
	ctx := b.ctx()
	base, bt := b.autoderefBase(x.X, mut)
	it := b.typeOf(x.Index)
	if (bt.Kind == types.KArray || bt.Kind == types.KSlice) && it.Kind == types.KInt {
		idx := b.temp(ctx.Ints[types.Usize])
		b.assign(idx, Use{X: b.operand(x.Index)})
		n := b.temp(ctx.Ints[types.Usize])
		b.assign(n, Len{Place: base})
		ok := b.temp(ctx.Bool)
		b.assign(ok, Bin{Op: Lt, X: Copy(idx), Y: Copy(n), Type: ctx.Ints[types.Usize]})
		next := b.newBlock()
		b.terminate(Assert{Cond: Copy(ok), Msg: "index out of bounds", Target: next, Pos: x.Lbrack, Bounds: true, Len: Copy(n), Index: Copy(idx)})
		b.cur = next
		return base.With(Proj{Kind: PIndex, Index: idx.Local})
	}
	// Overloaded (or builtin range) indexing: *Index::index(&base, idx).
	trait, method := "Index", "index"
	if mut {
		trait, method = "IndexMut", "index_mut"
	}
	m := b.info.TraitItem(trait, method)
	ref := b.temp(ctx.Ref(mut, bt))
	b.assign(ref, Ref{Mut: mut, Place: base})
	out := b.temp(ctx.Ref(mut, b.typeOf(x)))
	b.call(b.traitFn(m, bt, []*types.Type{it}), []Operand{Copy(ref), b.operand(x.Index)}, out, x.Lbrack)
	return out.Deref()
}

// aggregateCtor builds a struct/variant value from positional operands.
func (b *builder) aggregateCtor(d *resolve.Def, t *types.Type, ops []Operand, dest Place) {
	if d.Kind == resolve.DefVariant {
		b.assign(dest, Aggregate{Kind: AggAdt, Type: t, Variant: variantIndex(b.info, d), Ops: ops})
		return
	}
	b.assign(dest, Aggregate{Kind: AggAdt, Type: t, Ops: ops})
}

func variantIndex(info *types.Info, d *resolve.Def) int {
	for i, v := range info.Adts[d.Parent].Variants {
		if v.Def == d {
			return i
		}
	}
	return -1
}

// exprInto lowers x (with its recorded coercion) and stores the value in
// dest.
func (b *builder) exprInto(x syntax.Expr, dest Place) {
	if b.cur < 0 {
		return
	}
	if a := b.body.Adjust[x]; a != nil && (a.Autoref > 0 || a.Unsize || a.ToDyn != nil) {
		tmp := b.temp(b.typeOf(x))
		b.exprIntoRaw(x, tmp)
		b.assign(dest, Use{X: b.coerce(x, Copy(tmp))})
		return
	}
	b.exprIntoRaw(x, dest)
}

// exprIntoRaw lowers x without its coercion.
func (b *builder) exprIntoRaw(x syntax.Expr, dest Place) {
	ctx := b.ctx()
	if b.cur < 0 {
		return
	}
	switch x := x.(type) {
	case *syntax.LitExpr, *syntax.PathExpr:
		b.assign(dest, Use{X: b.rawOperand(x)})
	case *syntax.ParenExpr:
		b.exprInto(x.X, dest)
	case *syntax.TemplateExpr:
		b.template(x, dest)
	case *syntax.UnaryExpr:
		b.unary(x, dest)
	case *syntax.RefExpr:
		p := b.place(x.X, x.Mut)
		b.assign(dest, Ref{Mut: x.Mut, Place: p})
	case *syntax.BinaryExpr:
		b.binary(x, dest)
	case *syntax.AssignExpr:
		b.assignExpr(x)
		b.assign(dest, Use{X: unitConst(ctx)})
	case *syntax.CastExpr:
		b.cast(x, dest)
	case *syntax.RangeExpr:
		var ops []Operand
		if x.Lo != nil {
			ops = append(ops, b.operand(x.Lo))
		}
		if x.Hi != nil {
			ops = append(ops, b.operand(x.Hi))
		}
		b.assign(dest, Aggregate{Kind: AggAdt, Type: b.typeOf(x), Ops: ops})
	case *syntax.CallExpr:
		b.callExpr(x, dest)
	case *syntax.MethodCallExpr:
		b.methodCall(x, dest)
	case *syntax.FieldExpr, *syntax.IndexExpr:
		p := b.place(x, false)
		b.assign(dest, Use{X: Copy(p)})
	case *syntax.TryExpr:
		b.tryExpr(x, dest)
	case *syntax.StructLit:
		b.structLit(x, dest)
	case *syntax.TupleExpr:
		ops := make([]Operand, len(x.Elems))
		for i, e := range x.Elems {
			ops[i] = b.operand(e)
		}
		b.assign(dest, Aggregate{Kind: AggTuple, Type: b.typeOf(x), Ops: ops})
	case *syntax.ArrayExpr:
		ops := make([]Operand, len(x.Elems))
		for i, e := range x.Elems {
			ops[i] = b.operand(e)
		}
		b.assign(dest, Aggregate{Kind: AggArray, Type: b.typeOf(x), Ops: ops})
	case *syntax.ArrayRepeatExpr:
		t := b.typeOf(x)
		v := b.temp(t.Elem)
		b.exprInto(x.Value, v)
		ops := make([]Operand, t.Len)
		for i := range ops {
			ops[i] = Copy(v)
		}
		b.assign(dest, Aggregate{Kind: AggArray, Type: t, Ops: ops})
	case *syntax.BlockExpr:
		b.block(x, dest)
	case *syntax.IfExpr:
		b.ifExpr(x, dest)
	case *syntax.MatchExpr:
		b.match(x, dest)
	case *syntax.LoopExpr:
		b.loop(x, dest)
	case *syntax.WhileExpr:
		b.whileExpr(x)
		b.assign(dest, Use{X: unitConst(ctx)})
	case *syntax.ForExpr:
		b.forExpr(x)
		b.assign(dest, Use{X: unitConst(ctx)})
	case *syntax.BreakExpr:
		b.breakExpr(x)
	case *syntax.ContinueExpr:
		ls := b.loopFor(x.Label)
		if ls != nil {
			b.terminate(Goto{Target: ls.cont})
		}
	case *syntax.ReturnExpr:
		if x.Value != nil {
			b.exprInto(x.Value, LocalPlace(0))
		} else {
			b.assign(LocalPlace(0), Use{X: unitConst(ctx)})
		}
		b.terminate(Return{})
	case *syntax.ClosureExpr:
		b.closure(x, dest)
	case *syntax.SelectExpr:
		b.selectExpr(x, dest)
	default:
		b.assign(dest, Use{X: ConstOp(&Const{Kind: CZero, Type: b.typeOf(x)})})
	}
}

// ---------------------------------------------------------------------------
// Operators

func isPrim(t *types.Type) bool {
	switch t.Kind {
	case types.KInt, types.KFloat, types.KBool, types.KChar:
		return true
	}
	return false
}

func (b *builder) unary(x *syntax.UnaryExpr, dest Place) {
	t := b.typeOf(x.X)
	switch x.Op {
	case syntax.Star:
		p := b.place(x, false)
		b.assign(dest, Use{X: Copy(p)})
	case syntax.Minus, syntax.Not:
		op := Neg
		trait, method := "Neg", "neg"
		if x.Op == syntax.Not {
			op, trait, method = Not, "Not", "not"
		}
		v := b.operand(x.X)
		if isPrim(t) {
			b.assign(dest, Un{Op: op, X: v, Type: t, Pos: x.OpPos})
			return
		}
		b.call(b.traitFn(b.info.TraitItem(trait, method), t, nil), []Operand{v}, dest, x.OpPos)
	}
}

var binOps = map[syntax.Kind]BinOp{
	syntax.Plus: Add, syntax.Minus: Sub, syntax.Star: Mul, syntax.Slash: Div, syntax.Percent: Rem,
	syntax.And: BitAnd, syntax.Or: BitOr, syntax.Caret: BitXor, syntax.Shl: Shl, syntax.Shr: Shr,
	syntax.EqEq: Eq, syntax.Ne: Ne, syntax.Lt: Lt, syntax.Le: Le, syntax.Gt: Gt, syntax.Ge: Ge,
}

var opTraits = map[syntax.Kind][2]string{
	syntax.Plus: {"Add", "add"}, syntax.Minus: {"Sub", "sub"}, syntax.Star: {"Mul", "mul"},
	syntax.Slash: {"Div", "div"}, syntax.Percent: {"Rem", "rem"}, syntax.And: {"BitAnd", "bitand"},
	syntax.Or: {"BitOr", "bitor"}, syntax.Caret: {"BitXor", "bitxor"}, syntax.Shl: {"Shl", "shl"},
	syntax.Shr: {"Shr", "shr"},
}

func (b *builder) binary(x *syntax.BinaryExpr, dest Place) {
	ctx := b.ctx()
	switch x.Op {
	case syntax.AndAnd, syntax.OrOr:
		// Short-circuit: dest = x; if (x is false for &&) skip y.
		b.exprInto(x.X, dest)
		rhs, join := b.newBlock(), b.newBlock()
		if x.Op == syntax.AndAnd {
			b.terminate(Switch{X: Copy(dest), Type: ctx.Bool, Vals: []int64{0}, Targets: []int{join}, Else: rhs})
		} else {
			b.terminate(Switch{X: Copy(dest), Type: ctx.Bool, Vals: []int64{0}, Targets: []int{rhs}, Else: join})
		}
		b.cur = rhs
		b.exprInto(x.Y, dest)
		b.gotoBlock(join)
		return
	}
	lt, rt := b.typeOf(x.X), b.typeOf(x.Y)
	l := b.operand(x.X)
	r := b.operand(x.Y)
	switch x.Op {
	case syntax.EqEq, syntax.Ne, syntax.Lt, syntax.Le, syntax.Gt, syntax.Ge:
		if isPrim(lt) || lt.Kind == types.KPtr {
			b.assign(dest, Bin{Op: binOps[x.Op], X: l, Y: r, Type: lt})
			return
		}
		trait, method := "PartialEq", "eq"
		switch x.Op {
		case syntax.Ne:
			method = "ne"
		case syntax.Lt:
			trait, method = "PartialOrd", "lt"
		case syntax.Le:
			trait, method = "PartialOrd", "le"
		case syntax.Gt:
			trait, method = "PartialOrd", "gt"
		case syntax.Ge:
			trait, method = "PartialOrd", "ge"
		}
		lref, rref := b.refTo(l, lt), b.refTo(r, rt)
		b.call(b.traitFn(b.info.TraitItem(trait, method), lt, []*types.Type{rt}), []Operand{lref, rref}, dest, x.OpPos)
		return
	}
	if isPrim(lt) && isPrim(rt) {
		b.primBin(binOps[x.Op], l, r, lt, dest, x.OpPos)
		return
	}
	tm := opTraits[x.Op]
	b.call(b.traitFn(b.info.TraitItem(tm[0], tm[1]), lt, []*types.Type{rt}), []Operand{l, r}, dest, x.OpPos)
}

// primBin emits a primitive arithmetic operation with the checks Rust
// performs: division by zero always, overflow in debug builds (the
// interpreter and debug code generation check Bin ops on integers).
func (b *builder) primBin(op BinOp, l, r Operand, t *types.Type, dest Place, pos syntax.Pos) {
	ctx := b.ctx()
	if (op == Div || op == Rem) && t.Kind == types.KInt {
		nz := b.temp(ctx.Bool)
		b.assign(nz, Bin{Op: Ne, X: r, Y: ConstOp(&Const{Kind: CInt, Int: 0, Type: t}), Type: t})
		next := b.newBlock()
		b.terminate(Assert{Cond: Copy(nz), Msg: "attempt to divide by zero", Target: next, Pos: pos})
		b.cur = next
	}
	b.assign(dest, Bin{Op: op, X: l, Y: r, Type: t, Pos: pos})
}

// refTo stores an operand in a temporary and returns a reference to it.
func (b *builder) refTo(op Operand, t *types.Type) Operand {
	ctx := b.ctx()
	var p Place
	if op.Kind == OpCopy {
		p = op.Place
	} else {
		p = b.temp(t)
		b.assign(p, Use{X: op})
	}
	r := b.temp(ctx.Ref(false, t))
	b.assign(r, Ref{Place: p})
	return Copy(r)
}

func (b *builder) assignExpr(x *syntax.AssignExpr) {
	ctx := b.ctx()
	if x.Op == syntax.Eq {
		v := b.operand(x.Y)
		p := b.place(x.X, true)
		b.assign(p, Use{X: v})
		return
	}
	base := map[syntax.Kind]syntax.Kind{
		syntax.PlusEq: syntax.Plus, syntax.MinusEq: syntax.Minus, syntax.StarEq: syntax.Star,
		syntax.SlashEq: syntax.Slash, syntax.PercentEq: syntax.Percent, syntax.AndEq: syntax.And,
		syntax.OrEq: syntax.Or, syntax.CaretEq: syntax.Caret, syntax.ShlEq: syntax.Shl, syntax.ShrEq: syntax.Shr,
	}[x.Op]
	lt, rt := b.typeOf(x.X), b.typeOf(x.Y)
	r := b.operand(x.Y)
	p := b.place(x.X, true)
	if isPrim(lt) && isPrim(rt) {
		b.primBin(binOps[base], Copy(p), r, lt, p, x.OpPos)
		return
	}
	tm := opTraits[base]
	m := b.info.TraitItem(tm[0]+"Assign", tm[1]+"_assign")
	ref := b.temp(ctx.Ref(true, lt))
	b.assign(ref, Ref{Mut: true, Place: p})
	b.call(b.traitFn(m, lt, []*types.Type{rt}), []Operand{Copy(ref), r}, b.temp(ctx.Unit), x.OpPos)
}

func (b *builder) cast(x *syntax.CastExpr, dest Place) {
	from, to := b.typeOf(x.X), b.typeOf(x)
	v := b.operand(x.X)
	kind := CastNumeric
	switch {
	case (from.Kind == types.KRef || from.Kind == types.KPtr) && to.Kind == types.KPtr:
		kind = CastPtr
	case from.Kind == types.KPtr && to.Kind == types.KInt:
		kind = CastPtrInt
	case from.Kind == types.KInt && to.Kind == types.KPtr:
		kind = CastIntPtr
	case from.Kind == types.KAdt:
		kind = CastEnum
	case from.Kind == types.KFn:
		kind = CastFnPtr
	}
	b.assign(dest, Cast{Kind: kind, X: v, From: from, To: to})
}

// ---------------------------------------------------------------------------
// Calls

func (b *builder) callExpr(x *syntax.CallExpr, dest Place) {
	// Constructors build the value directly.
	if pe, ok := x.Fun.(*syntax.PathExpr); ok {
		if cl := b.body.Callees[pe]; cl != nil && cl.Def != nil && (cl.Def.Kind == resolve.DefStruct || cl.Def.Kind == resolve.DefVariant) {
			ops := make([]Operand, len(x.Args))
			for i, a := range x.Args {
				ops[i] = b.operand(a)
			}
			b.aggregateCtor(cl.Def, b.typeOf(x), ops, dest)
			return
		}
	}
	fn := b.operand(x.Fun)
	args := make([]Operand, len(x.Args))
	for i, a := range x.Args {
		args[i] = b.operand(a)
	}
	if b.typeOf(x).Kind == types.KNever {
		b.callDiverging(fn, args, x.Lparen)
		return
	}
	b.call(fn, args, dest, x.Lparen)
}

func (b *builder) methodCall(x *syntax.MethodCallExpr, dest Place) {
	ctx := b.ctx()
	cl := b.body.Callees[x]
	if cl == nil {
		b.assign(dest, Use{X: ConstOp(&Const{Kind: CZero, Type: b.typeOf(x)})})
		return
	}
	adj := cl.Recv
	mut := adj != nil && adj.Autoref == 2
	var recv Operand
	rt := b.typeOf(x.Recv)
	if adj == nil || adj.Derefs == 0 && adj.Autoref == 0 && !adj.Unsize {
		recv = b.operand(x.Recv)
	} else {
		p := b.placeOrTempMut(x.Recv, mut)
		t := rt
		derefs := adj.Derefs
		for i := 0; i < derefs; i++ {
			p, t = b.derefPlace(p, t, mut, x.Name.NamePos)
		}
		switch {
		case adj.Unsize:
			// The receiver is an array reached by autoderef: borrow it as a slice.
			arrRef := b.temp(ctx.Ref(mut, t))
			b.assign(arrRef, Ref{Mut: mut, Place: p})
			st := ctx.Ref(mut, ctx.Slice(t.Elem))
			if adj.Autoref == 0 {
				st = ctx.Ref(false, ctx.Slice(t.Elem))
			}
			sl := b.temp(st)
			b.assign(sl, Unsize{X: Copy(arrRef), Len: t.Len, To: st})
			recv = Copy(sl)
		case adj.Autoref > 0:
			r := b.temp(ctx.Ref(mut, t))
			b.assign(r, Ref{Mut: mut, Place: p})
			recv = Copy(r)
		default:
			recv = Copy(p)
		}
	}
	args := []Operand{recv}
	for _, a := range x.Args {
		args = append(args, b.operand(a))
	}
	fn := ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: cl.Def, Args: cl.Args, Self: cl.Self}})
	if b.typeOf(x).Kind == types.KNever {
		b.callDiverging(fn, args, x.Name.NamePos)
		return
	}
	b.call(fn, args, dest, x.Name.NamePos)
}

// ---------------------------------------------------------------------------
// Aggregates

func (b *builder) structLit(x *syntax.StructLit, dest Place) {
	t := b.typeOf(x)
	if t.Kind != types.KAdt {
		return
	}
	ai := b.info.Adts[t.Def]
	res := b.res.Paths[x.Path]
	variant := -1
	fields := ai.Fields
	if ai.IsEnum {
		name := ""
		if res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefVariant {
			name = res.Def.Name
		} else if len(res.Rest) == 1 {
			name = res.Rest[0].Name.Value
		}
		for i, v := range ai.Variants {
			if v.Def.Name == name {
				variant, fields = i, v.Fields
			}
		}
	}
	given := map[string]Operand{}
	for _, f := range x.Fields {
		given[f.Name.Value] = b.operand(f.Value)
	}
	var base Place
	if x.Base != nil {
		base = b.placeOrTemp(x.Base)
	}
	ops := make([]Operand, len(fields))
	for i, f := range fields {
		if op, ok := given[f.Name]; ok {
			ops[i] = op
		} else {
			ops[i] = Copy(base.Field(i))
		}
	}
	if variant >= 0 {
		b.assign(dest, Aggregate{Kind: AggAdt, Type: t, Variant: variant, Ops: ops})
		return
	}
	b.assign(dest, Aggregate{Kind: AggAdt, Type: t, Ops: ops})
}

// ---------------------------------------------------------------------------
// Control flow

func (b *builder) block(x *syntax.BlockExpr, dest Place) {
	ctx := b.ctx()
	for _, st := range x.Stmts {
		if b.cur < 0 {
			return
		}
		switch st := st.(type) {
		case *syntax.LetStmt:
			b.letStmt(st)
		case *syntax.ExprStmt:
			b.exprInto(st.X, b.temp(b.typeOf(st.X)))
		case *syntax.DeferStmt:
			b.deferStmt(st)
		}
	}
	if b.cur < 0 {
		return
	}
	if x.Tail != nil {
		b.exprInto(x.Tail, dest)
		return
	}
	b.assign(dest, Use{X: unitConst(ctx)})
}

func (b *builder) deferStmt(st *syntax.DeferStmt) {
	b.fn.HasDefer = true
	switch x := st.X.(type) {
	case *syntax.CallExpr:
		fn := b.operand(x.Fun)
		args := make([]Operand, len(x.Args))
		for i, a := range x.Args {
			args[i] = b.operand(a)
		}
		b.emit(PushDefer{Fn: fn, Args: args})
	case *syntax.MethodCallExpr:
		// Evaluate the receiver and arguments now; call later.
		cl := b.body.Callees[x]
		if cl == nil {
			return
		}
		recvOps := b.methodArgs(x)
		b.emit(PushDefer{Fn: ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: cl.Def, Args: cl.Args, Self: cl.Self}}), Args: recvOps})
	case *syntax.BlockExpr:
		// defer { ... }: run the block as a closure with no parameters.
		clo := b.deferClosures[x]
		fnTy := b.typeOf(clo)
		v := b.temp(fnTy)
		b.closure(clo, v)
		b.emit(PushDefer{Fn: Copy(v)})
	}
}

// methodArgs evaluates the receiver (with adjustments) and arguments of a
// method call.
func (b *builder) methodArgs(x *syntax.MethodCallExpr) []Operand {
	cl := b.body.Callees[x]
	ctx := b.ctx()
	adj := cl.Recv
	mut := adj != nil && adj.Autoref == 2
	var recv Operand
	if adj == nil || adj.Derefs == 0 && adj.Autoref == 0 {
		recv = b.operand(x.Recv)
	} else {
		p := b.placeOrTempMut(x.Recv, mut)
		t := b.typeOf(x.Recv)
		for i := 0; i < adj.Derefs; i++ {
			p, t = b.derefPlace(p, t, mut, x.Name.NamePos)
		}
		if adj.Autoref > 0 {
			r := b.temp(ctx.Ref(mut, t))
			b.assign(r, Ref{Mut: mut, Place: p})
			recv = Copy(r)
		} else {
			recv = Copy(p)
		}
	}
	args := []Operand{recv}
	for _, a := range x.Args {
		args = append(args, b.operand(a))
	}
	return args
}

func (b *builder) ifExpr(x *syntax.IfExpr, dest Place) {
	ctx := b.ctx()
	elseBlk, join := b.newBlock(), b.newBlock()
	if l, ok := x.Cond.(*syntax.LetExpr); ok {
		sp := b.placeOrTemp(l.X)
		b.testPat(l.Pat, sp, elseBlk)
	} else {
		c := b.operand(x.Cond)
		then := b.newBlock()
		b.terminate(Switch{X: c, Type: ctx.Bool, Vals: []int64{0}, Targets: []int{elseBlk}, Else: then})
		b.cur = then
	}
	b.exprInto(x.Then, dest)
	if b.cur >= 0 {
		b.terminate(Goto{Target: join})
	}
	b.cur = elseBlk
	if x.Else != nil {
		b.exprInto(x.Else, dest)
	} else {
		b.assign(dest, Use{X: unitConst(ctx)})
	}
	if b.cur >= 0 {
		b.terminate(Goto{Target: join})
	}
	b.cur = join
}

func (b *builder) loopFor(label *syntax.Name) *loopScope {
	if label != nil {
		node := b.res.Labels[label]
		for i := len(b.loops) - 1; i >= 0; i-- {
			if b.loops[i].node == node {
				return &b.loops[i]
			}
		}
		return nil
	}
	if len(b.loops) == 0 {
		return nil
	}
	return &b.loops[len(b.loops)-1]
}

func (b *builder) loop(x *syntax.LoopExpr, dest Place) {
	head, exit := b.newBlock(), b.newBlock()
	b.gotoBlock(head)
	b.loops = append(b.loops, loopScope{node: x, brk: exit, cont: head, dest: dest, hasValue: true})
	b.exprInto(x.Body, b.temp(b.ctx().Unit))
	b.loops = b.loops[:len(b.loops)-1]
	if b.cur >= 0 {
		b.terminate(Goto{Target: head})
	}
	b.cur = exit
}

func (b *builder) whileExpr(x *syntax.WhileExpr) {
	ctx := b.ctx()
	head, body, exit := b.newBlock(), b.newBlock(), b.newBlock()
	b.gotoBlock(head)
	if l, ok := x.Cond.(*syntax.LetExpr); ok {
		sp := b.placeOrTemp(l.X)
		b.testPat(l.Pat, sp, exit)
		b.gotoBlock(body)
	} else {
		c := b.operand(x.Cond)
		b.terminate(Switch{X: c, Type: ctx.Bool, Vals: []int64{0}, Targets: []int{exit}, Else: body})
		b.cur = body
	}
	b.loops = append(b.loops, loopScope{node: x, brk: exit, cont: head})
	b.exprInto(x.Body, b.temp(ctx.Unit))
	b.loops = b.loops[:len(b.loops)-1]
	if b.cur >= 0 {
		b.terminate(Goto{Target: head})
	}
	b.cur = exit
}

func (b *builder) forExpr(x *syntax.ForExpr) {
	ctx := b.ctx()
	iterTy := b.typeOf(x.Iter)
	if a := b.body.Adjust[x.Iter]; a != nil && a.Unsize && iterTy.Kind == types.KRef {
		iterTy = ctx.Ref(iterTy.Mut, ctx.Slice(iterTy.Elem.Elem))
	}
	intoIter := b.info.TraitItem("IntoIterator", "into_iter")
	next := b.info.TraitItem("Iterator", "next")
	itTy := b.info.Normalize(b.body.Owner, ctx.Proj(b.assocType("IntoIterator", "IntoIter"), &types.TraitRef{Trait: b.info.Lang["IntoIterator"], Self: iterTy}))
	itemTy := b.info.Normalize(b.body.Owner, ctx.Proj(b.assocType("IntoIterator", "Item"), &types.TraitRef{Trait: b.info.Lang["IntoIterator"], Self: iterTy}))
	optTy := ctx.Adt(b.info.Lang["Option"], []*types.Type{itemTy})
	iter := b.temp(itTy)
	b.call(b.traitFn(intoIter, iterTy, nil), []Operand{b.operand(x.Iter)}, iter, x.Iter.Pos())
	head, body, exit := b.newBlock(), b.newBlock(), b.newBlock()
	b.gotoBlock(head)
	ref := b.temp(ctx.Ref(true, itTy))
	b.assign(ref, Ref{Mut: true, Place: iter})
	opt := b.temp(optTy)
	b.call(b.traitFn(next, itTy, nil), []Operand{Copy(ref)}, opt, x.For)
	d := b.temp(ctx.Ints[types.Isize])
	b.assign(d, Discr{Place: opt})
	someIdx := variantIndex(b.info, b.someDef())
	b.terminate(Switch{X: Copy(d), Type: ctx.Ints[types.Isize], Vals: []int64{int64(someIdx)}, Targets: []int{body}, Else: exit})
	b.cur = body
	item := opt.With(Proj{Kind: PDowncast, Variant: someIdx}).Field(0)
	b.bindPattern(x.Pat, item)
	b.loops = append(b.loops, loopScope{node: x, brk: exit, cont: head})
	b.exprInto(x.Body, b.temp(ctx.Unit))
	b.loops = b.loops[:len(b.loops)-1]
	if b.cur >= 0 {
		b.terminate(Goto{Target: head})
	}
	b.cur = exit
}

func (b *builder) assocType(trait, name string) *resolve.Def {
	ti := b.info.Traits[b.info.Lang[trait]]
	return ti.Items[name]
}

func (b *builder) someDef() *resolve.Def {
	for _, v := range b.info.Adts[b.info.Lang["Option"]].Variants {
		if v.Def.Name == "Some" {
			return v.Def
		}
	}
	return nil
}

func (b *builder) breakExpr(x *syntax.BreakExpr) {
	ls := b.loopFor(x.Label)
	if ls == nil {
		return
	}
	if x.Value != nil && ls.hasValue {
		b.exprInto(x.Value, ls.dest)
	} else if ls.hasValue {
		b.assign(ls.dest, Use{X: unitConst(b.ctx())})
	}
	b.terminate(Goto{Target: ls.brk})
}

// tryExpr lowers x?: Ok(v)/Some(v) continue with v; Err(e) returns
// Err(From::from(e)); None returns None.
func (b *builder) tryExpr(x *syntax.TryExpr, dest Place) {
	ctx := b.ctx()
	t := b.typeOf(x.X)
	v := b.placeOrTemp(x.X)
	isOption := t.Def == b.info.Lang["Option"]
	ai := b.info.Adts[t.Def]
	okIdx, errIdx := 0, 1
	for i, vr := range ai.Variants {
		switch vr.Def.Name {
		case "Ok", "Some":
			okIdx = i
		case "Err", "None":
			errIdx = i
		}
	}
	d := b.temp(ctx.Ints[types.Isize])
	b.assign(d, Discr{Place: v})
	okBlk, errBlk := b.newBlock(), b.newBlock()
	b.terminate(Switch{X: Copy(d), Type: ctx.Ints[types.Isize], Vals: []int64{int64(okIdx)}, Targets: []int{okBlk}, Else: errBlk})
	b.cur = errBlk
	retTy := b.fn.Locals[0].Type
	if b.closureNode != nil {
		retTy = b.fn.Locals[0].Type
	}
	if isOption {
		b.assign(LocalPlace(0), Aggregate{Kind: AggAdt, Type: retTy, Variant: errIdx})
	} else {
		errVal := Copy(v.With(Proj{Kind: PDowncast, Variant: errIdx}).Field(0))
		fromTy, toTy := t.Args[1], retTy.Args[1]
		conv := errVal
		if fromTy != toTy {
			tmp := b.temp(toTy)
			from := b.info.TraitItem("From", "from")
			b.call(b.traitFn(from, toTy, []*types.Type{fromTy}), []Operand{errVal}, tmp, x.Question)
			conv = Copy(tmp)
		}
		retErr := 1
		for i, vr := range b.info.Adts[retTy.Def].Variants {
			if vr.Def.Name == "Err" {
				retErr = i
			}
		}
		b.assign(LocalPlace(0), Aggregate{Kind: AggAdt, Type: retTy, Variant: retErr, Ops: []Operand{conv}})
	}
	b.terminate(Return{})
	b.cur = okBlk
	b.assign(dest, Use{X: Copy(v.With(Proj{Kind: PDowncast, Variant: okIdx}).Field(0))})
}

// ---------------------------------------------------------------------------
// Literal helpers

func parseInt(s string) (int64, bool) {
	s = strings.ReplaceAll(s, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(s, "0x"):
		base, s = 16, s[2:]
	case strings.HasPrefix(s, "0o"):
		base, s = 8, s[2:]
	case strings.HasPrefix(s, "0b"):
		base, s = 2, s[2:]
	}
	for _, suf := range []string{"isize", "usize", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "f32", "f64"} {
		if strings.HasSuffix(s, suf) && !(base == 16 && strings.ContainsAny(suf, "abcdef")) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	u, err := strconv.ParseUint(s, base, 64)
	return int64(u), err == nil
}

// unquote decodes a string literal (plain or raw) to its bytes.
func unquote(lit string) string {
	if strings.HasPrefix(lit, "r") {
		s := lit[1:]
		s = strings.Trim(s, "#")
		return s[1 : len(s)-1]
	}
	return decodeEscapes(lit[1 : len(lit)-1])
}

func decodeEscapes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '0':
			b.WriteByte(0)
		case '\\', '\'', '"', '`', '$':
			b.WriteByte(s[i])
		case 'x':
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b.WriteByte(byte(v))
			i += 2
		case 'u':
			end := strings.IndexByte(s[i:], '}')
			v, _ := strconv.ParseUint(s[i+2:i+end], 16, 32)
			b.WriteRune(rune(v))
			i += end
		}
	}
	return b.String()
}

func charLit(lit string) (int64, bool) {
	s := lit[1 : len(lit)-1]
	if !strings.HasPrefix(s, `\`) {
		r := []rune(s)
		if len(r) == 0 {
			return 0, false
		}
		return int64(r[0]), true
	}
	d := []rune(decodeEscapes(s))
	if len(d) == 0 {
		return 0, false
	}
	return int64(d[0]), true
}
