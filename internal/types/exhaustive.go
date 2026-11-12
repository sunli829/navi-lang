package types

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

// Exhaustiveness checking with the usefulness algorithm (Maranget,
// "Warnings for pattern matching"), as in rustc. Checks run after type
// inference, when pattern types are known.

type ctorKind uint8

const (
	cWild    ctorKind = iota
	cSingle           // struct, tuple, reference, array
	cVariant          // enum variant (index)
	cBool             // false = 0, true = 1
	cRange            // integer or char range [lo, hi]
	cSlice            // slice of length n (or at least n if varLen)
	cOpaque           // string/float literal: never exhaustive
)

type dpat struct {
	kind   ctorKind
	index  int     // variant index / bool value
	lo, hi int64   // ranges
	n      int     // slice length
	varLen bool    // slice pattern with ..: length >= n (prefix/suffix split at pre)
	pre    int     // slice: elements before ..
	lit    string  // opaque literal
	fields []*dpat // subpatterns (for slices with ..: pre + suffix)
	ty     *Type
}

var wild = &dpat{kind: cWild}

// checkExhaustive records a match for checking after inference.
func (fc *fnCtx) checkExhaustive(x *syntax.MatchExpr, scrut *Type) {
	fc.deferred = append(fc.deferred, func() {
		t := fc.in.resolve(scrut)
		var rows [][]*dpat
		for _, a := range x.Arms {
			if a.Guard != nil {
				continue
			}
			for _, d := range fc.expandOr(fc.deconstruct(a.Pat, t)) {
				rows = append(rows, []*dpat{d})
			}
		}
		if ok, w := fc.useful(rows, []*dpat{wild}, []*Type{t}); ok {
			fc.errorf(x.Match, "non-exhaustive patterns: %s not covered", fc.showWitness(w, t))
		}
	})
}

// checkIrrefutable requires p to match every value of type t.
func (fc *fnCtx) checkIrrefutable(p syntax.Pat, t *Type, what string) {
	fc.deferred = append(fc.deferred, func() {
		rt := fc.in.resolve(t)
		var rows [][]*dpat
		for _, d := range fc.expandOr(fc.deconstruct(p, rt)) {
			rows = append(rows, []*dpat{d})
		}
		if ok, w := fc.useful(rows, []*dpat{wild}, []*Type{rt}); ok {
			fc.errorf(p.Pos(), "refutable pattern in %s binding: %s not covered; use if let or let-else", what, fc.showWitness(w, rt))
		}
	})
}

func (fc *fnCtx) showWitness(w []*dpat, t *Type) string {
	if len(w) == 0 {
		return "_"
	}
	return fc.showPat(w[0], t)
}

// deconstruct converts a pattern matched against type t.
func (fc *fnCtx) deconstruct(p syntax.Pat, t *Type) *dpat {
	t = fc.in.resolve(t)
	switch p := p.(type) {
	case *syntax.WildPat, *syntax.RestPat:
		return wild
	case *syntax.IdentPat:
		if res, ok := fc.info.Res.PatConsts[p]; ok {
			return fc.withRefs(t, func(inner *Type) *dpat { return fc.constPat(res, inner) })
		}
		if p.Sub != nil {
			return fc.deconstruct(p.Sub, t)
		}
		return wild
	case *syntax.RefPat:
		if t.Kind != KRef {
			return wild
		}
		return &dpat{kind: cSingle, fields: []*dpat{fc.deconstruct(p.Pat, t.Elem)}, ty: t}
	case *syntax.OrPat:
		out := &dpat{kind: cWild, fields: nil}
		var alts []*dpat
		for _, a := range p.Alts {
			alts = append(alts, fc.deconstruct(a, t))
		}
		out.fields = alts
		out.lit = "or"
		return out
	case *syntax.LitPat:
		if s, ok := p.X.(*syntax.LitExpr); ok && (s.Kind == syntax.LitString || s.Kind == syntax.LitByteString) {
			// &str literal: peel references above &str.
			if t.Kind == KRef && t.Elem.Kind == KRef {
				return &dpat{kind: cSingle, fields: []*dpat{fc.deconstruct(p, t.Elem)}, ty: t}
			}
			return &dpat{kind: cOpaque, lit: s.Value, ty: t}
		}
	}
	return fc.withRefs(t, func(inner *Type) *dpat { return fc.deconstructNonRef(p, inner) })
}

