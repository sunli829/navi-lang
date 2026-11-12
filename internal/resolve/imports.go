package resolve

import (
	"strings"

	"navi/internal/syntax"
)

type lookupStatus uint8

const (
	found lookupStatus = iota
	notFound
	private
	ambiguous
)

// lookupIn looks a name up in module m as seen from module from: first the
// items and single imports of m, then its glob imports.
func (r *resolver) lookupIn(m *Module, name string, ns Namespace, from *Module) (*Binding, lookupStatus) {
	if b := m.bindings[ns][name]; b != nil {
		if !b.Vis.accessibleFrom(from) {
			return b, private
		}
		return b, found
	}
	var hit *Binding
	sawPrivate := false
	for _, b := range m.globs[ns][name] {
		if !b.Vis.accessibleFrom(from) {
			sawPrivate = true
			continue
		}
		if hit != nil && !sameRes(hit.Res, b.Res) {
			return hit, ambiguous
		}
		hit = b
	}
	if hit != nil {
		return hit, found
	}
	if sawPrivate {
		return nil, private
	}
	return nil, notFound
}

func sameRes(a, b Res) bool {
	return a.Kind == b.Kind && a.Def == b.Def && a.Prim == b.Prim
}

// moduleOf returns the module-like scope a resolution names: a module, an
// enum or a crate root.
func moduleOf(res Res) *Module {
	if res.Kind == ResCrate || res.Kind == ResDef && (res.Def.Kind == DefMod || res.Def.Kind == DefEnum) {
		return res.Def.Module
	}
	return nil
}

// pathError describes why a module path failed to resolve.
type pathError struct {
	pos syntax.Pos
	msg string
}

// importScope resolves the first segment of a use path: a name in scope in
// the importing module (looking through block scopes) or an extern crate.
func (r *resolver) importScope(from *Module, name string, ns Namespace) (Res, bool) {
	for m := from; m != nil; m = m.Parent {
		if b, st := r.lookupIn(m, name, ns, from); st == found {
			return b.Res, true
		}
		if !m.Anonymous {
			break
		}
	}
	if ns == TypeNS {
		if c := from.Crate.Deps[name]; c != nil && c.Root != nil {
			return Res{Kind: ResCrate, Def: c.Root.Def}, true
		}
		if from.Crate.IsStd && name == "std" {
			return Res{Kind: ResCrate, Def: from.Crate.Root.Def}, true
		}
	}
	return Res{}, false
}

// modulePath resolves segs to a module-like scope, for use paths and for
// the module prefix of other paths when walking from a module.
func (r *resolver) modulePath(from *Module, segs []*syntax.Name) (*Module, *pathError) {
	var cur *Module
	for i, seg := range segs {
		switch seg.Value {
		case "crate":
			if i != 0 {
				return nil, &pathError{seg.NamePos, "crate can only appear at the start of a path"}
			}
			cur = from.Crate.Root
			continue
		case "self":
			if i != 0 {
				return nil, &pathError{seg.NamePos, "self can only appear at the start of a path"}
			}
			cur = from.nearestNamed()
			continue
		case "super":
			if i == 0 {
				cur = from.nearestNamed()
			} else if segs[i-1].Value != "super" && segs[i-1].Value != "self" {
				return nil, &pathError{seg.NamePos, "super can only follow self or super at the start of a path"}
			}
			if cur.Parent == nil {
				return nil, &pathError{seg.NamePos, "there are too many leading super keywords"}
			}
			cur = cur.Parent.nearestNamed()
			continue
		}
		var res Res
		if i == 0 {
			var ok bool
			res, ok = r.importScope(from, seg.Value, TypeNS)
			if !ok {
				return nil, &pathError{seg.NamePos, "cannot find module or crate " + seg.Value + " in this scope"}
			}
		} else {
			b, st := r.lookupIn(cur, seg.Value, TypeNS, from)
			switch st {
			case notFound:
				return nil, &pathError{seg.NamePos, "cannot find " + seg.Value + " in " + cur.Path()}
			case private:
				return nil, &pathError{seg.NamePos, seg.Value + " is private in " + cur.Path()}
			case ambiguous:
				return nil, &pathError{seg.NamePos, seg.Value + " is ambiguous (imported by more than one glob)"}
			}
			res = b.Res
		}
		cur = moduleOf(res)
		if cur == nil {
			return nil, &pathError{seg.NamePos, seg.Value + " is not a module or enum"}
		}
	}
	return cur, nil
}

