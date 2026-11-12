package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"navi/internal/build"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// resolveDump prints every name resolution of a crate and std (used to
// check the self-hosted compiler against stage0).
func resolveDump(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: navi resolve-dump <crate or file>")
		return 2
	}
	spec, err := build.LoadCrate(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, errs := resolve.Resolve(build.HostConfig(), []*resolve.CrateSpec{build.StdCrate(), spec})
	for _, e := range errs {
		fmt.Fprintln(stdout, "error", e.Error())
	}
	files := map[*syntax.File]bool{}
	var lines []string
	pos := func(f *syntax.File, p syntax.Pos) string { return f.Position(p).String() }
	defPos := func(d *resolve.Def) string {
		if d == nil {
			return "<nil>"
		}
		if d.File == nil {
			return fmt.Sprintf("%s %s", d.Kind, d.Name)
		}
		return fmt.Sprintf("%s %s @%s", d.Kind, d.Name, pos(d.File, d.Pos))
	}
	desc := func(r resolve.Res) string {
		var s string
		switch r.Kind {
		case resolve.ResErr:
			s = "err"
		case resolve.ResDef:
			s = "def " + defPos(r.Def)
		case resolve.ResLocal:
			s = fmt.Sprintf("local %s@%d", r.Local.Name, r.Local.Pos)
		case resolve.ResPrim:
			s = "prim " + r.Prim
		case resolve.ResSelfTy:
			s = "Self " + defPos(r.Def)
		case resolve.ResCrate:
			s = "crate " + r.Def.Name
		case resolve.ResQualified:
			s = "qualified"
		}
		if len(r.Rest) > 0 {
			s += fmt.Sprintf(" rest=%d", len(r.Rest))
		}
		return s
	}
	pathFile := map[*syntax.Path]*syntax.File{}
	patFile := map[*syntax.IdentPat]*syntax.File{}
	for _, c := range info.Crates {
		for _, sf := range c.Files {
			files[sf.File] = true
			f := sf.File
			syntax.Inspect(sf, func(n syntax.Node) bool {
				switch n := n.(type) {
				case *syntax.Path:
					pathFile[n] = f
				case *syntax.IdentPat:
					patFile[n] = f
				}
				return true
			})
		}
	}
	where := func(f *syntax.File, p syntax.Pos) string {
		if f == nil {
			return fmt.Sprintf("?:%d", p)
		}
		return pos(f, p)
	}
	for p, r := range info.Paths {
		lines = append(lines, fmt.Sprintf("%s: path %s", where(pathFile[p], p.Pos()), desc(r)))
	}
	for p, l := range info.Bindings {
		lines = append(lines, fmt.Sprintf("%s: bind %s@%d mut=%v", where(patFile[p], p.Pos()), l.Name, l.Pos, l.Mut))
	}
	for p, r := range info.PatConsts {
		lines = append(lines, fmt.Sprintf("%s: patconst %s", where(patFile[p], p.Pos()), desc(r)))
	}
	sort.Strings(lines)
	fmt.Fprintln(stdout, strings.Join(lines, "\n"))
	fmt.Fprintf(stdout, "defs %d\n", len(info.Defs))
	return 0
}

// typesDump prints the signatures and body types of a crate and std (used
// to check the self-hosted compiler against stage0).
func typesDump(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: navi types-dump <crate or file>")
		return 2
	}
	spec, err := build.LoadCrate(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, errs := build.CheckSpecs(build.HostConfig(), []*resolve.CrateSpec{build.StdCrate(), spec})
	for _, e := range errs {
		fmt.Fprintln(stdout, "error", e.Error())
	}
	for _, d := range info.Res.Defs {
		if s := info.SigString(d); s != "" {
			fmt.Fprintf(stdout, "sig %d %s\n", d.ID, s)
		}
	}
	for _, d := range info.Res.Defs {
		b := info.Bodies[d]
		if b == nil {
			continue
		}
		fmt.Fprintf(stdout, "body %d %s\n", d.ID, d.Name)
		for _, l := range b.DumpLines(d.File) {
			fmt.Fprintln(stdout, l)
		}
	}
	return 0
}

// mirDump prints the MIR of every body of a crate and std, then the
// closures in creation order.
func mirDump(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: navi mir-dump <crate or file>")
		return 2
	}
	spec, err := build.LoadCrate(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, errs := build.CheckSpecs(build.HostConfig(), []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(stdout, "error", e.Error())
		}
		return 1
	}
	p := mir.Build(info)
	var b strings.Builder
	for _, d := range info.Res.Defs {
		if f := p.Funcs[d]; f != nil {
			b.WriteString(f.String())
		}
	}
	for _, f := range p.Closures() {
		b.WriteString(f.String())
	}
	io.WriteString(stdout, b.String())
	return 0
}
