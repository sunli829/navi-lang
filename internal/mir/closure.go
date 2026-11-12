package mir

import (
	"fmt"
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// closure lowers a closure expression into its own Func and stores the
// closure value (code + environment of captured cell pointers) in dest.
func (b *builder) closure(x *syntax.ClosureExpr, dest Place) {
	ctx := b.ctx()
	ft := b.typeOf(x)
	caps := b.closureCaps[x]
	var capTys []*types.Type
	for _, l := range caps {
		capTys = append(capTys, ctx.Ref(true, b.body.Locals[l]))
	}
	child := &builder{
		p:           b.p,
		info:        b.info,
		res:         b.res,
		body:        b.body,
		locals:      map[*resolve.Local]int{},
		boxed:       b.boxed,
		captures:    map[*resolve.Local]int{},
		closureNode: x,
		closureCaps: b.closureCaps,
	}
	f := &Func{Name: fmt.Sprintf("%s::{closure#%d}", b.fn.Name, len(b.p.closures)), Owner: b.fn.Owner, File: b.fn.File, Closure: true, Captures: capTys}
	b.p.closures = append(b.p.closures, f)
	child.fn = f
	child.newLocal(ft.Ret, "")
	child.envLocal = child.newLocal(ctx.Ptr(false, ctx.Tuple(capTys)), "env")
	for i, l := range caps {
		child.captures[l] = i
	}
	var params []int
	for i := range x.Params {
		params = append(params, child.newLocal(ft.Elems[i], fmt.Sprintf("arg%d", i)))
	}
	f.NArgs = 1 + len(params)
	child.cur = child.newBlock()
	for i, p := range x.Params {
		child.bindPattern(p.Pat, LocalPlace(params[i]))
	}
	child.exprInto(x.Body, LocalPlace(0))
	child.terminate(Return{})
	ops := make([]Operand, len(caps))
	for i, l := range caps {
		ops[i] = b.cellPointer(l)
	}
	b.assign(dest, Aggregate{Kind: AggClosure, Type: ft, Closure: f, Ops: ops})
}

// ---------------------------------------------------------------------------
// Template strings

// spec is a parsed format spec: [[fill]align][+][#][0][width][.precision][type].
type spec struct {
	fill      rune
	align     int64 // 0 none, 1 left, 2 right, 3 center
	signPlus  bool
	alternate bool
	zero      bool
	width     int64
	hasWidth  bool
	prec      int64
	hasPrec   bool
	trait     string
}

func parseSpec(s string) spec {
	sp := spec{fill: ' ', trait: "Display"}
	alignOf := func(c byte) int64 {
		switch c {
		case '<':
			return 1
		case '>':
			return 2
		case '^':
			return 3
		}
		return 0
	}
	r := []rune(s)
	if len(r) >= 2 && alignOf(byte(r[1])) != 0 && r[1] < 128 {
		sp.fill, sp.align = r[0], alignOf(byte(r[1]))
		s = string(r[2:])
	} else if len(s) >= 1 && alignOf(s[0]) != 0 {
		sp.align = alignOf(s[0])
		s = s[1:]
	}
	if strings.HasPrefix(s, "+") {
		sp.signPlus, s = true, s[1:]
	}
	if strings.HasPrefix(s, "#") {
		sp.alternate, s = true, s[1:]
	}
	if strings.HasPrefix(s, "0") {
		sp.zero, s = true, s[1:]
	}
	for len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		sp.width = sp.width*10 + int64(s[0]-'0')
		sp.hasWidth = true
		s = s[1:]
	}
	if strings.HasPrefix(s, ".") {
		s = s[1:]
		sp.hasPrec = true
		for len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
			sp.prec = sp.prec*10 + int64(s[0]-'0')
			s = s[1:]
		}
	}
	switch s {
	case "?":
		sp.trait = "Debug"
	case "x":
		sp.trait = "LowerHex"
	case "X":
		sp.trait = "UpperHex"
	case "o":
		sp.trait = "Octal"
	case "b":
		sp.trait = "Binary"
	case "e":
		sp.trait = "LowerExp"
	case "E":
		sp.trait = "UpperExp"
	}
	return sp
}

