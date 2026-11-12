package interp

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"

	"navi/internal/layout"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// Machine interprets a MIR program.
type Machine struct {
	prog   *mir.Program
	info   *types.Info
	lay    *layout.Computer
	ctx    *types.Ctx
	Stdout io.Writer
	Stderr io.Writer

	nextID   int
	statics  map[*resolve.Def]*Alloc
	strs     map[string]*Alloc
	fnAllocs map[string]*Alloc
	vtables  map[string]*Alloc
	addrs    map[int64]*Alloc // fake addresses of allocations cast to integers

	gil      sync.Mutex
	exitCode int
	exited   bool
	rts      *rtState
}

// New returns a machine for prog.
func New(prog *mir.Program) *Machine {
	return &Machine{
		prog:     prog,
		info:     prog.Info,
		lay:      layout.New(prog.Info),
		ctx:      prog.Info.Ctx,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		statics:  map[*resolve.Def]*Alloc{},
		strs:     map[string]*Alloc{},
		fnAllocs: map[string]*Alloc{},
		vtables:  map[string]*Alloc{},
		addrs:    map[int64]*Alloc{},
	}
}

// navPanic is a panic in navi code, unwinding Go frames.
type navPanic struct{ msg string }

// fatalErr is an interpreter error (undefined behaviour or a bug).
type fatalErr struct{ msg string }

// exitErr is std::process::exit.
type exitErr struct{ code int }

func (m *Machine) fatal(format string, args ...any) {
	panic(fatalErr{msg: fmt.Sprintf(format, args...)})
}

// Run calls main in crate and returns the process exit code.
func (m *Machine) Run(main *resolve.Def) int {
	m.gil.Lock()
	code := m.runThread(func(t *thread) {
		inst := m.instFor(main, nil, nil)
		t.call(inst, nil)
	}, "main")
	m.gil.Unlock()
	return code
}

// thread is one interpreter thread (a navi green thread).
type thread struct {
	m     *Machine
	name  string
	stack []*frame
}

// runThread runs f, converting panics to an exit code.
func (m *Machine) runThread(f func(t *thread), name string) (code int) {
	t := &thread{m: m, name: name}
	defer func() {
		if r := recover(); r != nil {
			switch r := r.(type) {
			case navPanic:
				fmt.Fprintf(m.Stderr, "thread '%s' panicked at %s\n", name, r.msg)
				code = 101
			case exitErr:
				code = r.code
			case fatalErr:
				fmt.Fprintf(m.Stderr, "interpreter error in thread '%s': %s\n%s", name, r.msg, t.backtrace())
				code = 134
			default:
				fmt.Fprintf(m.Stderr, "interpreter bug in thread '%s': %v\n%s", name, r, t.backtrace())
				code = 134
			}
			m.exitCode, m.exited = code, true
		}
	}()
	f(t)
	return 0
}

func (t *thread) backtrace() string {
	var b strings.Builder
	for i := len(t.stack) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "  in %s\n", t.stack[i].fn.Name)
	}
	return b.String()
}

// frame is one function activation.
type frame struct {
	fn     *mir.Func
	inst   *fnInst
	locals []*Alloc
	ltypes []*types.Type
	defers []deferred
	block  int
	pos    syntax.Pos
}

type deferred struct {
	fn   val
	fnT  *types.Type
	ref  *mir.FnRef
	args []val
	ats  []*types.Type
}

// fnInst is a function with concrete generic arguments.
type fnInst struct {
	f     *mir.Func
	subst map[*resolve.Def]*types.Type
	self  *types.Type
	// builtin/derived shims and intrinsics have no MIR.
	shim      string
	shimTrait string
	shimDef   *resolve.Def
	shimSelf  *types.Type
	shimArgs  []*types.Type
	intrinsic string
	ctor      *resolve.Def
	ctorType  *types.Type
	key       string
}

