// Package testutil holds helpers shared by the compiler's tests.
package testutil

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"navi/internal/diag"
)

// Annotations returns the text after `// <tag>: ` on each line of file,
// keyed by line number.
func Annotations(t *testing.T, file, tag string) map[int]string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`// ` + regexp.QuoteMeta(tag) + `: (.*)$`)
	out := map[int]string{}
	for i, line := range strings.Split(string(src), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			out[i+1] = strings.TrimSpace(m[1])
		}
	}
	return out
}

// CheckErrors compares the diagnostics reported for file with its
// `// error: text` annotations.
func CheckErrors(t *testing.T, file string, errs diag.List) {
	t.Helper()
	want := Annotations(t, file, "error")
	matched := map[int]bool{}
	for _, e := range errs {
		w, ok := want[e.Pos.Line]
		if !ok || !strings.HasSuffix(e.Pos.File, fileBase(file)) || !strings.Contains(e.Msg, w) {
			t.Errorf("%s: unexpected error %v", file, e)
			continue
		}
		matched[e.Pos.Line] = true
	}
	for line, w := range want {
		if !matched[line] {
			t.Errorf("%s:%d: expected an error containing %q", file, line, w)
		}
	}
}

func fileBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
