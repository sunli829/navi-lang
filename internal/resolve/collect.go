package resolve

import (
	"io/fs"
	"path"

	"navi/internal/syntax"
)

// collectItems defines the items of module m (declared in file) and
// records its imports. Child modules are collected recursively.
func (r *resolver) collectItems(m *Module, file *syntax.File, items []syntax.Item, files map[string]*parsedFile) {
	m.file = file
	for _, it := range items {
		r.collectItem(m, file, it, files)
	}
}

func (r *resolver) collectItem(m *Module, file *syntax.File, it syntax.Item, files map[string]*parsedFile) {
	base := itemBase(it)
	if !r.cfgEnabled(file, base.Attrs) {
		return
	}
	m.items = append(m.items, it)
	c := m.Crate
	vis := r.visibility(m, file, base)
	define := func(kind DefKind, name *syntax.Name, ns ...Namespace) *Def {
		d := r.newDef(kind, name.Value, file, it, name.NamePos, m.Def, c)
		d.Vis = vis
		for _, n := range ns {
			r.bind(m, file, n, name, Res{Kind: ResDef, Def: d}, vis)
		}
		return d
	}

	switch it := it.(type) {
	case *syntax.FnDecl:
		define(DefFn, it.Name, ValueNS)
	case *syntax.StructDecl:
		if it.Kind == syntax.StructNamed {
			define(DefStruct, it.Name, TypeNS)
		} else {
			define(DefStruct, it.Name, TypeNS, ValueNS).Ctor = true
		}
	case *syntax.EnumDecl:
		d := define(DefEnum, it.Name, TypeNS)
		em := newModule(c, m, it.Name.Value)
		em.IsEnum = true
		em.Def = d
		d.Module = em
		for _, v := range it.Variants {
			vd := r.newDef(DefVariant, v.Name.Value, file, v, v.Name.NamePos, d, c)
			vd.Vis = vis
			r.info.DefOf[v] = vd
			r.bind(em, file, TypeNS, v.Name, Res{Kind: ResDef, Def: vd}, Public)
			if v.Kind != syntax.StructNamed {
				vd.Ctor = true
				r.bind(em, file, ValueNS, v.Name, Res{Kind: ResDef, Def: vd}, Public)
			}
		}
	case *syntax.TraitDecl:
		d := define(DefTrait, it.Name, TypeNS)
		r.collectAssoc(d, file, it.Items)
	case *syntax.TypeAlias:
		define(DefTypeAlias, it.Name, TypeNS)
	case *syntax.ConstDecl:
		if it.Name.Value == "_" {
			r.newDef(DefConst, "_", file, it, it.Name.NamePos, m.Def, c)
		} else {
			define(DefConst, it.Name, ValueNS)
		}
	case *syntax.StaticDecl:
		define(DefStatic, it.Name, ValueNS)
	case *syntax.ImplDecl:
		d := r.newDef(DefImpl, "", file, it, it.Pos(), m.Def, c)
		r.collectAssoc(d, file, it.Items)
	case *syntax.ExternBlock:
		for _, ei := range it.Items {
			if !r.cfgEnabled(file, itemBase(ei).Attrs) {
				continue
			}
			evis := r.visibility(m, file, itemBase(ei))
			switch ei := ei.(type) {
			case *syntax.FnDecl:
				d := r.newDef(DefExternFn, ei.Name.Value, file, ei, ei.Name.NamePos, m.Def, c)
				d.Vis = evis
				r.bind(m, file, ValueNS, ei.Name, Res{Kind: ResDef, Def: d}, evis)
			case *syntax.StaticDecl:
				d := r.newDef(DefExternStatic, ei.Name.Value, file, ei, ei.Name.NamePos, m.Def, c)
				d.Vis = evis
				r.bind(m, file, ValueNS, ei.Name, Res{Kind: ResDef, Def: d}, evis)
			}
		}
	case *syntax.ModDecl:
		d := define(DefMod, it.Name, TypeNS)
		child := newModule(c, m, it.Name.Value)
		child.Def = d
		d.Module = child
		if it.Inline {
			child.Dir = path.Join(m.Dir, it.Name.Value)
			r.collectItems(child, file, it.Items, files)
			return
		}
		p, candidates := modFile(c.FS, m.Dir, it.Name.Value)
		if p == "" {
			if len(candidates) == 2 && fileExists(c, candidates[0]) {
				r.errorf(file, it.Name.NamePos, "module %s is defined in both %s and %s", it.Name.Value, candidates[0], candidates[1])
			} else {
				r.errorf(file, it.Name.NamePos, "file not found for module %s (looked for %s and %s)", it.Name.Value, candidates[0], candidates[1])
			}
			return
		}
		pf := files[p]
		if pf == nil {
			return // read error, already reported
		}
		child.Dir = childDir(p, false)
		child.file = pf.sf.File
		c.Files = append(c.Files, pf.sf)
		if r.cfgEnabled(pf.sf.File, pf.sf.Attrs) {
			r.collectItems(child, pf.sf.File, pf.sf.Items, files)
		}
	case *syntax.UseDecl:
		r.collectUse(m, file, it.Tree, nil, vis)
	}
}