// concrete substitutes the instance's generic arguments in t.
func (fi *fnInst) concrete(m *Machine, t *types.Type) *types.Type {
	if t == nil {
		return nil
	}
	r := types.Subst(m.ctx, t, fi.subst, fi.self)
	if hasProj(r) {
		r = m.info.Normalize(nil, r)
	}
	if r.Kind == types.KOpaque {
		if h := m.info.Hidden[r]; h != nil {
			return fi.concrete(m, h)
		}
	}
	return r
}

func hasProj(t *types.Type) bool {
	switch t.Kind {
	case types.KProj, types.KOpaque:
		return true
	case types.KRef, types.KPtr, types.KSlice, types.KArray:
		return hasProj(t.Elem)
	case types.KTuple, types.KFn:
		for _, e := range t.Elems {
			if hasProj(e) {
				return true
			}
		}
		return t.Kind == types.KFn && t.Ret != nil && hasProj(t.Ret)
	case types.KAdt:
		for _, a := range t.Args {
			if hasProj(a) {
				return true
			}
		}
	}
	return false
}

func (m *Machine) layoutOf(t *types.Type) *layout.Layout { return m.lay.Of(t) }

func (m *Machine) sizeOf(t *types.Type) int64 {
	l := m.layoutOf(t)
	if l.Unsized {
		m.fatal("size of unsized type %s", t)
	}
	return l.Size
}

// ---------------------------------------------------------------------------
// Running functions

// call runs a function instance with argument values and returns its result.
func (t *thread) call(inst *fnInst, args []val) val {
	m := t.m
	if inst.f == nil {
		return t.callShim(inst, args)
	}
	f := inst.f
	fr := &frame{fn: f, inst: inst}
	fr.locals = make([]*Alloc, len(f.Locals))
	fr.ltypes = make([]*types.Type, len(f.Locals))
	for i, l := range f.Locals {
		lt := inst.concrete(m, l.Type)
		fr.ltypes[i] = lt
		size := int64(0)
		if lt.Kind == types.KError || hasError(lt) {
			m.fatal("local _%d of %s has type %s (from %s)", i, f.Name, lt, l.Type)
		}
		if ll := m.layoutOf(lt); !ll.Unsized {
			size = ll.Size
		}
		fr.locals[i] = m.newAlloc(size, fmt.Sprintf("%s local _%d", f.Name, i))
	}
	if len(args) != f.NArgs {
		m.fatal("%s called with %d arguments, expects %d", f.Name, len(args), f.NArgs)
	}
	for i, a := range args {
		m.write(Ptr{A: fr.locals[i+1]}, fitVal(a, int64(len(fr.locals[i+1].b))))
	}
	t.stack = append(t.stack, fr)
	defer func() { t.stack = t.stack[:len(t.stack)-1] }()
	unwinding := true
	defer func() {
		if unwinding && len(fr.defers) > 0 {
			// Run defers while a panic unwinds through this frame.
			if r := recover(); r != nil {
				if _, ok := r.(navPanic); ok {
					t.runDefers(fr)
				}
				panic(r)
			}
		}
	}()
	t.exec(fr)
	unwinding = false
	t.runDefers(fr)
	return m.read(Ptr{A: fr.locals[0]}, int64(len(fr.locals[0].b)))
}

func fitVal(v val, size int64) val {
	if int64(len(v.b)) == size {
		return v
	}
	out := newVal(size)
	copy(out.b, v.b)
	for off, p := range v.ptrs {
		if off < size {
			out.ensurePtrs()
			out.ptrs[off] = p
		}
	}
	return out
}

func (t *thread) runDefers(fr *frame) {
	for len(fr.defers) > 0 {
		d := fr.defers[len(fr.defers)-1]
		fr.defers = fr.defers[:len(fr.defers)-1]
		if d.ref != nil {
			t.call(t.m.resolveRef(fr.inst, d.ref), d.args)
		} else {
			t.callValue(d.fn, d.args)
		}
	}
}

