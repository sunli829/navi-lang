package types

import (
	"navi/internal/diag"
	"navi/internal/resolve"
	"navi/internal/syntax"
)

// collector computes item signatures on demand and caches them.
type collector struct {
	info  *Info
	errs  *diag.Sink
	quiet int // >0 while lowering speculatively; errors are dropped

	children map[*resolve.Def][]*resolve.Def // assoc items by impl/trait
	gens     map[*resolve.Def]*Generics
	implSelf map[*resolve.Def]*Type
	busy     map[*resolve.Def]bool // aliases and consts being computed
	consts   map[*resolve.Def]int64

	langOf      map[*resolve.Def]string // reverse of Info.Lang
	deriveCache map[*resolve.Def]map[string]bool
	paramOwner  map[*TypeParam]*Generics
	inherent    map[string][]*ImplInfo // inherent impls by implKey of the self type
}

func (c *collector) errorf(f *syntax.File, pos syntax.Pos, format string, args ...any) {
	if c.quiet > 0 {
		return
	}
	c.errs.Errorf(f, pos, format, args...)
}

// Collect computes the signatures of every item in the resolved crates and
// runs the item-level checks.
func Collect(res *resolve.Info, errs *diag.Sink) *Info {
	info := &Info{
		Ctx:     NewCtx(),
		Res:     res,
		Adts:    map[*resolve.Def]*AdtInfo{},
		Fns:     map[*resolve.Def]*FnSig{},
		Traits:  map[*resolve.Def]*TraitInfo{},
		Impls:   map[*resolve.Def]*ImplInfo{},
		Assoc:   map[*resolve.Def]*AssocTypeInfo{},
		Consts:  map[*resolve.Def]*ConstInfo{},
		Aliases: map[*resolve.Def]*AliasInfo{},
		ImplsOf: map[*resolve.Def][]*ImplInfo{},
		Lang:    map[string]*resolve.Def{},
	}
	c := &collector{
		info:     info,
		errs:     errs,
		children: map[*resolve.Def][]*resolve.Def{},
		gens:     map[*resolve.Def]*Generics{},
		implSelf: map[*resolve.Def]*Type{},
		busy:     map[*resolve.Def]bool{},
		consts:   map[*resolve.Def]int64{},

		langOf:      map[*resolve.Def]string{},
		deriveCache: map[*resolve.Def]map[string]bool{},
		paramOwner:  map[*TypeParam]*Generics{},
	}
	for _, d := range res.Defs {
		switch d.Kind {
		case resolve.DefAssocFn, resolve.DefAssocConst, resolve.DefAssocType:
			c.children[d.Parent] = append(c.children[d.Parent], d)
		}
	}
	c.findLang()
	for _, d := range res.Defs {
		c.collectDef(d)
	}
	for _, d := range res.Defs {
		switch d.Kind {
		case resolve.DefStruct, resolve.DefEnum:
			c.checkAdt(d)
			c.derives(d)
		case resolve.DefImpl:
			c.checkImpl(d)
			c.checkImplSigs(d)
		case resolve.DefTrait:
			c.checkTrait(d)
		}
	}
	c.checkOverlap()
	c.checkBodies()
	return info
}

func (c *collector) collectDef(d *resolve.Def) {
	switch d.Kind {
	case resolve.DefStruct, resolve.DefEnum:
		c.adt(d)
	case resolve.DefTrait:
		c.trait(d)
	case resolve.DefTypeAlias:
		c.alias(d)
	case resolve.DefFn, resolve.DefAssocFn, resolve.DefExternFn:
		c.fnSig(d)
	case resolve.DefImpl:
		c.impl(d)
	case resolve.DefAssocType:
		c.assocType(d)
	case resolve.DefConst, resolve.DefStatic, resolve.DefAssocConst, resolve.DefExternStatic:
		c.constInfo(d)
	}
}

