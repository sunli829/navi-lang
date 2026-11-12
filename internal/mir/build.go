package mir

import (
	"fmt"

	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// Program is the MIR of all crates.
type Program struct {
	Info  *types.Info
	Res   *resolve.Info
	Funcs map[*resolve.Def]*Func // functions, methods, consts and statics
	// Lang functions used by lowering, found by std path.
	langFns map[string]*resolve.Def
	// closures lists the closure bodies created while lowering.
	closures []*Func
	// shims caches generated builtin/derived impl bodies.
	shims map[string]*Func
}

// Build lowers every body that type-checked.
func Build(info *types.Info) *Program {
	p := &Program{Info: info, Res: info.Res, Funcs: map[*resolve.Def]*Func{}, langFns: map[string]*resolve.Def{}, shims: map[string]*Func{}}
	p.findLangFns()
	for def := range info.Bodies {
		p.Func(def)
	}
	return p
}

// Func returns the MIR of a body, building it on first use.
func (p *Program) Func(def *resolve.Def) *Func {
	if f := p.Funcs[def]; f != nil {
		return f
	}
	body := p.Info.Bodies[def]
	if body == nil {
		return nil
	}
	b := newBuilder(p, def, body)
	f := b.build()
	p.Funcs[def] = f
	return f
}

// findLangFns records std functions that lowering calls directly.
func (p *Program) findLangFns() {
	want := map[string][]string{
		"format": {"fmt", "format"},
		"str_eq": {"str", "str_eq"},
		"select": {"sync", "mpmc", "select_cases"},
	}
	var std *resolve.Crate
	for _, c := range p.Res.Crates {
		if c.IsStd {
			std = c
		}
	}
	if std == nil {
		return
	}
	for name, path := range want {
		m := std.Root
		for i, seg := range path {
			ns := resolve.TypeNS
			if i == len(path)-1 {
				ns = resolve.ValueNS
			}
			b := m.Lookup(seg, ns)
			if b == nil || b.Res.Def == nil {
				break
			}
			if i == len(path)-1 {
				p.langFns[name] = b.Res.Def
			} else {
				m = b.Res.Def.Module
			}
		}
	}
}

// LangFn returns a std function used by lowering.
func (p *Program) LangFn(name string) *resolve.Def { return p.langFns[name] }

type loopScope struct {
	node     syntax.Node
	brk      int // block to go to on break
	cont     int // block to go to on continue
	dest     Place
	hasValue bool
}

type builder struct {
	p    *Program
	info *types.Info
	res  *resolve.Info
	body *types.BodyInfo
	fn   *Func
	cur  int

	locals map[*resolve.Local]int
	boxed  map[*resolve.Local]bool
	// For closures: captured local -> index in the environment.
	captures map[*resolve.Local]int
	envLocal int
	loops    []loopScope
	// closureNode is the closure being lowered (nil for the item body).
	closureNode *syntax.ClosureExpr
	// closureCaps lists, for each closure in the body, the locals it uses
	// from outside.
	closureCaps map[*syntax.ClosureExpr][]*resolve.Local
	// deferClosures holds the closures standing for `defer { ... }` blocks.
	deferClosures map[*syntax.BlockExpr]*syntax.ClosureExpr
	// orBound holds the places of the bindings of the or-pattern being
	// lowered.
	orBound map[*resolve.Local]Place
}

func newBuilder(p *Program, def *resolve.Def, body *types.BodyInfo) *builder {
	return &builder{
		p:      p,
		info:   p.Info,
		res:    p.Res,
		body:   body,
		locals: map[*resolve.Local]int{},
		boxed:  map[*resolve.Local]bool{},
	}
}

func (b *builder) build() *Func {
	def := b.body.Owner
	b.fn = &Func{Name: qualName(def), Def: def, Owner: def, File: def.File}
	b.analyzeCaptures(def.Node)
	b.newLocal(nil, "") // return place
	b.cur = b.newBlock()
	switch n := def.Node.(type) {
	case *syntax.FnDecl:
		sig := b.info.Fns[def]
		b.fn.Locals[0].Type = b.retType(sig)
		if l := b.res.SelfLocals[n]; l != nil && sig.Self != nil {
			b.fn.NArgs++
			idx := b.newLocal(sig.Self, "self")
			b.bindArg(l, idx)
		}
		var argLocals []int
		for i, pr := range n.Params {
			b.fn.NArgs++
			argLocals = append(argLocals, b.newLocal(sig.Params[i], fmt.Sprintf("arg%d", i)))
			_ = pr
		}
		for i, pr := range n.Params {
			b.bindPattern(pr.Pat, LocalPlace(argLocals[i]))
		}
		b.exprInto(n.Body, LocalPlace(0))
	case *syntax.ConstDecl:
		b.fn.Locals[0].Type = b.info.Consts[def].Type
		b.exprInto(n.Value, LocalPlace(0))
	case *syntax.StaticDecl:
		b.fn.Locals[0].Type = b.info.Consts[def].Type
		b.exprInto(n.Value, LocalPlace(0))
	}
	b.terminate(Return{})
	boxAddressTaken(b.info.Ctx, b.fn)
	return b.fn
}

// retType is the declared return type, with opaque types replaced by the
// hidden type the body returns.
func (b *builder) retType(sig *types.FnSig) *types.Type {
	t := sig.Ret
	if h := b.info.Hidden[t]; h != nil {
		return h
	}
	return t
}

func qualName(d *resolve.Def) string {
	if d == nil {
		return "?"
	}
	name := d.Name
	if d.Parent != nil && d.Parent.Kind == resolve.DefImpl {
		if ii := d.Parent; ii != nil {
			name = "impl::" + name
		}
	}
	for p := d.Parent; p != nil; p = p.Parent {
		if p.Kind == resolve.DefMod || p.Kind == resolve.DefTrait {
			name = p.Name + "::" + name
		}
	}
	return name
}

// ---------------------------------------------------------------------------
// Locals and blocks

func (b *builder) newLocal(t *types.Type, name string) int {
	b.fn.Locals = append(b.fn.Locals, &Local{Type: t, Name: name})
	return len(b.fn.Locals) - 1
}

func (b *builder) temp(t *types.Type) Place {
	l := b.newLocal(t, "")
	b.emit(StorageLive{Local: l})
	return LocalPlace(l)
}

func (b *builder) newBlock() int {
	b.fn.Blocks = append(b.fn.Blocks, &Block{})
	return len(b.fn.Blocks) - 1
}

func (b *builder) emit(s Stmt) {
	if b.cur < 0 {
		return // unreachable code
	}
	blk := b.fn.Blocks[b.cur]
	blk.Stmts = append(blk.Stmts, s)
}

func (b *builder) assign(p Place, rv Rvalue) { b.emit(Assign{Place: p, Rv: rv}) }

func (b *builder) terminate(t Term) {
	if b.cur < 0 {
		return
	}
	b.fn.Blocks[b.cur].Term = t
	b.cur = -1
}

func (b *builder) gotoBlock(target int) {
	b.terminate(Goto{Target: target})
	b.cur = target
}

// startBlock makes blk current (the previous block must be terminated).
func (b *builder) startBlock(blk int) { b.cur = blk }

// call emits a call and continues in a new block; it returns the block.
func (b *builder) call(fn Operand, args []Operand, dest Place, pos syntax.Pos) {
	if b.cur < 0 {
		return
	}
	next := b.newBlock()
	b.terminate(Call{Fn: fn, Args: args, Dest: dest, Target: next, Pos: pos})
	b.cur = next
}

// callDiverging emits a call that never returns.
func (b *builder) callDiverging(fn Operand, args []Operand, pos syntax.Pos) {
	if b.cur < 0 {
		return
	}
	b.terminate(Call{Fn: fn, Args: args, Dest: b.temp(b.info.Ctx.Never), Target: -1, Pos: pos})
}

// bindArg makes resolver local l live in argument local idx (copying to a
// heap cell if it is captured).
func (b *builder) bindArg(l *resolve.Local, idx int) {
	if b.boxed[l] {
		t := b.fn.Locals[idx].Type
		cell := b.newLocal(b.info.Ctx.Ref(true, t), l.Name)
		b.fn.Locals[cell].Boxed = true
		b.emit(Box{Local: cell, Type: t})
		b.assign(LocalPlace(cell).Deref(), Use{X: Copy(LocalPlace(idx))})
		b.locals[l] = cell
		return
	}
	b.locals[l] = idx
}

// declare creates the local for a resolver local of type t and returns the
// place where its value lives.
func (b *builder) declare(l *resolve.Local, t *types.Type) Place {
	if b.boxed[l] {
		cell := b.newLocal(b.info.Ctx.Ref(true, t), l.Name)
		b.fn.Locals[cell].Boxed = true
		b.emit(Box{Local: cell, Type: t})
		b.locals[l] = cell
		return LocalPlace(cell).Deref()
	}
	idx := b.newLocal(t, l.Name)
	b.emit(StorageLive{Local: idx})
	b.locals[l] = idx
	return LocalPlace(idx)
}

// localPlace returns the place of a resolver local.
func (b *builder) localPlace(l *resolve.Local) Place {
	if i, ok := b.captures[l]; ok {
		// env -> cell pointer -> value
		return LocalPlace(b.envLocal).Deref().Field(i).Deref()
	}
	idx, ok := b.locals[l]
	if !ok {
		panic(fmt.Sprintf("mir: local %s has no slot", l.Name))
	}
	if b.fn.Locals[idx].Boxed {
		return LocalPlace(idx).Deref()
	}
	return LocalPlace(idx)
}

// cellPointer returns an operand with the address of a captured local's
// heap cell (for building a closure environment).
func (b *builder) cellPointer(l *resolve.Local) Operand {
	if i, ok := b.captures[l]; ok {
		return Copy(LocalPlace(b.envLocal).Deref().Field(i))
	}
	return Copy(LocalPlace(b.locals[l]))
}

// typeOf returns the checked type of an expression.
func (b *builder) typeOf(x syntax.Expr) *types.Type {
	if t := b.body.Types[x]; t != nil {
		return t
	}
	if p, ok := x.(*syntax.ParenExpr); ok {
		return b.typeOf(p.X)
	}
	return b.info.Ctx.Err
}

// analyzeCaptures finds the locals each closure in the body uses from
// outside and marks them boxed.
func (b *builder) analyzeCaptures(root syntax.Node) {
	b.closureCaps = map[*syntax.ClosureExpr][]*resolve.Local{}
	if root == nil {
		return
	}
	var closures []*syntax.ClosureExpr
	b.deferClosures = map[*syntax.BlockExpr]*syntax.ClosureExpr{}
	syntax.Inspect(root, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.ClosureExpr:
			closures = append(closures, n)
		case *syntax.DeferStmt:
			if blk, ok := n.X.(*syntax.BlockExpr); ok {
				// defer { ... } runs the block later, like a closure.
				c := &syntax.ClosureExpr{Or: blk.Lbrace, Body: blk}
				b.deferClosures[blk] = c
				b.body.Types[c] = b.info.Ctx.Fn(nil, b.info.Ctx.Unit, "")
				closures = append(closures, c)
			}
		}
		return true
	})
	for _, c := range closures {
		inside := map[syntax.Node]bool{c: true}
		syntax.Inspect(c.Body, func(n syntax.Node) bool {
			if cc, ok := n.(*syntax.ClosureExpr); ok {
				inside[cc] = true
			}
			return true
		})
		seen := map[*resolve.Local]bool{}
		syntax.Inspect(c.Body, func(n syntax.Node) bool {
			pe, ok := n.(*syntax.PathExpr)
			if !ok {
				return true
			}
			res := b.res.Paths[pe.Path]
			if res.Kind == resolve.ResLocal && !inside[res.Local.Fn] && !seen[res.Local] {
				seen[res.Local] = true
				b.closureCaps[c] = append(b.closureCaps[c], res.Local)
				b.boxed[res.Local] = true
			}
			return true
		})
		// self used inside a closure
	}
}