func (t *thread) exec(fr *frame) {
	m := t.m
	fr.block = 0
	for {
		if m.rts != nil && m.rts.killed.Load() {
			panic(exitErr{code: 101})
		}
		blk := fr.fn.Blocks[fr.block]
		for _, s := range blk.Stmts {
			t.stmt(fr, s)
		}
		switch term := blk.Term.(type) {
		case mir.Goto:
			fr.block = term.Target
		case mir.Return:
			return
		case mir.Unreachable:
			m.fatal("entered unreachable code in %s", fr.fn.Name)
		case mir.Switch:
			v := t.operand(fr, term.X)
			x := m.scalarInt(v, t.operandType(fr, term.X))
			next := term.Else
			for i, c := range term.Vals {
				if c == x {
					next = term.Targets[i]
					break
				}
			}
			fr.block = next
		case mir.Assert:
			v := t.operand(fr, term.Cond)
			if v.b[0] == 0 {
				fr.pos = term.Pos
				msg := term.Msg
				if term.Bounds {
					msg = fmt.Sprintf("index out of bounds: the len is %d but the index is %d", t.operand(fr, term.Len).u64(0), t.operand(fr, term.Index).u64(0))
				}
				t.panic(fr, msg)
			}
			fr.block = term.Target
		case mir.Call:
			fr.pos = term.Pos
			res := t.callTerm(fr, term)
			if term.Target < 0 {
				m.fatal("function marked as diverging returned in %s", fr.fn.Name)
			}
			p, pt, _ := t.place(fr, term.Dest)
			if pt.Kind != types.KNever {
				m.write(p, fitVal(res, m.sizeOf(pt)))
			}
			fr.block = term.Target
		case nil:
			m.fatal("block without terminator in %s", fr.fn.Name)
		}
	}
}

// panic starts a navi panic with the current position.
func (t *thread) panic(fr *frame, msg string) {
	loc := "<unknown>"
	if fr.fn.File != nil {
		loc = fr.fn.File.Position(fr.pos).String()
	}
	panic(navPanic{msg: loc + ":\n" + msg})
}

func (t *thread) stmt(fr *frame, s mir.Stmt) {
	m := t.m
	switch s := s.(type) {
	case mir.Assign:
		p, pt, _ := t.place(fr, s.Place)
		if pt.Kind == types.KNever {
			return
		}
		v := t.rvalue(fr, s.Rv, pt)
		m.write(p, fitVal(v, m.sizeOf(pt)))
	case mir.Box:
		ct := fr.inst.concrete(m, s.Type)
		cell := m.newAlloc(m.sizeOf(ct), "cell")
		m.write(Ptr{A: fr.locals[s.Local]}, ptrVal(Ptr{A: cell}))
	case mir.PushDefer:
		d := deferred{}
		if s.Fn.Kind == mir.OpConst && s.Fn.Const.Kind == mir.CFn {
			d.ref = s.Fn.Const.Fn
		} else {
			d.fn = t.operand(fr, s.Fn)
		}
		for _, a := range s.Args {
			d.args = append(d.args, t.operand(fr, a))
		}
		fr.defers = append(fr.defers, d)
	case mir.SetDiscr:
		p, pt, _ := t.place(fr, s.Place)
		m.writeTag(p, pt, s.Variant)
	}
}

// ---------------------------------------------------------------------------
// Places

