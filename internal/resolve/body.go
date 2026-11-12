package resolve

import (
	"sort"
	"strings"

	"navi/internal/syntax"
)

// rib is one lexical scope level: generic parameters (types) and local
// variables (values).
type rib struct {
	types  map[string]Res
	values map[string]*Local
}

func newRib() *rib {
	return &rib{types: map[string]Res{}, values: map[string]*Local{}}
}

type label struct {
	name string
	node syntax.Node
}

// scope is the lexical context while walking items and bodies.
type scope struct {
	module *Module
	file   *syntax.File
	ribs   []*rib
	self   *Res        // Self inside impls and traits
	fn     syntax.Node // innermost function or closure
	labels []label
	loops  int
}

func (s *scope) push() *rib {
	rb := newRib()
	s.ribs = append(s.ribs, rb)
	return rb
}

func (s *scope) pop() { s.ribs = s.ribs[:len(s.ribs)-1] }

func (s *scope) top() *rib { return s.ribs[len(s.ribs)-1] }

// fresh returns a scope for an item nested in m: items never see the locals,
// generics or Self of their surroundings.
func freshScope(m *Module) *scope {
	return &scope{module: m, file: m.file}
}

// resolveModule resolves every item of m and of its child modules.
func (r *resolver) resolveModule(m *Module) {
	s := freshScope(m)
	for _, it := range m.items {
		r.resolveItem(s, it)
	}
}

func (r *resolver) resolveItem(s *scope, it syntax.Item) {
	switch it := it.(type) {
	case *syntax.FnDecl:
		r.resolveFn(s, it)
	case *syntax.StructDecl:
		s.push()
		r.genericParams(s, it.Generics)
		for _, f := range it.Fields {
			r.resolveType(s, f.Type)
		}
		s.pop()
	case *syntax.EnumDecl:
		s.push()
		r.genericParams(s, it.Generics)
		for _, v := range it.Variants {
			for _, f := range v.Fields {
				r.resolveType(s, f.Type)
			}
			if v.Discriminant != nil {
				r.constExpr(s, v.Discriminant, v)
			}
		}
		s.pop()
	case *syntax.TypeAlias:
		s.push()
		r.genericParams(s, it.Generics)
		for _, b := range it.Bounds {
			r.resolveBound(s, b)
		}
		if it.Type != nil {
			r.resolveType(s, it.Type)
		}
		s.pop()
	case *syntax.ConstDecl:
		r.resolveType(s, it.Type)
		if it.Value != nil {
			r.constExpr(s, it.Value, it)
		}
	case *syntax.StaticDecl:
		r.resolveType(s, it.Type)
		if it.Value != nil {
			r.constExpr(s, it.Value, it)
		}
	case *syntax.ImplDecl:
		d := r.info.DefOf[it]
		s.push()
		r.genericParams(s, it.Generics)
		if it.Trait != nil {
			r.resolveBound(s, it.Trait)
		}
		r.resolveType(s, it.Type)
		old := s.self
		s.self = &Res{Kind: ResSelfTy, Def: d}
		for _, ai := range it.Items {
			if r.cfgEnabledQuiet(itemBase(ai).Attrs) {
				r.resolveItem(s, ai)
			}
		}
		s.self = old
		s.pop()
	case *syntax.TraitDecl:
		d := r.info.DefOf[it]
		s.push()
		old := s.self
		s.self = &Res{Kind: ResSelfTy, Def: d}
		r.genericParams(s, it.Generics)
		for _, b := range it.Supers {
			r.resolveBound(s, b)
		}
		for _, ai := range it.Items {
			if r.cfgEnabledQuiet(itemBase(ai).Attrs) {
				r.resolveItem(s, ai)
			}
		}
		s.self = old
		s.pop()
	case *syntax.ModDecl:
		if d := r.info.DefOf[it]; d != nil && d.Module != nil {
			r.resolveModule(d.Module)
		}
	case *syntax.ExternBlock:
		for _, ei := range it.Items {
			if r.cfgEnabledQuiet(itemBase(ei).Attrs) {
				r.resolveItem(s, ei)
			}
		}
	case *syntax.UseDecl:
		// resolved by resolveImports
	}
}

