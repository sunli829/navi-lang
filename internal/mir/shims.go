package mir

import (
	"fmt"

	"navi/internal/layout"
	"navi/internal/resolve"
	"navi/internal/types"
)

// Shims are MIR bodies the compiler generates for builtin impls (lang
// traits on primitive and structural types) and #[derive] impls, for one
// concrete self type. The interpreter and the native backends both run
// them, so they share one implementation.

// ShimMethods lists, per trait, the methods a shim implements; the others
// use the trait's default bodies.
var ShimMethods = map[string]map[string]bool{
	"Clone":      {"clone": true},
	"Default":    {"default": true},
	"PartialEq":  {"eq": true},
	"PartialOrd": {"partial_cmp": true},
	"Ord":        {"cmp": true},
	"Hash":       {"hash": true},
	"Debug":      {"fmt": true}, "Display": {"fmt": true},
	"LowerHex": {"fmt": true}, "UpperHex": {"fmt": true}, "Octal": {"fmt": true},
	"Binary": {"fmt": true}, "LowerExp": {"fmt": true}, "UpperExp": {"fmt": true},
	"Deref": {"deref": true}, "DerefMut": {"deref_mut": true},
	"Index": {"index": true}, "IndexMut": {"index_mut": true},
	"Step": {"forward": true, "backward": true},
}

// UsesShim reports whether method of a builtin/derived impl of trait is
// implemented by a shim (rather than the trait's default body).
func UsesShim(trait, method string) bool {
	if ms, ok := ShimMethods[trait]; ok {
		return ms[method]
	}
	return true // operator traits: every method
}

// sb builds a shim body.
type sb struct {
	p   *Program
	f   *Func
	cur int
	ctx *types.Ctx
	lay *layout.Computer
}

func (s *sb) local(t *types.Type) Place {
	s.f.Locals = append(s.f.Locals, &Local{Type: t})
	return LocalPlace(len(s.f.Locals) - 1)
}

func (s *sb) block() int {
	s.f.Blocks = append(s.f.Blocks, &Block{})
	return len(s.f.Blocks) - 1
}

func (s *sb) emit(st Stmt) { s.f.Blocks[s.cur].Stmts = append(s.f.Blocks[s.cur].Stmts, st) }

func (s *sb) assign(p Place, rv Rvalue) { s.emit(Assign{Place: p, Rv: rv}) }

func (s *sb) term(t Term) { s.f.Blocks[s.cur].Term = t }

func (s *sb) ret() { s.term(Return{}) }

// call calls def (a function or trait method) and continues in a new block.
func (s *sb) call(def *resolve.Def, self *types.Type, targs []*types.Type, args []Operand, dest Place) {
	next := s.block()
	s.term(Call{Fn: ConstOp(&Const{Kind: CFn, Fn: &FnRef{Def: def, Self: self, Args: targs}}), Args: args, Dest: dest, Target: next})
	s.cur = next
}

func (s *sb) trait(name, method string) *resolve.Def { return s.p.Info.TraitItem(name, method) }

func (s *sb) ref(p Place, t *types.Type) Operand {
	r := s.local(s.ctx.Ref(false, t))
	s.assign(r, Ref{Place: p})
	return Copy(r)
}

func (s *sb) boolConst(v bool) Operand {
	return ConstOp(&Const{Kind: CBool, Int: boolInt(v), Type: s.ctx.Bool})
}

// branch jumps to t if c is true, else to f.
func (s *sb) branch(c Operand, t, f int) {
	s.term(Switch{X: c, Type: s.ctx.Bool, Vals: []int64{0}, Targets: []int{f}, Else: t})
}

// stdFn finds a std function by path (module segments then name).
func (p *Program) stdFn(path ...string) *resolve.Def {
	for _, c := range p.Res.Crates {
		if !c.IsStd {
			continue
		}
		m := c.Root
		for i, seg := range path {
			ns := resolve.TypeNS
			if i == len(path)-1 {
				ns = resolve.ValueNS
			}
			b := m.Lookup(seg, ns)
			if b == nil || b.Res.Def == nil {
				panic(fmt.Sprintf("mir: std has no %v", path))
			}
			if i == len(path)-1 {
				return b.Res.Def
			}
			m = b.Res.Def.Module
		}
	}
	return nil
}

// inherentFn finds an inherent method of a std ADT.
func (p *Program) inherentFn(adt *resolve.Def, name string) *resolve.Def {
	for _, ii := range p.Info.ImplsOf[nil] {
		if ii.SelfTy.Kind == types.KAdt && ii.SelfTy.Def == adt {
			if it := ii.Items[name]; it != nil {
				return it
			}
		}
	}
	panic("mir: no std method " + adt.Name + "::" + name)
}

