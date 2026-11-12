package types

import (
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

// BodyInfo holds the type-checking results of one function, constant or
// static body. All types are fully resolved.
type BodyInfo struct {
	Owner  *resolve.Def
	Types  map[syntax.Expr]*Type
	Locals map[*resolve.Local]*Type
	// Callees records what a path expression or method call refers to.
	Callees map[syntax.Expr]*Callee
	// Fields maps field expressions to the field index (after autoderef).
	Fields map[*syntax.FieldExpr]int
	Adjust map[syntax.Expr]*Adjust
	Pats   map[syntax.Pat]*Type
	// RefBindings lists bindings that bind by reference because a
	// non-reference pattern matched a reference (match ergonomics).
	RefBindings map[*syntax.IdentPat]bool
	// Templates records whether a template string is a String or Arguments.
	Templates map[*syntax.TemplateExpr]*Type

	// Recording order of Types, Locals and Pats (for deterministic
	// diagnostics).
	typeOrder  []syntax.Expr
	localOrder []*resolve.Local
	patOrder   []syntax.Pat
}

// Callee is the resolved target of a path or method call.
type Callee struct {
	Def  *resolve.Def
	Args []*Type // generic arguments for all generics in scope of Def
	// Self is the self type for trait items (Trait::f, methods via traits).
	Self *Type
	// Recv describes how a method call's receiver is adjusted.
	Recv *Adjust
}

// Adjust records implicit conversions applied to an expression.
type Adjust struct {
	Derefs  int  // number of dereferences (builtin or Deref::deref)
	Autoref int  // 0: none, 1: &, 2: &mut
	Unsize  bool // [T; N] to [T] (after autoref)
	ToDyn   *Type
}

type loopCtx struct {
	node     syntax.Node
	isLoop   bool
	breakTy  *Type
	hasBreak bool
}

// fnCtx type-checks one body.
type fnCtx struct {
	c      *collector
	info   *Info
	in     *infer
	file   *syntax.File
	owner  *resolve.Def
	module *resolve.Module
	env    *env
	preds  []*TraitRef
	traits []*resolve.Def
	body   *BodyInfo

	ret         *Type // return type of the innermost function or closure
	loops       []*loopCtx
	pending     []*obligation
	varOrigin   map[int]*TypeParam
	curPos      syntax.Pos
	unsafe      int
	inConst     bool
	opaques     map[*Type]*Type // opaque return types -> hidden-type variables
	opaqueOrder []*Type
	deferred    []func()        // checks that need resolved types (exhaustiveness)
	errsAtStart int             // to suppress follow-on "annotations needed" errors
}

func (fc *fnCtx) errorf(pos syntax.Pos, format string, args ...any) {
	fc.c.errorf(fc.file, pos, format, args...)
}

// checkBodies type-checks every function, constant and static body.
func (c *collector) checkBodies() {
	c.info.Bodies = map[*resolve.Def]*BodyInfo{}
	c.info.Hidden = map[*Type]*Type{}
	for _, d := range c.info.Res.Defs {
		switch n := d.Node.(type) {
		case *syntax.FnDecl:
			if n.Body != nil && (d.Kind == resolve.DefFn || d.Kind == resolve.DefAssocFn) {
				c.checkFnBody(d, n)
			}
		case *syntax.ConstDecl:
			if n.Value != nil {
				c.checkConstBody(d, n.Value)
			}
		case *syntax.StaticDecl:
			if n.Value != nil {
				c.checkConstBody(d, n.Value)
			}
		}
	}
}

func (c *collector) newFnCtx(d *resolve.Def) *fnCtx {
	fc := &fnCtx{
		c:         c,
		info:      c.info,
		in:        &infer{ctx: c.info.Ctx},
		file:      d.File,
		owner:     d,
		module:    resolve.ModuleOf(d),
		env:       c.envFor(d),
		varOrigin: map[int]*TypeParam{},
		opaques:   map[*Type]*Type{},
		body: &BodyInfo{
			Owner:       d,
			Types:       map[syntax.Expr]*Type{},
			Locals:      map[*resolve.Local]*Type{},
			Callees:     map[syntax.Expr]*Callee{},
			Fields:      map[*syntax.FieldExpr]int{},
			Adjust:      map[syntax.Expr]*Adjust{},
			Pats:        map[syntax.Pat]*Type{},
			RefBindings: map[*syntax.IdentPat]bool{},
			Templates:   map[*syntax.TemplateExpr]*Type{},
		},
	}
	if fc.module != nil {
		fc.traits = fc.module.TraitsInScope()
	}
	fc.errsAtStart = c.errs.Len()
	g := c.genericsOf(d)
	var preds []*TraitRef
	for _, p := range g.AllPreds() {
		preds = append(preds, p.Trait)
	}
	owner := d
	if isAssoc(d) {
		owner = d.Parent
	}
	if owner.Kind == resolve.DefTrait {
		preds = append(preds, c.selfTraitRef(owner))
	}
	fc.preds = c.elaborate(preds)
	return fc
}

func (c *collector) checkFnBody(d *resolve.Def, n *syntax.FnDecl) {
	fs := c.fnSig(d)
	fc := c.newFnCtx(d)
	fc.curPos = n.Body.Lbrace
	if n.Unsafe {
		fc.unsafe++
	}
	fc.inConst = n.Const
	ret := fs.Ret
	// Inside the defining function an opaque return type stands for a
	// hidden type that the body determines.
	for _, o := range fs.Opaques {
		fc.opaques[o] = fc.in.fresh()
		fc.opaqueOrder = append(fc.opaqueOrder, o)
	}
	ret = fc.replaceOpaques(ret)
	fc.ret = fc.normalize(ret)
	if l := c.info.Res.SelfLocals[n]; l != nil && fs.Self != nil {
		fc.setLocal(l, fc.normalize(fs.Self))
	}
	for i, p := range n.Params {
		if i < len(fs.Params) {
			fc.checkPat(p.Pat, fc.normalize(fs.Params[i]), false)
		}
	}
	t := fc.checkBlock(n.Body, fc.ret)
	fc.coerceTo(n.Body, t, fc.ret, n.Body.Rbrace)
	for _, o := range fc.opaqueOrder {
		v := fc.opaques[o]
		for _, b := range o.Bounds {
			fc.require(&TraitRef{Trait: b.Trait, Self: v, Args: b.Args, Bindings: b.Bindings}, n.Name.NamePos)
		}
	}
	fc.finish()
	for _, o := range fc.opaqueOrder {
		c.info.Hidden[o] = fc.in.resolve(fc.opaques[o])
	}
}

func (c *collector) checkConstBody(d *resolve.Def, x syntax.Expr) {
	ci := c.constInfo(d)
	fc := c.newFnCtx(d)
	fc.inConst = true
	fc.curPos = x.Pos()
	fc.ret = ci.Type
	fc.check(x, ci.Type)
	fc.finish()
}

// replaceOpaques substitutes the hidden-type variables for opaque types.
func (fc *fnCtx) replaceOpaques(t *Type) *Type {
	if len(fc.opaques) == 0 {
		return t
	}
	for o, v := range fc.opaques {
		t = replaceType(fc.in.ctx, t, o, v)
	}
	return t
}

// replaceType replaces every occurrence of old in t by new.
func replaceType(ctx *Ctx, t, old, new *Type) *Type {
	if t == old {
		return new
	}
	switch t.Kind {
	case KRef:
		return ctx.Ref(t.Mut, replaceType(ctx, t.Elem, old, new))
	case KTuple:
		out := make([]*Type, len(t.Elems))
		for i, e := range t.Elems {
			out[i] = replaceType(ctx, e, old, new)
		}
		return ctx.Tuple(out)
	case KAdt:
		out := make([]*Type, len(t.Args))
		for i, e := range t.Args {
			out[i] = replaceType(ctx, e, old, new)
		}
		return ctx.Adt(t.Def, out)
	}
	return t
}

// finish solves the remaining obligations, applies literal defaults and
// stores fully resolved results.
func (fc *fnCtx) finish() {
	fc.solvePending(false)
	// Literal fallback: integers default to i32, floats to f64.
	for _, v := range fc.in.vars {
		if v.bound != nil {
			continue
		}
		switch v.kind {
		case varInt:
			fc.in.bind(v, fc.in.ctx.Ints[I32])
		case varFloat:
			fc.in.bind(v, fc.in.ctx.F64)
		}
	}
	// A function whose body never returns (todo(), loop {}) leaves its
	// opaque return type undetermined; it is ! then.
	for _, o := range fc.opaqueOrder {
		if r := fc.in.shallow(fc.opaques[o]); r.Kind == KInfer {
			fc.in.unify(r, fc.in.ctx.Never)
		}
	}
	fc.solvePending(false)
	tainted := fc.c.errs.Len() > fc.errsAtStart
	if tainted {
		// Earlier errors leave types unknown; reporting the resulting
		// ambiguities would only add noise.
		fc.c.quiet++
	}
	fc.solvePending(true)
	if !tainted {
		for _, f := range fc.deferred {
			f()
		}
	}
	reported := tainted
	res := func(t *Type, pos syntax.Pos) *Type {
		r := fc.in.resolve(t)
		if hasInfer(r) {
			if !reported {
				fc.errorf(pos, "type annotations needed: cannot infer the type %s", r)
				reported = true
			}
			r = fc.in.ctx.Err
		}
		return r
	}
	for _, x := range fc.body.typeOrder {
		fc.body.Types[x] = res(fc.body.Types[x], x.Pos())
	}
	for _, l := range fc.body.localOrder {
		fc.body.Locals[l] = res(fc.body.Locals[l], l.Pos)
	}
	for _, p := range fc.body.patOrder {
		fc.body.Pats[p] = res(fc.body.Pats[p], p.Pos())
	}
	for x, t := range fc.body.Templates {
		fc.body.Templates[x] = fc.in.resolve(t)
	}
	for x, cl := range fc.body.Callees {
		for i, a := range cl.Args {
			cl.Args[i] = res(a, x.Pos())
		}
		if cl.Self != nil {
			cl.Self = res(cl.Self, x.Pos())
		}
	}
	for _, a := range fc.body.Adjust {
		if a.ToDyn != nil {
			a.ToDyn = fc.in.resolve(a.ToDyn)
		}
	}
	if tainted {
		fc.c.quiet--
	}
	fc.info.Bodies[fc.owner] = fc.body
}

// record stores the type of an expression.
func (fc *fnCtx) record(x syntax.Expr, t *Type) *Type {
	if _, ok := fc.body.Types[x]; !ok {
		fc.body.typeOrder = append(fc.body.typeOrder, x)
	}
	fc.body.Types[x] = t
	return t
}

// setLocal stores the type of a local variable.
func (fc *fnCtx) setLocal(l *resolve.Local, t *Type) {
	if _, ok := fc.body.Locals[l]; !ok {
		fc.body.localOrder = append(fc.body.localOrder, l)
	}
	fc.body.Locals[l] = t
}

// setPat stores the type of a pattern.
func (fc *fnCtx) setPat(p syntax.Pat, t *Type) {
	if _, ok := fc.body.Pats[p]; !ok {
		fc.body.patOrder = append(fc.body.patOrder, p)
	}
	fc.body.Pats[p] = t
}

// lower lowers a type written inside the body (let annotations, casts,
// turbofish, closure parameters) and normalizes it.
func (fc *fnCtx) lower(t syntax.Type) *Type {
	e := fc.env.with(func(n *env) { n.implArg, n.implRet, n.implNote = nil, nil, "function bodies" })
	return fc.normalize(fc.c.lowerType(e, t))
}

// lowerOrInfer lowers t, turning `_` into a fresh variable.
func (fc *fnCtx) lowerOrInfer(t syntax.Type) *Type {
	if t == nil {
		return fc.in.fresh()
	}
	if _, ok := t.(*syntax.InferType); ok {
		return fc.in.fresh()
	}
	return fc.lower(t)
}

// mismatch reports a mismatch between expected and found.
func (fc *fnCtx) mismatch(pos syntax.Pos, want, got *Type) {
	w, g := fc.in.resolve(want), fc.in.resolve(got)
	if w.Kind == KError || g.Kind == KError {
		return
	}
	fc.errorf(pos, "mismatched types: expected %s, found %s", fc.show(w), fc.show(g))
}

// show renders a type for messages, writing unresolved literal variables
// as {integer} and {float}.
func (fc *fnCtx) show(t *Type) string {
	s := fc.in.resolve(t).String()
	if !strings.Contains(s, "?") {
		return s
	}
	for i := len(fc.in.vars) - 1; i >= 0; i-- {
		v := fc.in.vars[i]
		name := "?" + itoa(i)
		switch {
		case v.bound != nil:
			continue
		case v.kind == varInt:
			s = replaceVar(s, name, "{integer}")
		case v.kind == varFloat:
			s = replaceVar(s, name, "{float}")
		}
	}
	return s
}

// replaceVar replaces the variable name (not a longer one with the same
// prefix) in s.
func replaceVar(s, name, with string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, name)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(name)
		if end < len(s) && s[end] >= '0' && s[end] <= '9' {
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		b.WriteString(s[:i])
		b.WriteString(with)
		s = s[end:]
	}
}