// template lowers a template string: each hole becomes a closure that
// formats the (already evaluated) value; the pieces form fmt::Arguments,
// which is formatted into a String unless Arguments is wanted.
func (b *builder) template(x *syntax.TemplateExpr, dest Place) {
	ctx := b.ctx()
	argsDef := b.info.Lang["Arguments"]
	argsTy := ctx.Adt(argsDef, nil)
	pieceTy := b.fieldElemType(argsTy, 0)
	specTy := b.p.stdType("fmt", "Spec")
	fmtrTy := ctx.Adt(b.info.Lang["Formatter"], nil)
	resTy := b.info.FnSig(b.info.TraitItem("Display", "fmt")).Ret
	var pieces []Operand
	for _, part := range x.Parts {
		if part.X == nil {
			lit := ConstOp(&Const{Kind: CStr, Str: decodeEscapes(part.Text), Type: ctx.Ref(false, ctx.Str)})
			pv := b.temp(pieceTy)
			b.assign(pv, Aggregate{Kind: AggAdt, Type: pieceTy, Variant: 0, Ops: []Operand{lit}})
			pieces = append(pieces, Copy(pv))
			continue
		}
		vt := b.typeOf(part.X)
		cell := b.newLocal(ctx.Ref(true, vt), "")
		b.fn.Locals[cell].Boxed = true
		b.emit(Box{Local: cell, Type: vt})
		b.exprInto(part.X, LocalPlace(cell).Deref())
		sp := parseSpec(part.Spec)
		fnTy := ctx.Fn([]*types.Type{ctx.Ref(true, fmtrTy)}, resTy, "")
		f := b.fmtClosure(vt, sp.trait, fnTy, resTy, fmtrTy)
		cv := b.temp(fnTy)
		b.assign(cv, Aggregate{Kind: AggClosure, Type: fnTy, Closure: f, Ops: []Operand{Copy(LocalPlace(cell))}})
		usize := ctx.Ints[types.Usize]
		bl := func(v bool) Operand {
			i := int64(0)
			if v {
				i = 1
			}
			return ConstOp(&Const{Kind: CBool, Int: i, Type: ctx.Bool})
		}
		specOps := []Operand{
			ConstOp(&Const{Kind: CChar, Int: int64(sp.fill), Type: ctx.Char}),
			ConstOp(&Const{Kind: CInt, Int: sp.align, Type: ctx.Ints[types.U8]}),
			ConstOp(&Const{Kind: CInt, Int: sp.width, Type: usize}), bl(sp.hasWidth),
			ConstOp(&Const{Kind: CInt, Int: sp.prec, Type: usize}), bl(sp.hasPrec),
			bl(sp.alternate), bl(sp.signPlus), bl(sp.zero),
		}
		sv := b.temp(specTy)
		b.assign(sv, Aggregate{Kind: AggAdt, Type: specTy, Ops: specOps})
		pv := b.temp(pieceTy)
		b.assign(pv, Aggregate{Kind: AggAdt, Type: pieceTy, Variant: 1, Ops: []Operand{Copy(cv), Copy(sv)}})
		pieces = append(pieces, Copy(pv))
	}
	arrTy := ctx.Array(pieceTy, int64(len(pieces)))
	arr := b.newLocal(ctx.Ref(true, arrTy), "")
	b.fn.Locals[arr].Boxed = true
	b.emit(Box{Local: arr, Type: arrTy})
	b.assign(LocalPlace(arr).Deref(), Aggregate{Kind: AggArray, Type: arrTy, Ops: pieces})
	sliceTy := ctx.Ref(false, ctx.Slice(pieceTy))
	sl := b.temp(sliceTy)
	b.assign(sl, Unsize{X: Copy(LocalPlace(arr)), Len: int64(len(pieces)), To: sliceTy})
	want := b.body.Templates[x]
	if want != nil && want.Def == argsDef {
		b.assign(dest, Aggregate{Kind: AggAdt, Type: argsTy, Ops: []Operand{Copy(sl)}})
		return
	}
	args := b.temp(argsTy)
	b.assign(args, Aggregate{Kind: AggAdt, Type: argsTy, Ops: []Operand{Copy(sl)}})
	b.call(b.fnConst(b.p.LangFn("format"), nil), []Operand{Copy(args)}, dest, x.Backtick)
}