// constExpr resolves an expression evaluated at compile time (const and
// static initializers, array lengths, discriminants).
func (r *resolver) constExpr(s *scope, x syntax.Expr, owner syntax.Node) {
	oldFn, oldLabels, oldLoops := s.fn, s.labels, s.loops
	s.fn, s.labels, s.loops = owner, nil, 0
	s.push()
	r.expr(s, x)
	s.pop()
	s.fn, s.labels, s.loops = oldFn, oldLabels, oldLoops
}

// genericParams defines the generic parameters in the current (top) rib and
// resolves their bounds and where clauses.
func (r *resolver) genericParams(s *scope, g *syntax.Generics) {
	if g == nil {
		return
	}
	top := s.top()
	for _, gp := range g.Params {
		if _, dup := top.types[gp.Name.Value]; dup {
			r.errorf(s.file, gp.Name.NamePos, "the name %s is already used for a generic parameter", gp.Name.Value)
			continue
		}
		d := r.newDef(DefTypeParam, gp.Name.Value, s.file, gp, gp.Name.NamePos, nil, s.module.Crate)
		top.types[gp.Name.Value] = Res{Kind: ResDef, Def: d}
	}
	sawDefault := false
	for _, gp := range g.Params {
		for _, b := range gp.Bounds {
			r.resolveBound(s, b)
		}
		if gp.Default != nil {
			sawDefault = true
			r.resolveType(s, gp.Default)
		} else if sawDefault {
			r.errorf(s.file, gp.Name.NamePos, "generic parameters with a default must come after those without one")
		}
	}
	for _, wp := range g.Where {
		r.resolveType(s, wp.Type)
		for _, b := range wp.Bounds {
			r.resolveBound(s, b)
		}
	}
}

func (r *resolver) resolveFn(s *scope, fn *syntax.FnDecl) {
	s.push()
	r.genericParams(s, fn.Generics)
	for _, p := range fn.Params {
		r.resolveType(s, p.Type)
	}
	if fn.Result != nil {
		r.resolveType(s, fn.Result)
	}
	if fn.Self != nil && s.self == nil {
		r.errorf(s.file, fn.Self.SelfPos, "self parameter is only allowed in impl and trait methods")
	}
	if fn.Body != nil {
		oldFn, oldLabels, oldLoops := s.fn, s.labels, s.loops
		s.fn, s.labels, s.loops = fn, nil, 0
		params := s.push()
		if fn.Self != nil {
			l := &Local{Name: "self", Pos: fn.Self.SelfPos, Mut: fn.Self.Mut && !fn.Self.Ref, Fn: fn}
			params.values["self"] = l
			r.info.SelfLocals[fn] = l
		}
		names := map[string]*Local{}
		for _, p := range fn.Params {
			r.pat(s, p.Pat, names, nil)
		}
		for n, l := range names {
			params.values[n] = l
		}
		r.block(s, fn.Body)
		s.pop()
		s.fn, s.labels, s.loops = oldFn, oldLabels, oldLoops
	}
	s.pop()
}

// ---------------------------------------------------------------------------
// Paths

