package syntax

import (
	"fmt"
	"strings"
)

// Dump renders a node as a compact S-expression. It is used by tests and by
// debugging tools; the format is not stable.
func Dump(n any) string {
	var d dumper
	d.node(n)
	return d.b.String()
}

type dumper struct{ b strings.Builder }

func (d *dumper) w(format string, args ...any) { fmt.Fprintf(&d.b, format, args...) }

func (d *dumper) list(tag string, items ...any) {
	d.w("(%s", tag)
	for _, it := range items {
		d.w(" ")
		d.node(it)
	}
	d.w(")")
}

func (d *dumper) path(p *Path) string {
	var b strings.Builder
	if p.QSelf != nil {
		b.WriteString("<")
		b.WriteString(Dump(p.QSelf.Type))
		if p.QSelf.Trait != nil {
			b.WriteString(" as ")
			b.WriteString(d.path(p.QSelf.Trait))
		}
		b.WriteString(">::")
	}
	for i, s := range p.Segments {
		if i > 0 {
			b.WriteString("::")
		}
		b.WriteString(s.Name.Value)
		if s.Args != nil {
			b.WriteString("<")
			var parts []string
			for _, a := range s.Args.Args {
				parts = append(parts, Dump(a))
			}
			for _, bd := range s.Args.Bindings {
				parts = append(parts, bd.Name.Value+"="+Dump(bd.Type))
			}
			b.WriteString(strings.Join(parts, ","))
			b.WriteString(">")
		}
	}
	return b.String()
}

func (d *dumper) paths(ps []*Path) string {
	var parts []string
	for _, p := range ps {
		parts = append(parts, d.path(p))
	}
	return strings.Join(parts, "+")
}

func opName(k Kind) string { return k.String() }