// findLang records well-known std items by their std path.
func (c *collector) findLang() {
	var std *resolve.Crate
	for _, cr := range c.info.Res.Crates {
		if cr.IsStd {
			std = cr
		}
	}
	if std == nil {
		return
	}
	paths := map[string][]string{
		"Option": {"option", "Option"}, "Result": {"result", "Result"},
		"String": {"string", "String"}, "Vec": {"vec", "Vec"},
		"Iterator": {"iter", "Iterator"}, "IntoIterator": {"iter", "IntoIterator"},
		"FromIterator": {"iter", "FromIterator"},
		"Deref":        {"ops", "Deref"}, "DerefMut": {"ops", "DerefMut"},
		"Index": {"ops", "Index"}, "IndexMut": {"ops", "IndexMut"},
		"Add": {"ops", "Add"}, "Sub": {"ops", "Sub"}, "Mul": {"ops", "Mul"}, "Div": {"ops", "Div"},
		"Rem": {"ops", "Rem"}, "Neg": {"ops", "Neg"}, "Not": {"ops", "Not"},
		"BitAnd": {"ops", "BitAnd"}, "BitOr": {"ops", "BitOr"}, "BitXor": {"ops", "BitXor"},
		"Shl": {"ops", "Shl"}, "Shr": {"ops", "Shr"},
		"Range": {"ops", "Range"}, "RangeInclusive": {"ops", "RangeInclusive"},
		"RangeFrom": {"ops", "RangeFrom"}, "RangeTo": {"ops", "RangeTo"},
		"RangeToInclusive": {"ops", "RangeToInclusive"}, "RangeFull": {"ops", "RangeFull"},
		"PartialEq": {"cmp", "PartialEq"}, "PartialOrd": {"cmp", "PartialOrd"},
		"Eq": {"cmp", "Eq"}, "Ord": {"cmp", "Ord"},
		"Clone": {"clone", "Clone"}, "Default": {"default", "Default"},
		"Hash": {"hash", "Hash"}, "From": {"convert", "From"},
		"Display": {"fmt", "Display"}, "Debug": {"fmt", "Debug"},
		"Arguments": {"fmt", "Arguments"}, "Formatter": {"fmt", "Formatter"},
		"Location":  {"panic", "Location"},
		"AddAssign": {"ops", "AddAssign"}, "SubAssign": {"ops", "SubAssign"},
		"MulAssign": {"ops", "MulAssign"}, "DivAssign": {"ops", "DivAssign"},
		"RemAssign": {"ops", "RemAssign"}, "BitAndAssign": {"ops", "BitAndAssign"},
		"BitOrAssign": {"ops", "BitOrAssign"}, "BitXorAssign": {"ops", "BitXorAssign"},
		"ShlAssign": {"ops", "ShlAssign"}, "ShrAssign": {"ops", "ShrAssign"},
		"LowerHex": {"fmt", "LowerHex"}, "UpperHex": {"fmt", "UpperHex"},
		"Octal": {"fmt", "Octal"}, "Binary": {"fmt", "Binary"},
		"LowerExp": {"fmt", "LowerExp"}, "UpperExp": {"fmt", "UpperExp"},
		"Ordering": {"cmp", "Ordering"}, "Step": {"iter", "Step"},
		"RecvError": {"sync", "mpmc", "RecvError"}, "Sender": {"sync", "mpmc", "Sender"},
		"Receiver": {"sync", "mpmc", "Receiver"},
	}
	for name, path := range paths {
		m := std.Root
		var def *resolve.Def
		for i, seg := range path {
			b := m.Lookup(seg, resolve.TypeNS)
			if b == nil {
				break
			}
			if i == len(path)-1 {
				def = b.Res.Def
			} else if b.Res.Def == nil || b.Res.Def.Module == nil {
				break
			} else {
				m = b.Res.Def.Module
			}
		}
		if def != nil {
			c.info.Lang[name] = def
			c.langOf[def] = name
		}
	}
}

// ---------------------------------------------------------------------------
// Environments and generics

// itemGenerics returns the generics syntax of an item node.
func itemGenerics(n syntax.Node) *syntax.Generics {
	switch n := n.(type) {
	case *syntax.FnDecl:
		return n.Generics
	case *syntax.StructDecl:
		return n.Generics
	case *syntax.EnumDecl:
		return n.Generics
	case *syntax.TypeAlias:
		return n.Generics
	case *syntax.TraitDecl:
		return n.Generics
	case *syntax.ImplDecl:
		return n.Generics
	}
	return nil
}

func isAssoc(d *resolve.Def) bool {
	switch d.Kind {
	case resolve.DefAssocFn, resolve.DefAssocConst, resolve.DefAssocType:
		return true
	}
	return false
}