// lexical looks up a single name: locals and generic parameters (innermost
// first), then the module and its enclosing block scopes, then the prelude,
// then (for types) extern crates and primitive types.
func (r *resolver) lexical(s *scope, name string, ns Namespace, locals bool) (Res, bool) {
	if locals {
		for i := len(s.ribs) - 1; i >= 0; i-- {
			if ns == ValueNS {
				if l := s.ribs[i].values[name]; l != nil {
					return Res{Kind: ResLocal, Local: l}, true
				}
			} else if res, ok := s.ribs[i].types[name]; ok {
				return res, true
			}
		}
	}
	for m := s.module; m != nil; m = m.Parent {
		b, st := r.lookupIn(m, name, ns, s.module)
		switch st {
		case found:
			return b.Res, true
		case ambiguous:
			return Res{}, false
		}
		if !m.Anonymous {
			break
		}
	}
	if p := s.module.Crate.prelude; p != nil {
		if b, st := r.lookupIn(p, name, ns, s.module); st == found {
			return b.Res, true
		}
	}
	if ns == TypeNS {
		if c := s.module.Crate.Deps[name]; c != nil && c.Root != nil {
			return Res{Kind: ResCrate, Def: c.Root.Def}, true
		}
		if s.module.Crate.IsStd && name == "std" {
			return Res{Kind: ResCrate, Def: s.module.Crate.Root.Def}, true
		}
		if primitives[name] {
			return Res{Kind: ResPrim, Prim: name}, true
		}
	}
	return Res{}, false
}

// isTypeLike reports whether res names a type, so that further path
// segments are associated items.
func isTypeLike(res Res) bool {
	switch res.Kind {
	case ResPrim, ResSelfTy, ResQualified:
		return true
	case ResDef:
		switch res.Def.Kind {
		case DefStruct, DefEnum, DefTrait, DefTypeAlias, DefTypeParam, DefAssocType:
			return true
		}
	}
	return false
}

func describe(res Res) string {
	switch res.Kind {
	case ResLocal:
		return "local variable " + res.Local.Name
	case ResPrim:
		return "primitive type " + res.Prim
	case ResSelfTy:
		return "Self"
	case ResQualified:
		return "qualified path"
	case ResCrate:
		return "crate " + res.Def.Name
	case ResDef:
		return res.Def.Kind.String() + " " + res.Def.Name
	}
	return "unresolved name"
}

// resolvePath resolves a path in namespace ns and records the result.
func (r *resolver) resolvePath(s *scope, p *syntax.Path, ns Namespace) Res {
	res := r.resolvePathInner(s, p, ns)
	r.info.Paths[p] = res
	return res
}

func (r *resolver) resolvePathInner(s *scope, p *syntax.Path, ns Namespace) Res {
	for _, seg := range p.Segments {
		if seg.Args != nil {
			for _, t := range seg.Args.Args {
				r.resolveType(s, t)
			}
			for _, b := range seg.Args.Bindings {
				r.resolveType(s, b.Type)
			}
		}
	}
	if p.QSelf != nil {
		r.resolveType(s, p.QSelf.Type)
		if p.QSelf.Trait != nil {
			r.resolveBound(s, p.QSelf.Trait)
		}
		return Res{Kind: ResQualified, Rest: p.Segments}
	}

	segs := p.Segments
	single := len(segs) == 1
	first := segs[0].Name
	firstNS := TypeNS
	if single {
		firstNS = ns
	}
	var res Res
	i := 1
	switch first.Value {
	case "Self":
		if s.self == nil {
			r.errorf(s.file, first.NamePos, "Self is only available in impls and traits")
			return Res{}
		}
		res = *s.self
	case "self":
		if single && ns == ValueNS {
			if rs, ok := r.lexical(s, "self", ValueNS, true); ok && rs.Kind == ResLocal {
				return rs
			}
			r.errorf(s.file, first.NamePos, "self value is only available in methods with a self parameter")
			return Res{}
		}
		fallthrough
	case "crate", "super":
		j := 0
		for j < len(segs) && isModuleKeyword(segs[j].Name.Value) {
			j++
		}
		names := make([]*syntax.Name, j)
		for k := 0; k < j; k++ {
			names[k] = segs[k].Name
		}
		m, perr := r.modulePath(s.module, names)
		if perr != nil {
			r.errorf(s.file, perr.pos, "%s", perr.msg)
			return Res{}
		}
		res = Res{Kind: ResDef, Def: m.Def}
		i = j
	default:
		rs, ok := r.lexical(s, first.Value, firstNS, true)
		if !ok {
			r.notFound(s, first, firstNS, single)
			return Res{}
		}
		res = rs
	}

	for ; i < len(segs); i++ {
		seg := segs[i].Name
		if m := moduleOf(res); m != nil {
			nsHere := TypeNS
			if i == len(segs)-1 {
				nsHere = ns
			}
			b, st := r.lookupIn(m, seg.Value, nsHere, s.module)
			switch st {
			case found:
				res = b.Res
				continue
			case private:
				r.errorf(s.file, seg.NamePos, "%s is private in %s", seg.Value, m.Path())
			case ambiguous:
				r.errorf(s.file, seg.NamePos, "%s is ambiguous (imported by more than one glob)", seg.Value)
			default:
				other := TypeNS
				if nsHere == TypeNS {
					other = ValueNS
				}
				if b, st := r.lookupIn(m, seg.Value, other, s.module); st == found {
					r.errorf(s.file, seg.NamePos, "expected %s, found %s", nsWord(nsHere), describe(b.Res))
				} else {
					r.errorf(s.file, seg.NamePos, "cannot find %s in %s", seg.Value, m.Path())
				}
			}
			return Res{}
		}
		if isTypeLike(res) {
			res.Rest = segs[i:]
			return res
		}
		r.errorf(s.file, seg.NamePos, "%s is not a module or type, so it has no item %s", describe(res), seg.Value)
		return Res{}
	}
	return res
}