// withRefs wraps the result for implicit dereferences (match ergonomics).
func (fc *fnCtx) withRefs(t *Type, f func(*Type) *dpat) *dpat {
	if t.Kind == KRef {
		return &dpat{kind: cSingle, fields: []*dpat{fc.withRefs(t.Elem, f)}, ty: t}
	}
	return f(t)
}

func (fc *fnCtx) constPat(res resolve.Res, t *Type) *dpat {
	d := res.Def
	switch d.Kind {
	case resolve.DefStruct:
		return &dpat{kind: cSingle, ty: t}
	case resolve.DefVariant:
		return &dpat{kind: cVariant, index: fc.variantIndex(d), ty: t, fields: fc.wildFields(d, t)}
	case resolve.DefConst:
		if v, ok := fc.c.constValue(d); ok && (t.Kind == KInt || t.Kind == KChar) {
			return &dpat{kind: cRange, lo: v, hi: v, ty: t}
		}
	}
	return &dpat{kind: cOpaque, lit: d.Name, ty: t}
}

func (fc *fnCtx) variantIndex(d *resolve.Def) int {
	for i, v := range fc.c.adt(d.Parent).Variants {
		if v.Def == d {
			return i
		}
	}
	return -1
}

func (fc *fnCtx) wildFields(variant *resolve.Def, t *Type) []*dpat {
	for _, v := range fc.c.adt(variant.Parent).Variants {
		if v.Def == variant {
			out := make([]*dpat, len(v.Fields))
			for i := range out {
				out[i] = wild
			}
			return out
		}
	}
	return nil
}

func (fc *fnCtx) deconstructNonRef(p syntax.Pat, t *Type) *dpat {
	switch p := p.(type) {
	case *syntax.LitPat:
		return fc.litPat(p.X, t)
	case *syntax.RangePat:
		lo, hi := int64(math.MinInt64), int64(math.MaxInt64)
		if dl, dh, ok := domain(t); ok {
			lo, hi = dl, dh
		}
		ok := true
		if p.Lo != nil {
			v, k := fc.patValue(p.Lo)
			lo, ok = v, ok && k
		}
		if p.Hi != nil {
			v, k := fc.patValue(p.Hi)
			hi, ok = v, ok && k
			if !p.Inclusive {
				hi--
			}
		}
		if !ok {
			return &dpat{kind: cOpaque, lit: "range", ty: t}
		}
		return &dpat{kind: cRange, lo: lo, hi: hi, ty: t}
	case *syntax.PathPat:
		res := fc.info.Res.Paths[p.Path]
		if res.Kind == resolve.ResDef && len(res.Rest) == 0 {
			return fc.constPat(res, t)
		}
		return &dpat{kind: cOpaque, lit: syntax.Dump(p.Path), ty: t}
	case *syntax.TupleStructPat:
		res := fc.info.Res.Paths[p.Path]
		types := fc.ctorFieldTypes(res, t)
		fields := fc.positional(p.Elems, types)
		if res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefVariant {
			return &dpat{kind: cVariant, index: fc.variantIndex(res.Def), fields: fields, ty: t}
		}
		return &dpat{kind: cSingle, fields: fields, ty: t}
	case *syntax.StructPat:
		res := fc.info.Res.Paths[p.Path]
		var variant *resolve.Def
		if res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefVariant {
			variant = res.Def
		} else if len(res.Rest) == 1 && t.Kind == KAdt {
			for _, v := range fc.c.adt(t.Def).Variants {
				if v.Def.Name == res.Rest[0].Name.Value {
					variant = v.Def
				}
			}
		}
		fields := fc.adtFields(t, variant)
		out := make([]*dpat, len(fields))
		for i := range out {
			out[i] = wild
		}
		for _, fp := range p.Fields {
			for i, f := range fields {
				if f.Name == fp.Name.Value {
					out[i] = fc.deconstruct(fp.Pat, fc.fieldType(t, f))
				}
			}
		}
		if variant != nil {
			return &dpat{kind: cVariant, index: fc.variantIndex(variant), fields: out, ty: t}
		}
		return &dpat{kind: cSingle, fields: out, ty: t}
	case *syntax.TuplePat:
		if t.Kind != KTuple {
			return wild
		}
		return &dpat{kind: cSingle, fields: fc.positional(p.Elems, t.Elems), ty: t}
	case *syntax.SlicePat:
		if t.Kind != KArray && t.Kind != KSlice {
			return wild
		}
		rest := -1
		for i, el := range p.Elems {
			if isRest(el) {
				rest = i
			}
		}
		if t.Kind == KArray {
			types := make([]*Type, t.Len)
			for i := range types {
				types[i] = t.Elem
			}
			return &dpat{kind: cSingle, fields: fc.positional(p.Elems, types), ty: t}
		}
		var fields []*dpat
		for _, el := range p.Elems {
			if !isRest(el) {
				fields = append(fields, fc.deconstruct(el, t.Elem))
			}
		}
		if rest < 0 {
			return &dpat{kind: cSlice, n: len(fields), fields: fields, ty: t}
		}
		return &dpat{kind: cSlice, n: len(fields), varLen: true, pre: rest, fields: fields, ty: t}
	}
	return wild
}