// fmtClosure builds |f| <T as Trait>::fmt(&*cell, f) for a template hole.
func (b *builder) fmtClosure(vt *types.Type, trait string, fnTy, resTy, fmtrTy *types.Type) *Func {
	ctx := b.ctx()
	capTy := ctx.Ref(true, vt)
	f := &Func{Name: b.fn.Name + "::{fmt}", Owner: b.fn.Owner, File: b.fn.File, Closure: true, Captures: []*types.Type{capTy}}
	f.Locals = []*Local{{Type: resTy}, {Type: ctx.Ptr(false, ctx.Tuple([]*types.Type{capTy})), Name: "env"}, {Type: ctx.Ref(true, fmtrTy), Name: "f"}}
	f.NArgs = 2
	m := b.info.TraitItem(trait, "fmt")
	f.Locals = append(f.Locals, &Local{Type: ctx.Ref(false, vt)})
	f.Blocks = []*Block{
		{Stmts: []Stmt{Assign{Place: LocalPlace(3), Rv: Ref{Place: LocalPlace(1).Deref().Field(0).Deref()}}},
			Term: Call{Fn: ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: m, Self: vt}}), Args: []Operand{Copy(LocalPlace(3)), Copy(LocalPlace(2))}, Dest: LocalPlace(0), Target: 1}},
		{Term: Return{}},
	}
	b.p.closures = append(b.p.closures, f)
	return f
}

// fieldElemType returns the element type of the slice in field i of a
// struct type (Arguments.pieces: &[Piece] -> Piece).
func (b *builder) fieldElemType(t *types.Type, i int) *types.Type {
	ft := b.info.Adts[t.Def].Fields[i].Type
	for ft.Kind == types.KRef {
		ft = ft.Elem
	}
	if ft.Kind == types.KSlice {
		return ft.Elem
	}
	return ft
}

// stdType returns a non-generic std type by path.
func (p *Program) stdType(path ...string) *types.Type {
	for _, c := range p.Res.Crates {
		if !c.IsStd {
			continue
		}
		m := c.Root
		for i, seg := range path {
			bd := m.Lookup(seg, resolve.TypeNS)
			if bd == nil || bd.Res.Def == nil {
				return p.Info.Ctx.Err
			}
			if i == len(path)-1 {
				return p.Info.Ctx.Adt(bd.Res.Def, nil)
			}
			m = bd.Res.Def.Module
		}
	}
	return p.Info.Ctx.Err
}