func nsWord(ns Namespace) string {
	if ns == TypeNS {
		return "type"
	}
	return "value"
}

func (r *resolver) notFound(s *scope, name *syntax.Name, ns Namespace, single bool) {
	if !single {
		r.errorf(s.file, name.NamePos, "cannot find module or type %s in this scope", name.Value)
		return
	}
	other := TypeNS
	if ns == TypeNS {
		other = ValueNS
	}
	if res, ok := r.lexical(s, name.Value, other, true); ok {
		r.errorf(s.file, name.NamePos, "expected %s, found %s", nsWord(ns), describe(res))
		return
	}
	r.errorf(s.file, name.NamePos, "cannot find %s %s in this scope", nsWord(ns), name.Value)
}

// resolveType resolves the names in a type expression.
func (r *resolver) resolveType(s *scope, t syntax.Type) {
	switch t := t.(type) {
	case *syntax.PathType:
		res := r.resolvePath(s, t.Path, TypeNS)
		if res.Kind == ResErr || res.Rest != nil {
			return
		}
		if len(t.Path.Segments) == 1 && moduleOf(res) != nil && primitives[t.Path.Segments[0].Name.Value] {
			// A module named like a primitive (std::str) does not hide the type.
			r.info.Paths[t.Path] = Res{Kind: ResPrim, Prim: t.Path.Segments[0].Name.Value}
			return
		}
		if !isTypeLike(res) {
			r.errorf(s.file, t.Pos(), "expected type, found %s", describe(res))
		} else if res.Kind == ResDef && res.Def.Kind == DefTrait {
			r.errorf(s.file, t.Pos(), "trait %s used as a type; write dyn %s or impl %s", res.Def.Name, res.Def.Name, res.Def.Name)
		}
	case *syntax.RefType:
		r.resolveType(s, t.Elem)
	case *syntax.PtrType:
		r.resolveType(s, t.Elem)
	case *syntax.SliceType:
		r.resolveType(s, t.Elem)
	case *syntax.ArrayType:
		r.resolveType(s, t.Elem)
		r.constExpr(s, t.Len, t)
	case *syntax.TupleType:
		for _, e := range t.Elems {
			r.resolveType(s, e)
		}
	case *syntax.FnType:
		for _, p := range t.Params {
			r.resolveType(s, p)
		}
		if t.Result != nil {
			r.resolveType(s, t.Result)
		}
	case *syntax.DynType:
		for _, b := range t.Bounds {
			r.resolveBound(s, b)
		}
	case *syntax.ImplType:
		for _, b := range t.Bounds {
			r.resolveBound(s, b)
		}
	}
}

