package interp

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"navi/internal/layout"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/types"
)

// ref is a reference to a value in memory: an address plus metadata for
// unsized values (slice length or vtable).
type ref struct {
	p    Ptr
	meta Ptr
}

func (r ref) val(t *types.Type) val {
	if layout.IsFat(t) {
		return fatVal(r.p, r.meta)
	}
	return ptrVal(r.p)
}

// deref reads the reference stored in v (a &T value) for pointee type t.
func derefVal(v val, t *types.Type) ref {
	if layout.IsFat(t) {
		return ref{p: v.ptrAt(0), meta: v.ptrAt(8)}
	}
	return ref{p: v.ptrAt(0)}
}

func (t *thread) top() *frame {
	if len(t.stack) == 0 {
		return &frame{fn: &mir.Func{Name: "<shim>"}}
	}
	return t.stack[len(t.stack)-1]
}

// callTrait calls Trait::method for self type self.
func (t *thread) callTrait(trait, method string, self *types.Type, targs []*types.Type, args []val) val {
	m := t.m
	def := m.info.TraitItem(trait, method)
	inst := m.resolveRef(&fnInst{subst: map[*resolve.Def]*types.Type{}}, &mir.FnRef{Def: def, Self: self, Args: targs})
	return t.call(inst, args)
}

// inherent finds an inherent method of a std type.
func (m *Machine) inherent(adt *resolve.Def, name string) *resolve.Def {
	for _, ii := range m.info.ImplsOf[nil] {
		if ii.SelfTy.Kind == types.KAdt && ii.SelfTy.Def == adt {
			if it := ii.Items[name]; it != nil {
				return it
			}
		}
	}
	m.fatal("std has no method %s::%s", adt.Name, name)
	return nil
}

func (t *thread) callStd(adt *resolve.Def, name string, args ...val) val {
	return t.call(t.m.instFor(t.m.inherent(adt, name), nil, nil), args)
}

// heapVal stores v in a fresh allocation and returns a pointer to it.
func (m *Machine) heapVal(v val, name string) Ptr {
	a := m.newAlloc(int64(len(v.b)), name)
	m.write(Ptr{A: a}, v)
	return Ptr{A: a}
}

func (m *Machine) strVal(s string) val {
	return fatVal(Ptr{A: m.strAlloc(s)}, Ptr{Off: int64(len(s))})
}

func (m *Machine) variantNamed(t *types.Type, name string) int {
	for i, v := range m.info.Adts[t.Def].Variants {
		if v.Def.Name == name {
			return i
		}
	}
	m.fatal("%s has no variant %s", t, name)
	return 0
}

// enumVal builds a fieldless enum value.
func (m *Machine) enumVal(t *types.Type, variant int) val {
	l := m.layoutOf(t)
	tmp := m.newAlloc(l.Size, "enum")
	m.writeTag(Ptr{A: tmp}, t, variant)
	return m.read(Ptr{A: tmp}, l.Size)
}

func (m *Machine) orderingVal(c int) val {
	ord := m.ctx.Adt(m.info.Lang["Ordering"], nil)
	name := map[int]string{-1: "Less", 0: "Equal", 1: "Greater"}[c]
	return m.enumVal(ord, m.variantNamed(ord, name))
}

func (m *Machine) orderingOf(v val) int {
	ord := m.ctx.Adt(m.info.Lang["Ordering"], nil)
	p := m.heapVal(v, "ordering")
	name := m.info.Adts[ord.Def].Variants[m.readVariant(p, ord)].Def.Name
	return map[string]int{"Less": -1, "Equal": 0, "Greater": 1}[name]
}

