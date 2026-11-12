package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"testing"

	"navi/internal/resolve"
)

// TestNativeRun compiles every program in tests/run to a native binary
// and compares its output with the // out: lines (the same expectations
// the interpreter is held to).
func TestNativeRun(t *testing.T) {
	// NAVI_TEST_TARGET selects another target the host can run (for
	// example macos-x64 under Rosetta).
	target := HostTarget()
	if name := os.Getenv("NAVI_TEST_TARGET"); name != "" {
		tt, err := ParseTarget(name)
		if err != nil {
			t.Fatal(err)
		}
		target = tt
	} else if !NativeSupported() {
		t.Skip("native code generation is not available on this host")
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "run", "*.navi"))
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(src, []byte("// native: skip")) {
				t.Skip("marked // native: skip")
			}
			var want, wantStderr []string
			wantExit := 0
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
			spec, err := LoadCrate(f)
			if err != nil {
				t.Fatal(err)
			}
			info, errs := CheckSpecs(target.Config(true), []*resolve.CrateSpec{StdCrate(), spec})
			if len(errs) > 0 {
				t.Fatalf("compile errors:\n%v", errs)
			}
			exe := filepath.Join(t.TempDir(), "prog")
			if target.OS == "windows" {
				exe += ".exe"
			}
			if err := Native(info, FindMain(info, spec.Name), exe, target, true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err = cmd.Run()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
				if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					code = 128 + int(ws.Signal())
				}
				if ctx.Err() != nil {
					t.Fatalf("timed out; stderr:\n%s", stderr.String())
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
			if stdout.Len() == 0 {
				got = nil
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("output:\n%s\nwant:\n%s\nstderr:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), stderr.String())
			}
			if code != wantExit {
				t.Errorf("exit code %d (%v), want %d; stderr:\n%s", code, err, wantExit, stderr.String())
			}
			for _, s := range wantStderr {
				if !strings.Contains(stderr.String(), s) {
					t.Errorf("stderr %q does not contain %q", stderr.String(), s)
				}
			}
		})
	}
}
