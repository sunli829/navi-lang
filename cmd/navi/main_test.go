package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		args       []string
		code       int
		stdout     string
		stderrPart string
	}{
		{args: nil, code: 2, stderrPart: "usage: navi"},
		{args: []string{"version"}, code: 0, stdout: "navi " + version + "\n"},
		{args: []string{"help"}, code: 0},
		{args: []string{"build"}, code: 2, stderrPart: "usage: navi build"},
		{args: []string{"bench"}, code: 1, stderrPart: "planned for M9"},
		{args: []string{"run", "../../tests/run/structs.navi"}, code: 0, stdout: "Point { x: 1, y: 2 }\n(4, 6)\ntrue false\nCircle(1.5) Rect { w: 2.0, h: 3.0 } Empty\n7.0685773 6\nPoint {\n    x: 1,\n    y: 2,\n}\n"},
		{args: []string{"test", "../../tests/syntax/example"}, code: 0},
		// The example uses threads, which native code supports from M6.
		{args: []string{"run", "--interp", "../../tests/syntax/example"}, code: 0},
		{args: []string{"check", "../../tests/syntax/example"}, code: 0},
		{args: []string{"check", "missing-dir"}, code: 1, stderrPart: "missing-dir"},
		{args: []string{"frobnicate"}, code: 2, stderrPart: `unknown command "frobnicate"`},
		{args: []string{"parse"}, code: 2, stderrPart: "usage: navi parse"},
		{args: []string{"parse", "../../tests/syntax/items.navi"}, code: 0, stdout: ""},
		{args: []string{"parse", "missing.navi"}, code: 1, stderrPart: "missing.navi"},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		code := run(tt.args, &stdout, &stderr)
		if code != tt.code {
			t.Errorf("run(%q) = %d, want %d", tt.args, code, tt.code)
		}
		if tt.stdout != "" && stdout.String() != tt.stdout {
			t.Errorf("run(%q) stdout = %q, want %q", tt.args, stdout.String(), tt.stdout)
		}
		if !strings.Contains(stderr.String(), tt.stderrPart) {
			t.Errorf("run(%q) stderr = %q, want it to contain %q", tt.args, stderr.String(), tt.stderrPart)
		}
	}
}
