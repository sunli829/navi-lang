package types

import (
	"fmt"
	"strings"

	"navi/internal/syntax"
)

// DumpLines renders the results of a body for comparing the self-hosted
// compiler with stage0: every recorded expression, local and pattern in
// recording order.
func (b *BodyInfo) DumpLines(f *syntax.File) []string {
	pos := func(p syntax.Pos) string { return f.Position(p).String() }
	var out []string
	for _, x := range b.typeOrder {
		s := fmt.Sprintf("e %s %s", pos(x.Pos()), b.Types[x])
		if cl := b.Callees[x]; cl != nil {
			s += " -> " + calleeString(cl)
		}
		if a := b.Adjust[x]; a != nil {
			s += " adj " + adjustString(a)
		}
		if fx, ok := x.(*syntax.FieldExpr); ok {
			if i, ok := b.Fields[fx]; ok {
				s += fmt.Sprintf(" field=%d", i)
			}
		}
		if tx, ok := x.(*syntax.TemplateExpr); ok {
			if t := b.Templates[tx]; t != nil {
				s += " tmpl=" + t.String()
			}
		}
		out = append(out, s)
	}
	for _, l := range b.localOrder {
		out = append(out, fmt.Sprintf("l %s@%s %s", l.Name, pos(l.Pos), b.Locals[l]))
	}
	for _, p := range b.patOrder {
		s := fmt.Sprintf("p %s %s", pos(p.Pos()), b.Pats[p])
		if ip, ok := p.(*syntax.IdentPat); ok && b.RefBindings[ip] {
			s += " ref"
		}
		out = append(out, s)
	}
	return out
}

func calleeString(cl *Callee) string {
	var args []string
	for _, a := range cl.Args {
		args = append(args, a.String())
	}
	s := fmt.Sprintf("%s %s [%s]", cl.Def.Kind, cl.Def.Name, strings.Join(args, ", "))
	if cl.Self != nil {
		s += " self=" + cl.Self.String()
	}
	if cl.Recv != nil {
		s += " recv " + adjustString(cl.Recv)
	}
	return s
}

func adjustString(a *Adjust) string {
	s := fmt.Sprintf("d=%d r=%d u=%v", a.Derefs, a.Autoref, a.Unsize)
	if a.ToDyn != nil {
		s += " dyn=" + a.ToDyn.String()
	}
	return s
}