// envFor returns the lowering environment inside item d.
func (c *collector) envFor(d *resolve.Def) *env {
	e := &env{file: d.File, crate: d.Crate}
	owner := d
	if isAssoc(d) {
		owner = d.Parent
	}
	switch owner.Kind {
	case resolve.DefTrait:
		e.trait = owner
		e.selfTy = c.info.Ctx.SelfParam(owner)
		e.astGen = append(e.astGen, itemGenerics(owner.Node))
	case resolve.DefImpl:
		e.selfTy = c.implSelfType(owner)
		e.astGen = append(e.astGen, itemGenerics(owner.Node))
		if ii := c.impl(owner); ii != nil {
			e.implTrait = ii.Trait
		}
	}
	if d != owner {
		e.astGen = append(e.astGen, itemGenerics(d.Node))
	} else if owner.Kind != resolve.DefTrait && owner.Kind != resolve.DefImpl {
		e.astGen = append(e.astGen, itemGenerics(d.Node))
	}
	e.gen = c.genericsOf(d)
	return e
}

// genericsOf returns the generics of an item (with parent generics for
// associated items).
func (c *collector) genericsOf(d *resolve.Def) *Generics {
	if g, ok := c.gens[d]; ok {
		return g
	}
	var parent *Generics
	if isAssoc(d) {
		parent = c.genericsOf(d.Parent)
	}
	g := &Generics{Parent: parent}
	if d.Kind == resolve.DefTrait {
		g.SelfTrait = d
	}
	c.gens[d] = g
	ag := itemGenerics(d.Node)
	if ag == nil {
		return g
	}
	for _, gp := range ag.Params {
		pd := c.info.Res.DefOf[gp]
		if pd == nil {
			continue
		}
		tp := &TypeParam{Def: pd, Type: c.info.Ctx.Param(pd)}
		c.paramOwner[tp] = g
		g.Params = append(g.Params, tp)
	}
	e := &env{file: d.File, crate: d.Crate, gen: g, implNote: "generic bounds"}
	switch {
	case d.Kind == resolve.DefTrait:
		e.trait = d
		e.selfTy = c.info.Ctx.SelfParam(d)
		e.astGen = []*syntax.Generics{ag}
	case isAssoc(d) && d.Parent.Kind == resolve.DefTrait:
		e.trait = d.Parent
		e.selfTy = c.info.Ctx.SelfParam(d.Parent)
		e.astGen = []*syntax.Generics{itemGenerics(d.Parent.Node), ag}
	case isAssoc(d) && d.Parent.Kind == resolve.DefImpl:
		e.selfTy = c.implSelfType(d.Parent)
		e.astGen = []*syntax.Generics{itemGenerics(d.Parent.Node), ag}
	default:
		e.astGen = []*syntax.Generics{ag}
	}
	for i, gp := range ag.Params {
		if gp.Default != nil && i < len(g.Params) {
			if d.Kind == resolve.DefFn || isAssoc(d) || d.Kind == resolve.DefImpl {
				c.errorf(d.File, gp.Name.NamePos, "default type parameters are only allowed on structs, enums, type aliases and traits")
			}
			g.Params[i].Default = c.lowerType(e, gp.Default)
		}
	}
	for i, gp := range ag.Params {
		if i >= len(g.Params) {
			break
		}
		for _, b := range gp.Bounds {
			if tr := c.lowerTraitRef(e, b, g.Params[i].Type); tr != nil {
				g.Preds = append(g.Preds, &Pred{Trait: tr})
			}
		}
	}
	for _, wp := range ag.Where {
		self := c.lowerType(e, wp.Type)
		for _, b := range wp.Bounds {
			if tr := c.lowerTraitRef(e, b, self); tr != nil {
				g.Preds = append(g.Preds, &Pred{Trait: tr})
			}
		}
	}
	return g
}

// selfTraitRef is `Self: Trait<P1, ...>` inside trait d.
func (c *collector) selfTraitRef(trait *resolve.Def) *TraitRef {
	tr := &TraitRef{Trait: trait, Self: c.info.Ctx.SelfParam(trait)}
	for _, p := range c.genericsOf(trait).Params {
		tr.Args = append(tr.Args, p.Type)
	}
	return tr
}