// place evaluates a place to an address, its concrete type and metadata
// (length or vtable) for unsized places.
func (t *thread) place(fr *frame, p mir.Place) (Ptr, *types.Type, Ptr) {
	m := t.m
	ptr := Ptr{A: fr.locals[p.Local]}
	ty := fr.ltypes[p.Local]
	var meta Ptr
	variant := -1
	for _, pr := range p.Proj {
		switch pr.Kind {
		case mir.PDeref:
			if ty.Kind != types.KRef && ty.Kind != types.KPtr {
				m.fatal("deref of non-pointer %s in %s", ty, fr.fn.Name)
			}
			elem := ty.Elem
			if layout.IsFat(elem) {
				v := m.read(ptr, 16)
				ptr, meta = v.ptrAt(0), v.ptrAt(8)
			} else {
				ptr = m.read(ptr, 8).ptrAt(0)
				meta = Ptr{}
			}
			ty = elem
			variant = -1
		case mir.PDowncast:
			variant = pr.Variant
		case mir.PField:
			l := m.layoutOf(ty)
			var off int64
			var ft *types.Type
			switch ty.Kind {
			case types.KTuple:
				off, ft = l.Fields[pr.Field], ty.Elems[pr.Field]
			case types.KAdt:
				if variant >= 0 {
					off = l.Variants[variant][pr.Field]
					ft = m.lay.FieldTypes(ty, variant)[pr.Field]
				} else {
					off = l.Fields[pr.Field]
					ft = m.lay.FieldTypes(ty, -1)[pr.Field]
				}
			default:
				m.fatal("field of %s", ty)
			}
			ptr = ptr.add(off)
			ty = ft
			variant = -1
		case mir.PIndex:
			idx := int64(m.read(Ptr{A: fr.locals[pr.Index]}, 8).u64(0))
			var n int64
			switch ty.Kind {
			case types.KArray:
				n = ty.Len
			case types.KSlice:
				n = meta.Off
			default:
				m.fatal("index into %s", ty)
			}
			if idx < 0 || idx >= n {
				t.panic(fr, fmt.Sprintf("index out of bounds: the len is %d but the index is %d", n, idx))
			}
			el := m.layoutOf(ty.Elem)
			ptr = ptr.add(idx * el.Size)
			ty = ty.Elem
		}
	}
	return ptr, ty, meta
}

// ---------------------------------------------------------------------------
// Operands and rvalues

func (t *thread) operandType(fr *frame, op mir.Operand) *types.Type {
	if op.Kind == mir.OpCopy {
		_, ty, _ := t.place(fr, op.Place)
		return ty
	}
	return fr.inst.concrete(t.m, op.Const.Type)
}

func (t *thread) operand(fr *frame, op mir.Operand) val {
	m := t.m
	if op.Kind == mir.OpCopy {
		p, ty, _ := t.place(fr, op.Place)
		if ty.Kind == types.KNever {
			return newVal(0)
		}
		return m.read(p, m.sizeOf(ty))
	}
	c := op.Const
	ct := fr.inst.concrete(m, c.Type)
	switch c.Kind {
	case mir.CInt, mir.CChar, mir.CBool:
		size := int64(8)
		if ct != nil && ct.Kind != types.KError {
			size = m.sizeOf(ct)
		}
		return intVal(size, c.Int)
	case mir.CFloat:
		return floatVal(ct == nil || ct.F64, c.Float)
	case mir.CStr:
		return fatVal(Ptr{A: m.strAlloc(c.Str)}, Ptr{Off: int64(len(c.Str))})
	case mir.CBytes:
		return fatVal(Ptr{A: m.strAlloc(c.Str)}, Ptr{Off: int64(len(c.Str))})
	case mir.CUnit, mir.CZero:
		if ct == nil || ct.Kind == types.KError || ct.Kind == types.KNever {
			return newVal(0)
		}
		return newVal(m.sizeOf(ct))
	case mir.CFn:
		inst := m.resolveRef(fr.inst, c.Fn)
		return fatVal(Ptr{A: m.fnAlloc(inst)}, Ptr{})
	case mir.CStatic:
		return ptrVal(Ptr{A: t.static(c.Static)})
	}
	m.fatal("unknown constant")
	return val{}
}