// resolveBound resolves a trait bound, which must name a trait.
func (r *resolver) resolveBound(s *scope, p *syntax.Path) {
	res := r.resolvePath(s, p, TypeNS)
	if res.Kind == ResErr {
		return
	}
	if res.Kind != ResDef || res.Def.Kind != DefTrait || res.Rest != nil {
		r.errorf(s.file, p.Pos(), "expected trait, found %s", describe(res))
	}
}

// ---------------------------------------------------------------------------
// Blocks and statements

func (r *resolver) block(s *scope, b *syntax.BlockExpr) {
	oldModule := s.module
	if m := r.blockModule(s, b); m != nil {
		s.module = m
	}
	s.push()
	for _, st := range b.Stmts {
		switch st := st.(type) {
		case *syntax.LetStmt:
			if st.Type != nil {
				r.resolveType(s, st.Type)
			}
			if st.Init != nil {
				r.expr(s, st.Init)
			}
			if st.Else != nil {
				r.block(s, st.Else)
			}
			r.bindPattern(s, st.Pat)
		case *syntax.ExprStmt:
			r.expr(s, st.X)
		case *syntax.DeferStmt:
			r.expr(s, st.X)
		case *syntax.ItemStmt, *syntax.EmptyStmt:
		}
	}
	if b.Tail != nil {
		r.expr(s, b.Tail)
	}
	s.pop()
	s.module = oldModule
}

// blockModule creates the anonymous module for a block that declares items,
// resolves its imports and items, and returns it (nil if the block has no
// items).
func (r *resolver) blockModule(s *scope, b *syntax.BlockExpr) *Module {
	hasItems := false
	for _, st := range b.Stmts {
		if _, ok := st.(*syntax.ItemStmt); ok {
			hasItems = true
			break
		}
	}
	if !hasItems {
		return nil
	}
	m := newModule(s.module.Crate, s.module, "")
	m.Anonymous = true
	m.Dir = s.module.Dir
	m.Def = s.module.nearestNamed().Def
	r.blockMods[b] = m
	firstImport := len(r.imports)
	for _, st := range b.Stmts {
		if is, ok := st.(*syntax.ItemStmt); ok {
			if _, isMod := is.Item.(*syntax.ModDecl); isMod && !is.Item.(*syntax.ModDecl).Inline {
				r.errorf(s.file, is.Item.Pos(), "mod declarations with a file are not allowed inside functions")
				continue
			}
			r.collectItem(m, s.file, is.Item, nil)
		}
	}
	m.file = s.file
	// Resolve the block's own imports; everything else is already resolved.
	local := r.imports[firstImport:]
	for changed := true; changed; {
		changed = false
		for _, imp := range local {
			if imp.Glob {
				changed = r.propagateGlob(imp) || changed
			} else if !imp.done && r.trySingleImport(imp) {
				imp.done = true
				changed = true
			}
		}
	}
	for _, imp := range local {
		if !imp.Glob && !imp.done {
			r.reportImport(imp)
		}
	}
	r.resolveModule(m)
	return m
}

// ---------------------------------------------------------------------------
// Expressions

