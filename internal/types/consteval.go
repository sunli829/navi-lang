package types

import (
	"math"
	"strconv"
	"strings"

	"navi/internal/resolve"
	"navi/internal/syntax"
)

// evalConst evaluates an integer constant expression such as an array
// length or enum discriminant: literals, constants, arithmetic, bit
// operations and casts. Full const evaluation (const fn) runs on MIR in M2.
func (c *collector) evalConst(file *syntax.File, x syntax.Expr) (int64, bool) {
	v, ok := c.eval(file, x)
	if !ok {
		c.errorf(file, x.Pos(), "expected a constant integer expression")
	}
	return v, ok
}

func (c *collector) eval(file *syntax.File, x syntax.Expr) (int64, bool) {
	switch x := x.(type) {
	case *syntax.LitExpr:
		if x.Kind != syntax.LitInt {
			return 0, false
		}
		return parseIntLit(x.Value)
	case *syntax.ParenExpr:
		return c.eval(file, x.X)
	case *syntax.UnaryExpr:
		v, ok := c.eval(file, x.X)
		if !ok {
			return 0, false
		}
		switch x.Op {
		case syntax.Minus:
			return -v, true
		case syntax.Not:
			return ^v, true
		}
	case *syntax.CastExpr:
		return c.eval(file, x.X)
	case *syntax.BinaryExpr:
		a, ok1 := c.eval(file, x.X)
		b, ok2 := c.eval(file, x.Y)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch x.Op {
		case syntax.Plus:
			return a + b, true
		case syntax.Minus:
			return a - b, true
		case syntax.Star:
			return a * b, true
		case syntax.Slash, syntax.Percent:
			if b == 0 {
				c.errorf(file, x.OpPos, "division by zero in a constant expression")
				return 0, false
			}
			if x.Op == syntax.Slash {
				return a / b, true
			}
			return a % b, true
		case syntax.Shl:
			return a << uint64(b), true
		case syntax.Shr:
			return a >> uint64(b), true
		case syntax.And:
			return a & b, true
		case syntax.Or:
			return a | b, true
		case syntax.Caret:
			return a ^ b, true
		}
	case *syntax.PathExpr:
		res := c.info.Res.Paths[x.Path]
		if res.Kind != resolve.ResDef || res.Def.Kind != resolve.DefConst || len(res.Rest) > 0 {
			return 0, false
		}
		return c.constValue(res.Def)
	}
	return 0, false
}

func (c *collector) constValue(d *resolve.Def) (int64, bool) {
	if v, ok := c.consts[d]; ok {
		return v, true
	}
	n, ok := d.Node.(*syntax.ConstDecl)
	if !ok || n.Value == nil {
		return 0, false
	}
	if c.busy[d] {
		c.errorf(d.File, d.Pos, "constant %s refers to itself", d.Name)
		return 0, false
	}
	c.busy[d] = true
	defer delete(c.busy, d)
	v, ok := c.eval(d.File, n.Value)
	if ok {
		c.consts[d] = v
	}
	return v, ok
}

// parseIntLit parses an integer literal such as 1_000, 0xff or 10u8.
func parseIntLit(s string) (int64, bool) {
	s = strings.ReplaceAll(s, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(s, "0x"):
		base, s = 16, s[2:]
	case strings.HasPrefix(s, "0o"):
		base, s = 8, s[2:]
	case strings.HasPrefix(s, "0b"):
		base, s = 2, s[2:]
	}
	for _, suf := range []string{"isize", "usize", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64"} {
		if strings.HasSuffix(s, suf) && !(base == 16 && strings.ContainsAny(suf, "abcdef")) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	u, err := strconv.ParseUint(s, base, 64)
	if err != nil || u > math.MaxInt64 {
		return 0, false
	}
	return int64(u), true
}
