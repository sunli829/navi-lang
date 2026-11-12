// Package resolve loads crates (following mod declarations and #[cfg]),
// builds the module tree and resolves every name to its definition.
package resolve

import (
	"sort"

	"navi/internal/syntax"
)

// DefKind is the kind of a named definition.
type DefKind uint8

const (
	DefMod DefKind = iota
	DefStruct
	DefEnum
	DefVariant
	DefTrait
	DefTypeAlias
	DefFn
	DefConst
	DefStatic
	DefImpl
	DefAssocFn    // fn inside an impl or trait
	DefAssocConst // const inside an impl or trait
	DefAssocType  // type inside an impl or trait
	DefTypeParam
	DefExternFn
	DefExternStatic
)

var defKindNames = [...]string{
	DefMod:          "module",
	DefStruct:       "struct",
	DefEnum:         "enum",
	DefVariant:      "variant",
	DefTrait:        "trait",
	DefTypeAlias:    "type alias",
	DefFn:           "function",
	DefConst:        "constant",
	DefStatic:       "static",
	DefImpl:         "impl",
	DefAssocFn:      "associated function",
	DefAssocConst:   "associated constant",
	DefAssocType:    "associated type",
	DefTypeParam:    "type parameter",
	DefExternFn:     "extern function",
	DefExternStatic: "extern static",
}

func (k DefKind) String() string { return defKindNames[k] }

// Def is a named (or, for impls, anonymous) definition.
type Def struct {
	// ID is the definition's index in Info.Defs (creation order).
	ID     int
	Kind   DefKind
	Name   string
	Crate  *Crate
	File   *syntax.File
	Pos    syntax.Pos
	Node   syntax.Node // the declaring AST node
	Parent *Def        // enclosing module, enum, trait or impl
	Vis    Visibility

	// Module is set for modules and enums (whose variants live in it).
	Module *Module

	// For tuple and unit structs/variants, which also name a constructor in
	// the value namespace.
	Ctor bool
}

// Namespace separates types/modules from values, as in Rust.
type Namespace uint8

const (
	TypeNS Namespace = iota
	ValueNS
	numNS
)

func (ns Namespace) String() string {
	if ns == TypeNS {
		return "type"
	}
	return "value"
}

// Visibility says from where a definition or import can be named.
type Visibility struct {
	// Restricted is nil for pub; otherwise the item is visible inside that
	// module and its descendants.
	Restricted *Module
}

// Public is the visibility of pub items.
var Public = Visibility{}

// Module is a module, a crate root, an enum (holding its variants) or the
// anonymous scope of a block that declares items.
type Module struct {
	Def    *Def // nil for block scopes
	Crate  *Crate
	Parent *Module // lexical parent (nil for crate roots)
	Name   string

	// Anonymous block modules fall back to their parent for lookups.
	Anonymous bool
	IsEnum    bool

	// Dir is the directory used to find files for `mod name;` declared here.
	Dir string

	bindings [numNS]map[string]*Binding // items and single imports
	globs    [numNS]map[string][]*Binding
	globSrcs []*Import // glob imports into this module

	Children []*Module

	file  *syntax.File  // the file the module's items are declared in
	items []syntax.Item // cfg-enabled items, in source order

	underscoreTraits []*Def // traits imported with `as _`
}

func newModule(c *Crate, parent *Module, name string) *Module {
	m := &Module{Crate: c, Parent: parent, Name: name}
	for ns := range m.bindings {
		m.bindings[ns] = map[string]*Binding{}
		m.globs[ns] = map[string][]*Binding{}
	}
	if parent != nil {
		parent.Children = append(parent.Children, m)
	}
	return m
}

// Path returns the module's path such as std::fmt.
func (m *Module) Path() string {
	if m.Parent == nil {
		return m.Crate.Name
	}
	if m.Anonymous {
		return m.Parent.Path() + "::{block}"
	}
	return m.Parent.Path() + "::" + m.Name
}

// isDescendantOf reports whether m is anc or nested inside it.
func (m *Module) isDescendantOf(anc *Module) bool {
	for x := m; x != nil; x = x.Parent {
		if x == anc {
			return true
		}
	}
	return false
}

