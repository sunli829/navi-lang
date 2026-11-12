package mir

import (
	"fmt"
	"sort"
	"strings"

	"navi/internal/resolve"
	"navi/internal/types"
)

// GC shape instantiation (Go's "GC shape stenciling"): a generic function
// is compiled once per shape of its type arguments. Thin pointers to sized
// types share one shape, fat pointers another, closures a third; all other
// types are their own shape. Inside a shape instance every layout is known
// statically; what still depends on the actual type arguments (trait
// methods called on a parameter, type descriptors for allocation and dyn,
// generic callees) comes from a dictionary passed as a hidden argument.

// Shape kinds of a type argument.
const (
	ShapeThinPtr = "ptr"
	ShapeFatPtr  = "fatptr"
	ShapeDynPtr  = "dynptr"
	ShapeClosure = "closure"
)

// ShapeOf returns the shape key of a concrete type argument.
func ShapeOf(t *types.Type) string {
	switch t.Kind {
	case types.KRef, types.KPtr:
		switch t.Elem.Kind {
		case types.KDyn:
			return ShapeDynPtr
		case types.KStr, types.KSlice:
			return ShapeFatPtr
		}
		return ShapeThinPtr
	case types.KFn:
		if t.Abi == "" {
			return ShapeClosure
		}
		return ShapeThinPtr
	}
	return fmt.Sprintf("%p", t) // the interned type itself
}

// Instance is one shape instance of a function.
type Instance struct {
	Def    *resolve.Def
	Shapes []string
	// Dicts lists the distinct concrete instantiations sharing this
	// instance; each needs its own dictionary.
	Dicts map[string][]*types.Type
}

// Key identifies the shape instance.
func (in *Instance) Key() string {
	return fmt.Sprintf("%p[%s]", in.Def, strings.Join(in.Shapes, ","))
}

// Instances collects the shape instances reachable from root by walking
// MIR with concrete types (the same traversal the backend performs).
type Instances struct {
	p     *Program
	ByKey map[string]*Instance
	seen  map[string]bool
}

// CollectInstances walks every function reachable from root.
func (p *Program) CollectInstances(root *resolve.Def) *Instances {
	c := &Instances{p: p, ByKey: map[string]*Instance{}, seen: map[string]bool{}}
	c.visit(root, nil, nil)
	return c
}

func argsKey(args []*types.Type) string {
	var parts []string
	for _, a := range args {
		parts = append(parts, fmt.Sprintf("%p", a))
	}
	return strings.Join(parts, ",")
}

func (c *Instances) visit(def *resolve.Def, args []*types.Type, self *types.Type) {
	if def.Parent == nil || def.Parent.Kind != resolve.DefTrait {
		self = nil // Self only matters for trait default methods
	}
	key := fmt.Sprintf("%p<%s>%p", def, argsKey(args), self)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	f := c.p.Func(def)
	if f == nil {
		return
	}
	var shapes []string
	for _, a := range args {
		shapes = append(shapes, ShapeOf(a))
	}
	if self != nil {
		shapes = append(shapes, "self:"+ShapeOf(self))
	}
	in := &Instance{Def: def, Shapes: shapes, Dicts: map[string][]*types.Type{}}
	if old := c.ByKey[in.Key()]; old != nil {
		in = old
	} else {
		c.ByKey[in.Key()] = in
	}
	in.Dicts[argsKey(args)] = args

	subst := map[*resolve.Def]*types.Type{}
	for i, p := range c.p.Info.GenericsOf(def).All() {
		if i < len(args) {
			subst[p.Def] = args[i]
		}
	}
	conc := func(t *types.Type) *types.Type {
		if t == nil {
			return nil
		}
		return c.p.Info.Normalize(nil, types.Subst(c.p.Info.Ctx, t, subst, self))
	}
	var walkFunc func(f *Func)
	walkFunc = func(f *Func) {
		for _, b := range f.Blocks {
			for _, s := range b.Stmts {
				if a, ok := s.(Assign); ok {
					if agg, ok := a.Rv.(Aggregate); ok && agg.Closure != nil {
						walkFunc(agg.Closure)
					}
				}
			}
			call, ok := b.Term.(Call)
			if !ok || call.Fn.Kind != OpConst || call.Fn.Const.Kind != CFn {
				continue
			}
			ref := call.Fn.Const.Fn
			if ref.Ctor || ref.Def == nil {
				continue
			}
			cargs := make([]*types.Type, len(ref.Args))
			for i, a := range ref.Args {
				cargs[i] = conc(a)
			}
			cself := conc(ref.Self)
			d := ref.Def
			if (d.Kind == resolve.DefAssocFn || d.Kind == resolve.DefAssocConst) && d.Parent.Kind == resolve.DefTrait {
				if cself == nil {
					continue
				}
				r := c.p.Info.ResolveMethod(d, cself, cargs)
				if r == nil || r.Builtin != "" || r.Derived != "" || r.Virtual {
					continue
				}
				c.visit(r.Def, r.Args, r.Self)
				continue
			}
			c.visit(d, cargs, cself)
		}
	}
	walkFunc(f)
}

// Summary lists instances with their number of dictionaries, for tests
// and debugging.
func (c *Instances) Summary() []string {
	var out []string
	for _, in := range c.ByKey {
		out = append(out, fmt.Sprintf("%s %d", qualName(in.Def), len(in.Dicts)))
	}
	sort.Strings(out)
	return out
}