func (m *Machine) strAlloc(s string) *Alloc {
	if a := m.strs[s]; a != nil {
		return a
	}
	a := m.newAlloc(int64(len(s)), "string literal")
	copy(a.b, s)
	m.strs[s] = a
	return a
}

func (t *thread) static(d *resolve.Def) *Alloc {
	m := t.m
	if a := m.statics[d]; a != nil {
		return a
	}
	ct := m.info.Consts[d].Type
	a := m.newAlloc(m.sizeOf(ct), "static "+d.Name)
	m.statics[d] = a
	if f := m.prog.Func(d); f != nil {
		v := t.call(&fnInst{f: f, subst: map[*resolve.Def]*types.Type{}}, nil)
		m.write(Ptr{A: a}, fitVal(v, int64(len(a.b))))
	}
	return a
}

func (m *Machine) scalarInt(v val, t *types.Type) int64 {
	switch t.Kind {
	case types.KInt:
		return readInt(v.b, layout.IntSize(t.Int), t.Int.Signed())
	case types.KBool:
		return int64(v.b[0])
	case types.KChar:
		return readInt(v.b, 4, false)
	}
	return readInt(v.b, int64(len(v.b)), true)
}

func (t *thread) rvalue(fr *frame, rv mir.Rvalue, dt *types.Type) val {
	m := t.m
	switch r := rv.(type) {
	case mir.Use:
		return t.operand(fr, r.X)
	case mir.Ref:
		p, pt, meta := t.place(fr, r.Place)
		if layout.IsFat(pt) {
			return fatVal(p, meta)
		}
		return ptrVal(p)
	case mir.Bin:
		ty := fr.inst.concrete(m, r.Type)
		return t.binop(fr, r.Op, t.operand(fr, r.X), t.operand(fr, r.Y), ty)
	case mir.Un:
		ty := fr.inst.concrete(m, r.Type)
		x := t.operand(fr, r.X)
		switch ty.Kind {
		case types.KFloat:
			return floatVal(ty.F64, -readFloat(x.b, ty.F64))
		case types.KBool:
			return intVal(1, 1-int64(x.b[0]))
		}
		size := layout.IntSize(ty.Int)
		v := readInt(x.b, size, ty.Int.Signed())
		if r.Op == mir.Not {
			return intVal(size, ^v)
		}
		if ty.Int.Signed() && v == minInt(size) {
			t.panic(fr, "attempt to negate with overflow")
		}
		return intVal(size, -v)
	case mir.Cast:
		return t.cast(fr, r)
	case mir.Aggregate:
		return t.aggregate(fr, r, dt)
	case mir.Discr:
		p, pt, _ := t.place(fr, r.Place)
		return intVal(8, int64(m.readVariant(p, pt)))
	case mir.Len:
		_, pt, meta := t.place(fr, r.Place)
		if pt.Kind == types.KArray {
			return intVal(8, pt.Len)
		}
		return intVal(8, meta.Off)
	case mir.Unsize:
		v := t.operand(fr, r.X)
		return fatVal(v.ptrAt(0), Ptr{Off: r.Len})
	case mir.MakeDyn:
		v := t.operand(fr, r.X)
		concrete := fr.inst.concrete(m, r.Concrete)
		to := fr.inst.concrete(m, r.To)
		return fatVal(v.ptrAt(0), Ptr{A: m.vtable(concrete, to.Elem)})
	}
	m.fatal("unknown rvalue %T", rv)
	return val{}
}

func minInt(size int64) int64 { return -1 << (size*8 - 1) }

