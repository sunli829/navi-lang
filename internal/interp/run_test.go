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