// walkSupertraits calls f for tr and, recursively, each supertrait with
// parameters substituted.
func (c *collector) walkSupertraits(tr *TraitRef, f func(*TraitRef)) {
	seen := map[*resolve.Def]bool{}
	var walk func(*TraitRef)
	walk = func(t *TraitRef) {
		if seen[t.Trait] {
			return
		}
		seen[t.Trait] = true
		f(t)
		ti := c.trait(t.Trait)
		if ti == nil {
			return
		}
		m := paramMap(ti.Generics, t.Args)
		for _, sup := range ti.Supers {
			walk(SubstTraitRef(c.info.Ctx, sup, m, t.Self))
		}
	}
	walk(tr)
}

// traitRefFor returns the supertrait reference of tr naming trait def.
func (c *collector) traitRefFor(tr *TraitRef, def *resolve.Def) *TraitRef {
	var out *TraitRef
	c.walkSupertraits(tr, func(t *TraitRef) {
		if out == nil && t.Trait == def {
			out = t
		}
	})
	if out == nil {
		return tr
	}
	return out
}

func (c *collector) findOwnAssocType(trait *resolve.Def, name string) *resolve.Def {
	for _, ch := range c.children[trait] {
		if ch.Kind == resolve.DefAssocType && ch.Name == name {
			return ch
		}
	}
	return nil
}

// findAssocType finds an associated type in a trait or its supertraits.
func (c *collector) findAssocType(trait *resolve.Def, name string) *resolve.Def {
	return c.findAssocTypeInSupers(&TraitRef{Trait: trait, Self: c.info.Ctx.SelfParam(trait)}, name)
}

func (c *collector) findAssocTypeInSupers(tr *TraitRef, name string) *resolve.Def {
	var hit *resolve.Def
	c.walkSupertraits(tr, func(t *TraitRef) {
		if hit == nil {
			hit = c.findOwnAssocType(t.Trait, name)
		}
	})
	return hit
}

// ---------------------------------------------------------------------------
// Signatures

func (c *collector) adt(d *resolve.Def) *AdtInfo {
	if ai := c.info.Adts[d]; ai != nil {
		return ai
	}
	ai := &AdtInfo{Def: d, Generics: c.genericsOf(d), IsEnum: d.Kind == resolve.DefEnum}
	c.info.Adts[d] = ai
	e := c.envFor(d)
	e.implNote = "field types"
	lowerFields := func(fs []*syntax.Field) []*Field {
		out := make([]*Field, len(fs))
		for i, f := range fs {
			fd := &Field{Type: c.lowerType(e, f.Type), Pos: f.Type.Pos()}
			if f.Name != nil {
				fd.Name, fd.Pos = f.Name.Value, f.Name.NamePos
			} else {
				fd.Name = itoa(i)
			}
			fd.Vis = c.fieldVis(d, f)
			out[i] = fd
		}
		return out
	}
	switch n := d.Node.(type) {
	case *syntax.StructDecl:
		ai.Kind = n.Kind
		ai.Fields = lowerFields(n.Fields)
	case *syntax.EnumDecl:
		next := int64(0)
		seen := map[int64]string{}
		for _, v := range n.Variants {
			vd := c.info.Res.DefOf[v]
			vi := &VariantInfo{Def: vd, Kind: v.Kind, Fields: lowerFields(v.Fields)}
			for _, f := range vi.Fields {
				f.Vis = resolve.Public
			}
			if v.Discriminant != nil {
				if val, ok := c.evalConst(d.File, v.Discriminant); ok {
					next = val
				}
			}
			vi.Discr = next
			if other, dup := seen[next]; dup {
				c.errorf(d.File, v.Name.NamePos, "discriminant value %d is already used by variant %s", next, other)
			}
			seen[next] = v.Name.Value
			next++
			ai.Variants = append(ai.Variants, vi)
		}
	}
	return ai
}

// fieldVis computes the visibility of a struct field (variant fields are
// public).
func (c *collector) fieldVis(adt *resolve.Def, f *syntax.Field) resolve.Visibility {
	parent := adt.Parent
	var m *resolve.Module
	if parent != nil {
		m = parent.Module
	}
	switch f.Vis {
	case syntax.VisPub:
		return resolve.Public
	case syntax.VisCrate:
		return resolve.Visibility{Restricted: adt.Crate.Root}
	case syntax.VisSuper:
		if m != nil && m.Parent != nil {
			return resolve.Visibility{Restricted: m.Parent}
		}
	}
	return resolve.Visibility{Restricted: m}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}

