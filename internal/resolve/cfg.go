package resolve

import (
	"fmt"
	"strings"

	"navi/internal/syntax"
)

// Config describes the compilation target for #[cfg(...)] evaluation.
type Config struct {
	TargetOS   string // linux, macos, windows
	TargetArch string // x86_64, aarch64
	Test       bool   // building for navi test
	Debug      bool   // debug_assertions
	Features   map[string]bool
}

// cfgEnabled evaluates every #[cfg(...)] (or #![cfg(...)]) in attrs; all of
// them must hold. Malformed predicates are reported and treated as false.
func (r *resolver) cfgEnabled(file *syntax.File, attrs []*syntax.Attr) bool {
	ok, bad, err := evalCfg(r.cfg, attrs)
	if err != nil {
		r.errorf(file, bad.Pos(), "malformed cfg predicate: %v", err)
	}
	return ok
}

// cfgEnabledQuiet is cfgEnabled without error reporting, for the parallel
// file discovery pass; errors are reported when the module tree is built.
func (r *resolver) cfgEnabledQuiet(attrs []*syntax.Attr) bool {
	ok, _, _ := evalCfg(r.cfg, attrs)
	return ok
}

func evalCfg(cfg Config, attrs []*syntax.Attr) (bool, *syntax.Attr, error) {
	for _, a := range attrs {
		if len(a.Path.Segments) != 1 || a.Path.Segments[0].Name.Value != "cfg" {
			continue
		}
		p := &cfgParser{toks: a.Args}
		ok, err := p.pred(cfg)
		if err == nil && p.i != len(p.toks) {
			err = fmt.Errorf("unexpected %s", tokText(p.toks[p.i]))
		}
		if err != nil {
			return false, a, err
		}
		if !ok {
			return false, nil, nil
		}
	}
	return true, nil, nil
}

type cfgParser struct {
	toks []syntax.Token
	i    int
}

func tokText(t syntax.Token) string {
	if t.Text != "" {
		return t.Text
	}
	return t.Kind.String()
}

// pred parses and evaluates one predicate:
// name | name = "value" | all(p, ...) | any(p, ...) | not(p).
func (p *cfgParser) pred(cfg Config) (bool, error) {
	if p.i >= len(p.toks) || p.toks[p.i].Kind != syntax.Ident {
		return false, fmt.Errorf("expected a cfg option")
	}
	name := p.toks[p.i].Text
	p.i++
	switch name {
	case "all", "any", "not":
		if p.i >= len(p.toks) || p.toks[p.i].Kind != syntax.LParen {
			return false, fmt.Errorf("expected '(' after %s", name)
		}
		p.i++
		var vals []bool
		for p.i < len(p.toks) && p.toks[p.i].Kind != syntax.RParen {
			v, err := p.pred(cfg)
			if err != nil {
				return false, err
			}
			vals = append(vals, v)
			if p.i < len(p.toks) && p.toks[p.i].Kind == syntax.Comma {
				p.i++
			}
		}
		if p.i >= len(p.toks) {
			return false, fmt.Errorf("missing ')'")
		}
		p.i++
		switch name {
		case "all":
			for _, v := range vals {
				if !v {
					return false, nil
				}
			}
			return true, nil
		case "any":
			for _, v := range vals {
				if v {
					return true, nil
				}
			}
			return false, nil
		default:
			if len(vals) != 1 {
				return false, fmt.Errorf("not() takes exactly one predicate")
			}
			return !vals[0], nil
		}
	}
	if p.i < len(p.toks) && p.toks[p.i].Kind == syntax.Eq {
		p.i++
		if p.i >= len(p.toks) || p.toks[p.i].Kind != syntax.String {
			return false, fmt.Errorf("expected a string after %s =", name)
		}
		val := strings.Trim(p.toks[p.i].Text, `"`)
		p.i++
		switch name {
		case "target_os":
			return cfg.TargetOS == val, nil
		case "target_arch":
			return cfg.TargetArch == val, nil
		case "feature":
			return cfg.Features[val], nil
		}
		return false, fmt.Errorf("unknown cfg option %q", name)
	}
	switch name {
	case "test":
		return cfg.Test, nil
	case "debug_assertions":
		return cfg.Debug, nil
	}
	return false, fmt.Errorf("unknown cfg option %q", name)
}