// resolveImports resolves all use declarations to a fixed point: an import
// may depend on bindings created by other imports, including globs.
func (r *resolver) resolveImports() {
	for changed := true; changed; {
		changed = false
		for _, imp := range r.imports {
			if imp.Glob {
				if r.propagateGlob(imp) {
					changed = true
				}
				continue
			}
			if !imp.done && r.trySingleImport(imp) {
				imp.done = true
				changed = true
			}
		}
	}
	for _, imp := range r.imports {
		if imp.Glob {
			if _, perr := r.modulePath(imp.Module, imp.Segs); perr != nil {
				r.errorf(imp.File, perr.pos, "unresolved import: %s", perr.msg)
			}
			continue
		}
		if !imp.done {
			r.reportImport(imp)
		}
	}
}

// propagateGlob copies the bindings visible through a glob import into the
// importing module. It returns true if anything new was added.
func (r *resolver) propagateGlob(imp *Import) bool {
	src, perr := r.modulePath(imp.Module, imp.Segs)
	if perr != nil {
		return false
	}
	imp.done = true
	dst := imp.Module
	added := false
	for ns := Namespace(0); ns < numNS; ns++ {
		add := func(name string, b *Binding) {
			if !b.Vis.accessibleFrom(dst) {
				return
			}
			for _, old := range dst.globs[ns][name] {
				if sameRes(old.Res, b.Res) {
					return
				}
			}
			dst.globs[ns][name] = append(dst.globs[ns][name], &Binding{Res: b.Res, Vis: imp.Vis, Import: imp})
			added = true
		}
		for name, b := range src.bindings[ns] {
			add(name, b)
		}
		for name, bs := range src.globs[ns] {
			for _, b := range bs {
				add(name, b)
			}
		}
	}
	return added
}

// trySingleImport resolves a non-glob import if its target is available.
func (r *resolver) trySingleImport(imp *Import) bool {
	last := imp.Segs[len(imp.Segs)-1]
	var hits [numNS]*Binding
	if len(imp.Segs) == 1 || isModuleKeyword(last.Value) {
		// use std; use self::x as y where the last segment is a keyword is
		// handled by modulePath.
		if isModuleKeyword(last.Value) {
			m, perr := r.modulePath(imp.Module, imp.Segs)
			if perr != nil {
				return false
			}
			hits[TypeNS] = &Binding{Res: Res{Kind: ResDef, Def: m.Def}}
		} else {
			for ns := Namespace(0); ns < numNS; ns++ {
				if res, ok := r.importScope(imp.Module, last.Value, ns); ok {
					hits[ns] = &Binding{Res: res}
				}
			}
		}
	} else {
		src, perr := r.modulePath(imp.Module, imp.Segs[:len(imp.Segs)-1])
		if perr != nil {
			return false
		}
		for ns := Namespace(0); ns < numNS; ns++ {
			if b, st := r.lookupIn(src, last.Value, ns, imp.Module); st == found {
				hits[ns] = b
			}
		}
	}
	any := false
	for ns, b := range hits {
		if b == nil {
			continue
		}
		any = true
		if imp.Name == "_" {
			if b.Res.Kind == ResDef && b.Res.Def.Kind == DefTrait {
				imp.Module.underscoreTraits = append(imp.Module.underscoreTraits, b.Res.Def)
			}
			continue
		}
		if old := imp.Module.bindings[ns][imp.Name]; old != nil {
			if !sameRes(old.Res, b.Res) {
				r.errorf(imp.File, imp.Pos, "the name %s is defined multiple times in %s", imp.Name, imp.Module.Path())
			}
			continue
		}
		imp.Module.bindings[ns][imp.Name] = &Binding{Res: b.Res, Vis: imp.Vis, Import: imp}
	}
	return any
}

func isModuleKeyword(s string) bool { return s == "crate" || s == "self" || s == "super" }

// reportImport explains why a single import could not be resolved.
func (r *resolver) reportImport(imp *Import) {
	last := imp.Segs[len(imp.Segs)-1]
	if len(imp.Segs) == 1 {
		r.errorf(imp.File, last.NamePos, "unresolved import: cannot find %s in this scope", last.Value)
		return
	}
	src, perr := r.modulePath(imp.Module, imp.Segs[:len(imp.Segs)-1])
	if perr != nil {
		r.errorf(imp.File, perr.pos, "unresolved import: %s", perr.msg)
		return
	}
	for ns := Namespace(0); ns < numNS; ns++ {
		if _, st := r.lookupIn(src, last.Value, ns, imp.Module); st == private {
			r.errorf(imp.File, last.NamePos, "%s is private in %s", last.Value, src.Path())
			return
		} else if st == ambiguous {
			r.errorf(imp.File, last.NamePos, "%s is ambiguous (imported by more than one glob)", last.Value)
			return
		}
	}
	r.errorf(imp.File, last.NamePos, "unresolved import: cannot find %s in %s", last.Value, src.Path())
}

// joinSegs renders path segments for messages.
func joinSegs(segs []*syntax.Name) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.Value
	}
	return strings.Join(parts, "::")
}
