// Package diag holds the diagnostics shared by the compiler phases.
package diag

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"navi/internal/syntax"
)

// Error is one diagnostic.
type Error struct {
	Pos syntax.Position
	Msg string
}

func (e Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// List is a list of diagnostics.
type List []Error

func (l List) Error() string {
	var b strings.Builder
	for i, e := range l {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// Sort orders diagnostics by file, line and column.
func (l List) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		a, b := l[i].Pos, l[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}

// Sink collects diagnostics; it is safe for concurrent use.
type Sink struct {
	mu   sync.Mutex
	list List
}

// Errorf records a diagnostic at pos in f (f may be nil).
func (s *Sink) Errorf(f *syntax.File, pos syntax.Pos, format string, args ...any) {
	e := Error{Msg: fmt.Sprintf(format, args...)}
	if f != nil {
		e.Pos = f.Position(pos)
	}
	s.mu.Lock()
	s.list = append(s.list, e)
	s.mu.Unlock()
}

// Add records an already formatted diagnostic.
func (s *Sink) Add(e Error) {
	s.mu.Lock()
	s.list = append(s.list, e)
	s.mu.Unlock()
}

// Len returns the number of diagnostics so far.
func (s *Sink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.list)
}

// List returns the sorted diagnostics.
func (s *Sink) List() List {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := append(List(nil), s.list...)
	l.Sort()
	return l
}