// elements returns the element type and references of a tuple, array or
// slice at r.
func (m *Machine) elements(t *types.Type, r ref) ([]*types.Type, []ref) {
	switch t.Kind {
	case types.KTuple:
		l := m.layoutOf(t)
		var rs []ref
		for i := range t.Elems {
			rs = append(rs, ref{p: r.p.add(l.Fields[i])})
		}
		return t.Elems, rs
	case types.KArray, types.KSlice:
		n := t.Len
		if t.Kind == types.KSlice {
			n = r.meta.Off
		}
		size := m.sizeOf(t.Elem)
		var ts []*types.Type
		var rs []ref
		for i := int64(0); i < n; i++ {
			ts = append(ts, t.Elem)
			rs = append(rs, ref{p: r.p.add(i * size)})
		}
		return ts, rs
	}
	return nil, nil
}

// fields returns the field types and references of an ADT value (of the
// given variant for enums).
func (m *Machine) fields(t *types.Type, r ref) (int, []*types.Type, []ref) {
	ai := m.info.Adts[t.Def]
	l := m.layoutOf(t)
	variant := -1
	offs := l.Fields
	if ai.IsEnum {
		variant = m.readVariant(r.p, t)
		offs = l.Variants[variant]
	}
	fts := m.lay.FieldTypes(t, variant)
	rs := make([]ref, len(fts))
	for i := range fts {
		var off int64
		if i < len(offs) {
			off = offs[i]
		}
		rs[i] = ref{p: r.p.add(off)}
	}
	return variant, fts, rs
}

// ---------------------------------------------------------------------------
// Comparison

func (t *thread) eq(ty, rhs *types.Type, a, b ref) bool {
	m := t.m
	switch ty.Kind {
	case types.KInt, types.KBool, types.KChar:
		size := m.sizeOf(ty)
		return string(m.read(a.p, size).b) == string(m.read(b.p, size).b)
	case types.KFloat:
		return readFloat(m.read(a.p, m.sizeOf(ty)).b, ty.F64) == readFloat(m.read(b.p, m.sizeOf(ty)).b, ty.F64)
	case types.KStr:
		return a.meta.Off == b.meta.Off && string(m.read(a.p, a.meta.Off).b) == string(m.read(b.p, b.meta.Off).b)
	case types.KPtr, types.KFn:
		return m.read(a.p, 8).ptrAt(0) == m.read(b.p, 8).ptrAt(0)
	case types.KRef:
		return t.eq(ty.Elem, rhs.Elem, derefVal(m.read(a.p, m.sizeOf(ty)), ty.Elem), derefVal(m.read(b.p, m.sizeOf(rhs)), rhs.Elem))
	case types.KTuple, types.KArray, types.KSlice:
		ets, ers := m.elements(ty, a)
		_, brs := m.elements(rhs, b)
		if len(ers) != len(brs) {
			return false
		}
		for i := range ers {
			if !t.eq(ets[i], ets[i], ers[i], brs[i]) {
				return false
			}
		}
		return true
	}
	res := t.callTrait("PartialEq", "eq", ty, []*types.Type{rhs}, []val{a.val(ty), b.val(rhs)})
	return res.b[0] != 0
}