func (t *thread) binop(fr *frame, op mir.BinOp, x, y val, ty *types.Type) val {
	m := t.m
	switch ty.Kind {
	case types.KFloat:
		a, b := readFloat(x.b, ty.F64), readFloat(y.b, ty.F64)
		switch op {
		case mir.Add:
			return floatVal(ty.F64, a+b)
		case mir.Sub:
			return floatVal(ty.F64, a-b)
		case mir.Mul:
			return floatVal(ty.F64, a*b)
		case mir.Div:
			return floatVal(ty.F64, a/b)
		case mir.Rem:
			return floatVal(ty.F64, math.Mod(a, b))
		}
		return boolVal(cmpOp(op, compareFloat(a, b), a != a || b != b))
	case types.KPtr, types.KRef:
		pa, pb := x.ptrAt(0), y.ptrAt(0)
		switch op {
		case mir.Eq:
			return boolVal(pa == pb)
		case mir.Ne:
			return boolVal(pa != pb)
		}
		if pa.A != pb.A {
			m.fatal("comparison of pointers into different allocations")
		}
		c := 0
		if pa.Off < pb.Off {
			c = -1
		} else if pa.Off > pb.Off {
			c = 1
		}
		return boolVal(cmpOp(op, c, false))
	}
	size := int64(len(x.b))
	signed := ty.Kind == types.KInt && ty.Int.Signed()
	if ty.Kind == types.KInt {
		size = layout.IntSize(ty.Int)
	}
	a, b := readInt(x.b, size, signed), readInt(y.b, int64(len(y.b)), signed)
	if op == mir.Shl || op == mir.Shr {
		b = readInt(y.b, int64(len(y.b)), false)
		if b < 0 || b >= size*8 {
			t.panic(fr, "attempt to shift with overflow")
		}
		if op == mir.Shl {
			return intVal(size, a<<uint(b))
		}
		if signed {
			return intVal(size, a>>uint(b))
		}
		return intVal(size, int64(uint64(a)&mask(size)>>uint(b)))
	}
	switch op {
	case mir.Add, mir.Sub, mir.Mul:
		var r int64
		switch op {
		case mir.Add:
			r = a + b
		case mir.Sub:
			r = a - b
		case mir.Mul:
			r = a * b
		}
		if overflows(op, a, b, r, size, signed) {
			t.panic(fr, fmt.Sprintf("attempt to %s with overflow", map[mir.BinOp]string{mir.Add: "add", mir.Sub: "subtract", mir.Mul: "multiply"}[op]))
		}
		return intVal(size, r)
	case mir.Div, mir.Rem:
		if b == 0 {
			t.panic(fr, "attempt to divide by zero")
		}
		if signed {
			if a == minInt(size) && b == -1 {
				t.panic(fr, "attempt to divide with overflow")
			}
			if op == mir.Div {
				return intVal(size, a/b)
			}
			return intVal(size, a%b)
		}
		ua, ub := uint64(a)&mask(size), uint64(b)&mask(size)
		if op == mir.Div {
			return intVal(size, int64(ua/ub))
		}
		return intVal(size, int64(ua%ub))
	case mir.BitAnd:
		return intVal(size, a&b)
	case mir.BitOr:
		return intVal(size, a|b)
	case mir.BitXor:
		return intVal(size, a^b)
	}
	c := 0
	if signed {
		if a < b {
			c = -1
		} else if a > b {
			c = 1
		}
	} else {
		ua, ub := uint64(a)&mask(size), uint64(b)&mask(size)
		if ua < ub {
			c = -1
		} else if ua > ub {
			c = 1
		}
	}
	return boolVal(cmpOp(op, c, false))
}

func mask(size int64) uint64 {
	if size >= 8 {
		return ^uint64(0)
	}
	return 1<<(uint(size)*8) - 1
}

