package interp

import (
	"fmt"
	"strings"

	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

type vtable struct {
	concrete *types.Type
	dyn      *types.Type
}

func typesKey(ts []*types.Type) string {
	var parts []string
	for _, t := range ts {
		if t == nil {
			parts = append(parts, "-")
		} else {
			parts = append(parts, fmt.Sprintf("%p", t))
		}
	}
	return strings.Join(parts, ",")
}

// fnAlloc returns the allocation that represents a function instance as a
// pointer (function values point at it).
func (m *Machine) fnAlloc(inst *fnInst) *Alloc {
	key := inst.key
	if key == "" || inst.f != nil && inst.f.Closure {
		a := m.newAlloc(0, "fn")
		a.fn = inst
		return a
	}
	if a := m.fnAllocs[key]; a != nil {
		return a
	}
	a := m.newAlloc(0, "fn")
	a.fn = inst
	m.fnAllocs[key] = a
	return a
}

func (m *Machine) vtable(concrete, dyn *types.Type) *Alloc {
	key := fmt.Sprintf("%p/%p", concrete, dyn)
	if a := m.vtables[key]; a != nil {
		return a
	}
	a := m.newAlloc(8, "vtable")
	a.vt = &vtable{concrete: concrete, dyn: dyn}
	m.vtables[key] = a
	return a
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

// instFor builds the instance of a function definition with the given
// generic arguments (for all generics in scope) and Self.
func (m *Machine) instFor(def *resolve.Def, args []*types.Type, self *types.Type) *fnInst {
	key := fmt.Sprintf("%p<%s>%p", def, typesKey(args), self)
	if def.Kind == resolve.DefFn || def.Kind == resolve.DefAssocFn || def.Kind == resolve.DefExternFn {
		if hasAttr(def, "intrinsic") {
			return &fnInst{intrinsic: def.Name, shimDef: def, shimArgs: args, key: key}
		}
	}
	f := m.prog.Func(def)
	if f == nil {
		if def.Kind == resolve.DefExternFn {
			return &fnInst{intrinsic: "extern:" + def.Name, shimDef: def, key: key}
		}
		m.fatal("no body for %s", def.Name)
	}
	subst := map[*resolve.Def]*types.Type{}
	var params []*types.TypeParam
	switch def.Kind {
	case resolve.DefFn, resolve.DefAssocFn, resolve.DefConst, resolve.DefAssocConst, resolve.DefStatic:
		params = m.info.GenericsOf(def).All()
	}
	for i, p := range params {
		if i < len(args) {
			subst[p.Def] = args[i]
		}
	}
	return &fnInst{f: f, subst: subst, self: self, key: key}
}

// resolveRef resolves a FnRef in the context of the current instance.
func (m *Machine) resolveRef(cur *fnInst, ref *mir.FnRef) *fnInst {
	args := make([]*types.Type, len(ref.Args))
	for i, a := range ref.Args {
		args[i] = cur.concrete(m, a)
	}
	self := cur.concrete(m, ref.Self)
	def := ref.Def
	if ref.Ctor {
		var adt *types.Type
		if def.Kind == resolve.DefVariant {
			adt = m.ctx.Adt(def.Parent, args)
		} else {
			adt = m.ctx.Adt(def, args)
		}
		return &fnInst{ctor: def, ctorType: adt, key: fmt.Sprintf("ctor%p<%s>", def, typesKey(args))}
	}
	if (def.Kind == resolve.DefAssocFn || def.Kind == resolve.DefAssocConst) && def.Parent.Kind == resolve.DefTrait {
		if self == nil {
			m.fatal("trait item %s without a self type", def.Name)
		}
		in := m.info.ResolveMethod(def, self, args)
		if in == nil {
			m.fatal("no impl of %s::%s for %s", def.Parent.Name, def.Name, self)
		}
		switch {
		case in.Virtual:
			return &fnInst{shim: "virtual", shimDef: def, shimArgs: args, key: fmt.Sprintf("virt%p<%s>", def, typesKey(args))}
		case in.Builtin != "":
			return &fnInst{shim: "builtin", shimTrait: in.Builtin, shimDef: def, shimSelf: self, shimArgs: args, key: fmt.Sprintf("bi%p%p<%s>", def, self, typesKey(args))}
		case in.Derived != "":
			return &fnInst{shim: "derived", shimTrait: in.Derived, shimDef: def, shimSelf: self, shimArgs: args, key: fmt.Sprintf("dv%p%p<%s>", def, self, typesKey(args))}
		}
		return m.instFor(in.Def, in.Args, in.Self)
	}
	return m.instFor(def, args, self)
}

// callTerm executes a call terminator.
func (t *thread) callTerm(fr *frame, c mir.Call) val {
	var args []val
	for _, a := range c.Args {
		args = append(args, t.operand(fr, a))
	}
	if c.Fn.Kind == mir.OpConst && c.Fn.Const.Kind == mir.CFn {
		inst := t.m.resolveRef(fr.inst, c.Fn.Const.Fn)
		return t.call(inst, args)
	}
	return t.callValue(t.operand(fr, c.Fn), args)
}

// callValue calls a function value (code pointer plus environment).
func (t *thread) callValue(fv val, args []val) val {
	code := fv.ptrAt(0)
	if code.A == nil || code.A.fn == nil {
		t.m.fatal("call through an invalid function pointer")
	}
	inst := code.A.fn
	if inst.f != nil && inst.f.Closure {
		args = append([]val{ptrVal(fv.ptrAt(8))}, args...)
	}
	return t.call(inst, args)
}

// callShim runs functions without MIR: constructors, intrinsics, builtin
// and derived impls, and dynamic dispatch.
func (t *thread) callShim(inst *fnInst, args []val) val {
	m := t.m
	switch {
	case inst.ctor != nil:
		l := m.layoutOf(inst.ctorType)
		tmp := m.newAlloc(l.Size, "ctor")
		variant := -1
		offs := l.Fields
		if inst.ctor.Kind == resolve.DefVariant {
			for i, v := range m.info.Adts[inst.ctorType.Def].Variants {
				if v.Def == inst.ctor {
					variant = i
				}
			}
			offs = l.Variants[variant]
		}
		for i, a := range args {
			m.write(Ptr{A: tmp, Off: offs[i]}, a)
		}
		if variant >= 0 {
			m.writeTag(Ptr{A: tmp}, inst.ctorType, variant)
		}
		return m.read(Ptr{A: tmp}, l.Size)
	case inst.intrinsic != "":
		return t.intrinsic(inst, args)
	case inst.shim == "virtual":
		recv := args[0]
		vt := recv.ptrAt(8)
		if vt.A == nil || vt.A.vt == nil {
			m.fatal("dynamic call through an invalid vtable")
		}
		concrete := vt.A.vt.concrete
		in := m.info.ResolveMethod(inst.shimDef, concrete, inst.shimArgs)
		if in == nil {
			m.fatal("no impl of %s for %s", inst.shimDef.Name, concrete)
		}
		var target *fnInst
		switch {
		case in.Builtin != "":
			target = &fnInst{shim: "builtin", shimTrait: in.Builtin, shimDef: inst.shimDef, shimSelf: concrete, shimArgs: inst.shimArgs}
		case in.Derived != "":
			target = &fnInst{shim: "derived", shimTrait: in.Derived, shimDef: inst.shimDef, shimSelf: concrete, shimArgs: inst.shimArgs}
		default:
			target = m.instFor(in.Def, in.Args, in.Self)
		}
		args = append([]val{ptrVal(recv.ptrAt(0))}, args[1:]...)
		return t.call(target, args)
	case inst.shim == "builtin":
		return t.builtin(inst, args)
	case inst.shim == "derived":
		return t.derived(inst, args)
	}
	m.fatal("cannot call %v", inst.key)
	return val{}
}