// positional maps tuple-like subpatterns (with an optional ..) to fields.
func (fc *fnCtx) positional(elems []syntax.Pat, types []*Type) []*dpat {
	out := make([]*dpat, len(types))
	for i := range out {
		out[i] = wild
	}
	rest := -1
	for i, el := range elems {
		if isRest(el) {
			rest = i
		}
	}
	if rest < 0 {
		for i, el := range elems {
			if i < len(types) {
				out[i] = fc.deconstruct(el, types[i])
			}
		}
		return out
	}
	for i := 0; i < rest && i < len(types); i++ {
		out[i] = fc.deconstruct(elems[i], types[i])
	}
	after := len(elems) - rest - 1
	for i := 0; i < after; i++ {
		j := len(types) - after + i
		if j >= 0 && j < len(types) {
			out[j] = fc.deconstruct(elems[rest+1+i], types[j])
		}
	}
	return out
}

func (fc *fnCtx) adtFields(t *Type, variant *resolve.Def) []*Field {
	if t.Kind != KAdt {
		return nil
	}
	ai := fc.c.adt(t.Def)
	if variant == nil {
		return ai.Fields
	}
	for _, v := range ai.Variants {
		if v.Def == variant {
			return v.Fields
		}
	}
	return nil
}

func (fc *fnCtx) fieldType(t *Type, f *Field) *Type {
	ai := fc.c.adt(t.Def)
	return fc.in.resolve(fc.normalize(Subst(fc.in.ctx, f.Type, paramMap(ai.Generics, t.Args), nil)))
}

func (fc *fnCtx) ctorFieldTypes(res resolve.Res, t *Type) []*Type {
	if t.Kind != KAdt {
		return nil
	}
	var variant *resolve.Def
	if res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefVariant {
		variant = res.Def
	}
	fields := fc.adtFields(t, variant)
	out := make([]*Type, len(fields))
	for i, f := range fields {
		out[i] = fc.fieldType(t, f)
	}
	return out
}

func (fc *fnCtx) litPat(x syntax.Expr, t *Type) *dpat {
	if t.Kind == KBool {
		if l, ok := x.(*syntax.LitExpr); ok && l.Kind == syntax.LitBool {
			v := 0
			if l.Value == "true" {
				v = 1
			}
			return &dpat{kind: cBool, index: v, ty: t}
		}
	}
	if v, ok := fc.patValue(x); ok && (t.Kind == KInt || t.Kind == KChar) {
		return &dpat{kind: cRange, lo: v, hi: v, ty: t}
	}
	return &dpat{kind: cOpaque, lit: syntax.Dump(x), ty: t}
}

// patValue evaluates an integer, char or constant in a pattern.
func (fc *fnCtx) patValue(x syntax.Expr) (int64, bool) {
	switch x := x.(type) {
	case *syntax.LitExpr:
		switch x.Kind {
		case syntax.LitInt:
			return parseIntLit(x.Value)
		case syntax.LitChar:
			return charValue(x.Value)
		case syntax.LitByte:
			return charValue(x.Value[1:])
		}
	case *syntax.UnaryExpr:
		if x.Op == syntax.Minus {
			v, ok := fc.patValue(x.X)
			return -v, ok
		}
	case *syntax.PathExpr:
		res := fc.info.Res.Paths[x.Path]
		if res.Kind == resolve.ResDef && res.Def.Kind == resolve.DefConst && len(res.Rest) == 0 {
			return fc.c.constValue(res.Def)
		}
		if res.Kind == resolve.ResPrim && len(res.Rest) == 1 {
			lo, hi, ok := domain(fc.c.prim(res.Prim))
			switch res.Rest[0].Name.Value {
			case "MIN":
				return lo, ok
			case "MAX":
				return hi, ok
			}
		}
	}
	return 0, false
}

