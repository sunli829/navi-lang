package syntax

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyntaxCorpus parses every file under tests/syntax. Each file covers a
// section of spec/language.md and must parse without errors.
func TestSyntaxCorpus(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "syntax")
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".navi") {
			return err
		}
		n++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sf, errs := ParseFile(NewFile(path, src))
		if errs.Len() > 0 {
			t.Errorf("%v", errs)
		}
		if len(sf.Items) == 0 {
			t.Errorf("%s: no items parsed", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no corpus files found")
	}
}