func (r *resolver) expr(s *scope, x syntax.Expr) {
	switch x := x.(type) {
	case nil, *syntax.LitExpr, *syntax.BadExpr:
	case *syntax.TemplateExpr:
		for _, p := range x.Parts {
			if p.X != nil {
				r.expr(s, p.X)
			}
		}
	case *syntax.PathExpr:
		res := r.resolvePath(s, x.Path, ValueNS)
		r.checkValue(s, x.Path, res)
	case *syntax.UnaryExpr:
		r.expr(s, x.X)
	case *syntax.RefExpr:
		r.expr(s, x.X)
	case *syntax.BinaryExpr:
		r.expr(s, x.X)
		r.expr(s, x.Y)
	case *syntax.AssignExpr:
		r.expr(s, x.X)
		r.expr(s, x.Y)
	case *syntax.CastExpr:
		r.expr(s, x.X)
		r.resolveType(s, x.Type)
	case *syntax.RangeExpr:
		r.expr(s, x.Lo)
		r.expr(s, x.Hi)
	case *syntax.CallExpr:
		r.expr(s, x.Fun)
		for _, a := range x.Args {
			r.expr(s, a)
		}
	case *syntax.MethodCallExpr:
		r.expr(s, x.Recv)
		if x.TypeArgs != nil {
			for _, t := range x.TypeArgs.Args {
				r.resolveType(s, t)
			}
		}
		for _, a := range x.Args {
			r.expr(s, a)
		}
	case *syntax.FieldExpr:
		r.expr(s, x.X)
	case *syntax.IndexExpr:
		r.expr(s, x.X)
		r.expr(s, x.Index)
	case *syntax.TryExpr:
		r.expr(s, x.X)
	case *syntax.StructLit:
		res := r.resolvePath(s, x.Path, TypeNS)
		r.checkStructPath(s, x.Path, res)
		for _, f := range x.Fields {
			r.expr(s, f.Value)
		}
		r.expr(s, x.Base)
	case *syntax.TupleExpr:
		for _, e := range x.Elems {
			r.expr(s, e)
		}
	case *syntax.ParenExpr:
		r.expr(s, x.X)
	case *syntax.ArrayExpr:
		for _, e := range x.Elems {
			r.expr(s, e)
		}
	case *syntax.ArrayRepeatExpr:
		r.expr(s, x.Value)
		r.expr(s, x.Count)
	case *syntax.BlockExpr:
		r.block(s, x)
	case *syntax.IfExpr:
		s.push()
		r.cond(s, x.Cond)
		r.block(s, x.Then)
		s.pop()
		r.expr(s, x.Else)
	case *syntax.LetExpr:
		r.errorf(s.file, x.Let, "let expressions are only allowed as the condition of if and while")
		r.expr(s, x.X)
	case *syntax.MatchExpr:
		r.expr(s, x.X)
		for _, a := range x.Arms {
			s.push()
			r.bindPattern(s, a.Pat)
			r.expr(s, a.Guard)
			r.expr(s, a.Body)
			s.pop()
		}
	case *syntax.LoopExpr:
		r.loop(s, x.Label, x, func() { r.block(s, x.Body) })
	case *syntax.WhileExpr:
		r.loop(s, x.Label, x, func() {
			s.push()
			r.cond(s, x.Cond)
			r.block(s, x.Body)
			s.pop()
		})
	case *syntax.ForExpr:
		r.expr(s, x.Iter)
		r.loop(s, x.Label, x, func() {
			s.push()
			r.bindPattern(s, x.Pat)
			r.block(s, x.Body)
			s.pop()
		})
	case *syntax.BreakExpr:
		r.jump(s, "break", x.Break, x.Label)
		r.expr(s, x.Value)
	case *syntax.ContinueExpr:
		r.jump(s, "continue", x.Continue, x.Label)
	case *syntax.ReturnExpr:
		r.expr(s, x.Value)
	case *syntax.ClosureExpr:
		oldFn, oldLabels, oldLoops := s.fn, s.labels, s.loops
		s.fn, s.labels, s.loops = x, nil, 0
		params := s.push()
		names := map[string]*Local{}
		for _, p := range x.Params {
			if p.Type != nil {
				r.resolveType(s, p.Type)
			}
			r.pat(s, p.Pat, names, nil)
		}
		for n, l := range names {
			params.values[n] = l
		}
		if x.Result != nil {
			r.resolveType(s, x.Result)
		}
		r.expr(s, x.Body)
		s.pop()
		s.fn, s.labels, s.loops = oldFn, oldLabels, oldLoops
	case *syntax.SelectExpr:
		for _, a := range x.Arms {
			r.expr(s, a.Op)
			s.push()
			if a.Pat != nil {
				r.bindPattern(s, a.Pat)
			}
			r.expr(s, a.Body)
			s.pop()
		}
	}
}