// charValue decodes a char literal like 'a' or '\n'.
func charValue(lit string) (int64, bool) {
	s := strings.TrimSuffix(strings.TrimPrefix(lit, "'"), "'")
	if !strings.HasPrefix(s, `\`) {
		r, _ := utf8.DecodeRuneInString(s)
		return int64(r), true
	}
	switch s {
	case `\n`:
		return '\n', true
	case `\r`:
		return '\r', true
	case `\t`:
		return '\t', true
	case `\\`:
		return '\\', true
	case `\0`:
		return 0, true
	case `\'`:
		return '\'', true
	case `\"`:
		return '"', true
	}
	if strings.HasPrefix(s, `\x`) {
		v, err := strconv.ParseInt(s[2:], 16, 64)
		return v, err == nil
	}
	if strings.HasPrefix(s, `\u{`) {
		v, err := strconv.ParseInt(strings.TrimSuffix(s[3:], "}"), 16, 64)
		return v, err == nil
	}
	return 0, false
}

// domain returns the value range of an integer or char type.
func domain(t *Type) (int64, int64, bool) {
	switch t.Kind {
	case KChar:
		return 0, 0x10FFFF, true
	case KInt:
		switch t.Int {
		case I8:
			return math.MinInt8, math.MaxInt8, true
		case I16:
			return math.MinInt16, math.MaxInt16, true
		case I32:
			return math.MinInt32, math.MaxInt32, true
		case I64, Isize:
			return math.MinInt64, math.MaxInt64, true
		case U8:
			return 0, math.MaxUint8, true
		case U16:
			return 0, math.MaxUint16, true
		case U32:
			return 0, math.MaxUint32, true
		case U64, Usize:
			return 0, math.MaxInt64, true // values above MaxInt64 are not tracked
		}
	}
	return 0, 0, false
}

// expandOr turns or-patterns at the top into separate patterns. Nested or-
// patterns are expanded lazily during specialization.
func (fc *fnCtx) expandOr(d *dpat) []*dpat {
	if d.kind == cWild && d.lit == "or" {
		var out []*dpat
		for _, a := range d.fields {
			out = append(out, fc.expandOr(a)...)
		}
		return out
	}
	return []*dpat{d}
}

func isOr(d *dpat) bool { return d.kind == cWild && d.lit == "or" }