// overflows reports whether r (computed in 64 bits) does not fit the type.
func overflows(op mir.BinOp, a, b, r int64, size int64, signed bool) bool {
	if size < 8 {
		if signed {
			lo, hi := minInt(size), -minInt(size)-1
			return r < lo || r > hi
		}
		return r < 0 || uint64(r) > mask(size)
	}
	if signed {
		switch op {
		case mir.Add:
			return (a >= 0) == (b >= 0) && (r >= 0) != (a >= 0)
		case mir.Sub:
			return (a >= 0) != (b >= 0) && (r >= 0) != (a >= 0)
		case mir.Mul:
			return a != 0 && (r/a != b || a == -1 && b == math.MinInt64)
		}
	}
	ua, ub := uint64(a), uint64(b)
	switch op {
	case mir.Add:
		return ua+ub < ua
	case mir.Sub:
		return ub > ua
	case mir.Mul:
		return ua != 0 && (ua*ub)/ua != ub
	}
	return false
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpOp(op mir.BinOp, c int, unordered bool) bool {
	if unordered {
		return op == mir.Ne
	}
	switch op {
	case mir.Eq:
		return c == 0
	case mir.Ne:
		return c != 0
	case mir.Lt:
		return c < 0
	case mir.Le:
		return c <= 0
	case mir.Gt:
		return c > 0
	case mir.Ge:
		return c >= 0
	}
	return false
}

func boolVal(b bool) val {
	if b {
		return intVal(1, 1)
	}
	return intVal(1, 0)
}

func (t *thread) cast(fr *frame, r mir.Cast) val {
	m := t.m
	x := t.operand(fr, r.X)
	from := t.operandType(fr, r.X)
	to := fr.inst.concrete(m, r.To)
	switch r.Kind {
	case mir.CastPtr:
		if layout.IsFat(to.Elem) {
			return x
		}
		return ptrVal(x.ptrAt(0))
	case mir.CastPtrInt:
		p := x.ptrAt(0)
		if p.A == nil {
			return intVal(m.sizeOf(to), p.Off)
		}
		addr := int64(p.A.id)<<32 + p.Off
		m.addrs[int64(p.A.id)] = p.A
		return intVal(m.sizeOf(to), addr)
	case mir.CastIntPtr:
		v := readInt(x.b, int64(len(x.b)), false)
		if a := m.addrs[v>>32]; a != nil && v != 0 {
			return ptrVal(Ptr{A: a, Off: v & 0xffffffff})
		}
		return ptrVal(Ptr{Off: v})
	case mir.CastEnum:
		ai := m.info.Adts[from.Def]
		p := m.newAlloc(int64(len(x.b)), "enum")
		m.write(Ptr{A: p}, x)
		v := m.readVariant(Ptr{A: p}, from)
		return intVal(m.sizeOf(to), ai.Variants[v].Discr)
	case mir.CastFnPtr:
		return ptrVal(x.ptrAt(0))
	}
	// Numeric.
	switch {
	case from.Kind == types.KFloat && to.Kind == types.KFloat:
		return floatVal(to.F64, readFloat(x.b, from.F64))
	case from.Kind == types.KFloat:
		f := readFloat(x.b, from.F64)
		size := layout.IntSize(to.Int)
		return intVal(size, saturate(f, size, to.Int.Signed()))
	case to.Kind == types.KFloat:
		signed := from.Kind == types.KInt && from.Int.Signed()
		v := m.scalarInt(x, from)
		if !signed && from.Kind == types.KInt && layout.IntSize(from.Int) == 8 {
			return floatVal(to.F64, float64(uint64(v)))
		}
		return floatVal(to.F64, float64(v))
	}
	v := m.scalarInt(x, from)
	if from.Kind == types.KInt && !from.Int.Signed() {
		v = int64(uint64(v) & mask(layout.IntSize(from.Int)))
	}
	return intVal(m.sizeOf(to), v)
}

// saturate converts a float to an integer as Rust's `as` does.
func saturate(f float64, size int64, signed bool) int64 {
	if f != f {
		return 0
	}
	if signed {
		lo, hi := float64(minInt(size)), float64(-minInt(size)-1)
		if f <= lo {
			return minInt(size)
		}
		if f >= hi {
			return -minInt(size) - 1
		}
		return int64(f)
	}
	if f <= 0 {
		return 0
	}
	if f >= float64(mask(size)) {
		return int64(mask(size))
	}
	return int64(uint64(f))
}

func (t *thread) aggregate(fr *frame, r mir.Aggregate, dt *types.Type) val {
	m := t.m
	ty := dt
	if r.Type != nil {
		ty = fr.inst.concrete(m, r.Type)
	}
	if r.Kind == mir.AggClosure {
		inst := &fnInst{f: r.Closure, subst: fr.inst.subst, self: fr.inst.self}
		env := m.newAlloc(int64(8*len(r.Ops)), "closure env")
		for i, op := range r.Ops {
			m.write(Ptr{A: env, Off: int64(8 * i)}, t.operand(fr, op))
		}
		return fatVal(Ptr{A: m.fnAlloc(inst)}, Ptr{A: env})
	}
	l := m.layoutOf(ty)
	v := newVal(l.Size)
	put := func(off int64, x val) {
		copy(v.b[off:], x.b)
		for o, p := range x.ptrs {
			v.ensurePtrs()
			v.ptrs[off+o] = p
		}
	}
	switch r.Kind {
	case mir.AggTuple:
		for i, op := range r.Ops {
			put(l.Fields[i], t.operand(fr, op))
		}
	case mir.AggArray:
		if len(r.Ops) > 0 {
			stride := l.Size / int64(len(r.Ops))
			for i, op := range r.Ops {
				put(int64(i)*stride, t.operand(fr, op))
			}
		}
	case mir.AggAdt:
		ai := m.info.Adts[ty.Def]
		if !ai.IsEnum {
			for i, op := range r.Ops {
				put(l.Fields[i], t.operand(fr, op))
			}
			break
		}
		for i, op := range r.Ops {
			put(l.Variants[r.Variant][i], t.operand(fr, op))
		}
		tmp := m.newAlloc(l.Size, "enum")
		m.write(Ptr{A: tmp}, v)
		m.writeTag(Ptr{A: tmp}, ty, r.Variant)
		return m.read(Ptr{A: tmp}, l.Size)
	}
	return v
}

// writeTag records the variant of an enum value at p.
func (m *Machine) writeTag(p Ptr, ty *types.Type, variant int) {
	l := m.layoutOf(ty)
	switch l.Tag {
	case layout.TagInt:
		d := m.info.Adts[ty.Def].Variants[variant].Discr
		m.write(p.add(l.TagOff), intVal(l.TagSize, d))
	case layout.TagNiche:
		if variant == l.NicheVariant {
			m.write(p, newVal(8))
		}
	}
}

// readVariant returns the variant index of the enum value at p.
func (m *Machine) readVariant(p Ptr, ty *types.Type) int {
	if ty.Kind != types.KAdt {
		m.fatal("discriminant of %s", ty)
	}
	ai := m.info.Adts[ty.Def]
	l := m.layoutOf(ty)
	switch l.Tag {
	case layout.TagInt:
		d := readInt(m.read(p.add(l.TagOff), l.TagSize).b, l.TagSize, true)
		for i, v := range ai.Variants {
			if v.Discr == d || l.TagSize < 8 && int64(uint64(v.Discr)&mask(l.TagSize)) == int64(uint64(d)&mask(l.TagSize)) {
				return i
			}
		}
		m.fatal("invalid enum tag %d for %s", d, ty)
	case layout.TagNiche:
		if m.read(p, 8).ptrAt(0).isNull() {
			return l.NicheVariant
		}
		return 1 - l.NicheVariant
	}
	return 0
}

func hasError(t *types.Type) bool {
	switch t.Kind {
	case types.KError:
		return true
	case types.KRef, types.KPtr, types.KSlice, types.KArray:
		return hasError(t.Elem)
	case types.KTuple:
		for _, e := range t.Elems {
			if hasError(e) {
				return true
			}
		}
	case types.KAdt:
		for _, a := range t.Args {
			if hasError(a) {
				return true
			}
		}
	}
	return false
}