// cond resolves an if/while condition; `let pat = x` binds into the current
// (top) rib so that the body sees the bindings.
func (r *resolver) cond(s *scope, c syntax.Expr) {
	if l, ok := c.(*syntax.LetExpr); ok {
		r.expr(s, l.X)
		r.bindPattern(s, l.Pat)
		return
	}
	r.expr(s, c)
}

func (r *resolver) loop(s *scope, lbl *syntax.Name, node syntax.Node, body func()) {
	if lbl != nil {
		for _, l := range s.labels {
			if l.name == lbl.Value {
				r.errorf(s.file, lbl.NamePos, "label '%s shadows a label with the same name", lbl.Value)
			}
		}
		s.labels = append(s.labels, label{name: lbl.Value, node: node})
		r.info.Labels[lbl] = node
	}
	s.loops++
	body()
	s.loops--
	if lbl != nil {
		s.labels = s.labels[:len(s.labels)-1]
	}
}

func (r *resolver) jump(s *scope, kw string, pos syntax.Pos, lbl *syntax.Name) {
	if lbl != nil {
		for i := len(s.labels) - 1; i >= 0; i-- {
			if s.labels[i].name == lbl.Value {
				r.info.Labels[lbl] = s.labels[i].node
				return
			}
		}
		r.errorf(s.file, lbl.NamePos, "use of undeclared label '%s", lbl.Value)
		return
	}
	if s.loops == 0 {
		r.errorf(s.file, pos, "%s outside of a loop", kw)
	}
}

// checkValue verifies that a path used as an expression names a value.
func (r *resolver) checkValue(s *scope, p *syntax.Path, res Res) {
	if res.Kind == ResErr || res.Rest != nil || res.Kind == ResLocal || res.Kind == ResSelfTy || res.Kind == ResQualified {
		return
	}
	if res.Kind == ResDef {
		switch res.Def.Kind {
		case DefFn, DefConst, DefStatic, DefExternFn, DefExternStatic, DefAssocFn, DefAssocConst:
			return
		case DefStruct, DefVariant:
			if res.Def.Ctor {
				return
			}
		}
	}
	r.errorf(s.file, p.Pos(), "expected value, found %s", describe(res))
}

// checkStructPath verifies the path of a struct literal or struct pattern.
func (r *resolver) checkStructPath(s *scope, p *syntax.Path, res Res) {
	if res.Kind == ResErr || res.Kind == ResSelfTy || res.Rest != nil {
		return
	}
	if res.Kind == ResDef {
		switch res.Def.Kind {
		case DefStruct, DefVariant, DefTypeAlias:
			return
		}
	}
	r.errorf(s.file, p.Pos(), "expected struct or variant, found %s", describe(res))
}

// ---------------------------------------------------------------------------
// Patterns

// bindPattern resolves a pattern and adds its bindings to the top rib.
func (r *resolver) bindPattern(s *scope, p syntax.Pat) {
	names := map[string]*Local{}
	r.pat(s, p, names, nil)
	top := s.top()
	for n, l := range names {
		top.values[n] = l
	}
}

