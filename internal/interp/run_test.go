package interp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"navi/internal/build"
	"navi/internal/interp"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/testutil"
)

var cfg = resolve.Config{TargetOS: "linux", TargetArch: "x86_64", Debug: true}

// TestRun runs every program in tests/run on the interpreter and compares
// its output with the // out: lines.
func TestRun(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "run", "*.navi"))
	if len(files) == 0 {
		t.Fatal("no run tests")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(src, []byte("// interp: skip")) || bytes.Contains(src, []byte("// stage0: skip")) {
				t.Skip("marked // interp: skip")
			}
			var want []string
			wantExit := 0
			var wantStderr []string
			for _, line := range strings.Split(string(src), "\n") {
				switch {
				case strings.HasPrefix(line, "// out:"):
					want = append(want, strings.TrimPrefix(strings.TrimPrefix(line, "// out:"), " "))
				case strings.HasPrefix(line, "// exit: "):
					wantExit, _ = strconv.Atoi(strings.TrimPrefix(line, "// exit: "))
				case strings.HasPrefix(line, "// stderr: "):
					wantStderr = append(wantStderr, strings.TrimPrefix(line, "// stderr: "))
				}
			}
			spec, err := build.LoadCrate(f)
			if err != nil {
				t.Fatal(err)
			}
			info, errs := build.CheckSpecs(cfg, []*resolve.CrateSpec{build.StdCrate(), spec})
			if len(errs) > 0 {
				t.Fatalf("compile errors:\n%v", errs)
			}
			main := info.Res.Crates[1].Root.Lookup("main", resolve.ValueNS).Res.Def
			m := interp.New(mir.Build(info))
			var stdout, stderr bytes.Buffer
			m.Stdout, m.Stderr = &stdout, &stderr
			m.Args = []string{f}
			code := m.Run(main)
			got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
			if stdout.Len() == 0 {
				got = nil
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("output:\n%s\nwant:\n%s\nstderr:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), stderr.String())
			}
			if code != wantExit {
				t.Errorf("exit code %d, want %d; stderr:\n%s", code, wantExit, stderr.String())
			}
			for _, s := range wantStderr {
				if !strings.Contains(stderr.String(), s) {
					t.Errorf("stderr %q does not contain %q", stderr.String(), s)
				}
			}
		})
	}
	_ = testutil.Annotations
}

// TestSpecExample runs the complete example from spec/language.md 1.18.
func TestSpecExample(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "syntax", "example")
	want, err := os.ReadFile(filepath.Join(dir, "expected.out"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := build.LoadCrate(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, errs := build.CheckSpecs(cfg, []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	main := info.Res.Crates[1].Root.Lookup("main", resolve.ValueNS).Res.Def
	m := interp.New(mir.Build(info))
	var stdout, stderr bytes.Buffer
	m.Stdout, m.Stderr = &stdout, &stderr
	if code := m.Run(main); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if stdout.String() != string(want) {
		t.Fatalf("output %q, want %q", stdout.String(), want)
	}
}