// nearestNamed returns m, or the closest enclosing non-block module.
func (m *Module) nearestNamed() *Module {
	for m.Anonymous {
		m = m.Parent
	}
	return m
}

// Binding is a name bound in a module: an item defined there, or an import.
type Binding struct {
	Res    Res
	Vis    Visibility
	Import *Import // nil for items defined in the module
}

// accessibleFrom reports whether a binding with visibility v can be named
// from module m.
func (v Visibility) accessibleFrom(m *Module) bool {
	return v.Restricted == nil || m.isDescendantOf(v.Restricted)
}

// Import is one leaf of a use tree.
type Import struct {
	Module *Module // where the use declaration is
	File   *syntax.File
	Segs   []*syntax.Name // full path, last segment is the imported name
	Name   string         // the name it binds (alias, or last segment)
	Pos    syntax.Pos
	Glob   bool
	Vis    Visibility
	done   bool
	used   bool
}

// ResKind is what a path resolved to.
type ResKind uint8

const (
	ResErr       ResKind = iota // resolution failed (an error was reported)
	ResDef                      // a definition
	ResLocal                    // a local variable
	ResPrim                     // a primitive type such as i32 or str
	ResSelfTy                   // Self inside an impl or trait (Def is the impl/trait)
	ResCrate                    // the root of an extern crate (Def is its root module)
	ResQualified                // <T as Trait>::name; all segments are in Rest
)

// Res is the result of resolving a path. Rest holds trailing segments that
// name associated items (Vec::new, T::Item, i32::MAX); the type checker
// resolves those.
type Res struct {
	Kind  ResKind
	Def   *Def
	Local *Local
	Prim  string
	Rest  []*syntax.PathSegment
}

// Local is a local variable, parameter or pattern binding.
type Local struct {
	Name string
	Pos  syntax.Pos
	Mut  bool
	// Fn is the function or closure that declares the local; later phases
	// use it to find closure captures.
	Fn syntax.Node
}

// primitive types, resolved in the type namespace when nothing else
// matches.
var primitives = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true,
	"f32": true, "f64": true, "bool": true, "char": true, "str": true,
}

// Lookup returns the binding of name in namespace ns of m, ignoring
// visibility: an item or single import, else an unambiguous glob import.
func (m *Module) Lookup(name string, ns Namespace) *Binding {
	if b := m.bindings[ns][name]; b != nil {
		return b
	}
	if gs := m.globs[ns][name]; len(gs) > 0 {
		for _, g := range gs[1:] {
			if !sameRes(g.Res, gs[0].Res) {
				return nil
			}
		}
		return gs[0]
	}
	return nil
}

// TraitsInScope returns the traits whose methods can be called in m: traits
// named in m (and enclosing block scopes), imported with `as _`, or in the
// prelude.
func (m *Module) TraitsInScope() []*Def {
	seen := map[*Def]bool{}
	var out []*Def
	add := func(d *Def) {
		if d != nil && d.Kind == DefTrait && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	addModule := func(x *Module) {
		for _, b := range x.bindings[TypeNS] {
			add(b.Res.Def)
		}
		for _, bs := range x.globs[TypeNS] {
			for _, b := range bs {
				add(b.Res.Def)
			}
		}
		for _, d := range x.underscoreTraits {
			add(d)
		}
	}
	for x := m; x != nil; x = x.Parent {
		addModule(x)
		if !x.Anonymous {
			break
		}
	}
	if p := m.Crate.prelude; p != nil {
		addModule(p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ModuleOf returns the module a definition is declared in.
func ModuleOf(d *Def) *Module {
	for x := d.Parent; x != nil; x = x.Parent {
		if x.Module != nil && x.Kind == DefMod {
			return x.Module
		}
	}
	if d.Module != nil && d.Kind == DefMod {
		return d.Module
	}
	return nil
}

// AccessibleFrom reports whether something with visibility v can be named
// from module m.
func (v Visibility) AccessibleFrom(m *Module) bool { return v.accessibleFrom(m) }