// Shim returns the shim for a builtin or derived impl method.
func (p *Program) Shim(trait string, method *resolve.Def, self *types.Type, targs []*types.Type) *Func {
	key := fmt.Sprintf("%s.%s.%p.%s", trait, method.Name, self, argsKey(targs))
	if f := p.shims[key]; f != nil {
		return f
	}
	ctx := p.Info.Ctx
	sig := p.Info.FnSig(method)
	ti := p.Info.Traits[method.Parent]
	m := types.ParamMap(ti.Generics, targs)
	own := sig.Generics.Params
	for i, gp := range own {
		if len(ti.Generics.Params)+i < len(targs) {
			m[gp.Def] = targs[len(ti.Generics.Params)+i]
		}
	}
	conc := func(t *types.Type) *types.Type {
		return p.Info.Normalize(nil, types.Subst(ctx, t, m, self))
	}
	f := &Func{Name: fmt.Sprintf("<%s as %s>::%s", self, trait, method.Name), File: method.File, Shim: true}
	s := &sb{p: p, f: f, ctx: ctx, lay: layout.New(p.Info)}
	f.Locals = append(f.Locals, &Local{Type: conc(sig.Ret)})
	var params []*types.Type
	if sig.Self != nil {
		params = append(params, conc(sig.Self))
	}
	for _, pt := range sig.Params {
		params = append(params, conc(pt))
	}
	for _, pt := range params {
		f.Locals = append(f.Locals, &Local{Type: pt})
	}
	f.NArgs = len(params)
	s.cur = s.block()
	p.shims[key] = f
	s.gen(trait, method.Name, self, targs, params)
	boxAddressTaken(p.Info.Ctx, f)
	return f
}

func (s *sb) gen(trait, method string, self *types.Type, targs []*types.Type, params []*types.Type) {
	ctx := s.ctx
	ret := LocalPlace(0)
	a1, a2 := LocalPlace(1), LocalPlace(2)
	derived := self.Kind == types.KAdt && s.p.Info.Derives(self.Def)[trait]
	if derived {
		s.derived(trait, self, targs)
		return
	}
	switch trait {
	case "Clone":
		switch self.Kind {
		case types.KTuple, types.KArray:
			s.fieldwiseClone(self, a1.Deref(), ret)
		default:
			s.assign(ret, Use{X: Copy(a1.Deref())})
		}
		s.ret()
	case "Default":
		s.defaultVal(self, ret)
		s.ret()
	case "PartialEq":
		rhs := params[1].Elem
		s.eq(self, rhs, a1.Deref(), a2.Deref(), ret)
		s.ret()
	case "PartialOrd", "Ord":
		s.cmp(trait, self, a1.Deref(), a2.Deref(), ret)
		s.ret()
	case "Hash":
		s.hash(self, a1.Deref(), Copy(a2), params[1].Elem)
		s.assign(ret, Use{X: unitConst(ctx)})
		s.ret()
	case "Debug", "Display", "LowerHex", "UpperHex", "Octal", "Binary", "LowerExp", "UpperExp":
		s.format(trait, self, a1.Deref(), Copy(a2), ret)
		s.ret()
	case "Add", "Sub", "Mul", "Div", "Rem", "BitAnd", "BitOr", "BitXor", "Shl", "Shr":
		x, y, t := Copy(a1), Copy(a2), self
		if t.Kind == types.KRef {
			t = t.Elem
			x = Copy(a1.Deref())
		}
		if params[1].Kind == types.KRef {
			y = Copy(a2.Deref())
		}
		if (trait == "Div" || trait == "Rem") && t.Kind == types.KInt {
			nz := s.local(ctx.Bool)
			s.assign(nz, Bin{Op: Ne, X: y, Y: ConstOp(&Const{Kind: CInt, Int: 0, Type: params[1]}), Type: params[1]})
			next := s.block()
			s.term(Assert{Cond: Copy(nz), Msg: "attempt to divide by zero", Target: next})
			s.cur = next
		}
		s.assign(ret, Bin{Op: opOf(trait), X: x, Y: y, Type: t})
		s.ret()
	case "AddAssign", "SubAssign", "MulAssign", "DivAssign", "RemAssign", "BitAndAssign", "BitOrAssign", "BitXorAssign", "ShlAssign", "ShrAssign":
		base := trait[:len(trait)-len("Assign")]
		s.assign(a1.Deref(), Bin{Op: opOf(base), X: Copy(a1.Deref()), Y: Copy(a2), Type: self})
		s.assign(ret, Use{X: unitConst(ctx)})
		s.ret()
	case "Neg", "Not":
		op := Neg
		if trait == "Not" {
			op = Not
		}
		s.assign(ret, Un{Op: op, X: Copy(a1), Type: self})
		s.ret()
	case "Deref", "DerefMut":
		s.assign(ret, Use{X: Copy(a1.Deref())})
		s.ret()
	case "Index", "IndexMut":
		s.index(self, params, ret, trait == "IndexMut")
		s.ret()
	case "Step":
		n := s.local(self)
		s.assign(n, Cast{Kind: CastNumeric, X: Copy(a2), From: ctx.Ints[types.Usize], To: self})
		op := Add
		if method == "backward" {
			op = Sub
		}
		s.assign(ret, Bin{Op: op, X: Copy(a1), Y: Copy(n), Type: self})
		s.ret()
	default:
		panic("mir: no shim for builtin " + trait)
	}
}