func (c *collector) trait(d *resolve.Def) *TraitInfo {
	if ti := c.info.Traits[d]; ti != nil {
		return ti
	}
	n, ok := d.Node.(*syntax.TraitDecl)
	if !ok {
		return nil
	}
	ti := &TraitInfo{Def: d, Items: map[string]*resolve.Def{}}
	c.info.Traits[d] = ti
	ti.Generics = c.genericsOf(d)
	e := c.envFor(d)
	e.implNote = "supertraits"
	for _, s := range n.Supers {
		if tr := c.lowerTraitRef(e, s, e.selfTy); tr != nil {
			if tr.Trait == d {
				c.errorf(d.File, s.Pos(), "trait %s cannot be its own supertrait", d.Name)
				continue
			}
			ti.Supers = append(ti.Supers, tr)
		}
	}
	for _, ch := range c.children[d] {
		if prev := ti.Items[ch.Name]; prev != nil {
			c.errorf(ch.File, ch.Pos, "%s is defined multiple times in trait %s", ch.Name, d.Name)
			continue
		}
		ti.Items[ch.Name] = ch
		ti.Order = append(ti.Order, ch)
	}
	return ti
}

func (c *collector) implSelfType(d *resolve.Def) *Type {
	if t, ok := c.implSelf[d]; ok {
		return t
	}
	c.implSelf[d] = c.info.Ctx.Err // guards against cycles through Self
	n := d.Node.(*syntax.ImplDecl)
	e := &env{file: d.File, crate: d.Crate, gen: c.genericsOf(d), implNote: "impl headers",
		astGen: []*syntax.Generics{n.Generics}}
	t := c.lowerType(e, n.Type)
	c.implSelf[d] = t
	return t
}

func (c *collector) impl(d *resolve.Def) *ImplInfo {
	if ii := c.info.Impls[d]; ii != nil {
		return ii
	}
	n := d.Node.(*syntax.ImplDecl)
	ii := &ImplInfo{Def: d, Items: map[string]*resolve.Def{}}
	c.info.Impls[d] = ii
	ii.Generics = c.genericsOf(d)
	ii.SelfTy = c.implSelfType(d)
	if n.Trait != nil {
		e := &env{file: d.File, crate: d.Crate, gen: ii.Generics, implNote: "impl headers",
			astGen: []*syntax.Generics{n.Generics}}
		ii.Trait = c.lowerTraitRef(e, n.Trait, ii.SelfTy)
		if ii.Trait != nil {
			c.info.ImplsOf[ii.Trait.Trait] = append(c.info.ImplsOf[ii.Trait.Trait], ii)
		}
	} else {
		c.info.ImplsOf[nil] = append(c.info.ImplsOf[nil], ii)
	}
	for _, ch := range c.children[d] {
		if prev := ii.Items[ch.Name]; prev != nil {
			c.errorf(ch.File, ch.Pos, "%s is defined multiple times in this impl", ch.Name)
			continue
		}
		ii.Items[ch.Name] = ch
	}
	return ii
}

func (c *collector) alias(d *resolve.Def) *AliasInfo {
	if ai := c.info.Aliases[d]; ai != nil {
		return ai
	}
	if c.busy[d] {
		c.errorf(d.File, d.Pos, "type alias %s refers to itself", d.Name)
		return nil
	}
	c.busy[d] = true
	defer delete(c.busy, d)
	n := d.Node.(*syntax.TypeAlias)
	ai := &AliasInfo{Def: d, Generics: c.genericsOf(d)}
	e := c.envFor(d)
	e.implNote = "type aliases"
	if n.Type == nil {
		c.errorf(d.File, d.Pos, "type alias %s needs a type", d.Name)
		ai.Type = c.info.Ctx.Err
	} else {
		ai.Type = c.lowerType(e, n.Type)
	}
	c.info.Aliases[d] = ai
	return ai
}

