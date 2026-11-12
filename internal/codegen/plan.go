// Package codegen decides what native code to generate: it walks the MIR
// from main with concrete types, groups function instances by GC shape,
// assigns dictionary slots for the call targets and vtables that differ
// between the instantiations of a group, and collects the data (strings,
// statics, vtables, dictionaries) the code refers to. The per-architecture
// backends turn the result into machine code.
package codegen

import (
	"fmt"
	"sort"
	"strings"

	"navi/internal/layout"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// Plan is the set of functions and data to generate.
type Plan struct {
	Mir    *mir.Program
	Info   *types.Info
	Lay    *layout.Computer
	Groups []*Group
	Insts  map[string]*Inst
	Main   *Inst

	Statics  []*resolve.Def
	staticOK map[*resolve.Def]bool
	Vtables  map[string]*Vtable
	Strings  map[string]string // literal -> symbol
	Errors   []string
	// StaticInit holds the initializer instance of each static.
	StaticInit map[*resolve.Def]*Inst
	// Runtime holds the std::rt::native functions the backend calls
	// directly (panic_raw, panic_bounds, gc_alloc).
	Runtime map[string]*Inst

	groups map[string]*Group
	native *resolve.Module
}

// Inline lists the intrinsics the backends generate inline; the others
// call the function of the same name in std::rt::native.
var Inline = map[string]bool{
	"size_of": true, "align_of": true, "ptr_offset": true, "slice_from_raw": true,
	"slice_from_raw_mut": true, "str_from_raw": true, "slice_len": true, "slice_ptr": true,
	"str_as_bytes": true, "caller_location": true, "abort": true, "has_pointers": true, "is_copy_clone": true, "save_context": true, "resume_context": true,
	"frame_address": true, "stack_top": true, "data_start": true, "data_end": true, "run_defers": true,
	"swap_context": true, "gstart_address": true, "set_stack_limit": true, "spill_registers": true, "stack_pointer": true,
	"wrapping_add": true, "wrapping_sub": true, "wrapping_mul": true,
}

// TargetKind is how a function reference is reached.
type TargetKind uint8

const (
	TInst      TargetKind = iota // a generated function instance
	TIntrinsic                   // handled by the backend
	TVirtual                     // dynamic dispatch through a vtable
)

// Target is a resolved function reference.
type Target struct {
	Kind      TargetKind
	Inst      *Inst
	Intrinsic string
	TypeArgs  []*types.Type // concrete type arguments (intrinsics)
	Method    int           // vtable slot (virtual)
	Def       *resolve.Def  // the referenced item
}

func (t *Target) key() string {
	switch t.Kind {
	case TInst:
		return "i" + t.Inst.Key
	case TIntrinsic:
		return "x" + t.Intrinsic
	}
	return fmt.Sprintf("v%d", t.Method)
}

// Inst is one concrete instantiation of a function body.
type Inst struct {
	Key   string
	Func  *mir.Func
	Def   *resolve.Def
	Args  []*types.Type
	Subst map[*resolve.Def]*types.Type
	Self  *types.Type
	Group *Group
	Dict  string // symbol of the dictionary
	// TrackCaller functions receive the caller's location.
	TrackCaller bool

	targets map[any]*Target
	vtables map[any]*Vtable
	// sites lists the keys of targets and vtables in walk order (slot
	// assignment follows it, so output does not depend on map order).
	sites []any
	// statics lists the statics the code reads.
	statics []*resolve.Def
}

// Concrete substitutes the instance's type arguments in t.
func (in *Inst) Concrete(p *Plan, t *types.Type) *types.Type {
	if t == nil {
		return nil
	}
	return p.Info.Normalize(nil, types.Subst(p.Info.Ctx, t, in.Subst, in.Self))
}

// Group is a shape instance: one piece of machine code shared by the
// instantiations in Insts. Rep provides the (layout-identical) types.
type Group struct {
	Key   string
	Sym   string
	Rep   *Inst
	Insts []*Inst
	// Slots maps dynamic sites (targets or vtables that differ between the
	// instantiations) to dictionary slot indices.
	Slots  map[any]int
	NSlots int
	// ClosureSyms names the code of closures defined in the function.
	ClosureSyms map[*mir.Func]string
}

// Vtable is the vtable of a concrete type for a dyn type.
type Vtable struct {
	Sym      string
	Concrete *types.Type
	Dyn      *types.Type
	Methods  []*Target
}

// SiteTarget returns the target of a function constant in inst, and the
// dictionary slot to load it from (-1 if the call is static).
func (p *Plan) SiteTarget(inst *Inst, c *mir.Const) (*Target, int) {
	t := inst.targets[c]
	if slot, ok := inst.Group.Slots[c]; ok {
		return t, slot
	}
	return t, -1
}

// SiteVtable returns the vtable for a MakeDyn site and its slot (-1 if
// static).
func (p *Plan) SiteVtable(inst *Inst, key any) (*Vtable, int) {
	v := inst.vtables[key]
	if slot, ok := inst.Group.Slots[key]; ok {
		return v, slot
	}
	return v, -1
}

// DictEntries returns, for each slot of inst's group, the target or
// vtable of inst.
func (p *Plan) DictEntries(inst *Inst) []any {
	out := make([]any, inst.Group.NSlots)
	for k, i := range inst.Group.Slots {
		if t, ok := inst.targets[k]; ok {
			out[i] = t
		} else {
			out[i] = inst.vtables[k]
		}
	}
	return out
}

// DynSite identifies a MakeDyn statement.
type DynSite struct {
	F    *mir.Func
	B, S int
}

// Build plans code generation for the program rooted at main.
func Build(prog *mir.Program, main *resolve.Def) *Plan {
	p := &Plan{
		Mir:      prog,
		Info:     prog.Info,
		Lay:      layout.New(prog.Info),
		Insts:    map[string]*Inst{},
		Vtables:  map[string]*Vtable{},
		Strings:  map[string]string{},
		staticOK: map[*resolve.Def]bool{},
		groups:   map[string]*Group{},

		StaticInit: map[*resolve.Def]*Inst{},
		Runtime:    map[string]*Inst{},
	}
	p.findNative()
	for _, name := range []string{"panic_raw", "panic_bounds", "gc_alloc", "sched_init", "gmain", "set_args"} {
		if d := p.nativeFn(name); d != nil {
			p.Runtime[name] = p.inst(d, nil, nil, prog.Func(d))
		} else {
			p.Errors = append(p.Errors, "std::rt::native::"+name+" is missing")
		}
	}
	p.Main = p.inst(main, nil, nil, prog.Func(main))
	p.orderStatics()
	p.assignSlots()
	sort.Slice(p.Groups, func(i, j int) bool { return p.Groups[i].Sym < p.Groups[j].Sym })
	return p
}

// Str returns the symbol of a string literal.
func (p *Plan) Str(s string) string { return p.str(s) }

func (p *Plan) findNative() {
	for _, c := range p.Info.Res.Crates {
		if !c.IsStd {
			continue
		}
		rt := c.Root.Lookup("rt", resolve.TypeNS)
		if rt == nil || rt.Res.Def == nil {
			return
		}
		if n := rt.Res.Def.Module.Lookup("native", resolve.TypeNS); n != nil && n.Res.Def != nil {
			p.native = n.Res.Def.Module
		}
	}
}

func (p *Plan) nativeFn(name string) *resolve.Def {
	if p.native == nil {
		return nil
	}
	b := p.native.Lookup(name, resolve.ValueNS)
	if b == nil || b.Res.Def == nil || p.Mir.Func(b.Res.Def) == nil {
		return nil
	}
	return b.Res.Def
}

func argsKey(ts []*types.Type) string {
	var parts []string
	for _, t := range ts {
		parts = append(parts, fmt.Sprintf("%p", t))
	}
	return strings.Join(parts, ",")
}

// inst returns the instance of f (the body of def, or a shim) for the
// given type arguments, creating and walking it the first time.
func (p *Plan) inst(def *resolve.Def, args []*types.Type, self *types.Type, f *mir.Func) *Inst {
	if def != nil && (def.Parent == nil || def.Parent.Kind != resolve.DefTrait) {
		self = nil
	}
	key := fmt.Sprintf("%p<%s>%p", f, argsKey(args), self)
	if in := p.Insts[key]; in != nil {
		return in
	}
	in := &Inst{Key: key, Func: f, Def: def, Args: args, Self: self, Subst: map[*resolve.Def]*types.Type{},
		targets: map[any]*Target{}, vtables: map[any]*Vtable{}}
	if def != nil {
		switch def.Kind {
		case resolve.DefFn, resolve.DefAssocFn, resolve.DefConst, resolve.DefAssocConst, resolve.DefStatic:
			for i, gp := range p.Info.GenericsOf(def).All() {
				if i < len(args) {
					in.Subst[gp.Def] = args[i]
				}
			}
		}
		in.TrackCaller = hasAttr(def, "track_caller")
	}
	if f.Shim {
		in.TrackCaller = true
	}
	p.Insts[key] = in
	p.walk(in, f)
	p.addToGroup(in)
	return in
}

func hasAttr(d *resolve.Def, name string) bool {
	fn, ok := d.Node.(*syntax.FnDecl)
	if !ok {
		return false
	}
	for _, a := range fn.Attrs {
		if len(a.Path.Segments) == 1 && a.Path.Segments[0].Name.Value == name {
			return true
		}
	}
	return false
}

// walk resolves every function reference and dyn conversion in f (and
// the closures it defines) for instance in.
func (p *Plan) walk(in *Inst, f *mir.Func) {
	visitOp := func(op mir.Operand) {
		if op.Kind != mir.OpConst {
			return
		}
		switch op.Const.Kind {
		case mir.CFn:
			if _, ok := in.targets[op.Const]; !ok {
				in.sites = append(in.sites, op.Const)
			}
			in.targets[op.Const] = p.resolve(in, op.Const.Fn)
		case mir.CStatic:
			p.static(op.Const.Static)
			in.statics = append(in.statics, op.Const.Static)
		case mir.CStr, mir.CBytes:
			p.str(op.Const.Str)
		}
	}
	visitRv := func(rv mir.Rvalue) {
		switch r := rv.(type) {
		case mir.Use:
			visitOp(r.X)
		case mir.Bin:
			visitOp(r.X)
			visitOp(r.Y)
		case mir.Un:
			visitOp(r.X)
		case mir.Cast:
			visitOp(r.X)
		case mir.Aggregate:
			for _, o := range r.Ops {
				visitOp(o)
			}
			if r.Closure != nil {
				p.walk(in, r.Closure)
			}
		case mir.Unsize:
			visitOp(r.X)
		case mir.MakeDyn:
			visitOp(r.X)
		}
	}
	for bi, b := range f.Blocks {
		for si, s := range b.Stmts {
			switch s := s.(type) {
			case mir.Assign:
				visitRv(s.Rv)
				if md, ok := s.Rv.(mir.MakeDyn); ok {
					concrete := in.Concrete(p, md.Concrete)
					to := in.Concrete(p, md.To)
					key := DynSite{f, bi, si}
					if _, ok := in.vtables[key]; !ok {
						in.sites = append(in.sites, key)
					}
					in.vtables[key] = p.vtable(concrete, to.Elem)
				}
			case mir.PushDefer:
				visitOp(s.Fn)
				for _, a := range s.Args {
					visitOp(a)
				}
			}
		}
		switch t := b.Term.(type) {
		case mir.Call:
			visitOp(t.Fn)
			for _, a := range t.Args {
				visitOp(a)
			}
		case mir.Switch:
			visitOp(t.X)
		case mir.Assert:
			visitOp(t.Cond)
			p.str(t.Msg)
		}
	}
}

func (p *Plan) str(s string) string {
	if sym, ok := p.Strings[s]; ok {
		return sym
	}
	sym := fmt.Sprintf("_navi_str%d", len(p.Strings))
	p.Strings[s] = sym
	return sym
}

func (p *Plan) static(d *resolve.Def) {
	if p.staticOK[d] {
		return
	}
	p.staticOK[d] = true
	p.Statics = append(p.Statics, d)
	if f := p.Mir.Func(d); f != nil {
		p.StaticInit[d] = p.inst(d, nil, nil, f)
	}
}

func isIntrinsic(d *resolve.Def) bool { return hasAttr(d, "intrinsic") }

// resolve turns a function reference into a concrete target.
func (p *Plan) resolve(in *Inst, ref *mir.FnRef) *Target {
	args := make([]*types.Type, len(ref.Args))
	for i, a := range ref.Args {
		args[i] = in.Concrete(p, a)
	}
	self := in.Concrete(p, ref.Self)
	def := ref.Def
	if ref.Ctor {
		var adt *types.Type
		if def.Kind == resolve.DefVariant {
			adt = p.Info.Ctx.Adt(def.Parent, args)
		} else {
			adt = p.Info.Ctx.Adt(def, args)
		}
		return &Target{Kind: TInst, Inst: p.inst(nil, nil, nil, p.Mir.CtorThunk(def, adt)), Def: def}
	}
	if (def.Kind == resolve.DefAssocFn || def.Kind == resolve.DefAssocConst) && def.Parent.Kind == resolve.DefTrait {
		if self == nil {
			p.Errors = append(p.Errors, fmt.Sprintf("trait item %s without a self type", def.Name))
			return &Target{Kind: TIntrinsic, Intrinsic: "unreachable", Def: def}
		}
		r := p.Info.ResolveMethod(def, self, args)
		if r != nil && r.Virtual {
			return &Target{Kind: TVirtual, Method: p.vtableIndex(self, def), Def: def, TypeArgs: args}
		}
		if r == nil {
			p.Errors = append(p.Errors, fmt.Sprintf("no impl of %s::%s for %s", def.Parent.Name, def.Name, self))
			return &Target{Kind: TIntrinsic, Intrinsic: "unreachable", Def: def}
		}
		return p.resolved(r, def, self, args)
	}
	if isIntrinsic(def) {
		if !Inline[def.Name] {
			if nd := p.nativeFn(def.Name); nd != nil {
				return &Target{Kind: TInst, Inst: p.inst(nd, args, nil, p.Mir.Func(nd)), Def: nd}
			}
		}
		return &Target{Kind: TIntrinsic, Intrinsic: def.Name, TypeArgs: args, Def: def}
	}
	if def.Kind == resolve.DefExternFn {
		return &Target{Kind: TIntrinsic, Intrinsic: "extern", Def: def}
	}
	f := p.Mir.Func(def)
	if f == nil {
		p.Errors = append(p.Errors, "no body for "+def.Name)
		return &Target{Kind: TIntrinsic, Intrinsic: "unreachable", Def: def}
	}
	return &Target{Kind: TInst, Inst: p.inst(def, args, self, f), Def: def}
}

func (p *Plan) resolved(r *types.Instance, method *resolve.Def, self *types.Type, args []*types.Type) *Target {
	trait := r.Builtin
	if trait == "" {
		trait = r.Derived
	}
	if trait == "" {
		return &Target{Kind: TInst, Inst: p.inst(r.Def, r.Args, r.Self, p.Mir.Func(r.Def)), Def: r.Def}
	}
	if !mir.UsesShim(trait, method.Name) {
		return &Target{Kind: TInst, Inst: p.inst(method, args, self, p.Mir.Func(method)), Def: method}
	}
	return &Target{Kind: TInst, Inst: p.inst(nil, nil, nil, p.Mir.Shim(trait, method, self, args)), Def: method}
}

// dynMethods lists the dyn-callable methods of a dyn type's traits, in
// vtable order.
func (p *Plan) dynMethods(dyn *types.Type) []*resolve.Def {
	var out []*resolve.Def
	for _, b := range dyn.Bounds {
		for _, tr := range p.Info.Supertraits(&types.TraitRef{Trait: b.Trait, Self: dyn, Args: b.Args}) {
			for _, m := range p.Info.TraitMethods(tr.Trait) {
				if p.Info.DynCallable(m) {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func (p *Plan) vtableIndex(dyn *types.Type, m *resolve.Def) int {
	for i, d := range p.dynMethods(dyn) {
		if d == m {
			return i
		}
	}
	p.Errors = append(p.Errors, "method "+m.Name+" is not callable through dyn")
	return 0
}

func (p *Plan) vtable(concrete, dyn *types.Type) *Vtable {
	key := fmt.Sprintf("%p/%p", concrete, dyn)
	if v := p.Vtables[key]; v != nil {
		return v
	}
	v := &Vtable{Sym: fmt.Sprintf("_navi_vt%d", len(p.Vtables)), Concrete: concrete, Dyn: dyn}
	p.Vtables[key] = v
	for _, m := range p.dynMethods(dyn) {
		r := p.Info.ResolveMethod(m, concrete, nil)
		if r == nil {
			p.Errors = append(p.Errors, fmt.Sprintf("no impl of %s for %s", m.Name, concrete))
			v.Methods = append(v.Methods, &Target{Kind: TIntrinsic, Intrinsic: "unreachable", Def: m})
			continue
		}
		v.Methods = append(v.Methods, p.resolved(r, m, concrete, nil))
	}
	return v
}

// shapeKey is the identity of an instance's code: the function and the
// GC shapes of every local (including closures').
func (p *Plan) shapeKey(in *Inst) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%p", in.Func)
	// The type arguments' shapes (as Go does): generic code may depend on
	// layouts that no local exposes, such as a field offset of S<T> reached
	// through a pointer.
	for _, a := range in.Args {
		b.WriteString("<" + p.shape(a, 0))
	}
	if in.Self != nil {
		b.WriteString("<self " + p.shape(in.Self, 0))
	}
	var walk func(f *mir.Func)
	walk = func(f *mir.Func) {
		for _, l := range f.Locals {
			b.WriteByte('|')
			b.WriteString(p.shape(in.Concrete(p, l.Type), 0))
		}
		for _, blk := range f.Blocks {
			for _, s := range blk.Stmts {
				if a, ok := s.(mir.Assign); ok {
					if agg, ok := a.Rv.(mir.Aggregate); ok && agg.Closure != nil {
						b.WriteString("{")
						walk(agg.Closure)
						b.WriteString("}")
					}
				}
			}
		}
	}
	walk(in.Func)
	return b.String()
}

// shape describes the layout-relevant structure of a type: thin pointers
// are all alike, everything else is described structurally.
func (p *Plan) shape(t *types.Type, depth int) string {
	switch t.Kind {
	case types.KRef, types.KPtr:
		return mir.ShapeOf(t)
	case types.KFn:
		return mir.ShapeOf(t)
	case types.KTuple:
		var parts []string
		for _, e := range t.Elems {
			parts = append(parts, p.shape(e, depth+1))
		}
		return "(" + strings.Join(parts, ",") + ")"
	case types.KArray:
		return fmt.Sprintf("[%s;%d]", p.shape(t.Elem, depth+1), t.Len)
	case types.KAdt:
		if depth > 8 {
			return fmt.Sprintf("%p", t)
		}
		ai := p.Info.Adts[t.Def]
		var parts []string
		if ai.IsEnum {
			for v := range ai.Variants {
				for _, ft := range p.Lay.FieldTypes(t, v) {
					parts = append(parts, p.shape(ft, depth+1))
				}
				parts = append(parts, "|")
			}
		} else {
			for _, ft := range p.Lay.FieldTypes(t, -1) {
				parts = append(parts, p.shape(ft, depth+1))
			}
		}
		return fmt.Sprintf("%p{%s}", t.Def, strings.Join(parts, ","))
	}
	return t.String()
}

func (p *Plan) addToGroup(in *Inst) {
	key := p.shapeKey(in)
	g := p.groups[key]
	if g == nil {
		name := "anon"
		if in.Def != nil {
			name = in.Def.Name
		} else if in.Func.Name != "" {
			name = in.Func.Name
		}
		g = &Group{Key: key, Sym: fmt.Sprintf("_navi_%d_%s", len(p.Groups), sanitize(name)), Rep: in, Slots: map[any]int{}, ClosureSyms: map[*mir.Func]string{}}
		p.groups[key] = g
		p.Groups = append(p.Groups, g)
	}
	g.Insts = append(g.Insts, in)
	in.Group = g
	in.Dict = fmt.Sprintf("%s_dict%d", g.Sym, len(g.Insts)-1)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// orderStatics sorts the statics so that each is initialized after the
// statics its initializer (and everything it calls) reads.
func (p *Plan) orderStatics() {
	var order []*resolve.Def
	state := map[*resolve.Def]int{}
	seen := map[*Inst]bool{}
	var visitStatic func(d *resolve.Def)
	var visitInst func(in *Inst)
	visitInst = func(in *Inst) {
		if in == nil || seen[in] {
			return
		}
		seen[in] = true
		for _, d := range in.statics {
			visitStatic(d)
		}
		for _, k := range in.sites {
			if t := in.targets[k]; t != nil && t.Kind == TInst {
				visitInst(t.Inst)
			}
			if v := in.vtables[k]; v != nil {
				for _, m := range v.Methods {
					if m.Kind == TInst {
						visitInst(m.Inst)
					}
				}
			}
		}
	}
	visitStatic = func(d *resolve.Def) {
		if state[d] != 0 {
			return
		}
		state[d] = 1
		visitInst(p.StaticInit[d])
		state[d] = 2
		order = append(order, d)
	}
	for _, d := range p.Statics {
		visitStatic(d)
	}
	p.Statics = order
}

// assignSlots finds, for every group, the sites whose target or vtable
// differs between its instantiations; each gets a dictionary slot.
func (p *Plan) assignSlots() {
	for _, g := range p.Groups {
		if len(g.Insts) < 2 {
			continue
		}
		rep := g.Rep
		for _, k := range rep.sites {
			differs := false
			for _, other := range g.Insts[1:] {
				if t, ok := rep.targets[k]; ok {
					if o := other.targets[k]; o == nil || o.key() != t.key() {
						differs = true
					}
				} else if rep.vtables[k] != other.vtables[k] {
					differs = true
				}
			}
			if differs {
				g.Slots[k] = g.NSlots
				g.NSlots++
			}
		}
	}
}