func opOf(trait string) BinOp {
	return map[string]BinOp{"Add": Add, "Sub": Sub, "Mul": Mul, "Div": Div, "Rem": Rem,
		"BitAnd": BitAnd, "BitOr": BitOr, "BitXor": BitXor, "Shl": Shl, "Shr": Shr}[trait]
}

func isScalarType(t *types.Type) bool {
	switch t.Kind {
	case types.KInt, types.KFloat, types.KBool, types.KChar, types.KPtr, types.KFn, types.KNever:
		return true
	}
	return false
}

// elems returns the element types of a tuple or array.
func elems(t *types.Type) []*types.Type {
	if t.Kind == types.KTuple {
		return t.Elems
	}
	out := make([]*types.Type, t.Len)
	for i := range out {
		out[i] = t.Elem
	}
	return out
}

// elemPlace returns the place of element i of a tuple or array place.
func (s *sb) elemPlace(t *types.Type, p Place, i int) Place {
	if t.Kind == types.KTuple {
		return p.Field(i)
	}
	idx := s.local(s.ctx.Ints[types.Usize])
	s.assign(idx, Use{X: ConstOp(&Const{Kind: CInt, Int: int64(i), Type: s.ctx.Ints[types.Usize]})})
	return p.With(Proj{Kind: PIndex, Index: idx.Local})
}

// cloneInto clones the value at p (of type t) into dest.
func (s *sb) cloneInto(t *types.Type, p, dest Place) {
	switch {
	case isScalarType(t), t.Kind == types.KRef && !t.Mut:
		s.assign(dest, Use{X: Copy(p)})
	case t.Kind == types.KTuple || t.Kind == types.KArray:
		s.fieldwiseClone(t, p, dest)
	default:
		s.call(s.trait("Clone", "clone"), t, nil, []Operand{s.ref(p, t)}, dest)
	}
}

func (s *sb) fieldwiseClone(t *types.Type, p, dest Place) {
	es := elems(t)
	ops := make([]Operand, len(es))
	for i, et := range es {
		tmp := s.local(et)
		s.cloneInto(et, s.elemPlace(t, p, i), tmp)
		ops[i] = Copy(tmp)
	}
	kind := AggTuple
	if t.Kind == types.KArray {
		kind = AggArray
	}
	s.assign(dest, Aggregate{Kind: kind, Type: t, Ops: ops})
}

func (s *sb) defaultVal(t *types.Type, dest Place) {
	ctx := s.ctx
	switch {
	case t.Kind == types.KRef:
		s.assign(dest, Use{X: ConstOp(&Const{Kind: CStr, Str: "", Type: t})})
	case isScalarType(t):
		s.assign(dest, Use{X: ConstOp(&Const{Kind: CZero, Type: t})})
	case t.Kind == types.KTuple || t.Kind == types.KArray:
		es := elems(t)
		ops := make([]Operand, len(es))
		for i, et := range es {
			tmp := s.local(et)
			s.defaultVal(et, tmp)
			ops[i] = Copy(tmp)
		}
		kind := AggTuple
		if t.Kind == types.KArray {
			kind = AggArray
		}
		s.assign(dest, Aggregate{Kind: kind, Type: t, Ops: ops})
	default:
		s.call(s.trait("Default", "default"), t, nil, nil, dest)
	}
	_ = ctx
}

// eq stores a == b into dest.
func (s *sb) eq(t, rhs *types.Type, a, b Place, dest Place) {
	ctx := s.ctx
	switch {
	case isScalarType(t):
		s.assign(dest, Bin{Op: Eq, X: Copy(a), Y: Copy(b), Type: t})
	case t.Kind == types.KStr:
		s.call(s.p.stdFn("str", "str_eq"), nil, nil, []Operand{s.ref(a, t), s.ref(b, t)}, dest)
	case t.Kind == types.KRef:
		s.eq(t.Elem, rhs.Elem, a.Deref(), b.Deref(), dest)
	case t.Kind == types.KTuple:
		end, fail := s.block(), s.block()
		for i, et := range t.Elems {
			r := s.local(ctx.Bool)
			s.eq(et, rhs.Elems[i], a.Field(i), b.Field(i), r)
			next := s.block()
			s.branch(Copy(r), next, fail)
			s.cur = next
		}
		s.assign(dest, Use{X: s.boolConst(true)})
		s.term(Goto{Target: end})
		s.cur = fail
		s.assign(dest, Use{X: s.boolConst(false)})
		s.term(Goto{Target: end})
		s.cur = end
	case t.Kind == types.KArray || t.Kind == types.KSlice:
		sa, sbv := s.asSlice(t, a), s.asSlice(rhs, b)
		s.call(s.p.stdFn("slice", "eq_slices"), nil, []*types.Type{t.Elem, rhs.Elem}, []Operand{sa, sbv}, dest)
	default:
		s.call(s.trait("PartialEq", "eq"), t, []*types.Type{rhs}, []Operand{s.ref(a, t), s.ref(b, rhs)}, dest)
	}
}

