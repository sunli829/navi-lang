package mir

import (
	"sort"

	"navi/internal/types"
)

// boxAddressTaken moves every local whose address is taken into a heap
// cell, allocated where the local starts its life (its StorageLive), so a
// reference stays valid after the function returns and a variable
// declared in a loop is a new variable in every iteration (as in Go).
// Arguments and the return place get a cell at function entry. All
// StorageLive statements are removed.
//
// There is no escape analysis yet: every address-taken local is boxed.
func boxAddressTaken(ctx *types.Ctx, f *Func) {
	taken := map[int]bool{}
	for _, b := range f.Blocks {
		for _, s := range b.Stmts {
			if a, ok := s.(Assign); ok {
				if r, ok := a.Rv.(Ref); ok && (len(r.Place.Proj) == 0 || r.Place.Proj[0].Kind != PDeref) {
					taken[r.Place.Local] = true
				}
			}
		}
	}
	// In local order, so that the result is deterministic.
	var takenOrder []int
	for l := range taken {
		takenOrder = append(takenOrder, l)
	}
	sort.Ints(takenOrder)
	firstArg := 1
	if f.Closure {
		firstArg = 2
	}
	// Locals that start their life at entry (arguments, the return place)
	// are redirected to a new cell local.
	redirect := map[int]int{}
	var entry []Stmt
	var exit []Stmt
	live := map[int]bool{}
	for _, l := range takenOrder {
		if l == 0 || l >= firstArg && l <= f.NArgs || f.Closure && l == 1 {
			t := f.Locals[l].Type
			cell := len(f.Locals)
			f.Locals = append(f.Locals, &Local{Type: ctx.Ref(true, t), Name: f.Locals[l].Name, Boxed: true})
			redirect[l] = cell
			entry = append(entry, Box{Local: cell, Type: t})
			if l == 0 {
				exit = append(exit, Assign{Place: LocalPlace(0), Rv: Use{X: Copy(LocalPlace(cell).Deref())}})
			} else {
				entry = append(entry, Assign{Place: LocalPlace(cell).Deref(), Rv: Use{X: Copy(LocalPlace(l))}})
			}
		}
	}
	if len(taken) == 0 {
		stripStorage(f)
		return
	}
	// Other locals become pointers to cells allocated at each StorageLive.
	for _, b := range f.Blocks {
		for _, s := range b.Stmts {
			if sl, ok := s.(StorageLive); ok && taken[sl.Local] {
				if _, ok := redirect[sl.Local]; !ok {
					live[sl.Local] = true
				}
			}
		}
	}
	elem := map[int]*types.Type{}
	for _, l := range takenOrder {
		if _, ok := redirect[l]; ok {
			continue
		}
		t := f.Locals[l].Type
		elem[l] = t
		f.Locals[l].Type = ctx.Ref(true, t)
		f.Locals[l].Boxed = true
		if !live[l] {
			// No declaration point (generated code): allocate at entry.
			entry = append(entry, Box{Local: l, Type: t})
		}
	}
	fix := func(p Place) Place {
		if c, ok := redirect[p.Local]; ok {
			return Place{Local: c, Proj: append([]Proj{{Kind: PDeref}}, p.Proj...)}
		}
		if _, ok := elem[p.Local]; ok {
			return Place{Local: p.Local, Proj: append([]Proj{{Kind: PDeref}}, p.Proj...)}
		}
		return p
	}
	fixOp := func(o Operand) Operand {
		if o.Kind == OpCopy {
			o.Place = fix(o.Place)
		}
		return o
	}
	fixOps := func(os []Operand) []Operand {
		out := make([]Operand, len(os))
		for i, o := range os {
			out[i] = fixOp(o)
		}
		return out
	}
	for bi, b := range f.Blocks {
		var out []Stmt
		if bi == 0 {
			out = append(out, entry...)
		}
		for _, s := range b.Stmts {
			switch s := s.(type) {
			case StorageLive:
				if t, ok := elem[s.Local]; ok && live[s.Local] {
					out = append(out, Box{Local: s.Local, Type: t})
				}
				continue
			case Assign:
				s.Place = fix(s.Place)
				switch r := s.Rv.(type) {
				case Use:
					r.X = fixOp(r.X)
					s.Rv = r
				case Ref:
					r.Place = fix(r.Place)
					s.Rv = r
				case Bin:
					r.X, r.Y = fixOp(r.X), fixOp(r.Y)
					s.Rv = r
				case Un:
					r.X = fixOp(r.X)
					s.Rv = r
				case Cast:
					r.X = fixOp(r.X)
					s.Rv = r
				case Aggregate:
					r.Ops = fixOps(r.Ops)
					s.Rv = r
				case Discr:
					r.Place = fix(r.Place)
					s.Rv = r
				case Len:
					r.Place = fix(r.Place)
					s.Rv = r
				case Unsize:
					r.X = fixOp(r.X)
					s.Rv = r
				case MakeDyn:
					r.X = fixOp(r.X)
					s.Rv = r
				}
				out = append(out, s)
			case SetDiscr:
				s.Place = fix(s.Place)
				out = append(out, s)
			case PushDefer:
				s.Fn = fixOp(s.Fn)
				s.Args = fixOps(s.Args)
				out = append(out, s)
			default:
				out = append(out, s)
			}
		}
		switch t := b.Term.(type) {
		case Switch:
			t.X = fixOp(t.X)
			b.Term = t
		case Call:
			t.Fn = fixOp(t.Fn)
			t.Args = fixOps(t.Args)
			t.Dest = fix(t.Dest)
			b.Term = t
		case Assert:
			t.Cond = fixOp(t.Cond)
			t.Len = fixOp(t.Len)
			t.Index = fixOp(t.Index)
			b.Term = t
		case Return:
			out = append(out, exit...)
		}
		b.Stmts = out
	}
}

func stripStorage(f *Func) {
	for _, b := range f.Blocks {
		out := b.Stmts[:0]
		for _, s := range b.Stmts {
			if _, ok := s.(StorageLive); !ok {
				out = append(out, s)
			}
		}
		b.Stmts = out
	}
}
