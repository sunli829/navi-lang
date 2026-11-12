package types_test

import (
	"path/filepath"
	"testing"

	"navi/internal/build"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/testutil"
	"navi/internal/types"
)

var testCfg = resolve.Config{TargetOS: "linux", TargetArch: "x86_64", Debug: true}

func check(t *testing.T, file string) (*types.Info, *resolve.Crate, []error) {
	t.Helper()
	spec, err := build.LoadCrate(file)
	if err != nil {
		t.Fatal(err)
	}
	info, errs := build.CheckSpecs(testCfg, []*resolve.CrateSpec{build.StdCrate(), spec})
	var crate *resolve.Crate
	for _, c := range info.Res.Crates {
		if !c.IsStd {
			crate = c
		}
	}
	var out []error
	for _, e := range errs {
		out = append(out, e)
	}
	return info, crate, out
}

func TestStdCollects(t *testing.T) {
	_, errs := build.CheckSpecs(testCfg, []*resolve.CrateSpec{build.StdCrate()})
	if len(errs) > 0 {
		t.Fatalf("std has errors:\n%v", errs)
	}
}

func TestSignatures(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "types", "sigs", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no signature tests")
	}
	for _, f := range files {
		info, crate, errs := check(t, f)
		for _, e := range errs {
			t.Errorf("%s: unexpected error: %v", f, e)
		}
		want := testutil.Annotations(t, f, "sig")
		got := map[int][]string{}
		for _, d := range info.Res.Defs {
			if d.Crate != crate || d.File == nil {
				continue
			}
			if s := info.SigString(d); s != "" {
				line := d.File.Position(d.Pos).Line
				got[line] = append(got[line], s)
			}
		}
		for line, w := range want {
			ok := false
			for _, s := range got[line] {
				if s == w {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s:%d: signature %q, got %q", f, line, w, got[line])
			}
		}
	}
}

func TestErrors(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "types", "err", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no error tests")
	}
	for _, f := range files {
		spec, err := build.LoadCrate(f)
		if err != nil {
			t.Fatal(err)
		}
		_, errs := build.CheckSpecs(testCfg, []*resolve.CrateSpec{build.StdCrate(), spec})
		testutil.CheckErrors(t, f, errs)
	}
}

func TestCheckOK(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "check", "ok", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no ok tests")
	}
	for _, f := range files {
		info, crate, errs := check(t, f)
		for _, e := range errs {
			t.Errorf("unexpected error: %v", e)
		}
		want := testutil.Annotations(t, f, "type")
		got := map[int]string{}
		for def, body := range info.Bodies {
			if def.Crate != crate {
				continue
			}
			fn, ok := def.Node.(*syntax.FnDecl)
			if !ok || fn.Body == nil {
				continue
			}
			syntax.Inspect(fn.Body, func(n syntax.Node) bool {
				ls, ok := n.(*syntax.LetStmt)
				if !ok {
					return true
				}
				line := def.File.Position(ls.Let).Line
				if _, seen := got[line]; seen {
					return true
				}
				var first *syntax.IdentPat
				syntax.Inspect(ls.Pat, func(n syntax.Node) bool {
					if ip, ok := n.(*syntax.IdentPat); ok && first == nil {
						first = ip
					}
					return first == nil
				})
				if first == nil {
					return true
				}
				if l := info.Res.Bindings[first]; l != nil && body.Locals[l] != nil {
					got[line] = body.Locals[l].String()
				}
				return true
			})
		}
		for line, w := range want {
			if got[line] != w {
				t.Errorf("%s:%d: type %q, got %q", f, line, w, got[line])
			}
		}
	}
}

func TestCheckErrors(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "check", "err", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no error tests")
	}
	for _, f := range files {
		spec, err := build.LoadCrate(f)
		if err != nil {
			t.Fatal(err)
		}
		_, errs := build.CheckSpecs(testCfg, []*resolve.CrateSpec{build.StdCrate(), spec})
		testutil.CheckErrors(t, f, errs)
	}
}