// asSlice returns a &[T] for an array or slice place.
func (s *sb) asSlice(t *types.Type, p Place) Operand {
	ctx := s.ctx
	if t.Kind == types.KSlice {
		return s.ref(p, t)
	}
	r := s.ref(p, t)
	st := ctx.Ref(false, ctx.Slice(t.Elem))
	out := s.local(st)
	s.assign(out, Unsize{X: r, Len: t.Len, To: st})
	return Copy(out)
}

func (s *sb) ordering() *types.Type { return s.ctx.Adt(s.p.Info.Lang["Ordering"], nil) }

func (s *sb) variantNamed(t *types.Type, name string) int {
	for i, v := range s.p.Info.Adts[t.Def].Variants {
		if v.Def.Name == name {
			return i
		}
	}
	return -1
}

// orderingConst stores Less/Equal/Greater in dest (wrapped in Some for
// partial_cmp).
func (s *sb) storeOrdering(name string, partial bool, dest Place) {
	ord := s.ordering()
	if !partial {
		s.assign(dest, Aggregate{Kind: AggAdt, Type: ord, Variant: s.variantNamed(ord, name)})
		return
	}
	tmp := s.local(ord)
	s.assign(tmp, Aggregate{Kind: AggAdt, Type: ord, Variant: s.variantNamed(ord, name)})
	opt := s.ctx.Adt(s.p.Info.Lang["Option"], []*types.Type{ord})
	s.assign(dest, Aggregate{Kind: AggAdt, Type: opt, Variant: s.variantNamed(opt, "Some"), Ops: []Operand{Copy(tmp)}})
}

// cmp stores the comparison of a and b in dest (Ordering for Ord,
// Option<Ordering> for PartialOrd).
func (s *sb) cmp(trait string, t *types.Type, a, b Place, dest Place) {
	ctx := s.ctx
	partial := trait == "PartialOrd"
	switch {
	case isScalarType(t):
		lt, gt := s.local(ctx.Bool), s.local(ctx.Bool)
		s.assign(lt, Bin{Op: Lt, X: Copy(a), Y: Copy(b), Type: t})
		s.assign(gt, Bin{Op: Gt, X: Copy(a), Y: Copy(b), Type: t})
		less, notLess, greater, notGreater, end := s.block(), s.block(), s.block(), s.block(), s.block()
		s.branch(Copy(lt), less, notLess)
		s.cur = less
		s.storeOrdering("Less", partial, dest)
		s.term(Goto{Target: end})
		s.cur = notLess
		s.branch(Copy(gt), greater, notGreater)
		s.cur = greater
		s.storeOrdering("Greater", partial, dest)
		s.term(Goto{Target: end})
		s.cur = notGreater
		if t.Kind == types.KFloat && partial {
			eq := s.local(ctx.Bool)
			s.assign(eq, Bin{Op: Eq, X: Copy(a), Y: Copy(b), Type: t})
			equal, nan := s.block(), s.block()
			s.branch(Copy(eq), equal, nan)
			s.cur = nan
			opt := ctx.Adt(s.p.Info.Lang["Option"], []*types.Type{s.ordering()})
			s.assign(dest, Aggregate{Kind: AggAdt, Type: opt, Variant: s.variantNamed(opt, "None")})
			s.term(Goto{Target: end})
			s.cur = equal
		}
		s.storeOrdering("Equal", partial, dest)
		s.term(Goto{Target: end})
		s.cur = end
	case t.Kind == types.KStr:
		if partial {
			o := s.local(s.ordering())
			s.call(s.p.stdFn("str", "str_cmp"), nil, nil, []Operand{s.ref(a, t), s.ref(b, t)}, o)
			opt := ctx.Adt(s.p.Info.Lang["Option"], []*types.Type{s.ordering()})
			s.assign(dest, Aggregate{Kind: AggAdt, Type: opt, Variant: s.variantNamed(opt, "Some"), Ops: []Operand{Copy(o)}})
		} else {
			s.call(s.p.stdFn("str", "str_cmp"), nil, nil, []Operand{s.ref(a, t), s.ref(b, t)}, dest)
		}
	case t.Kind == types.KRef:
		s.cmp(trait, t.Elem, a.Deref(), b.Deref(), dest)
	case t.Kind == types.KArray || t.Kind == types.KSlice:
		fn := "cmp_slices"
		if partial {
			fn = "partial_cmp_slices"
		}
		s.call(s.p.stdFn("slice", fn), nil, []*types.Type{t.Elem}, []Operand{s.asSlice(t, a), s.asSlice(t, b)}, dest)
	case t.Kind == types.KTuple:
		s.lexicographic(trait, t.Elems, func(i int) (Place, Place) { return a.Field(i), b.Field(i) }, dest)
	default:
		method := "cmp"
		var targs []*types.Type
		if partial {
			method, targs = "partial_cmp", []*types.Type{t}
		}
		s.call(s.trait(trait, method), t, targs, []Operand{s.ref(a, t), s.ref(b, t)}, dest)
	}
}