// cmp compares values: -1, 0, 1, or 2 for unordered.
func (t *thread) cmp(ty, rhs *types.Type, a, b ref) int {
	m := t.m
	switch ty.Kind {
	case types.KInt, types.KBool, types.KChar:
		x := m.scalarInt(m.read(a.p, m.sizeOf(ty)), ty)
		y := m.scalarInt(m.read(b.p, m.sizeOf(ty)), ty)
		if ty.Kind == types.KInt && !ty.Int.Signed() && layout.IntSize(ty.Int) == 8 {
			ux, uy := uint64(x), uint64(y)
			switch {
			case ux < uy:
				return -1
			case ux > uy:
				return 1
			}
			return 0
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case types.KFloat:
		x := readFloat(m.read(a.p, m.sizeOf(ty)).b, ty.F64)
		y := readFloat(m.read(b.p, m.sizeOf(ty)).b, ty.F64)
		if x != x || y != y {
			return 2
		}
		return compareFloat(x, y)
	case types.KStr:
		return strings.Compare(string(m.read(a.p, a.meta.Off).b), string(m.read(b.p, b.meta.Off).b))
	case types.KRef:
		return t.cmp(ty.Elem, rhs.Elem, derefVal(m.read(a.p, m.sizeOf(ty)), ty.Elem), derefVal(m.read(b.p, m.sizeOf(rhs)), rhs.Elem))
	case types.KTuple, types.KArray, types.KSlice:
		ets, ers := m.elements(ty, a)
		_, brs := m.elements(rhs, b)
		for i := 0; i < len(ers) && i < len(brs); i++ {
			if c := t.cmp(ets[i], ets[i], ers[i], brs[i]); c != 0 {
				return c
			}
		}
		switch {
		case len(ers) < len(brs):
			return -1
		case len(ers) > len(brs):
			return 1
		}
		return 0
	}
	res := t.callTrait("PartialOrd", "partial_cmp", ty, []*types.Type{rhs}, []val{a.val(ty), b.val(rhs)})
	opt := m.ctx.Adt(m.info.Lang["Option"], []*types.Type{m.ctx.Adt(m.info.Lang["Ordering"], nil)})
	p := m.heapVal(res, "option")
	v := m.readVariant(p, opt)
	if m.info.Adts[opt.Def].Variants[v].Def.Name == "None" {
		return 2
	}
	l := m.layoutOf(opt)
	return m.orderingOf(m.read(p.add(l.Variants[v][0]), m.sizeOf(opt.Args[0])))
}

func (t *thread) cmpResult(method string, c int, ty *types.Type, a, b val) val {
	m := t.m
	switch method {
	case "partial_cmp":
		ord := m.ctx.Adt(m.info.Lang["Ordering"], nil)
		if c == 2 {
			return m.optionVal(ord, val{}, false)
		}
		return m.optionVal(ord, m.orderingVal(c), true)
	case "cmp":
		return m.orderingVal(c)
	case "lt":
		return boolVal(c == -1)
	case "le":
		return boolVal(c == -1 || c == 0)
	case "gt":
		return boolVal(c == 1)
	case "ge":
		return boolVal(c == 1 || c == 0)
	case "max":
		if c == 1 {
			return a
		}
		return b
	case "min":
		if c == 1 {
			return b
		}
		return a
	}
	m.fatal("unknown comparison method %s", method)
	return val{}
}

// ---------------------------------------------------------------------------
// Builtin impls

func (t *thread) builtin(inst *fnInst, args []val) val {
	m := t.m
	trait, method, self := inst.shimTrait, inst.shimDef.Name, inst.shimSelf
	rhs := self
	if len(inst.shimArgs) > 0 && inst.shimArgs[0] != nil {
		rhs = inst.shimArgs[0]
	}
	fr := t.top()
	switch trait {
	case "Clone":
		return t.cloneVal(self, derefVal(args[0], self))
	case "Default":
		return t.defaultVal(self)
	case "PartialEq":
		e := t.eq(self, rhs, derefVal(args[0], self), derefVal(args[1], rhs))
		if method == "ne" {
			e = !e
		}
		return boolVal(e)
	case "PartialOrd", "Ord":
		if method == "max" || method == "min" {
			a, b := m.heapVal(args[0], "cmp"), m.heapVal(args[1], "cmp")
			return t.cmpResult(method, t.cmp(self, self, ref{p: a}, ref{p: b}), self, args[0], args[1])
		}
		return t.cmpResult(method, t.cmp(self, rhs, derefVal(args[0], self), derefVal(args[1], rhs)), self, val{}, val{})
	case "Hash":
		t.hash(self, derefVal(args[0], self), args[1], inst.shimArgs[len(inst.shimArgs)-1])
		return newVal(0)
	case "Add", "Sub", "Mul", "Div", "Rem", "BitAnd", "BitOr", "BitXor", "Shl", "Shr":
		x, y, ty := args[0], args[1], self
		if ty.Kind == types.KRef {
			ty = ty.Elem
			x = m.read(x.ptrAt(0), m.sizeOf(ty))
		}
		if rhs.Kind == types.KRef {
			y = m.read(y.ptrAt(0), m.sizeOf(rhs.Elem))
		}
		return t.binop(fr, binOpOf(trait), x, y, ty)
	case "AddAssign", "SubAssign", "MulAssign", "DivAssign", "RemAssign", "BitAndAssign", "BitOrAssign", "BitXorAssign", "ShlAssign", "ShrAssign":
		p := args[0].ptrAt(0)
		cur := m.read(p, m.sizeOf(self))
		m.write(p, t.binop(fr, binOpOf(strings.TrimSuffix(trait, "Assign")), cur, args[1], self))
		return newVal(0)
	case "Neg", "Not":
		op := mir.Neg
		if trait == "Not" {
			op = mir.Not
		}
		tmp := &frame{fn: fr.fn, inst: &fnInst{subst: map[*resolve.Def]*types.Type{}}, locals: []*Alloc{m.newAlloc(int64(len(args[0].b)), "tmp")}, ltypes: []*types.Type{self}}
		m.write(Ptr{A: tmp.locals[0]}, args[0])
		return t.rvalue(tmp, mir.Un{Op: op, X: mir.Copy(mir.LocalPlace(0)), Type: self}, self)
	case "Deref", "DerefMut":
		r := derefVal(args[0], self)
		return m.read(r.p, m.sizeOf(self))
	case "Index", "IndexMut":
		return t.index(self, derefVal(args[0], self), args[1], rhs)
	case "Step":
		return t.binop(fr, mir.Add, args[0], intVal(int64(len(args[0].b)), int64(args[1].u64(0))), self)
	case "Display", "Debug", "LowerHex", "UpperHex", "Octal", "Binary", "LowerExp", "UpperExp":
		t.fmtBuiltin(trait, self, derefVal(args[0], self), args[1])
		return m.okUnit(inst.shimDef)
	}
	m.fatal("builtin %s::%s for %s is not implemented", trait, method, self)
	return val{}
}

func binOpOf(trait string) mir.BinOp {
	return map[string]mir.BinOp{"Add": mir.Add, "Sub": mir.Sub, "Mul": mir.Mul, "Div": mir.Div, "Rem": mir.Rem,
		"BitAnd": mir.BitAnd, "BitOr": mir.BitOr, "BitXor": mir.BitXor, "Shl": mir.Shl, "Shr": mir.Shr}[trait]
}

// okUnit returns Ok(()) of the return type of a fmt method.
func (m *Machine) okUnit(method *resolve.Def) val {
	ret := m.info.FnSig(method).Ret
	ret = m.info.Normalize(nil, ret)
	return m.enumVal(ret, m.variantNamed(ret, "Ok"))
}

func (t *thread) cloneVal(ty *types.Type, r ref) val {
	m := t.m
	switch ty.Kind {
	case types.KTuple, types.KArray:
		v := newVal(m.sizeOf(ty))
		ets, ers := m.elements(ty, r)
		l := m.layoutOf(ty)
		for i := range ers {
			var off int64
			if ty.Kind == types.KTuple {
				off = l.Fields[i]
			} else {
				off = int64(i) * m.sizeOf(ty.Elem)
			}
			e := t.cloneElem(ets[i], ers[i])
			copy(v.b[off:], e.b)
			for o, p := range e.ptrs {
				v.ensurePtrs()
				v.ptrs[off+o] = p
			}
		}
		return v
	}
	return m.read(r.p, m.sizeOf(ty))
}

func (t *thread) cloneElem(ty *types.Type, r ref) val {
	switch ty.Kind {
	case types.KInt, types.KFloat, types.KBool, types.KChar, types.KPtr, types.KFn:
		return t.m.read(r.p, t.m.sizeOf(ty))
	case types.KRef:
		if !ty.Mut {
			return t.m.read(r.p, t.m.sizeOf(ty))
		}
	case types.KTuple, types.KArray:
		return t.cloneVal(ty, r)
	}
	return t.callTrait("Clone", "clone", ty, nil, []val{ptrVal(r.p)})
}

func (t *thread) defaultVal(ty *types.Type) val {
	m := t.m
	switch ty.Kind {
	case types.KRef:
		return m.strVal("")
	case types.KTuple, types.KArray:
		v := newVal(m.sizeOf(ty))
		l := m.layoutOf(ty)
		elems := ty.Elems
		if ty.Kind == types.KArray {
			elems = nil
			for i := int64(0); i < ty.Len; i++ {
				elems = append(elems, ty.Elem)
			}
		}
		for i, et := range elems {
			var e val
			if isScalar(et) {
				e = newVal(m.sizeOf(et))
			} else {
				e = t.callTrait("Default", "default", et, nil, nil)
			}
			off := int64(i) * m.sizeOf(et)
			if ty.Kind == types.KTuple {
				off = l.Fields[i]
			}
			copy(v.b[off:], e.b)
			for o, p := range e.ptrs {
				v.ensurePtrs()
				v.ptrs[off+o] = p
			}
		}
		return v
	}
	return newVal(m.sizeOf(ty))
}

func isScalar(t *types.Type) bool {
	switch t.Kind {
	case types.KInt, types.KFloat, types.KBool, types.KChar:
		return true
	}
	return false
}

func (t *thread) hash(ty *types.Type, r ref, state val, hasher *types.Type) {
	m := t.m
	write := func(b []byte) {
		p := m.heapVal(val{b: b}, "hash bytes")
		t.callTrait2("Hasher", "write", hasher, []val{state, fatVal(p, Ptr{Off: int64(len(b))})})
	}
	switch ty.Kind {
	case types.KInt, types.KBool, types.KChar:
		write(m.read(r.p, m.sizeOf(ty)).b)
	case types.KStr:
		write(append(m.read(r.p, r.meta.Off).b, 0xff))
	case types.KRef:
		t.hash(ty.Elem, derefVal(m.read(r.p, m.sizeOf(ty)), ty.Elem), state, hasher)
	case types.KPtr:
		v := m.read(r.p, 8)
		write(v.b)
	case types.KTuple, types.KArray, types.KSlice:
		ets, ers := m.elements(ty, r)
		if ty.Kind == types.KSlice {
			write(intVal(8, int64(len(ers))).b)
		}
		for i := range ers {
			t.hash(ets[i], ers[i], state, hasher)
		}
	default:
		t.callTrait("Hash", "hash", ty, []*types.Type{hasher}, []val{ptrVal(r.p), state})
	}
}

// callTrait2 calls a method of a std trait that is not a lang item.
func (t *thread) callTrait2(trait, method string, self *types.Type, args []val) val {
	m := t.m
	for d, ti := range m.info.Traits {
		if d.Crate.IsStd && d.Name == trait {
			it := ti.Items[method]
			inst := m.resolveRef(&fnInst{subst: map[*resolve.Def]*types.Type{}}, &mir.FnRef{Def: it, Self: self})
			return t.call(inst, args)
		}
	}
	m.fatal("std has no trait %s", trait)
	return val{}
}

// index implements Index for arrays, slices and str.
func (t *thread) index(self *types.Type, r ref, idx val, idxTy *types.Type) val {
	m := t.m
	fr := t.top()
	n := r.meta.Off
	if self.Kind == types.KArray {
		n = self.Len
	}
	elemSize := int64(1)
	if self.Kind != types.KStr {
		elemSize = m.sizeOf(self.Elem)
	}
	if idxTy.Kind == types.KInt {
		i := int64(idx.u64(0))
		if i < 0 || i >= n {
			t.panic(fr, fmt.Sprintf("index out of bounds: the len is %d but the index is %d", n, i))
		}
		return ptrVal(r.p.add(i * elemSize))
	}
	// Ranges: read start/end fields by name.
	lo, hi := int64(0), n
	l := m.layoutOf(idxTy)
	for i, f := range m.info.Adts[idxTy.Def].Fields {
		v := int64(idx.u64(l.Fields[i]))
		switch f.Name {
		case "start":
			lo = v
		case "end":
			hi = v
			if m.info.LangName(idxTy.Def) == "RangeInclusive" || m.info.LangName(idxTy.Def) == "RangeToInclusive" {
				hi++
			}
		}
	}
	if lo > hi {
		t.panic(fr, fmt.Sprintf("slice index starts at %d but ends at %d", lo, hi))
	}
	if hi > n {
		t.panic(fr, fmt.Sprintf("range end index %d out of range for slice of length %d", hi, n))
	}
	return fatVal(r.p.add(lo*elemSize), Ptr{Off: hi - lo})
}

// ---------------------------------------------------------------------------
// Formatting of primitives

func (m *Machine) formatterField(f val, name string) val {
	fmtr := m.ctx.Adt(m.info.Lang["Formatter"], nil)
	l := m.layoutOf(fmtr)
	for i, fd := range m.info.Adts[fmtr.Def].Fields {
		if fd.Name == name {
			ft := m.lay.FieldTypes(fmtr, -1)[i]
			return m.read(f.ptrAt(0).add(l.Fields[i]), m.sizeOf(ft))
		}
	}
	m.fatal("Formatter has no field %s", name)
	return val{}
}

// optUsize reads an Option<usize>.
func (m *Machine) optUsize(v val) (int64, bool) {
	opt := m.ctx.Adt(m.info.Lang["Option"], []*types.Type{m.ctx.Ints[types.Usize]})
	p := m.heapVal(v, "opt")
	vi := m.readVariant(p, opt)
	if m.info.Adts[opt.Def].Variants[vi].Def.Name == "None" {
		return 0, false
	}
	return int64(m.read(p.add(m.layoutOf(opt).Variants[vi][0]), 8).u64(0)), true
}

func (t *thread) fmtBuiltin(trait string, ty *types.Type, r ref, f val) {
	m := t.m
	fmtrDef := m.info.Lang["Formatter"]
	pad := func(s string) { t.callStd(fmtrDef, "pad", f, m.strVal(s)) }
	writeStr := func(s string) { t.callStd(fmtrDef, "write_str", f, m.strVal(s)) }
	padInt := func(nonneg bool, prefix, digits string) {
		t.callStd(fmtrDef, "pad_integral", f, boolVal(nonneg), m.strVal(prefix), m.strVal(digits))
	}
	switch ty.Kind {
	case types.KRef:
		inner := derefVal(m.read(r.p, m.sizeOf(ty)), ty.Elem)
		if isBuiltinFmt(ty.Elem) {
			t.fmtBuiltin(trait, ty.Elem, inner, f)
			return
		}
		t.callTrait(trait, "fmt", ty.Elem, nil, []val{inner.val(ty.Elem), f})
		return
	case types.KStr:
		s := string(m.read(r.p, r.meta.Off).b)
		if trait == "Debug" {
			writeStr(strconv.Quote(s))
			return
		}
		pad(s)
		return
	case types.KBool:
		pad(strconv.FormatBool(m.read(r.p, 1).b[0] != 0))
		return
	case types.KChar:
		c := rune(readInt(m.read(r.p, 4).b, 4, false))
		if trait == "Debug" {
			writeStr(strconv.QuoteRune(c))
			return
		}
		pad(string(c))
		return
	case types.KInt:
		size := layout.IntSize(ty.Int)
		v := readInt(m.read(r.p, size).b, size, ty.Int.Signed())
		u := uint64(v) & mask(size)
		switch trait {
		case "LowerHex":
			padInt(true, "0x", strconv.FormatUint(u, 16))
		case "UpperHex":
			padInt(true, "0x", strings.ToUpper(strconv.FormatUint(u, 16)))
		case "Octal":
			padInt(true, "0o", strconv.FormatUint(u, 8))
		case "Binary":
			padInt(true, "0b", strconv.FormatUint(u, 2))
		default:
			if ty.Int.Signed() && v < 0 {
				padInt(false, "", strconv.FormatUint(uint64(-v), 10))
			} else {
				padInt(true, "", strconv.FormatUint(u, 10))
			}
		}
		return
	case types.KFloat:
		x := readFloat(m.read(r.p, m.sizeOf(ty)).b, ty.F64)
		prec, hasPrec := m.optUsize(m.formatterField(f, "precision"))
		style := byte(0)
		switch trait {
		case "Debug":
			style = 1
		case "LowerExp":
			style = 2
		case "UpperExp":
			style = 3
		}
		bits := 32
		if ty.F64 {
			bits = 64
		}
		s := formatFloatBits(x, int(prec), hasPrec, style, bits)
		nonneg := !strings.HasPrefix(s, "-")
		padInt(nonneg, "", strings.TrimPrefix(s, "-"))
		return
	case types.KTuple:
		if len(ty.Elems) == 0 {
			pad("()")
			return
		}
		b := t.callStd(fmtrDef, "debug_tuple", f, m.strVal(""))
		bp := m.heapVal(b, "debug tuple")
		ets, ers := m.elements(ty, r)
		dt := m.info.Lang["Debug"]
		for i := range ers {
			t.callStd(b2def(m, "DebugTuple"), "field", ptrVal(bp), fatVal(ers[i].p, Ptr{A: m.vtable(ets[i], m.ctx.Dyn([]*types.TraitRef{{Trait: dt}}))}))
		}
		t.callStd(b2def(m, "DebugTuple"), "finish", ptrVal(bp))
		return
	case types.KArray, types.KSlice:
		b := t.callStd(fmtrDef, "debug_list", f)
		bp := m.heapVal(b, "debug list")
		ets, ers := m.elements(ty, r)
		dt := m.info.Lang["Debug"]
		for i := range ers {
			t.callStd(b2def(m, "DebugList"), "entry", ptrVal(bp), fatVal(ers[i].p, Ptr{A: m.vtable(ets[i], m.ctx.Dyn([]*types.TraitRef{{Trait: dt}}))}))
		}
		t.callStd(b2def(m, "DebugList"), "finish", ptrVal(bp))
		return
	case types.KPtr, types.KFn:
		p := m.read(r.p, 8).ptrAt(0)
		id := int64(0)
		if p.A != nil {
			id = int64(p.A.id)
		}
		writeStr(fmt.Sprintf("0x%x", id<<32+p.Off))
		return
	}
	m.fatal("cannot format %s with %s", ty, trait)
}

func isBuiltinFmt(t *types.Type) bool {
	switch t.Kind {
	case types.KInt, types.KFloat, types.KBool, types.KChar, types.KStr, types.KRef, types.KTuple, types.KArray, types.KSlice:
		return true
	}
	return false
}

// b2def finds a std fmt builder type by name.
func b2def(m *Machine, name string) *resolve.Def {
	fmtr := m.info.Lang["Formatter"]
	return fmtr.Parent.Module.Lookup(name, resolve.TypeNS).Res.Def
}

// ---------------------------------------------------------------------------
// Derived impls

func (t *thread) derived(inst *fnInst, args []val) val {
	m := t.m
	trait, method, self := inst.shimTrait, inst.shimDef.Name, inst.shimSelf
	ai := m.info.Adts[self.Def]
	switch trait {
	case "Clone":
		r := derefVal(args[0], self)
		variant, fts, frs := m.fields(self, r)
		l := m.layoutOf(self)
		out := m.newAlloc(l.Size, "clone")
		offs := l.Fields
		if variant >= 0 {
			offs = l.Variants[variant]
		}
		for i := range frs {
			m.write(Ptr{A: out, Off: offs[i]}, t.cloneElem(fts[i], frs[i]))
		}
		if variant >= 0 {
			m.writeTag(Ptr{A: out}, self, variant)
		}
		return m.read(Ptr{A: out}, l.Size)
	case "Default":
		l := m.layoutOf(self)
		out := m.newAlloc(l.Size, "default")
		fts := m.lay.FieldTypes(self, -1)
		for i, ft := range fts {
			var v val
			if isScalar(ft) {
				v = newVal(m.sizeOf(ft))
			} else {
				v = t.callTrait("Default", "default", ft, nil, nil)
			}
			m.write(Ptr{A: out, Off: l.Fields[i]}, v)
		}
		return m.read(Ptr{A: out}, l.Size)
	case "PartialEq":
		a, b := derefVal(args[0], self), derefVal(args[1], self)
		va, fts, ars := m.fields(self, a)
		vb, _, brs := m.fields(self, b)
		eq := va == vb
		for i := 0; eq && i < len(ars); i++ {
			eq = t.eq(fts[i], fts[i], ars[i], brs[i])
		}
		if method == "ne" {
			eq = !eq
		}
		return boolVal(eq)
	case "PartialOrd", "Ord":
		var a, b ref
		if method == "max" || method == "min" {
			a, b = ref{p: m.heapVal(args[0], "cmp")}, ref{p: m.heapVal(args[1], "cmp")}
		} else {
			a, b = derefVal(args[0], self), derefVal(args[1], self)
		}
		va, fts, ars := m.fields(self, a)
		vb, _, brs := m.fields(self, b)
		c := 0
		switch {
		case va < vb:
			c = -1
		case va > vb:
			c = 1
		}
		for i := 0; c == 0 && i < len(ars); i++ {
			c = t.cmp(fts[i], fts[i], ars[i], brs[i])
		}
		if method == "max" || method == "min" {
			return t.cmpResult(method, c, self, args[0], args[1])
		}
		return t.cmpResult(method, c, self, val{}, val{})
	case "Hash":
		r := derefVal(args[0], self)
		variant, fts, frs := m.fields(self, r)
		hasher := inst.shimArgs[len(inst.shimArgs)-1]
		if variant >= 0 {
			t.hash(m.ctx.Ints[types.Isize], ref{p: m.heapVal(intVal(8, int64(variant)), "discr")}, args[1], hasher)
		}
		for i := range frs {
			t.hash(fts[i], frs[i], args[1], hasher)
		}
		return newVal(0)
	case "Debug":
		r := derefVal(args[0], self)
		f := args[1]
		variant, fts, frs := m.fields(self, r)
		name := self.Def.Name
		kind := ai.Kind
		var fields []*types.Field
		if variant >= 0 {
			name = ai.Variants[variant].Def.Name
			kind = ai.Variants[variant].Kind
			fields = ai.Variants[variant].Fields
		} else {
			fields = ai.Fields
		}
		fmtrDef := m.info.Lang["Formatter"]
		dt := m.info.Lang["Debug"]
		dyn := func(i int) val {
			return fatVal(frs[i].p, Ptr{A: m.vtable(fts[i], m.ctx.Dyn([]*types.TraitRef{{Trait: dt}}))})
		}
		switch kind {
		case 2: // unit
			t.callStd(fmtrDef, "write_str", f, m.strVal(name))
		case 1: // tuple
			b := t.callStd(fmtrDef, "debug_tuple", f, m.strVal(name))
			bp := m.heapVal(b, "debug tuple")
			for i := range frs {
				t.callStd(b2def(m, "DebugTuple"), "field", ptrVal(bp), dyn(i))
			}
			t.callStd(b2def(m, "DebugTuple"), "finish", ptrVal(bp))
		default:
			b := t.callStd(fmtrDef, "debug_struct", f, m.strVal(name))
			bp := m.heapVal(b, "debug struct")
			for i := range frs {
				t.callStd(b2def(m, "DebugStruct"), "field", ptrVal(bp), m.strVal(fields[i].Name), dyn(i))
			}
			t.callStd(b2def(m, "DebugStruct"), "finish", ptrVal(bp))
		}
		return m.okUnit(inst.shimDef)
	}
	m.fatal("derived %s::%s is not implemented", trait, method)
	return val{}
}

var _ = utf8.RuneError
