package resolve_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"navi/internal/build"
	"navi/internal/resolve"
	"navi/internal/syntax"
)

var testCfg = resolve.Config{TargetOS: "linux", TargetArch: "x86_64", Debug: true}

func resolveSpec(t *testing.T, spec *resolve.CrateSpec) (*resolve.Info, resolve.ErrorList) {
	t.Helper()
	return resolve.Resolve(testCfg, []*resolve.CrateSpec{build.StdCrate(), spec})
}

func TestStdResolves(t *testing.T) {
	info, errs := resolve.Resolve(testCfg, []*resolve.CrateSpec{build.StdCrate()})
	if len(errs) > 0 {
		t.Fatalf("std has resolution errors:\n%v", errs)
	}
	if len(info.Crates) != 1 || len(info.Crates[0].Files) < 20 {
		t.Fatalf("std loaded %d files, want at least 20", len(info.Crates[0].Files))
	}
}

func TestExampleCrate(t *testing.T) {
	spec, err := build.LoadCrate(filepath.Join("..", "..", "tests", "syntax", "example"))
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := resolveSpec(t, spec); len(errs) > 0 {
		t.Fatalf("example crate has resolution errors:\n%v", errs)
	}
}

func TestOKCorpus(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "resolve", "ok", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no ok corpus files")
	}
	for _, f := range files {
		spec, err := build.LoadCrate(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, errs := resolveSpec(t, spec); len(errs) > 0 {
			t.Errorf("%s: unexpected errors:\n%v", f, errs)
		}
	}
}

var errorAnnot = regexp.MustCompile(`// error: (.*)$`)

func TestErrorCorpus(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "resolve", "err", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no error corpus files")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int]string{}
		for i, line := range strings.Split(string(src), "\n") {
			if m := errorAnnot.FindStringSubmatch(line); m != nil {
				want[i+1] = strings.TrimSpace(m[1])
			}
		}
		spec, err := build.LoadCrate(f)
		if err != nil {
			t.Fatal(err)
		}
		_, errs := resolveSpec(t, spec)
		matched := map[int]bool{}
		for _, e := range errs {
			w, ok := want[e.Pos.Line]
			if !ok || !strings.Contains(e.Msg, w) {
				t.Errorf("%s: unexpected error %v", f, e)
				continue
			}
			matched[e.Pos.Line] = true
		}
		for line, w := range want {
			if !matched[line] {
				t.Errorf("%s:%d: expected an error containing %q", f, line, w)
			}
		}
	}
}

func mapCrate(files map[string]string) *resolve.CrateSpec {
	fsys := fstest.MapFS{}
	for name, src := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	return &resolve.CrateSpec{Name: "app", FS: fsys, Root: "src/main.navi"}
}

func TestModuleFiles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string // substring of the error, or "" for success
	}{
		{"sibling file", map[string]string{
			"src/main.navi": "mod a; fn main() { a::f(); }",
			"src/a.navi":    "pub fn f() {}",
		}, ""},
		{"mod.navi", map[string]string{
			"src/main.navi":  "mod a; fn main() { a::b::g(); }",
			"src/a/mod.navi": "pub mod b;",
			"src/a/b.navi":   "pub fn g() {}",
		}, ""},
		{"nested non-mod file", map[string]string{
			"src/main.navi": "mod a; fn main() { a::b::g(); }",
			"src/a.navi":    "pub mod b;",
			"src/a/b.navi":  "pub fn g() {}",
		}, ""},
		{"inline then file", map[string]string{
			"src/main.navi": "mod a { pub mod b; } fn main() { a::b::g(); }",
			"src/a/b.navi":  "pub fn g() {}",
		}, ""},
		{"missing", map[string]string{
			"src/main.navi": "mod a;",
		}, "file not found for module a"},
		{"both", map[string]string{
			"src/main.navi":  "mod a;",
			"src/a.navi":     "",
			"src/a/mod.navi": "",
		}, "defined in both"},
		{"cfg disabled module needs no file", map[string]string{
			"src/main.navi": `#[cfg(target_os = "windows")] mod win; fn main() {}`,
		}, ""},
		{"cfg disabled item is invisible", map[string]string{
			"src/main.navi": `#[cfg(target_os = "windows")] fn only_win() {} fn main() { only_win(); }`,
		}, "cannot find value only_win"},
		{"cfg combinators", map[string]string{
			"src/main.navi": `#[cfg(all(target_os = "linux", not(test), any(debug_assertions, feature = "x")))] fn f() {} fn main() { f(); }`,
		}, ""},
		{"file-level cfg", map[string]string{
			"src/main.navi": "mod a; fn main() { a::f(); }",
			"src/a.navi":    "#![cfg(test)]\npub fn f() {}",
		}, "cannot find f in app::a"},
		{"malformed cfg", map[string]string{
			"src/main.navi": `#[cfg(target_os)] fn f() {}`,
		}, "malformed cfg predicate"},
		{"syntax error in module file", map[string]string{
			"src/main.navi": "mod a;",
			"src/a.navi":    "fn f( {}",
		}, "expected"},
	}
	for _, tt := range tests {
		_, errs := resolveSpec(t, mapCrate(tt.files))
		switch {
		case tt.want == "" && len(errs) > 0:
			t.Errorf("%s: unexpected errors:\n%v", tt.name, errs)
		case tt.want != "" && !strings.Contains(errs.Error(), tt.want):
			t.Errorf("%s: got %q, want an error containing %q", tt.name, errs.Error(), tt.want)
		}
	}
}