// lexicographic compares fields in order, stopping at the first that is
// not Equal.
func (s *sb) lexicographic(trait string, fts []*types.Type, field func(int) (Place, Place), dest Place) {
	ctx := s.ctx
	partial := trait == "PartialOrd"
	end := s.block()
	ord := s.ordering()
	for i, ft := range fts {
		fa, fb := field(i)
		r := s.local(s.cmpType(partial))
		s.cmp(trait, ft, fa, fb, r)
		// Equal (or Some(Equal)) continues with the next field.
		var ordPlace Place
		if partial {
			opt := ctx.Adt(s.p.Info.Lang["Option"], []*types.Type{ord})
			d := s.local(ctx.Ints[types.Isize])
			s.assign(d, Discr{Place: r})
			isSome, done := s.block(), s.block()
			s.term(Switch{X: Copy(d), Type: ctx.Ints[types.Isize], Vals: []int64{int64(s.variantNamed(opt, "Some"))}, Targets: []int{isSome}, Else: done})
			s.cur = done
			s.assign(dest, Use{X: Copy(r)})
			s.term(Goto{Target: end})
			s.cur = isSome
			ordPlace = r.With(Proj{Kind: PDowncast, Variant: s.variantNamed(opt, "Some")}).Field(0)
		} else {
			ordPlace = r
		}
		d := s.local(ctx.Ints[types.Isize])
		s.assign(d, Discr{Place: ordPlace})
		next, done := s.block(), s.block()
		s.term(Switch{X: Copy(d), Type: ctx.Ints[types.Isize], Vals: []int64{int64(s.variantNamed(ord, "Equal"))}, Targets: []int{next}, Else: done})
		s.cur = done
		s.assign(dest, Use{X: Copy(r)})
		s.term(Goto{Target: end})
		s.cur = next
	}
	s.storeOrdering("Equal", partial, dest)
	s.term(Goto{Target: end})
	s.cur = end
}

// cmpType is Option<Ordering> for partial comparisons, else Ordering.
func (s *sb) cmpType(partial bool) *types.Type {
	if partial {
		return s.ctx.Adt(s.p.Info.Lang["Option"], []*types.Type{s.ordering()})
	}
	return s.ordering()
}

func (s *sb) hash(t *types.Type, p Place, state Operand, hasher *types.Type) {
	ctx := s.ctx
	unit := s.local(ctx.Unit)
	switch {
	case t.Kind == types.KInt || t.Kind == types.KBool || t.Kind == types.KChar || t.Kind == types.KPtr:
		size := s.lay.Of(t).Size
		ptr := s.local(ctx.Ptr(false, ctx.Ints[types.U8]))
		s.assign(ptr, Cast{Kind: CastPtr, X: s.ref(p, t), From: ctx.Ref(false, t), To: ctx.Ptr(false, ctx.Ints[types.U8])})
		sl := s.local(ctx.Ref(false, ctx.Slice(ctx.Ints[types.U8])))
		s.call(s.p.stdFn("rt", "slice_from_raw"), nil, []*types.Type{ctx.Ints[types.U8]}, []Operand{Copy(ptr), ConstOp(&Const{Kind: CInt, Int: size, Type: ctx.Ints[types.Usize]})}, sl)
		s.call(s.hasherWrite(), hasher, nil, []Operand{state, Copy(sl)}, unit)
	case t.Kind == types.KStr:
		s.call(s.p.stdFn("str", "str_hash"), nil, []*types.Type{hasher}, []Operand{s.ref(p, t), state}, unit)
	case t.Kind == types.KRef:
		s.hash(t.Elem, p.Deref(), state, hasher)
	case t.Kind == types.KTuple:
		for i, et := range t.Elems {
			s.hash(et, p.Field(i), state, hasher)
		}
	case t.Kind == types.KArray || t.Kind == types.KSlice:
		s.call(s.p.stdFn("slice", "hash_slice"), nil, []*types.Type{t.Elem, hasher}, []Operand{s.asSlice(t, p), state}, unit)
	default:
		s.call(s.trait("Hash", "hash"), t, []*types.Type{hasher}, []Operand{s.ref(p, t), state}, unit)
	}
}

func (s *sb) hasherWrite() *resolve.Def {
	for d, ti := range s.p.Info.Traits {
		if d.Crate.IsStd && d.Name == "Hasher" {
			return ti.Items["write"]
		}
	}
	panic("mir: std has no Hasher")
}

