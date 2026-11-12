package mir

import (
	"fmt"
	"strings"
)

// String renders a function for debugging.
func (f *Func) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "fn %s(", f.Name)
	for i := 1; i <= f.NArgs && i < len(f.Locals); i++ {
		if i > 1 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "_%d: %s", i, f.Locals[i].Type)
	}
	fmt.Fprintf(&b, ") -> %s {\n", f.Locals[0].Type)
	for i := f.NArgs + 1; i < len(f.Locals); i++ {
		l := f.Locals[i]
		fmt.Fprintf(&b, "    let _%d: %s", i, l.Type)
		if l.Name != "" {
			fmt.Fprintf(&b, " // %s", l.Name)
		}
		if l.Boxed {
			b.WriteString(" (boxed)")
		}
		b.WriteByte('\n')
	}
	for i, blk := range f.Blocks {
		fmt.Fprintf(&b, "  bb%d:\n", i)
		for _, s := range blk.Stmts {
			fmt.Fprintf(&b, "    %s\n", stmtString(s))
		}
		fmt.Fprintf(&b, "    %s\n", termString(blk.Term))
	}
	b.WriteString("}\n")
	return b.String()
}

func (p Place) String() string {
	s := fmt.Sprintf("_%d", p.Local)
	for _, pr := range p.Proj {
		switch pr.Kind {
		case PDeref:
			s = "(*" + s + ")"
		case PField:
			s = fmt.Sprintf("%s.%d", s, pr.Field)
		case PIndex:
			s = fmt.Sprintf("%s[_%d]", s, pr.Index)
		case PDowncast:
			s = fmt.Sprintf("(%s as v%d)", s, pr.Variant)
		}
	}
	return s
}

func (o Operand) String() string {
	if o.Kind == OpCopy {
		return o.Place.String()
	}
	c := o.Const
	switch c.Kind {
	case CInt, CChar:
		return fmt.Sprintf("%d", c.Int)
	case CBool:
		return fmt.Sprintf("%v", c.Int != 0)
	case CFloat:
		return fmt.Sprintf("%g", c.Float)
	case CStr:
		return fmt.Sprintf("%q", c.Str)
	case CBytes:
		return fmt.Sprintf("b%q", c.Str)
	case CUnit:
		return "()"
	case CFn:
		return c.Fn.String()
	case CStatic:
		return "&" + c.Static.Name
	}
	return "zero"
}

func (r *FnRef) String() string {
	if r.Closure != nil {
		return r.Closure.Name
	}
	s := qualName(r.Def)
	if r.Self != nil {
		s = fmt.Sprintf("<%s>::%s", r.Self, r.Def.Name)
	}
	if len(r.Args) > 0 {
		var parts []string
		for _, a := range r.Args {
			parts = append(parts, a.String())
		}
		s += "::<" + strings.Join(parts, ", ") + ">"
	}
	return s
}

func opsString(ops []Operand) string {
	var parts []string
	for _, o := range ops {
		parts = append(parts, o.String())
	}
	return strings.Join(parts, ", ")
}

func rvString(rv Rvalue) string {
	switch r := rv.(type) {
	case Use:
		return r.X.String()
	case Ref:
		if r.Mut {
			return "&mut " + r.Place.String()
		}
		return "&" + r.Place.String()
	case Bin:
		return fmt.Sprintf("%s(%s, %s)", r.Op, r.X, r.Y)
	case Un:
		if r.Op == Neg {
			return "neg " + r.X.String()
		}
		return "not " + r.X.String()
	case Cast:
		return fmt.Sprintf("%s as %s", r.X, r.To)
	case Aggregate:
		switch r.Kind {
		case AggClosure:
			return fmt.Sprintf("closure %s [%s]", r.Closure.Name, opsString(r.Ops))
		case AggAdt:
			return fmt.Sprintf("%s#%d {%s}", r.Type, r.Variant, opsString(r.Ops))
		}
		return fmt.Sprintf("(%s)", opsString(r.Ops))
	case Discr:
		return "discr " + r.Place.String()
	case Len:
		return "len " + r.Place.String()
	case Unsize:
		return fmt.Sprintf("unsize %s len %d", r.X, r.Len)
	case MakeDyn:
		return fmt.Sprintf("dyn %s as %s", r.X, r.To)
	}
	return "?"
}

func stmtString(s Stmt) string {
	switch s := s.(type) {
	case Assign:
		return fmt.Sprintf("%s = %s", s.Place, rvString(s.Rv))
	case SetDiscr:
		return fmt.Sprintf("discr %s = %d", s.Place, s.Variant)
	case Box:
		return fmt.Sprintf("_%d = box %s", s.Local, s.Type)
	case PushDefer:
		return fmt.Sprintf("defer %s(%s)", s.Fn, opsString(s.Args))
	}
	return "?"
}

func termString(t Term) string {
	switch t := t.(type) {
	case Goto:
		return fmt.Sprintf("goto bb%d", t.Target)
	case Switch:
		var parts []string
		for i, v := range t.Vals {
			parts = append(parts, fmt.Sprintf("%d: bb%d", v, t.Targets[i]))
		}
		return fmt.Sprintf("switch %s [%s, else: bb%d]", t.X, strings.Join(parts, ", "), t.Else)
	case Return:
		return "return"
	case Unreachable:
		return "unreachable"
	case Call:
		if t.Target < 0 {
			return fmt.Sprintf("%s = %s(%s) -> !", t.Dest, t.Fn, opsString(t.Args))
		}
		return fmt.Sprintf("%s = %s(%s) -> bb%d", t.Dest, t.Fn, opsString(t.Args), t.Target)
	case Assert:
		return fmt.Sprintf("assert %s %q -> bb%d", t.Cond, t.Msg, t.Target)
	case nil:
		return "<no terminator>"
	}
	return "?"
}

// Closures lists the closure bodies created so far, in creation order.
func (p *Program) Closures() []*Func { return p.closures }