func (d *dumper) node(n any) {
	switch n := n.(type) {
	case nil:
		d.w("_")
	case string:
		d.w("%s", n)
	case *Name:
		if n == nil {
			d.w("_")
			return
		}
		d.w("%s", n.Value)
	case *Path:
		d.w("%s", d.path(n))

	// Types
	case *PathType:
		d.w("%s", d.path(n.Path))
	case *RefType:
		if n.Mut {
			d.list("&mut", n.Elem)
		} else {
			d.list("&", n.Elem)
		}
	case *PtrType:
		if n.Mut {
			d.list("*mut", n.Elem)
		} else {
			d.list("*const", n.Elem)
		}
	case *SliceType:
		d.list("slice", n.Elem)
	case *ArrayType:
		d.list("array", n.Elem, n.Len)
	case *TupleType:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = e
		}
		d.list("tuple", items...)
	case *FnType:
		items := make([]any, 0, len(n.Params)+1)
		for _, p := range n.Params {
			items = append(items, p)
		}
		items = append(items, n.Result)
		tag := "fn"
		if n.Abi != "" {
			tag = "extern-fn"
		}
		d.list(tag, items...)
	case *DynType:
		d.w("(dyn %s)", d.paths(n.Bounds))
	case *ImplType:
		d.w("(impl %s)", d.paths(n.Bounds))
	case *NeverType:
		d.w("!")
	case *InferType:
		d.w("_")

	// Expressions
	case *LitExpr:
		d.w("%s", n.Value)
	case *TemplateExpr:
		d.w("(template")
		for _, p := range n.Parts {
			if p.X == nil {
				d.w(" %q", p.Text)
				continue
			}
			d.w(" ${")
			d.node(p.X)
			if p.Spec != "" {
				d.w(":%s", p.Spec)
			}
			d.w("}")
		}
		d.w(")")
	case *PathExpr:
		d.w("%s", d.path(n.Path))
	case *UnaryExpr:
		d.list(opName(n.Op), n.X)
	case *RefExpr:
		if n.Mut {
			d.list("&mut", n.X)
		} else {
			d.list("&", n.X)
		}
	case *BinaryExpr:
		d.list(opName(n.Op), n.X, n.Y)
	case *AssignExpr:
		d.list(opName(n.Op), n.X, n.Y)
	case *CastExpr:
		d.list("as", n.X, n.Type)
	case *RangeExpr:
		tag := ".."
		if n.Inclusive {
			tag = "..="
		}
		d.list(tag, n.Lo, n.Hi)
	case *CallExpr:
		items := []any{n.Fun}
		for _, a := range n.Args {
			items = append(items, a)
		}
		d.list("call", items...)
	case *MethodCallExpr:
		name := "." + n.Name.Value
		if n.TypeArgs != nil {
			name += d.path(&Path{Segments: []*PathSegment{{Name: &Name{}, Args: n.TypeArgs}}})
		}
		items := []any{n.Recv}
		for _, a := range n.Args {
			items = append(items, a)
		}
		d.list(name, items...)
	case *FieldExpr:
		d.list("."+n.Name.Value, n.X)
	case *IndexExpr:
		d.list("index", n.X, n.Index)
	case *TryExpr:
		d.list("?", n.X)
	case *StructLit:
		d.w("(struct %s", d.path(n.Path))
		for _, f := range n.Fields {
			d.w(" %s:", f.Name.Value)
			d.node(f.Value)
		}
		if n.Base != nil {
			d.w(" ..")
			d.node(n.Base)
		}
		d.w(")")
	case *TupleExpr:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = e
		}
		d.list("tuple", items...)
	case *ParenExpr:
		d.list("paren", n.X)
	case *ArrayExpr:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = e
		}
		d.list("array", items...)
	case *ArrayRepeatExpr:
		d.list("repeat", n.Value, n.Count)
	case *BlockExpr:
		tag := "block"
		if n.Unsafe {
			tag = "unsafe"
		}
		items := make([]any, 0, len(n.Stmts)+1)
		for _, s := range n.Stmts {
			items = append(items, s)
		}
		if n.Tail != nil {
			items = append(items, n.Tail)
		}
		d.list(tag, items...)
	case *IfExpr:
		if n.Else != nil {
			d.list("if", n.Cond, n.Then, n.Else)
		} else {
			d.list("if", n.Cond, n.Then)
		}
	case *LetExpr:
		d.list("let", n.Pat, n.X)
	case *MatchExpr:
		items := []any{n.X}
		for _, a := range n.Arms {
			items = append(items, a)
		}
		d.list("match", items...)
	case *MatchArm:
		if n.Guard != nil {
			d.list("arm", n.Pat, n.Guard, n.Body)
		} else {
			d.list("arm", n.Pat, n.Body)
		}
	case *LoopExpr:
		d.list("loop"+label(n.Label), n.Body)
	case *WhileExpr:
		d.list("while"+label(n.Label), n.Cond, n.Body)
	case *ForExpr:
		d.list("for"+label(n.Label), n.Pat, n.Iter, n.Body)
	case *BreakExpr:
		if n.Value != nil {
			d.list("break"+label(n.Label), n.Value)
		} else {
			d.w("(break%s)", label(n.Label))
		}
	case *ContinueExpr:
		d.w("(continue%s)", label(n.Label))
	case *ReturnExpr:
		if n.Value != nil {
			d.list("return", n.Value)
		} else {
			d.w("(return)")
		}
	case *ClosureExpr:
		d.w("(closure (")
		for i, p := range n.Params {
			if i > 0 {
				d.w(" ")
			}
			d.node(p.Pat)
			if p.Type != nil {
				d.w(":")
				d.node(p.Type)
			}
		}
		d.w(")")
		if n.Result != nil {
			d.w(" -> ")
			d.node(n.Result)
		}
		d.w(" ")
		d.node(n.Body)
		d.w(")")
	case *SelectExpr:
		items := make([]any, len(n.Arms))
		for i, a := range n.Arms {
			items[i] = a
		}
		d.list("select", items...)
	case *SelectArm:
		switch {
		case n.Default:
			d.list("default", n.Body)
		case n.Pat != nil:
			d.list("recv", n.Pat, n.Op, n.Body)
		default:
			d.list("case", n.Op, n.Body)
		}
	case *BadExpr:
		d.w("BAD")

	// Statements
	case *LetStmt:
		items := []any{n.Pat}
		if n.Type != nil {
			items = append(items, n.Type)
		}
		if n.Init != nil {
			items = append(items, n.Init)
		}
		if n.Else != nil {
			items = append(items, "else", n.Else)
		}
		d.list("let", items...)
	case *ExprStmt:
		d.node(n.X)
		if n.Semi {
			d.w(";")
		}
	case *ItemStmt:
		d.node(n.Item)
	case *DeferStmt:
		d.list("defer", n.X)
	case *EmptyStmt:
		d.w(";")

	// Patterns
	case *WildPat:
		d.w("_")
	case *RestPat:
		d.w("..")
	case *IdentPat:
		name := n.Name.Value
		if n.Mut {
			name = "mut " + name
		}
		if n.Sub != nil {
			d.list("@ "+name, n.Sub)
		} else {
			d.w("%s", name)
		}
	case *LitPat:
		d.node(n.X)
	case *RangePat:
		tag := "..pat"
		if n.Inclusive {
			tag = "..=pat"
		}
		d.list(tag, n.Lo, n.Hi)
	case *PathPat:
		d.w("%s", d.path(n.Path))
	case *TuplePat:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = e
		}
		d.list("tuple", items...)
	case *TupleStructPat:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = e
		}
		d.list(d.path(n.Path), items...)
	case *StructPat:
		d.w("(%s {", d.path(n.Path))
		for i, f := range n.Fields {
			if i > 0 {
				d.w(",")
			}
			d.w(" %s:", f.Name.Value)
			d.node(f.Pat)
		}
		if n.Rest {
			d.w(" ..")
		}
		d.w(" })")
	case *SlicePat:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = e
		}
		d.list("slice", items...)
	case *RefPat:
		d.list("&", n.Pat)
	case *OrPat:
		items := make([]any, len(n.Alts))
		for i, a := range n.Alts {
			items[i] = a
		}
		d.list("|", items...)

	// Items
	case *SourceFile:
		items := make([]any, len(n.Items))
		for i, it := range n.Items {
			items[i] = it
		}
		d.list("file", items...)
	case *FnDecl:
		d.w("(fn %s", n.Name.Value)
		d.generics(n.Generics)
		d.w(" (")
		sep := ""
		if n.Self != nil {
			switch {
			case n.Self.Ref && n.Self.Mut:
				d.w("&mut self")
			case n.Self.Ref:
				d.w("&self")
			case n.Self.Mut:
				d.w("mut self")
			default:
				d.w("self")
			}
			sep = " "
		}
		for _, p := range n.Params {
			d.w("%s", sep)
			d.node(p.Pat)
			d.w(":")
			d.node(p.Type)
			sep = " "
		}
		d.w(")")
		if n.Result != nil {
			d.w(" -> ")
			d.node(n.Result)
		}
		if n.Body != nil {
			d.w(" ")
			d.node(n.Body)
		}
		d.w(")")
	case *StructDecl:
		d.w("(struct %s", n.Name.Value)
		d.generics(n.Generics)
		d.fields(n.Kind, n.Fields)
		d.w(")")
	case *EnumDecl:
		d.w("(enum %s", n.Name.Value)
		d.generics(n.Generics)
		for _, v := range n.Variants {
			d.w(" (%s", v.Name.Value)
			d.fields(v.Kind, v.Fields)
			if v.Discriminant != nil {
				d.w(" = ")
				d.node(v.Discriminant)
			}
			d.w(")")
		}
		d.w(")")
	case *TypeAlias:
		d.w("(type %s", n.Name.Value)
		d.generics(n.Generics)
		if len(n.Bounds) > 0 {
			d.w(" : %s", d.paths(n.Bounds))
		}
		if n.Type != nil {
			d.w(" = ")
			d.node(n.Type)
		}
		d.w(")")
	case *ConstDecl:
		d.list("const "+n.Name.Value, n.Type, n.Value)
	case *StaticDecl:
		tag := "static "
		if n.Mut {
			tag = "static mut "
		}
		d.list(tag+n.Name.Value, n.Type, n.Value)
	case *ImplDecl:
		d.w("(impl")
		d.generics(n.Generics)
		if n.Trait != nil {
			d.w(" %s for", d.path(n.Trait))
		}
		d.w(" ")
		d.node(n.Type)
		for _, it := range n.Items {
			d.w(" ")
			d.node(it)
		}
		d.w(")")
	case *TraitDecl:
		d.w("(trait %s", n.Name.Value)
		d.generics(n.Generics)
		if len(n.Supers) > 0 {
			d.w(" : %s", d.paths(n.Supers))
		}
		for _, it := range n.Items {
			d.w(" ")
			d.node(it)
		}
		d.w(")")
	case *ModDecl:
		d.w("(mod %s", n.Name.Value)
		for _, it := range n.Items {
			d.w(" ")
			d.node(it)
		}
		d.w(")")
	case *UseDecl:
		d.w("(use ")
		d.useTree(n.Tree)
		d.w(")")
	case *ExternBlock:
		items := make([]any, len(n.Items))
		for i, it := range n.Items {
			items[i] = it
		}
		d.list("extern "+n.Abi, items...)
	default:
		d.w("<%T>", n)
	}
}