// format formats the value at p with trait (Display, Debug, ...).
func (s *sb) format(trait string, t *types.Type, p Place, f Operand, dest Place) {
	ctx := s.ctx
	rt := func(name string, args ...Operand) {
		s.call(s.p.stdFn("fmt", "rt", name), nil, nil, append(args, f), dest)
	}
	intConst := func(v int64, ty *types.Type) Operand { return ConstOp(&Const{Kind: CInt, Int: v, Type: ty}) }
	cast := func(to *types.Type) Operand {
		tmp := s.local(to)
		s.assign(tmp, Cast{Kind: CastNumeric, X: Copy(p), From: t, To: to})
		return Copy(tmp)
	}
	u64, i64 := ctx.Ints[types.U64], ctx.Ints[types.I64]
	switch t.Kind {
	case types.KInt:
		// Hex and friends show the bits of the value's own width.
		bits := cast(u64)
		if t.Int.Signed() {
			size := s.lay.Of(t).Size
			if size < 8 {
				masked := s.local(u64)
				s.assign(masked, Bin{Op: BitAnd, X: bits, Y: intConst(int64(1)<<(size*8)-1, u64), Type: u64})
				bits = Copy(masked)
			}
		}
		strConst := func(v string) Operand {
			return ConstOp(&Const{Kind: CStr, Str: v, Type: ctx.Ref(false, ctx.Str)})
		}
		switch trait {
		case "LowerHex":
			rt("fmt_radix", bits, intConst(16, u64), s.boolConst(false), strConst("0x"))
		case "UpperHex":
			rt("fmt_radix", bits, intConst(16, u64), s.boolConst(true), strConst("0x"))
		case "Octal":
			rt("fmt_radix", bits, intConst(8, u64), s.boolConst(false), strConst("0o"))
		case "Binary":
			rt("fmt_radix", bits, intConst(2, u64), s.boolConst(false), strConst("0b"))
		default:
			if t.Int.Signed() {
				rt("fmt_i64", cast(i64))
			} else {
				rt("fmt_u64", cast(u64))
			}
		}
	case types.KFloat:
		style := int64(0)
		switch trait {
		case "Debug":
			style = 1
		case "LowerExp":
			style = 2
		case "UpperExp":
			style = 3
		}
		if !t.F64 {
			style += 16
		}
		rt("fmt_float", cast(ctx.F64), intConst(style, ctx.Ints[types.U8]))
	case types.KBool:
		rt("fmt_bool", Copy(p))
	case types.KChar:
		if trait == "Debug" {
			rt("debug_char", Copy(p))
		} else {
			rt("fmt_char", Copy(p))
		}
	case types.KStr:
		if trait == "Debug" {
			rt("debug_str", s.ref(p, t))
		} else {
			s.call(s.p.inherentFn(s.p.Info.Lang["Formatter"], "pad"), nil, nil, []Operand{f, s.ref(p, t)}, dest)
		}
	case types.KRef:
		s.call(s.trait(trait, "fmt"), t.Elem, nil, []Operand{Copy(p), f}, dest)
	case types.KTuple:
		if len(t.Elems) == 0 {
			rt("debug_unit")
			return
		}
		s.debugTuple("", t.Elems, func(i int) Place { return p.Field(i) }, f, dest)
	case types.KArray, types.KSlice:
		s.call(s.p.stdFn("fmt", "rt", "debug_slice"), nil, []*types.Type{t.Elem}, []Operand{s.asSlice(t, p), f}, dest)
	default:
		s.call(s.trait(trait, "fmt"), t, nil, []Operand{s.ref(p, t), f}, dest)
	}
}

// debugTuple writes name(f0, f1, ...) with Formatter::debug_tuple.
func (s *sb) debugTuple(name string, fts []*types.Type, field func(int) Place, f Operand, dest Place) {
	ctx := s.ctx
	fmtr := s.p.Info.Lang["Formatter"]
	bt := s.p.stdType("fmt", "DebugTuple")
	b := s.local(bt)
	s.call(s.p.inherentFn(fmtr, "debug_tuple"), nil, nil, []Operand{f, ConstOp(&Const{Kind: CStr, Str: name, Type: ctx.Ref(false, ctx.Str)})}, b)
	bref := s.local(ctx.Ref(true, bt))
	s.assign(bref, Ref{Mut: true, Place: b})
	for i, ft := range fts {
		r := s.local(ctx.Ref(true, bt))
		s.call(s.p.inherentFn(bt.Def, "field"), nil, nil, []Operand{Copy(bref), s.dynDebug(field(i), ft)}, r)
	}
	s.call(s.p.inherentFn(bt.Def, "finish"), nil, nil, []Operand{Copy(bref)}, dest)
}

// dynDebug returns &field as &dyn Debug.
func (s *sb) dynDebug(p Place, t *types.Type) Operand {
	ctx := s.ctx
	dyn := ctx.Ref(false, ctx.Dyn([]*types.TraitRef{{Trait: s.p.Info.Lang["Debug"]}}))
	out := s.local(dyn)
	s.assign(out, MakeDyn{X: s.ref(p, t), Concrete: t, To: dyn})
	return Copy(out)
}

