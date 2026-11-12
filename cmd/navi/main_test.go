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
		{args: []string{"check"}, code: 1, stderrPart: "planned for M1"},
		{args: []string{"frobnicate"}, code: 2, stderrPart: `unknown command "frobnicate"`},
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
