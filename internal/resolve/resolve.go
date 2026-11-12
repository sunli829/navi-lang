package resolve

import (
	"io/fs"

	"navi/internal/diag"
	"navi/internal/syntax"
)

// CrateSpec describes a crate to load: its files live in FS and Root is the
// path of the root file (src/main.navi or src/lib.navi) inside FS.
type CrateSpec struct {
	Name string
	FS   fs.FS
	Root string
	Deps []string // names of other crates in the same Resolve call; std is implicit
	// Dir is prepended to file paths in diagnostics (the crate directory as
	// the user named it); empty means the crate name.
	Dir string
}

// Crate is a loaded crate.
type Crate struct {
	Name  string
	Dir   string // display prefix for file names
	FS    fs.FS
	Root  *Module
	Files []*syntax.SourceFile
	Deps  map[string]*Crate
	IsStd bool

	prelude  *Module                 // std::prelude, nil until resolved
	fileKeys map[*syntax.File]string // file -> its path inside FS
}

// Error and ErrorList are the shared diagnostic types.
type (
	Error     = diag.Error
	ErrorList = diag.List
)

// Info holds the results of name resolution.
type Info struct {
	Crates []*Crate
	Defs   []*Def
	DefOf  map[syntax.Node]*Def // declaring node -> def (items, variants, generic params)

	Paths      map[*syntax.Path]Res        // every resolved path
	Bindings   map[*syntax.IdentPat]*Local // pattern bindings
	PatConsts  map[*syntax.IdentPat]Res    // identifier patterns naming a constant or unit struct/variant
	SelfLocals map[*syntax.FnDecl]*Local   // the self parameter of methods
	Labels     map[*syntax.Name]syntax.Node
}

type resolver struct {
	cfg  Config
	info *Info

	errs diag.Sink

	crates  map[string]*Crate
	imports []*Import

	// blockMods maps blocks that declare items to their anonymous module.
	blockMods map[*syntax.BlockExpr]*Module
}

func (r *resolver) errorf(f *syntax.File, pos syntax.Pos, format string, args ...any) {
	r.errs.Errorf(f, pos, format, args...)
}

// Resolve loads the given crates and resolves all names. The std crate must
// be among the specs (named "std"); every other crate depends on it.
func Resolve(cfg Config, specs []*CrateSpec) (*Info, ErrorList) {
	r := &resolver{
		cfg: cfg,
		info: &Info{
			DefOf:      map[syntax.Node]*Def{},
			Paths:      map[*syntax.Path]Res{},
			Bindings:   map[*syntax.IdentPat]*Local{},
			PatConsts:  map[*syntax.IdentPat]Res{},
			SelfLocals: map[*syntax.FnDecl]*Local{},
			Labels:     map[*syntax.Name]syntax.Node{},
		},
		crates:    map[string]*Crate{},
		blockMods: map[*syntax.BlockExpr]*Module{},
	}

	// Load and parse every crate (files are parsed in parallel).
	for _, spec := range specs {
		c := &Crate{Name: spec.Name, Dir: spec.Dir, FS: spec.FS, Deps: map[string]*Crate{}, IsStd: spec.Name == "std"}
		if c.Dir == "" {
			c.Dir = c.Name
		}
		r.crates[spec.Name] = c
		r.info.Crates = append(r.info.Crates, c)
		r.load(c, spec.Root)
	}
	std := r.crates["std"]
	for _, spec := range specs {
		c := r.crates[spec.Name]
		for _, d := range spec.Deps {
			if dc := r.crates[d]; dc != nil {
				c.Deps[d] = dc
			} else {
				r.errorf(nil, 0, "crate %s: unknown dependency %q", spec.Name, d)
			}
		}
		if std != nil && c != std {
			c.Deps["std"] = std
		}
	}

	r.resolveImports()
	if std != nil {
		if b := std.Root.bindings[TypeNS]["prelude"]; b != nil && b.Res.Def != nil && b.Res.Def.Module != nil {
			for _, c := range r.info.Crates {
				c.prelude = b.Res.Def.Module
			}
		}
	}
	for _, c := range r.info.Crates {
		if c.Root != nil {
			r.resolveModule(c.Root)
		}
	}

	return r.info, r.errs.List()
}

func (r *resolver) newDef(kind DefKind, name string, file *syntax.File, node syntax.Node, pos syntax.Pos, parent *Def, c *Crate) *Def {
	d := &Def{Kind: kind, Name: name, Crate: c, File: file, Pos: pos, Node: node, Parent: parent}
	r.info.Defs = append(r.info.Defs, d)
	if node != nil {
		r.info.DefOf[node] = d
	}
	return d
}