func (s *sb) index(self *types.Type, params []*types.Type, ret Place, mut bool) {
	ctx := s.ctx
	idxTy := params[1]
	recv := LocalPlace(1)
	var sl Operand
	switch self.Kind {
	case types.KArray:
		sl = s.asSlice(self, recv.Deref())
	default:
		sl = Copy(recv)
	}
	usize := ctx.Ints[types.Usize]
	if idxTy.Kind == types.KInt {
		s.call(s.p.stdFn("slice", "index_ref"), nil, []*types.Type{self.Elem}, []Operand{sl, Copy(LocalPlace(2))}, ret)
		return
	}
	start := ConstOp(&Const{Kind: CInt, Int: 0, Type: usize})
	var end Operand
	if self.Kind == types.KArray {
		end = ConstOp(&Const{Kind: CInt, Int: self.Len, Type: usize})
	} else {
		n := s.local(usize)
		s.assign(n, Len{Place: recv.Deref()})
		end = Copy(n)
	}
	ai := s.p.Info.Adts[idxTy.Def]
	inclusive := s.p.Info.LangName(idxTy.Def) == "RangeInclusive" || s.p.Info.LangName(idxTy.Def) == "RangeToInclusive"
	for i, f := range ai.Fields {
		switch f.Name {
		case "start":
			start = Copy(LocalPlace(2).Field(i))
		case "end":
			end = Copy(LocalPlace(2).Field(i))
			if inclusive {
				e := s.local(usize)
				s.assign(e, Bin{Op: Add, X: end, Y: ConstOp(&Const{Kind: CInt, Int: 1, Type: usize}), Type: usize})
				end = Copy(e)
			}
		}
	}
	switch {
	case self.Kind == types.KStr:
		s.call(s.p.stdFn("str", "str_index_range"), nil, nil, []Operand{sl, start, end}, ret)
	case mut:
		s.call(s.p.stdFn("slice", "index_range_mut"), nil, []*types.Type{self.Elem}, []Operand{sl, start, end}, ret)
	default:
		s.call(s.p.stdFn("slice", "index_range"), nil, []*types.Type{self.Elem}, []Operand{sl, start, end}, ret)
	}
}

// ---------------------------------------------------------------------------
// Derived impls