func (c *collector) fnSig(d *resolve.Def) *FnSig {
	if fs := c.info.Fns[d]; fs != nil {
		return fs
	}
	n := d.Node.(*syntax.FnDecl)
	g := c.genericsOf(d)
	fs := &FnSig{Def: d, Generics: g, Abi: n.Abi, Const: n.Const, Unsafe: n.Unsafe, HasBody: n.Body != nil}
	if d.Kind == resolve.DefExternFn {
		fs.Abi = "C"
	}
	c.info.Fns[d] = fs
	e := c.envFor(d)
	e.fnDef = d
	if n.Self != nil && e.selfTy != nil {
		switch {
		case n.Self.Ref:
			fs.Self = c.info.Ctx.Ref(n.Self.Mut, e.selfTy)
		default:
			fs.Self = e.selfTy
		}
	}
	argEnv := e.with(func(x *env) { x.implArg = &g.Params })
	if fs.Abi == "C" {
		argEnv.implArg, argEnv.implNote = nil, "extern \"C\" functions"
	}
	for _, p := range n.Params {
		fs.Params = append(fs.Params, c.lowerType(argEnv, p.Type))
	}
	retEnv := e.with(func(x *env) { x.implRet = &fs.Opaques })
	if fs.Abi == "C" || d.Kind == resolve.DefAssocFn && d.Parent.Kind == resolve.DefTrait {
		retEnv.implRet = nil
		retEnv.implNote = "trait methods and extern functions"
	}
	if n.Result != nil {
		fs.Ret = c.lowerType(retEnv, n.Result)
	} else {
		fs.Ret = c.info.Ctx.Unit
	}
	if !fs.HasBody && d.Kind != resolve.DefExternFn && !(d.Kind == resolve.DefAssocFn && d.Parent.Kind == resolve.DefTrait) {
		if !hasAttr(n.Attrs, "intrinsic") && !hasAttr(n.Attrs, "asm") {
			c.errorf(d.File, d.Pos, "function %s has no body", d.Name)
		} else if (hasAttr(n.Attrs, "intrinsic")) && !d.Crate.IsStd {
			c.errorf(d.File, d.Pos, "#[intrinsic] functions can only be declared in std")
		}
	}
	return fs
}

func hasAttr(attrs []*syntax.Attr, name string) bool {
	for _, a := range attrs {
		if len(a.Path.Segments) == 1 && a.Path.Segments[0].Name.Value == name {
			return true
		}
	}
	return false
}

func (c *collector) assocType(d *resolve.Def) *AssocTypeInfo {
	if ai := c.info.Assoc[d]; ai != nil {
		return ai
	}
	n := d.Node.(*syntax.TypeAlias)
	ai := &AssocTypeInfo{Def: d}
	c.info.Assoc[d] = ai
	e := c.envFor(d)
	e.implNote = "associated types"
	if d.Parent.Kind == resolve.DefTrait {
		self := c.info.Ctx.Proj(d, c.selfTraitRef(d.Parent))
		for _, b := range n.Bounds {
			if tr := c.lowerTraitRef(e, b, self); tr != nil {
				ai.Bounds = append(ai.Bounds, tr)
			}
		}
	} else if len(n.Bounds) > 0 {
		c.errorf(d.File, d.Pos, "bounds on associated types are only allowed in traits")
	}
	if n.Type != nil {
		ai.Value = c.lowerType(e, n.Type)
	} else if d.Parent.Kind == resolve.DefImpl {
		c.errorf(d.File, d.Pos, "associated type %s in an impl needs a type", d.Name)
	}
	return ai
}

func (c *collector) constInfo(d *resolve.Def) *ConstInfo {
	if ci := c.info.Consts[d]; ci != nil {
		return ci
	}
	ci := &ConstInfo{Def: d}
	c.info.Consts[d] = ci
	e := c.envFor(d)
	e.implNote = "constants and statics"
	switch n := d.Node.(type) {
	case *syntax.ConstDecl:
		ci.Type = c.lowerType(e, n.Type)
		if n.Value == nil && !(d.Kind == resolve.DefAssocConst && d.Parent.Kind == resolve.DefTrait) {
			c.errorf(d.File, d.Pos, "constant %s needs a value", d.Name)
		}
	case *syntax.StaticDecl:
		ci.Type = c.lowerType(e, n.Type)
		ci.Mut = n.Mut
	}
	return ci
}