// findPath returns the resolution of the first path in the crate whose
// rendering equals text.
func findRes(info *resolve.Info, text string) (resolve.Res, bool) {
	for p, res := range info.Paths {
		if syntax.Dump(p) == text {
			return res, true
		}
	}
	return resolve.Res{}, false
}

func TestResolutionResults(t *testing.T) {
	info, errs := resolveSpec(t, mapCrate(map[string]string{"src/main.navi": `
		struct Point { x: i32 }
		const ZERO: i32 = 0;
		fn main() {
			let opt = Some(1);
			let v = Vec::<i32>::new();
			let m = i32::MAX;
			match 3 { ZERO => {}, n => {} }
			let p = Point { x: 1 };
		}
	`}))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	checks := []struct {
		path string
		kind resolve.ResKind
		def  resolve.DefKind
		rest int
	}{
		{"Some", resolve.ResDef, resolve.DefVariant, 0},
		{"Vec<i32>::new", resolve.ResDef, resolve.DefStruct, 1},
		{"i32::MAX", resolve.ResPrim, 0, 1},
		{"Point", resolve.ResDef, resolve.DefStruct, 0},
	}
	for _, c := range checks {
		res, ok := findRes(info, c.path)
		if !ok {
			t.Errorf("path %s not recorded", c.path)
			continue
		}
		if res.Kind != c.kind || len(res.Rest) != c.rest || (c.kind == resolve.ResDef && res.Def.Kind != c.def) {
			t.Errorf("%s resolved to kind=%d def=%v rest=%d", c.path, res.Kind, res.Def, len(res.Rest))
		}
	}
	consts, binds := 0, 0
	for _, p := range userIdentPats(info) {
		if _, ok := info.PatConsts[p]; ok && p.Name.Value == "ZERO" {
			consts++
		}
		if _, ok := info.Bindings[p]; ok && p.Name.Value == "n" {
			binds++
		}
	}
	if consts != 1 || binds != 1 {
		t.Errorf("pattern ZERO const=%d, n binding=%d; want 1 and 1", consts, binds)
	}
}

func TestOrPatternSharesLocals(t *testing.T) {
	info, errs := resolveSpec(t, mapCrate(map[string]string{"src/main.navi": `
		enum E { A(i32), B(i32) }
		fn main() { match E::A(1) { E::A(n) | E::B(n) => { let m = n; } } }
	`}))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var locals []*resolve.Local
	for _, p := range userIdentPats(info) {
		if l := info.Bindings[p]; l != nil && p.Name.Value == "n" {
			locals = append(locals, l)
		}
	}
	if len(locals) != 2 || locals[0] != locals[1] {
		t.Fatalf("or-pattern bindings of n: got %d locals, want 2 entries sharing one local", len(locals))
	}
}

// userIdentPats returns the identifier patterns of the non-std crate.
func userIdentPats(info *resolve.Info) []*syntax.IdentPat {
	var pats []*syntax.IdentPat
	for _, c := range info.Crates {
		if c.IsStd {
			continue
		}
		for _, f := range c.Files {
			syntax.Inspect(f, func(n syntax.Node) bool {
				if p, ok := n.(*syntax.IdentPat); ok {
					pats = append(pats, p)
				}
				return true
			})
		}
	}
	return pats
}