// pat resolves pattern p, adding new bindings to names. shared holds the
// bindings of the first alternative of an enclosing or-pattern, which later
// alternatives must reuse.
func (r *resolver) pat(s *scope, p syntax.Pat, names, shared map[string]*Local) {
	switch p := p.(type) {
	case *syntax.WildPat, *syntax.RestPat:
	case *syntax.IdentPat:
		if p.Sub == nil && !p.Mut {
			if res, ok := r.patConst(s, p.Name.Value); ok {
				r.info.PatConsts[p] = res
				return
			}
		}
		if names[p.Name.Value] != nil {
			r.errorf(s.file, p.Name.NamePos, "identifier %s is bound more than once in the same pattern", p.Name.Value)
		} else {
			l := shared[p.Name.Value]
			if l == nil {
				l = &Local{Name: p.Name.Value, Pos: p.Name.NamePos, Mut: p.Mut, Fn: s.fn}
			}
			names[p.Name.Value] = l
			r.info.Bindings[p] = l
		}
		if p.Sub != nil {
			r.pat(s, p.Sub, names, shared)
		}
	case *syntax.LitPat:
		r.expr(s, p.X)
	case *syntax.RangePat:
		r.expr(s, p.Lo)
		r.expr(s, p.Hi)
	case *syntax.PathPat:
		res := r.resolvePath(s, p.Path, ValueNS)
		r.checkValue(s, p.Path, res)
	case *syntax.TupleStructPat:
		res := r.resolvePath(s, p.Path, ValueNS)
		if res.Kind == ResDef && !(res.Def.Kind == DefStruct || res.Def.Kind == DefVariant) {
			r.errorf(s.file, p.Path.Pos(), "expected tuple struct or variant, found %s", describe(res))
		}
		for _, e := range p.Elems {
			r.pat(s, e, names, shared)
		}
	case *syntax.StructPat:
		res := r.resolvePath(s, p.Path, TypeNS)
		r.checkStructPath(s, p.Path, res)
		for _, f := range p.Fields {
			r.pat(s, f.Pat, names, shared)
		}
	case *syntax.TuplePat:
		for _, e := range p.Elems {
			r.pat(s, e, names, shared)
		}
	case *syntax.SlicePat:
		for _, e := range p.Elems {
			r.pat(s, e, names, shared)
		}
	case *syntax.RefPat:
		r.pat(s, p.Pat, names, shared)
	case *syntax.OrPat:
		first := map[string]*Local{}
		r.pat(s, p.Alts[0], first, shared)
		for _, alt := range p.Alts[1:] {
			other := map[string]*Local{}
			r.pat(s, alt, other, first)
			if missing := diffKeys(first, other); missing != "" {
				r.errorf(s.file, alt.Pos(), "variable %s is not bound in all alternatives of this pattern", missing)
			}
			if extra := diffKeys(other, first); extra != "" {
				r.errorf(s.file, alt.Pos(), "variable %s is not bound in all alternatives of this pattern", extra)
			}
		}
		for n, l := range first {
			if names[n] != nil {
				r.errorf(s.file, l.Pos, "identifier %s is bound more than once in the same pattern", n)
				continue
			}
			names[n] = l
		}
	}
}

// diffKeys returns the names in a that are missing from b, comma separated.
func diffKeys(a, b map[string]*Local) string {
	var missing []string
	for n := range a {
		if b[n] == nil {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	return strings.Join(missing, ", ")
}

// patConst reports whether a lone identifier in a pattern names a constant,
// static, or unit struct/variant (rather than introducing a binding).
func (r *resolver) patConst(s *scope, name string) (Res, bool) {
	res, ok := r.lexical(s, name, ValueNS, false)
	if !ok || res.Kind != ResDef {
		return Res{}, false
	}
	switch res.Def.Kind {
	case DefConst, DefStatic, DefAssocConst:
		return res, true
	case DefStruct:
		if sd, ok := res.Def.Node.(*syntax.StructDecl); ok && sd.Kind == syntax.StructUnit {
			return res, true
		}
	case DefVariant:
		if v, ok := res.Def.Node.(*syntax.Variant); ok && v.Kind == syntax.StructUnit {
			return res, true
		}
	}
	return Res{}, false
}