// elaborate adds the predicates implied by preds: supertraits, and the
// bounds declared on associated types (I: IntoIterator implies
// <I as IntoIterator>::IntoIter: Iterator<Item = I::Item>).
func (c *collector) elaborate(preds []*TraitRef) []*TraitRef {
	var out []*TraitRef
	seen := map[string]bool{}
	var add func(tr *TraitRef, depth int)
	add = func(tr *TraitRef, depth int) {
		c.walkSupertraits(tr, func(s *TraitRef) {
			key := traitKey(s)
			if seen[key] {
				return
			}
			seen[key] = true
			out = append(out, s)
			if depth > 4 {
				return
			}
			ti := c.trait(s.Trait)
			if ti == nil {
				return
			}
			m := paramMap(ti.Generics, s.Args)
			for _, it := range ti.Order {
				if it.Kind != resolve.DefAssocType {
					continue
				}
				ai := c.assocType(it)
				if ai == nil || len(ai.Bounds) == 0 {
					continue
				}
				self := c.info.Ctx.Proj(it, &TraitRef{Trait: s.Trait, Self: s.Self, Args: s.Args})
				for _, b := range ai.Bounds {
					nb := SubstTraitRef(c.info.Ctx, b, m, s.Self)
					nb.Self = self
					add(nb, depth+1)
				}
			}
		})
	}
	for _, p := range preds {
		add(p, 0)
	}
	return out
}
