package syntax

import (
	"fmt"
	"sort"
	"strings"
)

// File is a source file being compiled.
type File struct {
	Name  string
	Src   []byte
	lines []Pos // offsets of line starts, computed on demand
}

// NewFile returns a File for the given name and contents.
func NewFile(name string, src []byte) *File {
	return &File{Name: name, Src: src}
}

// Position is a human-readable source location. Line and Col count from one;
// Col counts bytes.
type Position struct {
	File string
	Line int
	Col  int
}

func (p Position) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Position converts a byte offset into a line and column.
func (f *File) Position(pos Pos) Position {
	if f.lines == nil {
		f.lines = append(f.lines, 0)
		for i, b := range f.Src {
			if b == '\n' {
				f.lines = append(f.lines, Pos(i+1))
			}
		}
	}
	i := sort.Search(len(f.lines), func(i int) bool { return f.lines[i] > pos }) - 1
	return Position{File: f.Name, Line: i + 1, Col: int(pos-f.lines[i]) + 1}
}

// Diagnostic is an error found while compiling a file.
type Diagnostic struct {
	Pos Pos
	Msg string
}

// Errors is the list of diagnostics produced for one file.
type Errors struct {
	File  *File
	Diags []Diagnostic
}

func (e *Errors) add(pos Pos, format string, args ...any) {
	e.Diags = append(e.Diags, Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// Len returns the number of diagnostics.
func (e *Errors) Len() int { return len(e.Diags) }

func (e *Errors) Error() string {
	var b strings.Builder
	for i, d := range e.Diags {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s: %s", e.File.Position(d.Pos), d.Msg)
	}
	return b.String()
}