func (s *sb) derived(trait string, self *types.Type, targs []*types.Type) {
	ctx := s.ctx
	ai := s.p.Info.Adts[self.Def]
	ret := LocalPlace(0)
	a1, a2 := LocalPlace(1), LocalPlace(2)
	// forEachVariant runs body for every variant of the value at p (once
	// with variant -1 for structs) and joins.
	forEachVariant := func(p Place, body func(variant int, fp Place, fts []*types.Type)) {
		if !ai.IsEnum {
			body(-1, p, s.lay.FieldTypes(self, -1))
			return
		}
		end := s.block()
		d := s.local(ctx.Ints[types.Isize])
		s.assign(d, Discr{Place: p})
		var vals []int64
		var targets []int
		for i := range ai.Variants {
			vals = append(vals, int64(i))
			targets = append(targets, s.block())
		}
		unreach := s.block()
		s.term(Switch{X: Copy(d), Type: ctx.Ints[types.Isize], Vals: vals, Targets: targets, Else: unreach})
		s.cur = unreach
		s.term(Unreachable{})
		for i := range ai.Variants {
			s.cur = targets[i]
			body(i, p.With(Proj{Kind: PDowncast, Variant: i}), s.lay.FieldTypes(self, i))
			if s.f.Blocks[s.cur].Term == nil {
				s.term(Goto{Target: end})
			}
		}
		s.cur = end
	}
	switch trait {
	case "Clone":
		forEachVariant(a1.Deref(), func(v int, fp Place, fts []*types.Type) {
			ops := make([]Operand, len(fts))
			for i, ft := range fts {
				tmp := s.local(ft)
				s.cloneInto(ft, fp.Field(i), tmp)
				ops[i] = Copy(tmp)
			}
			s.assign(ret, Aggregate{Kind: AggAdt, Type: self, Variant: max(v, 0), Ops: ops})
		})
		s.ret()
	case "Default":
		fts := s.lay.FieldTypes(self, -1)
		ops := make([]Operand, len(fts))
		for i, ft := range fts {
			tmp := s.local(ft)
			s.defaultVal(ft, tmp)
			ops[i] = Copy(tmp)
		}
		s.assign(ret, Aggregate{Kind: AggAdt, Type: self, Ops: ops})
		s.ret()
	case "PartialEq":
		end, fail := s.block(), s.block()
		if ai.IsEnum {
			d1, d2 := s.local(ctx.Ints[types.Isize]), s.local(ctx.Ints[types.Isize])
			s.assign(d1, Discr{Place: a1.Deref()})
			s.assign(d2, Discr{Place: a2.Deref()})
			same := s.local(ctx.Bool)
			s.assign(same, Bin{Op: Eq, X: Copy(d1), Y: Copy(d2), Type: ctx.Ints[types.Isize]})
			next := s.block()
			s.branch(Copy(same), next, fail)
			s.cur = next
		}
		forEachVariant(a1.Deref(), func(v int, fp Place, fts []*types.Type) {
			other := a2.Deref()
			if v >= 0 {
				other = other.With(Proj{Kind: PDowncast, Variant: v})
			}
			for i, ft := range fts {
				r := s.local(ctx.Bool)
				s.eq(ft, ft, fp.Field(i), other.Field(i), r)
				next := s.block()
				s.branch(Copy(r), next, fail)
				s.cur = next
			}
		})
		s.assign(ret, Use{X: s.boolConst(true)})
		s.term(Goto{Target: end})
		s.cur = fail
		s.assign(ret, Use{X: s.boolConst(false)})
		s.term(Goto{Target: end})
		s.cur = end
		s.ret()
	case "PartialOrd", "Ord":
		if ai.IsEnum {
			d1, d2 := s.local(ctx.Ints[types.Isize]), s.local(ctx.Ints[types.Isize])
			s.assign(d1, Discr{Place: a1.Deref()})
			s.assign(d2, Discr{Place: a2.Deref()})
			r := s.local(s.cmpType(trait == "PartialOrd"))
			s.cmp(trait, ctx.Ints[types.Isize], d1, d2, r)
			// Different variants: the variant order decides.
			eqv := s.local(ctx.Bool)
			s.assign(eqv, Bin{Op: Eq, X: Copy(d1), Y: Copy(d2), Type: ctx.Ints[types.Isize]})
			same, diff, end := s.block(), s.block(), s.block()
			s.branch(Copy(eqv), same, diff)
			s.cur = diff
			s.assign(ret, Use{X: Copy(r)})
			s.term(Goto{Target: end})
			s.cur = same
			forEachVariant(a1.Deref(), func(v int, fp Place, fts []*types.Type) {
				other := a2.Deref().With(Proj{Kind: PDowncast, Variant: v})
				s.lexicographic(trait, fts, func(i int) (Place, Place) { return fp.Field(i), other.Field(i) }, ret)
			})
			s.term(Goto{Target: end})
			s.cur = end
			s.ret()
			return
		}
		fts := s.lay.FieldTypes(self, -1)
		s.lexicographic(trait, fts, func(i int) (Place, Place) { return a1.Deref().Field(i), a2.Deref().Field(i) }, ret)
		s.ret()
	case "Hash":
		hasher := targs[len(targs)-1]
		if ai.IsEnum {
			d := s.local(ctx.Ints[types.Isize])
			s.assign(d, Discr{Place: a1.Deref()})
			s.hash(ctx.Ints[types.Isize], d, Copy(a2), hasher)
		}
		forEachVariant(a1.Deref(), func(v int, fp Place, fts []*types.Type) {
			for i, ft := range fts {
				s.hash(ft, fp.Field(i), Copy(a2), hasher)
			}
		})
		s.assign(ret, Use{X: unitConst(ctx)})
		s.ret()
	case "Debug":
		fmtr := s.p.Info.Lang["Formatter"]
		f := Copy(a2)
		forEachVariant(a1.Deref(), func(v int, fp Place, fts []*types.Type) {
			name, kind, fields := self.Def.Name, ai.Kind, ai.Fields
			if v >= 0 {
				vi := ai.Variants[v]
				name, kind, fields = vi.Def.Name, vi.Kind, vi.Fields
			}
			strC := func(v string) Operand { return ConstOp(&Const{Kind: CStr, Str: v, Type: ctx.Ref(false, ctx.Str)}) }
			switch kind {
			case 2: // unit
				s.call(s.p.inherentFn(fmtr, "write_str"), nil, nil, []Operand{f, strC(name)}, ret)
			case 1: // tuple
				s.debugTuple(name, fts, func(i int) Place { return fp.Field(i) }, f, ret)
			default:
				bt := s.p.stdType("fmt", "DebugStruct")
				b := s.local(bt)
				s.call(s.p.inherentFn(fmtr, "debug_struct"), nil, nil, []Operand{f, strC(name)}, b)
				bref := s.local(ctx.Ref(true, bt))
				s.assign(bref, Ref{Mut: true, Place: b})
				for i, ft := range fts {
					r := s.local(ctx.Ref(true, bt))
					s.call(s.p.inherentFn(bt.Def, "field"), nil, nil, []Operand{Copy(bref), strC(fields[i].Name), s.dynDebug(fp.Field(i), ft)}, r)
				}
				s.call(s.p.inherentFn(bt.Def, "finish"), nil, nil, []Operand{Copy(bref)}, ret)
			}
		})
		s.ret()
	default:
		panic("mir: no derived shim for " + trait)
	}
}

// CtorThunk returns a function that builds a struct or variant from its
// arguments, for constructors used as function values (`.map(Some)`).
func (p *Program) CtorThunk(def *resolve.Def, adt *types.Type) *Func {
	key := fmt.Sprintf("ctor.%p.%p", def, adt)
	if f := p.shims[key]; f != nil {
		return f
	}
	lay := layout.New(p.Info)
	variant := -1
	if def.Kind == resolve.DefVariant {
		variant = variantIndex(p.Info, def)
	}
	fts := lay.FieldTypes(adt, variant)
	f := &Func{Name: "ctor " + def.Name, Shim: true, NArgs: len(fts)}
	f.Locals = append(f.Locals, &Local{Type: adt})
	ops := make([]Operand, len(fts))
	for i, ft := range fts {
		f.Locals = append(f.Locals, &Local{Type: ft})
		ops[i] = Copy(LocalPlace(i + 1))
	}
	f.Blocks = []*Block{{
		Stmts: []Stmt{Assign{Place: LocalPlace(0), Rv: Aggregate{Kind: AggAdt, Type: adt, Variant: max(variant, 0), Ops: ops}}},
		Term:  Return{},
	}}
	boxAddressTaken(p.Info.Ctx, f)
	p.shims[key] = f
	return f
}