// expandRows expands or-patterns in the first column.
func (fc *fnCtx) expandRows(rows [][]*dpat) [][]*dpat {
	var out [][]*dpat
	for _, r := range rows {
		if len(r) > 0 && isOr(r[0]) {
			for _, a := range fc.expandOr(r[0]) {
				nr := append([]*dpat{a}, r[1:]...)
				out = append(out, fc.expandRows([][]*dpat{nr})...)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// ctor is one constructor of a type's value space after splitting.
type ctor struct {
	kind   ctorKind
	index  int
	lo, hi int64
	n      int
	varLen bool
}

// arity returns the number of fields of a constructor at type t.
func (fc *fnCtx) arity(c ctor, t *Type) []*Type {
	switch c.kind {
	case cSingle:
		switch t.Kind {
		case KTuple:
			return t.Elems
		case KRef:
			return []*Type{t.Elem}
		case KArray:
			out := make([]*Type, t.Len)
			for i := range out {
				out[i] = t.Elem
			}
			return out
		case KAdt:
			fields := fc.adtFields(t, nil)
			out := make([]*Type, len(fields))
			for i, f := range fields {
				out[i] = fc.fieldType(t, f)
			}
			return out
		}
	case cVariant:
		v := fc.c.adt(t.Def).Variants[c.index]
		out := make([]*Type, len(v.Fields))
		for i, f := range v.Fields {
			out[i] = fc.fieldType(t, f)
		}
		return out
	case cSlice:
		out := make([]*Type, c.n)
		for i := range out {
			out[i] = t.Elem
		}
		return out
	}
	return nil
}

// allCtors returns the constructors of t split according to the patterns
// in the first column, and whether that set is complete (finite).
func (fc *fnCtx) allCtors(t *Type, heads []*dpat) ([]ctor, bool) {
	switch t.Kind {
	case KBool:
		return []ctor{{kind: cBool, index: 0}, {kind: cBool, index: 1}}, true
	case KNever:
		return nil, true
	case KTuple, KRef, KArray:
		return []ctor{{kind: cSingle}}, true
	case KAdt:
		ai := fc.c.adt(t.Def)
		if !ai.IsEnum {
			return []ctor{{kind: cSingle}}, true
		}
		out := make([]ctor, len(ai.Variants))
		for i := range out {
			out[i] = ctor{kind: cVariant, index: i}
		}
		return out, true
	case KInt, KChar:
		lo, hi, _ := domain(t)
		// Split the domain at every range boundary.
		points := map[int64]bool{lo: true}
		for _, h := range heads {
			if h.kind == cRange {
				points[h.lo] = true
				if h.hi < hi {
					points[h.hi+1] = true
				}
			}
		}
		var ps []int64
		for p := range points {
			if p >= lo && p <= hi {
				ps = append(ps, p)
			}
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		var out []ctor
		for i, p := range ps {
			end := hi
			if i+1 < len(ps) {
				end = ps[i+1] - 1
			}
			out = append(out, ctor{kind: cRange, lo: p, hi: end})
		}
		return out, true
	case KSlice:
		maxLen := 0
		for _, h := range heads {
			if h.kind == cSlice && h.n+1 > maxLen {
				maxLen = h.n + 1
			}
		}
		var out []ctor
		for n := 0; n < maxLen; n++ {
			out = append(out, ctor{kind: cSlice, n: n})
		}
		out = append(out, ctor{kind: cSlice, n: maxLen, varLen: true})
		return out, true
	}
	return nil, false
}

// covers reports whether head pattern d matches all values of ctor c.
func covers(d *dpat, c ctor) bool {
	switch d.kind {
	case cWild:
		return true
	case cSingle:
		return c.kind == cSingle
	case cVariant, cBool:
		return d.kind == c.kind && d.index == c.index
	case cRange:
		return c.kind == cRange && d.lo <= c.lo && c.hi <= d.hi
	case cSlice:
		if c.kind != cSlice {
			return false
		}
		if d.varLen {
			return c.n >= d.n
		}
		return !c.varLen && c.n == d.n
	}
	return false
}

// specialize returns the fields of head pattern d for constructor c with
// the given field types.
func specializeFields(d *dpat, c ctor, nfields int) []*dpat {
	out := make([]*dpat, nfields)
	for i := range out {
		out[i] = wild
	}
	switch d.kind {
	case cSingle, cVariant:
		copy(out, d.fields)
	case cSlice:
		if !d.varLen {
			copy(out, d.fields)
			break
		}
		suffix := len(d.fields) - d.pre
		for i := 0; i < d.pre && i < nfields; i++ {
			out[i] = d.fields[i]
		}
		for i := 0; i < suffix; i++ {
			j := nfields - suffix + i
			if j >= 0 {
				out[j] = d.fields[d.pre+i]
			}
		}
	}
	return out
}

// useful reports whether v matches some value not matched by rows, and
// returns such a value as a witness.
func (fc *fnCtx) useful(rows [][]*dpat, v []*dpat, tys []*Type) (bool, []*dpat) {
	if len(v) == 0 {
		return len(rows) == 0, nil
	}
	rows = fc.expandRows(rows)
	if isOr(v[0]) {
		for _, a := range fc.expandOr(v[0]) {
			if ok, w := fc.useful(rows, append([]*dpat{a}, v[1:]...), tys); ok {
				return true, w
			}
		}
		return false, nil
	}
	t := tys[0]
	var heads []*dpat
	for _, r := range rows {
		heads = append(heads, r[0])
	}
	head := v[0]

	specialize := func(c ctor) (bool, []*dpat) {
		ftys := fc.arity(c, t)
		var srows [][]*dpat
		for _, r := range rows {
			if covers(r[0], c) {
				srows = append(srows, append(specializeFields(r[0], c, len(ftys)), r[1:]...))
			}
		}
		sv := append(specializeFields(head, c, len(ftys)), v[1:]...)
		ok, w := fc.useful(srows, sv, append(append([]*Type(nil), ftys...), tys[1:]...))
		if !ok {
			return false, nil
		}
		wd := &dpat{kind: c.kind, index: c.index, lo: c.lo, hi: c.hi, n: c.n, varLen: c.varLen, ty: t}
		wd.fields = w[:len(ftys)]
		return true, append([]*dpat{wd}, w[len(ftys):]...)
	}

	if head.kind != cWild {
		ctors, _ := fc.allCtors(t, append(heads, head))
		for _, c := range ctors {
			if covers(head, c) {
				if ok, w := specialize(c); ok {
					return true, w
				}
			}
		}
		if head.kind == cOpaque {
			// Opaque literals: useful unless an identical literal or a wildcard precedes.
			for _, r := range rows {
				if r[0].kind == cWild || r[0].kind == cOpaque && r[0].lit == head.lit {
					return fc.useful(nil, v[1:], tys[1:])
				}
			}
			return true, append([]*dpat{head}, v[1:]...)
		}
		return false, nil
	}

	ctors, complete := fc.allCtors(t, heads)
	var missing []ctor
	if complete {
		for _, c := range ctors {
			seen := false
			for _, h := range heads {
				if h.kind != cWild && covers(h, c) {
					seen = true
				}
			}
			if !seen {
				missing = append(missing, c)
			}
		}
	}
	if complete && len(missing) == 0 {
		for _, c := range ctors {
			if ok, w := specialize(c); ok {
				return true, w
			}
		}
		return false, nil
	}
	// Default matrix: rows whose head is a wildcard.
	var drows [][]*dpat
	for _, r := range rows {
		if r[0].kind == cWild {
			drows = append(drows, r[1:])
		}
	}
	ok, w := fc.useful(drows, v[1:], tys[1:])
	if !ok {
		return false, nil
	}
	var wd *dpat
	if len(missing) > 0 {
		c := missing[0]
		wd = &dpat{kind: c.kind, index: c.index, lo: c.lo, hi: c.hi, n: c.n, varLen: c.varLen, ty: t}
		for range fc.arity(c, t) {
			wd.fields = append(wd.fields, wild)
		}
	} else {
		wd = wild
	}
	return true, append([]*dpat{wd}, w...)
}

// showPat renders a witness pattern for messages.
func (fc *fnCtx) showPat(d *dpat, t *Type) string {
	if d == nil || d.kind == cWild {
		return "_"
	}
	if d.ty != nil {
		t = d.ty
	}
	fields := func(tys []*Type) []string {
		var out []string
		for i, f := range d.fields {
			var ft *Type
			if i < len(tys) {
				ft = tys[i]
			}
			out = append(out, fc.showPat(f, ft))
		}
		return out
	}
	c := ctor{kind: d.kind, index: d.index, lo: d.lo, hi: d.hi, n: d.n, varLen: d.varLen}
	switch d.kind {
	case cBool:
		if d.index == 1 {
			return "true"
		}
		return "false"
	case cRange:
		show := func(v int64) string {
			if t != nil && t.Kind == KChar {
				return strconv.QuoteRune(rune(v))
			}
			return strconv.FormatInt(v, 10)
		}
		if d.lo == d.hi {
			return show(d.lo)
		}
		lo, hi, _ := domain(t)
		if d.hi == hi && d.lo == lo {
			return "_"
		}
		l, h := show(d.lo), show(d.hi)
		if t.Kind == KInt && d.lo == lo {
			l = fmt.Sprintf("%s::MIN", t)
		}
		if t.Kind == KInt && d.hi == hi {
			h = fmt.Sprintf("%s::MAX", t)
		}
		return l + "..=" + h
	case cSlice:
		fs := fields(fc.arity(c, t))
		if d.varLen {
			fs = append(fs, "..")
		}
		return "[" + strings.Join(fs, ", ") + "]"
	case cVariant:
		v := fc.c.adt(t.Def).Variants[d.index]
		name := t.Def.Name + "::" + v.Def.Name
		if lang := fc.langName(t.Def); lang == "Option" || lang == "Result" {
			name = v.Def.Name
		}
		fs := fields(fc.arity(c, t))
		switch v.Kind {
		case syntax.StructUnit:
			return name
		case syntax.StructTuple:
			return name + "(" + strings.Join(fs, ", ") + ")"
		}
		return name + " { .. }"
	case cSingle:
		fs := fields(fc.arity(c, t))
		switch t.Kind {
		case KRef:
			return "&" + fs[0]
		case KTuple:
			return "(" + strings.Join(fs, ", ") + ")"
		case KArray:
			return "[" + strings.Join(fs, ", ") + "]"
		case KAdt:
			if fc.c.adt(t.Def).Kind == syntax.StructTuple {
				return t.Def.Name + "(" + strings.Join(fs, ", ") + ")"
			}
			return t.Def.Name + " { .. }"
		}
	case cOpaque:
		return "_"
	}
	return "_"
}