func fileExists(c *Crate, p string) bool {
	_, err := fs.Stat(c.FS, p)
	return err == nil
}

func itemBase(it syntax.Item) *syntax.ItemBase {
	switch it := it.(type) {
	case *syntax.FnDecl:
		return &it.ItemBase
	case *syntax.StructDecl:
		return &it.ItemBase
	case *syntax.EnumDecl:
		return &it.ItemBase
	case *syntax.TypeAlias:
		return &it.ItemBase
	case *syntax.ConstDecl:
		return &it.ItemBase
	case *syntax.StaticDecl:
		return &it.ItemBase
	case *syntax.ImplDecl:
		return &it.ItemBase
	case *syntax.TraitDecl:
		return &it.ItemBase
	case *syntax.ModDecl:
		return &it.ItemBase
	case *syntax.UseDecl:
		return &it.ItemBase
	case *syntax.ExternBlock:
		return &it.ItemBase
	}
	panic("unknown item")
}

// collectAssoc creates defs for the items of an impl or trait. They are not
// bound in any module; the type checker looks them up through their parent.
func (r *resolver) collectAssoc(parent *Def, file *syntax.File, items []syntax.Item) {
	for _, it := range items {
		if !r.cfgEnabled(file, itemBase(it).Attrs) {
			continue
		}
		switch it := it.(type) {
		case *syntax.FnDecl:
			r.newDef(DefAssocFn, it.Name.Value, file, it, it.Name.NamePos, parent, parent.Crate)
		case *syntax.ConstDecl:
			r.newDef(DefAssocConst, it.Name.Value, file, it, it.Name.NamePos, parent, parent.Crate)
		case *syntax.TypeAlias:
			r.newDef(DefAssocType, it.Name.Value, file, it, it.Name.NamePos, parent, parent.Crate)
		default:
			r.errorf(file, it.Pos(), "only functions, constants and types are allowed in %s blocks", parent.Kind)
		}
	}
}

// visibility computes the visibility of an item declared in module m.
func (r *resolver) visibility(m *Module, file *syntax.File, base *syntax.ItemBase) Visibility {
	named := m.nearestNamed()
	switch base.Vis {
	case syntax.VisPub:
		return Public
	case syntax.VisCrate:
		return Visibility{Restricted: m.Crate.Root}
	case syntax.VisSuper:
		if named.Parent == nil {
			r.errorf(file, base.Start, "pub(super) is not allowed in the crate root")
			return Visibility{Restricted: named}
		}
		return Visibility{Restricted: named.Parent.nearestNamed()}
	}
	return Visibility{Restricted: m}
}

// bind adds a name to a module namespace, reporting duplicates.
func (r *resolver) bind(m *Module, file *syntax.File, ns Namespace, name *syntax.Name, res Res, vis Visibility) {
	if old := m.bindings[ns][name.Value]; old != nil {
		r.errorf(file, name.NamePos, "the name %s is defined multiple times in %s", name.Value, m.Path())
		return
	}
	m.bindings[ns][name.Value] = &Binding{Res: res, Vis: vis}
}

// collectUse flattens a use tree into imports.
func (r *resolver) collectUse(m *Module, file *syntax.File, t *syntax.UseTree, prefix []*syntax.Name, vis Visibility) {
	segs := append([]*syntax.Name(nil), prefix...)
	if t.Prefix != nil {
		for _, s := range t.Prefix.Segments {
			segs = append(segs, s.Name)
		}
	}
	switch {
	case t.Glob:
		if len(segs) == 0 {
			r.errorf(file, m.Crate.Root.Def.Pos, "glob import needs a path")
			return
		}
		imp := &Import{Module: m, File: file, Segs: segs, Pos: segs[0].NamePos, Glob: true, Vis: vis}
		r.imports = append(r.imports, imp)
		m.globSrcs = append(m.globSrcs, imp)
	case t.Group:
		for _, ch := range t.Children {
			r.collectUse(m, file, ch, segs, vis)
		}
	default:
		last := segs[len(segs)-1]
		if last.Value == "self" {
			if len(segs) == 1 {
				r.errorf(file, last.NamePos, "self import can only appear in a group with a non-empty prefix")
				return
			}
			segs = segs[:len(segs)-1]
			last = segs[len(segs)-1]
		}
		name := last.Value
		if t.Alias != nil {
			name = t.Alias.Value
		}
		r.imports = append(r.imports, &Import{Module: m, File: file, Segs: segs, Name: name, Pos: last.NamePos, Vis: vis})
	}
}