// selectExpr lowers select { ... } into a call of the runtime's select
// with one case per arm; see std::sync::mpmc::select.
func (b *builder) selectExpr(x *syntax.SelectExpr, dest Place) {
	ctx := b.ctx()
	selDef := b.p.LangFn("select")
	caseTy := b.p.stdType("sync", "mpmc", "SelectCase")
	if selDef == nil || caseTy.Kind == types.KError {
		b.terminate(Unreachable{})
		return
	}
	type armInfo struct {
		slot   Place
		hasPat bool
	}
	var cases []Operand
	var infos []armInfo
	hasDefault := false
	for _, arm := range x.Arms {
		if arm.Default {
			hasDefault = true
			infos = append(infos, armInfo{})
			continue
		}
		mc := arm.Op.(*syntax.MethodCallExpr)
		cl := b.body.Callees[mc]
		resTy := b.typeOf(mc)
		args := b.methodArgs(mc)
		recvTy := b.typeOf(mc.Recv)
		_ = recvTy
		slot := b.newLocal(ctx.Ref(true, resTy), "")
		b.fn.Locals[slot].Boxed = true
		b.emit(Box{Local: slot, Type: resTy})
		chanPtr := b.temp(ctx.Ptr(false, ctx.Ints[types.U8]))
		b.assign(chanPtr, Cast{Kind: CastPtr, X: args[0], From: ctx.Ref(false, ctx.Err), To: ctx.Ptr(false, ctx.Ints[types.U8])})
		var c Place
		if mc.Name.Value == "recv" {
			slotPtr := b.temp(ctx.Ptr(true, ctx.Ints[types.U8]))
			b.assign(slotPtr, Cast{Kind: CastPtr, X: Copy(LocalPlace(slot)), From: ctx.Ref(true, resTy), To: ctx.Ptr(true, ctx.Ints[types.U8])})
			c = b.temp(caseTy)
			b.assign(c, Aggregate{Kind: AggAdt, Type: caseTy, Ops: []Operand{ConstOp(&Const{Kind: CBool, Int: 0, Type: ctx.Bool}), Copy(chanPtr), Copy(slotPtr), b.sizeConst(cl)}})
		} else {
			valTy := b.typeOf(mc.Args[0])
			val := b.newLocal(ctx.Ref(true, valTy), "")
			b.fn.Locals[val].Boxed = true
			b.emit(Box{Local: val, Type: valTy})
			b.assign(LocalPlace(val).Deref(), Use{X: args[1]})
			valPtr := b.temp(ctx.Ptr(true, ctx.Ints[types.U8]))
			b.assign(valPtr, Cast{Kind: CastPtr, X: Copy(LocalPlace(val)), From: ctx.Ref(true, valTy), To: ctx.Ptr(true, ctx.Ints[types.U8])})
			c = b.temp(caseTy)
			b.assign(c, Aggregate{Kind: AggAdt, Type: caseTy, Ops: []Operand{ConstOp(&Const{Kind: CBool, Int: 1, Type: ctx.Bool}), Copy(chanPtr), Copy(valPtr), b.sizeConst(cl)}})
		}
		cases = append(cases, Copy(c))
		infos = append(infos, armInfo{slot: LocalPlace(slot).Deref(), hasPat: arm.Pat != nil})
	}
	arrTy := ctx.Array(caseTy, int64(len(cases)))
	arr := b.temp(arrTy)
	b.assign(arr, Aggregate{Kind: AggArray, Type: arrTy, Ops: cases})
	ref := b.temp(ctx.Ref(false, arrTy))
	b.assign(ref, Ref{Place: arr})
	sliceTy := ctx.Ref(false, ctx.Slice(caseTy))
	sl := b.temp(sliceTy)
	b.assign(sl, Unsize{X: Copy(ref), Len: int64(len(cases)), To: sliceTy})
	idx := b.temp(ctx.Ints[types.Usize])
	b.call(b.fnConst(selDef, nil), []Operand{Copy(sl), ConstOp(&Const{Kind: CBool, Int: boolInt(hasDefault), Type: ctx.Bool})}, idx, x.Select)
	join := b.newBlock()
	var vals []int64
	var targets []int
	caseNo := 0
	elseBlk := b.newBlock()
	armBlocks := make([]int, len(x.Arms))
	for i, arm := range x.Arms {
		armBlocks[i] = b.newBlock()
		if arm.Default {
			elseBlk = armBlocks[i]
			continue
		}
		vals = append(vals, int64(caseNo))
		targets = append(targets, armBlocks[i])
		caseNo++
	}
	b.terminate(Switch{X: Copy(idx), Type: ctx.Ints[types.Usize], Vals: vals, Targets: targets, Else: elseBlk})
	if !hasDefault {
		b.cur = elseBlk
		b.terminate(Unreachable{})
	}
	for i, arm := range x.Arms {
		b.cur = armBlocks[i]
		if infos[i].hasPat {
			b.bindPattern(arm.Pat, infos[i].slot)
		}
		b.exprInto(arm.Body, dest)
		if b.cur >= 0 {
			b.terminate(Goto{Target: join})
		}
	}
	b.cur = join
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// sizeConst is a placeholder size for select cases; the runtime learns the
// element type from the channel itself.
func (b *builder) sizeConst(cl *types.Callee) Operand {
	return ConstOp(&Const{Kind: CInt, Int: 0, Type: b.ctx().Ints[types.Usize]})
}