func label(n *Name) string {
	if n == nil {
		return ""
	}
	return " '" + n.Value
}

func (d *dumper) generics(g *Generics) {
	if g == nil || (len(g.Params) == 0 && len(g.Where) == 0) {
		return
	}
	d.w(" <")
	for i, p := range g.Params {
		if i > 0 {
			d.w(",")
		}
		d.w("%s", p.Name.Value)
		if len(p.Bounds) > 0 {
			d.w(":%s", d.paths(p.Bounds))
		}
		if p.Default != nil {
			d.w("=%s", Dump(p.Default))
		}
	}
	d.w(">")
	for _, wp := range g.Where {
		d.w(" where ")
		d.node(wp.Type)
		d.w(":%s", d.paths(wp.Bounds))
	}
}

func (d *dumper) fields(kind StructKind, fields []*Field) {
	switch kind {
	case StructUnit:
		return
	case StructTuple:
		d.w(" (")
	default:
		d.w(" {")
	}
	for i, f := range fields {
		if i > 0 {
			d.w(" ")
		}
		if f.Vis != VisPrivate {
			d.w("pub ")
		}
		if f.Name != nil {
			d.w("%s:", f.Name.Value)
		}
		d.node(f.Type)
	}
	if kind == StructTuple {
		d.w(")")
	} else {
		d.w("}")
	}
}

func (d *dumper) useTree(t *UseTree) {
	if t.Prefix != nil {
		d.w("%s", d.path(t.Prefix))
	}
	switch {
	case t.Glob:
		if t.Prefix != nil {
			d.w("::")
		}
		d.w("*")
	case t.Group:
		if t.Prefix != nil {
			d.w("::")
		}
		d.w("{")
		for i, c := range t.Children {
			if i > 0 {
				d.w(",")
			}
			d.useTree(c)
		}
		d.w("}")
	}
	if t.Alias != nil {
		d.w(" as %s", t.Alias.Value)
	}
}
