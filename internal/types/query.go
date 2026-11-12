package types

import (
	"navi/internal/resolve"
)

// Queries used by later phases (MIR building, interpretation, codegen).
// They run the trait solver outside any function body, on types that are
// concrete or that mention only the parameters of owner (whose where
// clauses are assumed).

func (info *Info) scratch(owner *resolve.Def) *fnCtx {
	c := info.c
	if owner != nil {
		fc := c.newFnCtx(owner)
		return fc
	}
	return &fnCtx{c: c, info: info, in: &infer{ctx: info.Ctx}, varOrigin: map[int]*TypeParam{}, opaques: map[*Type]*Type{}}
}

// DerefTarget returns <t as Deref>::Target, or nil if t does not implement
// Deref.
func (info *Info) DerefTarget(owner *resolve.Def, t *Type) *Type {
	fc := info.scratch(owner)
	c := info.c
	c.quiet++
	defer func() { c.quiet-- }()
	r := fc.overloadedDeref(t)
	if r == nil {
		return nil
	}
	return fc.in.resolve(r)
}

// Normalize resolves projections in t as far as possible.
func (info *Info) Normalize(owner *resolve.Def, t *Type) *Type {
	fc := info.scratch(owner)
	c := info.c
	c.quiet++
	defer func() { c.quiet-- }()
	return fc.in.resolve(fc.normalize(t))
}

// TraitItem returns the item of a lang trait by name.
func (info *Info) TraitItem(trait, item string) *resolve.Def {
	d := info.Lang[trait]
	if d == nil {
		return nil
	}
	ti := info.Traits[d]
	if ti == nil {
		return nil
	}
	return ti.Items[item]
}

// Instance is the resolution of a (possibly trait) function for concrete
// types.
type Instance struct {
	Def  *resolve.Def // the function to run (impl method or trait default)
	Args []*Type      // generic arguments for Def's generics (all levels)
	Self *Type        // Self for trait default methods
	// Builtin is set for compiler-provided impls: the trait's lang name.
	Builtin string
	// Derived is set for #[derive] impls: the trait's lang name.
	Derived string
	// Virtual is set for calls through dyn.
	Virtual bool
}

// ResolveMethod resolves a trait method for a concrete self type and
// generic arguments (trait parameters followed by the method's own).
func (info *Info) ResolveMethod(method *resolve.Def, self *Type, args []*Type) *Instance {
	c := info.c
	trait := method.Parent
	ti := info.Traits[trait]
	nTrait := len(ti.Generics.Params)
	traitArgs, ownArgs := args, []*Type(nil)
	if len(args) > nTrait {
		traitArgs, ownArgs = args[:nTrait], args[nTrait:]
	}
	if self.Kind == KDyn {
		// Methods of the dyn's own traits dispatch through the vtable; other
		// traits (ToString for dyn Display) are found through impls below.
		for _, b := range self.Bounds {
			for _, sup := range info.Supertraits(&TraitRef{Trait: b.Trait, Self: self, Args: b.Args}) {
				if sup.Trait == trait {
					return &Instance{Def: method, Args: args, Self: self, Virtual: true}
				}
			}
		}
	}
	fc := info.scratch(nil)
	c.quiet++
	defer func() { c.quiet-- }()
	tr := &TraitRef{Trait: trait, Self: self, Args: traitArgs}
	// The method may be defined in a supertrait's impl when called through
	// a subtrait bound; the trait here is the method's own trait.
	sel, st := fc.selectTrait(tr, 0)
	if st != selYes {
		return nil
	}
	switch sel.kind {
	case selImpl:
		var full []*Type
		for _, a := range sel.args {
			full = append(full, fc.in.resolve(a))
		}
		if it := sel.impl.Items[method.Name]; it != nil {
			return &Instance{Def: it, Args: append(full, ownArgs...)}
		}
		// Default method from the trait.
		return &Instance{Def: method, Args: append(append([]*Type(nil), traitArgs...), ownArgs...), Self: self}
	case selBuiltin:
		name := info.c.langOf[trait]
		if self.Kind == KAdt && c.derives(self.Def)[name] {
			return &Instance{Def: method, Args: args, Self: self, Derived: name}
		}
		return &Instance{Def: method, Args: args, Self: self, Builtin: name}
	case selDyn:
		return &Instance{Def: method, Args: args, Self: self, Virtual: true}
	}
	return nil
}

// FnSig returns the signature of a function definition.
func (info *Info) FnSig(d *resolve.Def) *FnSig { return info.c.fnSig(d) }

// GenericsOf returns the generics of a definition.
func (info *Info) GenericsOf(d *resolve.Def) *Generics { return info.c.genericsOf(d) }

// ImplOf returns the impl information for an impl definition.
func (info *Info) ImplOf(d *resolve.Def) *ImplInfo { return info.c.impl(d) }

// Derives reports the derived traits of an ADT.
func (info *Info) Derives(d *resolve.Def) map[string]bool { return info.c.derives(d) }

// LangName returns the lang name of a std item, or "".
func (info *Info) LangName(d *resolve.Def) string { return info.c.langOf[d] }

// DynCallable reports whether a trait method can be called through dyn.
func (info *Info) DynCallable(m *resolve.Def) bool { return info.c.dynCallable(m) }

// TraitMethods lists the methods of a trait in declaration order.
func (info *Info) TraitMethods(trait *resolve.Def) []*resolve.Def {
	var out []*resolve.Def
	for _, it := range info.Traits[trait].Order {
		if it.Kind == resolve.DefAssocFn {
			out = append(out, it)
		}
	}
	return out
}

// Supertraits returns tr and all its supertraits.
func (info *Info) Supertraits(tr *TraitRef) []*TraitRef {
	var out []*TraitRef
	info.c.walkSupertraits(tr, func(t *TraitRef) { out = append(out, t) })
	return out
}
