package syntax

// Inspect traverses the AST rooted at n in depth-first order. It calls f for
// each node; if f returns false, the node's children are skipped.
func Inspect(n Node, f func(Node) bool) {
	if n == nil || !f(n) {
		return
	}
	for _, c := range children(n) {
		Inspect(c, f)
	}
}

// children returns the direct child nodes of n. Optional pointer fields
// (*BlockExpr, *Path) are checked before being added so that no typed nil
// ends up in the result.
func children(n Node) []Node {
	var out []Node
	add := func(ns ...Node) {
		for _, c := range ns {
			if c != nil {
				out = append(out, c)
			}
		}
	}
	addTypes := func(ts []Type) {
		for _, t := range ts {
			add(t)
		}
	}
	addExprs := func(xs []Expr) {
		for _, x := range xs {
			add(x)
		}
	}
	addPats := func(ps []Pat) {
		for _, p := range ps {
			add(p)
		}
	}
	addItems := func(items []Item) {
		for _, it := range items {
			add(it)
		}
	}
	addPath := func(p *Path) {
		if p != nil {
			add(p)
		}
	}
	addPaths := func(ps []*Path) {
		for _, p := range ps {
			addPath(p)
		}
	}
	addGenerics := func(g *Generics) {
		if g == nil {
			return
		}
		for _, gp := range g.Params {
			addPaths(gp.Bounds)
			add(gp.Default)
		}
		for _, wp := range g.Where {
			add(wp.Type)
			addPaths(wp.Bounds)
		}
	}
	addFields := func(fs []*Field) {
		for _, f := range fs {
			add(f.Type)
		}
	}

	switch n := n.(type) {
	case *SourceFile:
		addItems(n.Items)
	case *Path:
		if n.QSelf != nil {
			add(n.QSelf.Type)
			addPath(n.QSelf.Trait)
		}
		for _, s := range n.Segments {
			if s.Args != nil {
				addTypes(s.Args.Args)
				for _, b := range s.Args.Bindings {
					add(b.Type)
				}
			}
		}

	// Items
	case *FnDecl:
		addGenerics(n.Generics)
		for _, p := range n.Params {
			add(p.Pat, p.Type)
		}
		add(n.Result)
		if n.Body != nil {
			add(n.Body)
		}
	case *StructDecl:
		addGenerics(n.Generics)
		addFields(n.Fields)
	case *EnumDecl:
		addGenerics(n.Generics)
		for _, v := range n.Variants {
			addFields(v.Fields)
			add(v.Discriminant)
		}
	case *TypeAlias:
		addGenerics(n.Generics)
		addPaths(n.Bounds)
		add(n.Type)
	case *ConstDecl:
		add(n.Type, n.Value)
	case *StaticDecl:
		add(n.Type, n.Value)
	case *ImplDecl:
		addGenerics(n.Generics)
		addPath(n.Trait)
		add(n.Type)
		addItems(n.Items)
	case *TraitDecl:
		addGenerics(n.Generics)
		addPaths(n.Supers)
		addItems(n.Items)
	case *ModDecl:
		addItems(n.Items)
	case *UseDecl:
	case *ExternBlock:
		addItems(n.Items)

	// Types
	case *PathType:
		addPath(n.Path)
	case *RefType:
		add(n.Elem)
	case *PtrType:
		add(n.Elem)
	case *SliceType:
		add(n.Elem)
	case *ArrayType:
		add(n.Elem, n.Len)
	case *TupleType:
		addTypes(n.Elems)
	case *FnType:
		addTypes(n.Params)
		add(n.Result)
	case *DynType:
		addPaths(n.Bounds)
	case *ImplType:
		addPaths(n.Bounds)

	// Expressions
	case *TemplateExpr:
		for _, p := range n.Parts {
			add(p.X)
		}
	case *PathExpr:
		addPath(n.Path)
	case *UnaryExpr:
		add(n.X)
	case *RefExpr:
		add(n.X)
	case *BinaryExpr:
		add(n.X, n.Y)
	case *AssignExpr:
		add(n.X, n.Y)
	case *CastExpr:
		add(n.X, n.Type)
	case *RangeExpr:
		add(n.Lo, n.Hi)
	case *CallExpr:
		add(n.Fun)
		addExprs(n.Args)
	case *MethodCallExpr:
		add(n.Recv)
		if n.TypeArgs != nil {
			addTypes(n.TypeArgs.Args)
		}
		addExprs(n.Args)
	case *FieldExpr:
		add(n.X)
	case *IndexExpr:
		add(n.X, n.Index)
	case *TryExpr:
		add(n.X)
	case *StructLit:
		addPath(n.Path)
		for _, f := range n.Fields {
			add(f.Value)
		}
		add(n.Base)
	case *TupleExpr:
		addExprs(n.Elems)
	case *ParenExpr:
		add(n.X)
	case *ArrayExpr:
		addExprs(n.Elems)
	case *ArrayRepeatExpr:
		add(n.Value, n.Count)
	case *BlockExpr:
		for _, s := range n.Stmts {
			add(s)
		}
		add(n.Tail)
	case *IfExpr:
		add(n.Cond)
		add(n.Then)
		add(n.Else)
	case *LetExpr:
		add(n.Pat, n.X)
	case *MatchExpr:
		add(n.X)
		for _, a := range n.Arms {
			add(a.Pat, a.Guard, a.Body)
		}
	case *LoopExpr:
		add(n.Body)
	case *WhileExpr:
		add(n.Cond)
		add(n.Body)
	case *ForExpr:
		add(n.Pat, n.Iter)
		add(n.Body)
	case *BreakExpr:
		add(n.Value)
	case *ReturnExpr:
		add(n.Value)
	case *ClosureExpr:
		for _, p := range n.Params {
			add(p.Pat, p.Type)
		}
		add(n.Result, n.Body)
	case *SelectExpr:
		for _, a := range n.Arms {
			add(a.Pat, a.Op, a.Body)
		}

	// Statements
	case *LetStmt:
		add(n.Pat, n.Type, n.Init)
		if n.Else != nil {
			add(n.Else)
		}
	case *ExprStmt:
		add(n.X)
	case *ItemStmt:
		add(n.Item)
	case *DeferStmt:
		add(n.X)

	// Patterns
	case *IdentPat:
		add(n.Sub)
	case *LitPat:
		add(n.X)
	case *RangePat:
		add(n.Lo, n.Hi)
	case *PathPat:
		addPath(n.Path)
	case *TuplePat:
		addPats(n.Elems)
	case *TupleStructPat:
		addPath(n.Path)
		addPats(n.Elems)
	case *StructPat:
		addPath(n.Path)
		for _, f := range n.Fields {
			add(f.Pat)
		}
	case *SlicePat:
		addPats(n.Elems)
	case *RefPat:
		add(n.Pat)
	case *OrPat:
		addPats(n.Alts)
	}
	return out
}
