package lower

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"navi/internal/codegen"
	"navi/internal/layout"
	"navi/internal/mir"
	"navi/internal/obj"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// Internal calling convention (all navi functions, closures and shims):
//
//	CTX  context: the dictionary of the instance, or for closures the
//	     environment (whose word at -8 holds the dictionary)
//	RET  address of the result slot
//	LOC  caller location (a *Location) for #[track_caller]
//	args on the caller's stack at sp+off, each rounded up to 8 bytes;
//	     the callee sees them at FP+16+off
//
// No register survives a call. Every MIR local lives in the frame:
//
//	FP+16+off  incoming arguments
//	FP-8       context (dictionary)
//	FP-16      result slot address
//	FP-24      caller location
//	FP-32      head of the defer list (panics walk the frames for it)
//	FP-...     locals, the call result buffer, the staging buffer
//	SP+0       outgoing arguments
const (
	slotCtx    = -8
	slotRet    = -16
	slotLoc    = -24
	slotDefers = -32
	fixedFrame = 40
)

// Target describes the platform conventions that matter to lowering.
type Target struct {
	// CPrefix is prepended to C symbol names ("_" on macOS).
	CPrefix string
	// Write is the C function writing to a file descriptor.
	Write string
}

// Options control code generation.
type Options struct {
	// Debug enables integer overflow checks.
	Debug bool
}

type gen struct {
	p       *codegen.Plan
	o       *obj.Object
	m       Machine
	tgt     Target
	opts    Options
	lay     *layout.Computer
	locs    map[string]string
	locList []locData
	closure map[closureKey]string
	statics map[*resolve.Def]string
	errs    []string
}

type closureKey struct {
	g *codegen.Group
	f *mir.Func
}

type locData struct {
	sym       string
	file      string
	line, col int
}

// Generate lowers the plan with machine m into a new object for arch.
func Generate(p *codegen.Plan, arch string, m Machine, tgt Target, opts Options) (*obj.Object, error) {
	if len(p.Errors) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(p.Errors, "\n"))
	}
	g := &gen{p: p, o: obj.New(arch), m: m, tgt: tgt, opts: opts, lay: p.Lay, locs: map[string]string{},
		closure: map[closureKey]string{}, statics: map[*resolve.Def]string{}}
	for i, d := range p.Statics {
		g.statics[d] = fmt.Sprintf("_navi_static%d_%s", i, d.Name)
	}
	for _, grp := range p.Groups {
		g.genGroup(grp)
	}
	g.genMain()
	g.genRunDefers()
	g.genSched()
	g.genData()
	if len(g.errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(g.errs, "\n"))
	}
	return g.o, nil
}

func (g *gen) errorf(format string, args ...any) {
	g.errs = append(g.errs, fmt.Sprintf(format, args...))
}

// emitFunc places the machine's current function in the text section.
func (g *gen) emitFunc(sym string, global bool) {
	code, relocs, err := g.m.Finish()
	if err != nil {
		g.errorf("%s: %v", sym, err)
		return
	}
	align := 4
	if g.o.Arch == "amd64" {
		align = 16
	}
	off := g.o.Define(sym, obj.Text, align, global)
	g.o.Sects[obj.Text] = append(g.o.Sects[obj.Text], code...)
	for _, r := range relocs {
		g.o.Reloc(obj.Text, uint32(off)+r.Off, r.Kind, r.Sym)
	}
}

// extern returns the symbol of a C function, declaring it undefined.
func (g *gen) extern(name string) string {
	sym := g.tgt.CPrefix + name
	g.o.Undefined(sym)
	return sym
}

// loc returns the symbol of a static Location.
func (g *gen) loc(file *syntax.File, pos syntax.Pos) string {
	name, line, col := "<unknown>", 0, 0
	if file != nil {
		ps := file.Position(pos)
		name, line, col = ps.File, ps.Line, ps.Col
	}
	return g.locAt(name, line, col)
}

func (g *gen) locAt(file string, line, col int) string {
	key := fmt.Sprintf("%s:%d:%d", file, line, col)
	if s, ok := g.locs[key]; ok {
		return s
	}
	s := fmt.Sprintf("_navi_loc%d", len(g.locs))
	g.locs[key] = s
	g.locList = append(g.locList, locData{s, file, line, col})
	g.p.Str(file)
	return s
}

// genData writes strings, locations, dictionaries, vtables and statics.
func (g *gen) genData() {
	o := g.o
	// Locations: { file: &str, line: u32, col: u32 }.
	locTy := g.p.Info.Ctx.Adt(g.p.Info.Lang["Location"], nil)
	ll := g.lay.Of(locTy)
	for _, l := range g.locList {
		off := o.Define(l.sym, obj.DataConst, 8, false)
		buf := make([]byte, ll.Size)
		putU64(buf[ll.Fields[0]+8:], uint64(len(l.file)))
		putU32(buf[ll.Fields[1]:], uint32(l.line))
		putU32(buf[ll.Fields[2]:], uint32(l.col))
		o.Sects[obj.DataConst] = append(o.Sects[obj.DataConst], buf...)
		o.Reloc(obj.DataConst, uint32(off+uint64(ll.Fields[0])), obj.Abs64, g.p.Str(l.file))
	}
	// Dictionaries: [code, ctx] per slot (vtables: [vtable, 0]).
	for _, grp := range g.p.Groups {
		for _, in := range grp.Insts {
			off := o.Define(in.Dict, obj.DataConst, 8, false)
			n := grp.NSlots
			if n == 0 {
				o.Sects[obj.DataConst] = append(o.Sects[obj.DataConst], make([]byte, 8)...)
				continue
			}
			o.Sects[obj.DataConst] = append(o.Sects[obj.DataConst], make([]byte, 16*n)...)
			for i, e := range g.p.DictEntries(in) {
				at := uint32(off) + uint32(16*i)
				switch e := e.(type) {
				case *codegen.Target:
					g.targetWords(at, e)
				case *codegen.Vtable:
					o.Reloc(obj.DataConst, at, obj.Abs64, e.Sym)
				}
			}
		}
	}
	// Vtables: [code, ctx] per dyn method.
	var vts []*codegen.Vtable
	for _, v := range g.p.Vtables {
		vts = append(vts, v)
	}
	sort.Slice(vts, func(i, j int) bool { return vts[i].Sym < vts[j].Sym })
	for _, v := range vts {
		off := o.Define(v.Sym, obj.DataConst, 8, false)
		o.Sects[obj.DataConst] = append(o.Sects[obj.DataConst], make([]byte, 16*len(v.Methods)+8)...)
		for i, t := range v.Methods {
			g.targetWords(uint32(off)+uint32(16*i), t)
		}
	}
	// Writable data: the stack top, then the statics (zero-initialized;
	// the entry point runs their initializers). The collector scans from
	// _navi_data_start to _navi_data_end.
	o.Define("_navi_data_start", obj.Data, 8, false)
	for _, sym := range []string{"_navi_stack_top", "_navi_stack_lo", "_navi_os_sp"} {
		o.Define(sym, obj.Data, 8, false)
		o.Sects[obj.Data] = append(o.Sects[obj.Data], make([]byte, 8)...)
	}
	for _, d := range g.p.Statics {
		ty := g.p.Info.Consts[d].Type
		size := int64(8)
		if l := g.lay.Of(ty); l.Size > 0 {
			size = l.Size
		}
		o.Define(g.statics[d], obj.Data, 8, false)
		o.Sects[obj.Data] = append(o.Sects[obj.Data], make([]byte, size)...)
	}
	o.Define("_navi_data_end", obj.Data, 8, false)
	o.Sects[obj.Data] = append(o.Sects[obj.Data], make([]byte, 8)...)
	// Strings last: code generation adds file names.
	type str struct{ sym, s string }
	var strs []str
	for s, sym := range g.p.Strings {
		strs = append(strs, str{sym, s})
	}
	sort.Slice(strs, func(i, j int) bool { return strs[i].sym < strs[j].sym })
	for _, s := range strs {
		o.Define(s.sym, obj.RoData, 1, false)
		o.Sects[obj.RoData] = append(o.Sects[obj.RoData], s.s...)
		o.Sects[obj.RoData] = append(o.Sects[obj.RoData], 0)
	}
}

func (g *gen) targetWords(at uint32, t *codegen.Target) {
	switch t.Kind {
	case codegen.TInst:
		g.o.Reloc(obj.DataConst, at, obj.Abs64, t.Inst.Group.Sym)
		g.o.Reloc(obj.DataConst, at+8, obj.Abs64, t.Inst.Dict)
	case codegen.TIntrinsic:
		if t.Intrinsic == "extern" {
			g.o.Reloc(obj.DataConst, at, obj.Abs64, g.extern(linkName(t.Def)))
		} else if t.Intrinsic != "unreachable" {
			g.errorf("intrinsic %s cannot be called dynamically", t.Intrinsic)
		}
	default:
		g.errorf("virtual call target in a dictionary")
	}
}

func putU64(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

func putU32(b []byte, v uint32) {
	for i := 0; i < 4; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

// linkName is the C symbol of an extern function (#[link_name = "..."]).
func linkName(d *resolve.Def) string {
	if fn, ok := d.Node.(*syntax.FnDecl); ok {
		for _, a := range fn.Attrs {
			if len(a.Path.Segments) == 1 && a.Path.Segments[0].Name.Value == "link_name" {
				if lit, ok := a.Value.(*syntax.LitExpr); ok {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						return s
					}
				}
			}
		}
	}
	return d.Name
}

// genMain writes the C entry point: record the stack top, initialize the
// statics, call main, return 0.
func (g *gen) genMain() {
	m := g.m
	m.Begin()
	main := g.p.Main
	retSize := int64(16)
	if s := g.sizeOf(main.Concrete(g.p, main.Func.Locals[0].Type)); s > retSize {
		retSize = s
	}
	frame := (retSize + 15) &^ 15
	m.EntryPrologue(frame)
	m.AdrSym(I1, "_navi_stack_top")
	m.Store(8, FP, I1, 0)
	mainLoc := g.locAt("<main>", 0, 0)
	for i := len(g.p.Statics) - 1; i >= 0; i-- {
		d := g.p.Statics[i]
		in := g.p.StaticInit[d]
		if in == nil {
			continue
		}
		m.AdrSym(RET, g.statics[d])
		m.AdrSym(LOC, mainLoc)
		m.AdrSym(CTX, in.Dict)
		m.Call(in.Group.Sym)
	}
	// Create the main green thread and continue on its stack.
	init := g.p.Runtime["sched_init"]
	m.AddrOf(RET, SP, 0)
	m.AdrSym(LOC, mainLoc)
	m.AdrSym(CTX, init.Dict)
	m.Call(init.Group.Sym)
	m.Load(8, V1, SP, 0)
	m.AdrSym(I1, "_navi_os_sp")
	m.Mov(I2, SP)
	m.Store(8, I2, I1, 0)
	m.Mov(SP, V1)
	m.AddImm(SP, -frame)
	m.AddrOf(RET, SP, 0)
	m.AdrSym(LOC, mainLoc)
	m.AdrSym(CTX, main.Dict)
	m.Call(main.Group.Sym)
	m.AdrSym(I1, "_navi_os_sp")
	m.Load(8, V1, I1, 0)
	m.Mov(SP, V1)
	m.EntryReturn0()
	g.emitFunc(g.tgt.CPrefix+"main", true)
}

// genSched writes the scheduler's routines: the context switch, the start
// of a new green thread (which calls std::rt::native::gmain with an empty
// frame chain) and the stack overflow handler (C calls only: it runs on
// the red zone below the stack limit).
func (g *gen) genSched() {
	m := g.m
	m.Begin()
	m.SwapContext()
	g.emitFunc("_navi_swapctx", false)

	m.Begin()
	gm := g.p.Runtime["gmain"]
	m.AddImm(SP, -32)
	m.AddrOf(RET, SP, 0)
	m.AdrSym(LOC, g.locAt("<thread>", 0, 0))
	m.AdrSym(CTX, gm.Dict)
	m.Call(gm.Group.Sym)
	m.Trap()
	g.emitFunc("_navi_gstart", false)

	m.Begin()
	m.Prologue(32)
	msg := "fatal error: stack overflow\n"
	m.MovImm(V1, 2)
	m.Store(8, V1, SP, 0)
	m.AdrSym(V1, g.p.Str(msg))
	m.Store(8, V1, SP, 8)
	m.MovImm(V1, int64(len(msg)))
	m.Store(8, V1, SP, 16)
	m.CallC(g.extern(g.tgt.Write), 0, []CArg{{}, {}, {}}, false, false)
	m.MovImm(V1, 2)
	m.Store(8, V1, SP, 0)
	m.CallC(g.extern("exit"), 0, []CArg{{}}, false, false)
	m.Trap()
	g.emitFunc("_navi_overflow", false)
}

// genRunDefers writes _navi_rundefers(CTX = list head): calls each
// deferred record [next, code, ctx, argsize, retsize, loc, args...].
func (g *gen) genRunDefers() {
	g.m.Begin()
	g.m.RunDefers()
	g.emitFunc("_navi_rundefers", false)
}

func (g *gen) sizeOf(t *types.Type) int64 {
	if t == nil || t.Kind == types.KNever {
		return 0
	}
	l := g.lay.Of(t)
	if l.Unsized {
		g.errorf("size of unsized type %s", t)
		return 0
	}
	return l.Size
}

func round8(n int64) int64 { return (n + 7) &^ 7 }

func (g *gen) closureSym(grp *codegen.Group, f *mir.Func) string {
	k := closureKey{grp, f}
	if s, ok := g.closure[k]; ok {
		return s
	}
	s := fmt.Sprintf("%s_c%d", grp.Sym, len(g.closure))
	g.closure[k] = s
	return s
}

// genGroup generates the shared code of a shape group and its closures.
func (g *gen) genGroup(grp *codegen.Group) {
	var todo []*mir.Func
	var walk func(f *mir.Func)
	walk = func(f *mir.Func) {
		for _, b := range f.Blocks {
			for _, s := range b.Stmts {
				if as, ok := s.(mir.Assign); ok {
					if agg, ok := as.Rv.(mir.Aggregate); ok && agg.Closure != nil {
						todo = append(todo, agg.Closure)
						walk(agg.Closure)
					}
				}
			}
		}
	}
	walk(grp.Rep.Func)
	g.genFunc(grp, grp.Rep.Func, grp.Sym)
	for _, c := range todo {
		g.genFunc(grp, c, g.closureSym(grp, c))
	}
}

// fnGen generates one function body.
type fnGen struct {
	g      *gen
	m      Machine
	grp    *codegen.Group
	inst   *codegen.Inst
	f      *mir.Func
	ltypes []*types.Type
	loff   []int64 // FP-relative offsets of locals
	blocks []Label
	resOff int64 // call result buffer
	tmpOff int64 // staging buffer
	stubs  map[string]Label
	stubFn []func()
}

func (g *gen) genFunc(grp *codegen.Group, f *mir.Func, sym string) {
	g.m.Begin()
	fg := &fnGen{g: g, m: g.m, grp: grp, inst: grp.Rep, f: f, stubs: map[string]Label{}}
	fg.gen()
	g.emitFunc(sym, false)
}

func (f *fnGen) conc(t *types.Type) *types.Type {
	if t == nil {
		return nil
	}
	c := f.inst.Concrete(f.g.p, t)
	if c.Kind == types.KOpaque {
		if h := f.g.p.Info.Hidden[c]; h != nil {
			return f.conc(h)
		}
	}
	return c
}

func (f *fnGen) size(t *types.Type) int64 { return f.g.sizeOf(t) }

// firstArg is the first local passed on the stack (closures receive the
// environment in CTX).
func (f *fnGen) firstArg() int {
	if f.f.Closure {
		return 2
	}
	return 1
}

func (f *fnGen) gen() {
	m := f.m
	fn := f.f
	f.ltypes = make([]*types.Type, len(fn.Locals))
	f.loff = make([]int64, len(fn.Locals))
	for i, l := range fn.Locals {
		f.ltypes[i] = f.conc(l.Type)
	}
	// Incoming arguments.
	off := int64(16)
	for i := f.firstArg(); i <= fn.NArgs; i++ {
		f.loff[i] = off
		off += round8(f.size(f.ltypes[i]))
	}
	// Frame locals.
	cur := int64(fixedFrame)
	alloc := func(size, align int64) int64 {
		if align < 1 {
			align = 1
		}
		cur = (cur + size + align - 1) / align * align
		return -cur
	}
	for i := range fn.Locals {
		if i >= f.firstArg() && i <= fn.NArgs {
			continue
		}
		t := f.ltypes[i]
		size, align := int64(0), int64(1)
		if t.Kind != types.KNever {
			l := f.g.lay.Of(t)
			size, align = l.Size, l.Align
		}
		if size == 0 {
			size = 1
		}
		f.loff[i] = alloc(size, align)
	}
	// Buffers and the outgoing argument area.
	maxRes, maxTmp, maxOut := int64(16), int64(16), int64(48)
	for _, b := range fn.Blocks {
		for _, s := range b.Stmts {
			if as, ok := s.(mir.Assign); ok {
				// Aggregates and large copies are staged in the buffer.
				if s := f.size(f.placeType(as.Place)); s > maxTmp {
					maxTmp = s
				}
			}
		}
		if c, ok := b.Term.(mir.Call); ok {
			if s := f.size(f.placeType(c.Dest)); s > maxRes {
				maxRes = s
			}
			n := int64(0)
			for _, arg := range c.Args {
				n += round8(f.size(f.operandType(arg)))
			}
			if n > maxOut {
				maxOut = n
			}
		}
	}
	f.resOff = alloc(maxRes, 8)
	f.tmpOff = alloc(maxTmp, 8)
	frame := (cur+15)&^15 + (maxOut+15)&^15

	m.Prologue(frame)
	overflow := m.NewLabel()
	m.AdrSym(I1, "_navi_stack_lo")
	m.Load(8, I1, I1, 0)
	m.Mov(I2, SP)
	m.Cmp(I2, I1)
	m.BranchIf(LTU, overflow)
	f.stubFn = append(f.stubFn, func() {
		m.Bind(overflow)
		m.Call("_navi_overflow")
		m.Trap()
	})
	if fn.Closure {
		m.Store(8, CTX, FP, f.loff[1])
		m.Load(8, I1, CTX, -8)
		m.Store(8, I1, FP, slotCtx)
	} else {
		m.Store(8, CTX, FP, slotCtx)
	}
	m.Store(8, RET, FP, slotRet)
	m.Store(8, LOC, FP, slotLoc)
	m.StoreZero(8, FP, slotDefers)

	f.blocks = make([]Label, len(fn.Blocks))
	for i := range f.blocks {
		f.blocks[i] = m.NewLabel()
	}
	for bi, b := range fn.Blocks {
		m.Bind(f.blocks[bi])
		for si, s := range b.Stmts {
			f.stmt(bi, si, s)
		}
		f.term(bi, b.Term)
	}
	for _, s := range f.stubFn {
		s()
	}
}

// --- Places ----------------------------------------------------------------

func (f *fnGen) localAddr(r Reg, l int) { f.m.AddrOf(r, FP, f.loff[l]) }

func (f *fnGen) fieldOf(ty *types.Type, variant, field int) (int64, *types.Type) {
	l := f.g.lay.Of(ty)
	switch ty.Kind {
	case types.KTuple:
		return l.Fields[field], ty.Elems[field]
	case types.KAdt:
		if variant >= 0 {
			return l.Variants[variant][field], f.reveal(f.g.lay.FieldTypes(ty, variant)[field])
		}
		return l.Fields[field], f.reveal(f.g.lay.FieldTypes(ty, -1)[field])
	}
	f.g.errorf("%s: field of %s", f.f.Name, ty)
	return 0, ty
}

func (f *fnGen) reveal(t *types.Type) *types.Type {
	if t.Kind == types.KOpaque {
		if h := f.g.p.Info.Hidden[t]; h != nil {
			return f.conc(h)
		}
	}
	return t
}

// placeType is the type of a place (no code).
func (f *fnGen) placeType(pl mir.Place) *types.Type {
	ty := f.ltypes[pl.Local]
	variant := -1
	for _, pr := range pl.Proj {
		switch pr.Kind {
		case mir.PDeref:
			ty, variant = f.reveal(ty.Elem), -1
		case mir.PDowncast:
			variant = pr.Variant
		case mir.PField:
			_, ty = f.fieldOf(ty, variant, pr.Field)
			variant = -1
		case mir.PIndex:
			ty = f.reveal(ty.Elem)
		}
	}
	return ty
}

// place computes the address of a place into RA (and the metadata of an
// unsized place into RM). Clobbers I1, I2.
func (f *fnGen) place(pl mir.Place) *types.Type {
	m := f.m
	f.localAddr(RA, pl.Local)
	ty := f.ltypes[pl.Local]
	variant := -1
	for _, pr := range pl.Proj {
		switch pr.Kind {
		case mir.PDeref:
			elem := f.reveal(ty.Elem)
			if layout.IsFat(elem) {
				m.Load(8, RM, RA, 8)
			}
			m.Load(8, RA, RA, 0)
			ty, variant = elem, -1
		case mir.PDowncast:
			variant = pr.Variant
		case mir.PField:
			var off int64
			off, ty = f.fieldOf(ty, variant, pr.Field)
			variant = -1
			if off != 0 {
				m.AddImm(RA, off)
			}
		case mir.PIndex:
			elem := f.reveal(ty.Elem)
			f.localAddr(I1, pr.Index)
			m.Load(8, I1, I1, 0)
			m.MovImm(I2, f.size(elem))
			m.MulAdd(RA, I1, I2, RA)
			ty = elem
		}
	}
	return ty
}

// --- Operands --------------------------------------------------------------

func (f *fnGen) operandType(op mir.Operand) *types.Type {
	if op.Kind == mir.OpCopy {
		return f.placeType(op.Place)
	}
	return f.conc(op.Const.Type)
}

func isSigned(t *types.Type) bool { return t.Kind == types.KInt && t.Int.Signed() }

// scalarSize is the size of a scalar used in arithmetic.
func (f *fnGen) scalarSize(t *types.Type) int64 {
	switch t.Kind {
	case types.KInt:
		return layout.IntSize(t.Int)
	case types.KBool:
		return 1
	case types.KChar:
		return 4
	case types.KFloat:
		if t.F64 {
			return 8
		}
		return 4
	}
	return 8
}

// loadScalar loads a value of at most 8 bytes, zero-extended, into r
// (fat values: their first word). Clobbers RA, RM, I1, I2 (and T1 for
// function constants).
func (f *fnGen) loadScalar(op mir.Operand, r Reg) *types.Type {
	m := f.m
	if op.Kind == mir.OpCopy {
		ty := f.place(op.Place)
		size := f.size(ty)
		if size > 8 {
			size = 8
		}
		switch size {
		case 0:
			m.MovImm(r, 0)
		case 1, 2, 4, 8:
			m.Load(size, r, RA, 0)
		default:
			f.g.errorf("%s: scalar load of %d bytes", f.f.Name, size)
		}
		return ty
	}
	c := op.Const
	ty := f.conc(c.Type)
	switch c.Kind {
	case mir.CInt, mir.CChar, mir.CBool:
		v := c.Int
		if ty != nil && ty.Kind == types.KInt && !ty.Int.Signed() {
			if s := layout.IntSize(ty.Int); s < 8 {
				v &= 1<<(8*s) - 1
			}
		}
		m.MovImm(r, v)
	case mir.CFloat:
		if ty == nil || ty.F64 {
			m.MovImm(r, int64(math.Float64bits(c.Float)))
		} else {
			m.MovImm(r, int64(math.Float32bits(float32(c.Float))))
		}
	case mir.CStatic:
		m.AdrSym(r, f.g.statics[c.Static])
	case mir.CStr, mir.CBytes:
		m.AdrSym(r, f.g.p.Str(c.Str))
	case mir.CFn:
		f.fnValue(c, r, T1)
	default:
		m.MovImm(r, 0)
	}
	return ty
}

// loadVal loads a value of 1, 2, 4, 8 or 16 bytes into V1 (and V2).
func (f *fnGen) loadVal(op mir.Operand, size int64) {
	m := f.m
	if size <= 8 {
		f.loadScalar(op, V1)
		return
	}
	if op.Kind == mir.OpCopy {
		f.place(op.Place)
		m.Load(8, V1, RA, 0)
		m.Load(8, V2, RA, 8)
		return
	}
	c := op.Const
	switch c.Kind {
	case mir.CStr, mir.CBytes:
		m.AdrSym(V1, f.g.p.Str(c.Str))
		m.MovImm(V2, int64(len(c.Str)))
	case mir.CFn:
		f.fnValue(c, V1, V2)
	default:
		m.MovImm(V1, 0)
		m.MovImm(V2, 0)
	}
}

// fnValue materializes a function constant as (code, ctx).
func (f *fnGen) fnValue(c *mir.Const, code, ctx Reg) {
	m := f.m
	t, slot := f.g.p.SiteTarget(f.inst, c)
	if t == nil {
		f.g.errorf("%s: unresolved function constant", f.f.Name)
		return
	}
	if slot >= 0 {
		m.Load(8, I1, FP, slotCtx)
		m.Load(8, code, I1, int64(16*slot))
		m.Load(8, ctx, I1, int64(16*slot+8))
		return
	}
	switch t.Kind {
	case codegen.TInst:
		m.AdrSym(code, t.Inst.Group.Sym)
		m.AdrSym(ctx, t.Inst.Dict)
	case codegen.TIntrinsic:
		if t.Intrinsic == "extern" {
			m.AdrSym(code, f.g.extern(linkName(t.Def)))
			m.MovImm(ctx, 0)
			return
		}
		f.g.errorf("%s: intrinsic %s used as a value", f.f.Name, t.Intrinsic)
	default:
		f.g.errorf("%s: dynamic method %s used as a value", f.f.Name, t.Def.Name)
	}
}

// storeOperand writes an operand to [dst + off]; dst must be BASE, FP or
// SP. Clobbers RA, RM, V1, V2, T1, I1, I2.
func (f *fnGen) storeOperand(op mir.Operand, dst Reg, off int64) {
	m := f.m
	ty := f.operandType(op)
	size := f.size(ty)
	if op.Kind == mir.OpConst && op.Const.Kind == mir.CFn && ty != nil && ty.Kind == types.KFn && ty.Abi == "C" {
		size = 8
	}
	if size == 0 {
		return
	}
	if op.Kind == mir.OpCopy {
		f.place(op.Place)
		f.copyMem(dst, off, RA, 0, size)
		return
	}
	switch op.Const.Kind {
	case mir.CZero, mir.CUnit:
		f.zeroMem(dst, off, size)
		return
	}
	switch size {
	case 1, 2, 4, 8:
		f.loadScalar(op, V1)
		m.Store(size, V1, dst, off)
	case 16:
		f.loadVal(op, 16)
		m.Store(8, V1, dst, off)
		m.Store(8, V2, dst, off+8)
	default:
		f.g.errorf("%s: constant of %d bytes", f.f.Name, size)
	}
}

// copyMem copies size bytes; clobbers V2, T1, I1, I2.
func (f *fnGen) copyMem(dst Reg, dOff int64, src Reg, sOff int64, size int64) {
	m := f.m
	if size > 256 {
		m.AddrOf(I1, src, sOff)
		m.AddrOf(I2, dst, dOff)
		m.CopyWords(I2, I1, size/8)
		f.copySmall(I2, 0, I1, 0, size%8)
		return
	}
	f.copySmall(dst, dOff, src, sOff, size)
}

func (f *fnGen) copySmall(dst Reg, dOff int64, src Reg, sOff int64, size int64) {
	pos := int64(0)
	for _, chunk := range []int64{8, 4, 2, 1} {
		for size-pos >= chunk {
			f.m.Load(chunk, T1, src, sOff+pos)
			f.m.Store(chunk, T1, dst, dOff+pos)
			pos += chunk
		}
	}
}

func (f *fnGen) zeroMem(dst Reg, off, size int64) {
	pos := int64(0)
	for _, chunk := range []int64{8, 4, 2, 1} {
		for size-pos >= chunk {
			f.m.StoreZero(chunk, dst, off+pos)
			pos += chunk
		}
	}
}

// storeRegs stores V1 (and V2) into the place; sizes 1, 2, 4, 8, 16.
func (f *fnGen) storeRegs(pl mir.Place, size int64) {
	m := f.m
	f.place(pl)
	switch size {
	case 0:
	case 1, 2, 4, 8:
		m.Store(size, V1, RA, 0)
	case 16:
		m.Store(8, V1, RA, 0)
		m.Store(8, V2, RA, 8)
	default:
		f.g.errorf("%s: register store of %d bytes", f.f.Name, size)
	}
}

// --- Statements ------------------------------------------------------------

func (f *fnGen) stmt(bi, si int, s mir.Stmt) {
	m := f.m
	switch s := s.(type) {
	case mir.Assign:
		f.assign(bi, si, s)
	case mir.SetDiscr:
		ty := f.place(s.Place)
		f.writeTag(RA, ty, s.Variant)
	case mir.Box:
		ct := f.conc(s.Type)
		l := f.g.lay.Of(ct)
		f.callAlloc(l.Size, f.noscan(ct))
		m.Load(8, V1, FP, f.resOff)
		f.localAddr(RA, s.Local)
		m.Store(8, V1, RA, 0)
	case mir.PushDefer:
		f.pushDefer(s)
	}
}

// callAlloc calls std::rt::native::gc_alloc(size, noscan); the pointer
// is left in the result buffer.
func (f *fnGen) callAlloc(size int64, noscan bool) {
	m := f.m
	m.MovImm(V1, size)
	m.Store(8, V1, SP, 0)
	if noscan {
		m.MovImm(V1, 1)
		m.Store(1, V1, SP, 8)
	} else {
		m.StoreZero(1, SP, 8)
	}
	f.callRuntime("gc_alloc")
}

// noscan reports whether values of t hold no pointers.
func (f *fnGen) noscan(t *types.Type) bool {
	if t.Kind == types.KNever {
		return true
	}
	return len(f.g.lay.Of(t).Ptrs) == 0
}

func (f *fnGen) callRuntime(name string) {
	m := f.m
	in := f.g.p.Runtime[name]
	m.AddrOf(RET, FP, f.resOff)
	m.AdrSym(LOC, f.g.locAt("<runtime>", 0, 0))
	m.AdrSym(CTX, in.Dict)
	m.Call(in.Group.Sym)
}

func (f *fnGen) writeTag(base Reg, ty *types.Type, variant int) {
	m := f.m
	l := f.g.lay.Of(ty)
	switch l.Tag {
	case layout.TagInt:
		m.MovImm(V1, f.g.p.Info.Adts[ty.Def].Variants[variant].Discr)
		m.Store(l.TagSize, V1, base, l.TagOff)
	case layout.TagNiche:
		if variant == l.NicheVariant {
			m.StoreZero(8, base, 0)
		}
	}
}

// readVariant leaves the variant index of the enum at RA in V1.
func (f *fnGen) readVariant(ty *types.Type) {
	m := f.m
	l := f.g.lay.Of(ty)
	switch l.Tag {
	case layout.TagInt:
		m.Load(l.TagSize, V1, RA, l.TagOff)
		vs := f.g.p.Info.Adts[ty.Def].Variants
		identity := true
		for i, v := range vs {
			if v.Discr != int64(i) {
				identity = false
			}
		}
		if identity {
			return
		}
		mask := uint64(math.MaxUint64)
		if l.TagSize < 8 {
			mask = 1<<(8*l.TagSize) - 1
		}
		m.MovImm(V2, 0)
		for i, v := range vs {
			m.MovImm(T1, int64(uint64(v.Discr)&mask))
			m.Cmp(V1, T1)
			m.MovImm(I1, int64(i))
			m.Select(V2, I1, V2, EQ)
		}
		m.Mov(V1, V2)
	case layout.TagNiche:
		m.Load(8, V1, RA, 0)
		m.CmpImm(V1, 0)
		if l.NicheVariant == 0 {
			m.SetCond(V1, NE)
		} else {
			m.SetCond(V1, EQ)
		}
	default:
		m.MovImm(V1, 0)
	}
}

func (f *fnGen) assign(bi, si int, s mir.Assign) {
	m := f.m
	dt := f.placeType(s.Place)
	if dt.Kind == types.KNever {
		return
	}
	size := f.size(dt)
	regs := size == 1 || size == 2 || size == 4 || size == 8 || size == 16
	switch r := s.Rv.(type) {
	case mir.Use:
		if size == 0 {
			return
		}
		if regs {
			f.loadVal(r.X, size)
			f.storeRegs(s.Place, size)
			return
		}
		m.AddrOf(BASE, FP, f.tmpOff)
		f.storeOperand(r.X, BASE, 0)
		f.place(s.Place)
		f.copyMem(RA, 0, BASE, 0, size)
	case mir.Ref:
		pt := f.place(r.Place)
		m.Mov(V1, RA)
		if layout.IsFat(pt) {
			m.Mov(V2, RM)
		}
		f.storeRegs(s.Place, size)
	case mir.Bin:
		f.binop(r)
		f.storeRegs(s.Place, size)
	case mir.Un:
		f.unop(r)
		f.storeRegs(s.Place, size)
	case mir.Cast:
		f.cast(r, dt)
		f.storeRegs(s.Place, size)
	case mir.Discr:
		ty := f.place(r.Place)
		f.readVariant(ty)
		f.storeRegs(s.Place, size)
	case mir.Len:
		pt := f.placeType(r.Place)
		if pt.Kind == types.KArray {
			m.MovImm(V1, pt.Len)
		} else {
			f.place(r.Place)
			m.Mov(V1, RM)
		}
		f.storeRegs(s.Place, size)
	case mir.Unsize:
		f.loadScalar(r.X, V1)
		m.MovImm(V2, r.Len)
		f.storeRegs(s.Place, size)
	case mir.MakeDyn:
		f.loadScalar(r.X, V1)
		vt, slot := f.g.p.SiteVtable(f.inst, codegen.DynSite{F: f.f, B: bi, S: si})
		if slot >= 0 {
			m.Load(8, I1, FP, slotCtx)
			m.Load(8, V2, I1, int64(16*slot))
		} else if vt != nil {
			m.AdrSym(V2, vt.Sym)
		} else {
			f.g.errorf("%s: missing vtable", f.f.Name)
		}
		f.storeRegs(s.Place, size)
	case mir.Aggregate:
		if r.Kind == mir.AggClosure {
			f.closure(r)
			f.storeRegs(s.Place, size)
			return
		}
		if size == 0 {
			return
		}
		m.AddrOf(BASE, FP, f.tmpOff)
		ty := dt
		if r.Type != nil {
			ty = f.conc(r.Type)
		}
		l := f.g.lay.Of(ty)
		switch r.Kind {
		case mir.AggTuple:
			for i, op := range r.Ops {
				f.storeOperand(op, BASE, l.Fields[i])
			}
		case mir.AggArray:
			if len(r.Ops) > 0 {
				stride := l.Size / int64(len(r.Ops))
				for i, op := range r.Ops {
					f.storeOperand(op, BASE, int64(i)*stride)
				}
			}
		case mir.AggAdt:
			if !f.g.p.Info.Adts[ty.Def].IsEnum {
				for i, op := range r.Ops {
					f.storeOperand(op, BASE, l.Fields[i])
				}
				break
			}
			for i, op := range r.Ops {
				f.storeOperand(op, BASE, l.Variants[r.Variant][i])
			}
			f.writeTag(BASE, ty, r.Variant)
		}
		f.place(s.Place)
		f.copyMem(RA, 0, BASE, 0, size)
	default:
		f.g.errorf("%s: unsupported rvalue %T", f.f.Name, s.Rv)
	}
}

// closure allocates the environment [dict, captures...] and leaves the
// closure value (code, env+8) in V1, V2.
func (f *fnGen) closure(r mir.Aggregate) {
	m := f.m
	f.callAlloc(int64(8+8*len(r.Ops)), false)
	m.Load(8, BASE, FP, f.resOff)
	m.Load(8, T1, FP, slotCtx)
	m.Store(8, T1, BASE, 0)
	for i, op := range r.Ops {
		f.storeOperand(op, BASE, int64(8+8*i))
	}
	m.AdrSym(V1, f.g.closureSym(f.grp, r.Closure))
	m.AddrOf(V2, BASE, 8)
}

// --- Arithmetic ------------------------------------------------------------

func intMin(size int64) int64 { return -1 << (size*8 - 1) }

func (f *fnGen) panicStub(msg string, pos syntax.Pos) Label {
	key := fmt.Sprintf("%s@%d", msg, pos)
	if l, ok := f.stubs[key]; ok {
		return l
	}
	m := f.m
	l := m.NewLabel()
	f.stubs[key] = l
	f.stubFn = append(f.stubFn, func() {
		m.Bind(l)
		f.callPanic(msg, pos)
	})
	return l
}

// callPanic calls panic_raw(msg, file, line, col). Compiler-generated
// impls report their caller's location.
func (f *fnGen) callPanic(msg string, pos syntax.Pos) {
	m := f.m
	m.AdrSym(V1, f.g.p.Str(msg))
	m.Store(8, V1, SP, 0)
	m.MovImm(V1, int64(len(msg)))
	m.Store(8, V1, SP, 8)
	if f.f.Shim {
		f.storeCallerLoc(16)
	} else {
		file, line, col := "<unknown>", 0, 0
		if f.f.File != nil {
			ps := f.f.File.Position(pos)
			file, line, col = ps.File, ps.Line, ps.Col
		}
		f.storeFileLine(16, file, line, col)
	}
	f.callRuntime("panic_raw")
	m.Trap()
}

// storeCallerLoc writes the received caller location as (file, line,
// col) arguments at sp+off.
func (f *fnGen) storeCallerLoc(off int64) {
	m := f.m
	ll := f.g.lay.Of(f.g.p.Info.Ctx.Adt(f.g.p.Info.Lang["Location"], nil))
	m.Load(8, BASE, FP, slotLoc)
	m.Load(8, V1, BASE, ll.Fields[0])
	m.Store(8, V1, SP, off)
	m.Load(8, V1, BASE, ll.Fields[0]+8)
	m.Store(8, V1, SP, off+8)
	m.Load(4, V1, BASE, ll.Fields[1])
	m.Store(4, V1, SP, off+16)
	m.Load(4, V1, BASE, ll.Fields[2])
	m.Store(4, V1, SP, off+24)
}

func (f *fnGen) storeFileLine(off int64, file string, line, col int) {
	m := f.m
	m.AdrSym(V1, f.g.p.Str(file))
	m.Store(8, V1, SP, off)
	m.MovImm(V1, int64(len(file)))
	m.Store(8, V1, SP, off+8)
	m.MovImm(V1, int64(line))
	m.Store(4, V1, SP, off+16)
	m.MovImm(V1, int64(col))
	m.Store(4, V1, SP, off+24)
}

// noPos is the position of arithmetic panics in generated code without
// a source position.
var noPos syntax.Pos

var floatConds = map[mir.BinOp]Cond{mir.Eq: EQ, mir.Ne: NE, mir.Lt: LT, mir.Le: LE, mir.Gt: GT, mir.Ge: GE}

// binop leaves the result in V1.
func (f *fnGen) binop(r mir.Bin) {
	m := f.m
	ty := f.conc(r.Type)
	f.loadScalar(r.X, V1)
	f.loadScalar(r.Y, V2)
	if ty.Kind == types.KFloat {
		f64 := ty.F64
		m.FMovTo(F0, V1, f64)
		m.FMovTo(F1, V2, f64)
		switch r.Op {
		case mir.Add:
			m.FArith(Add, F0, F0, F1, f64)
		case mir.Sub:
			m.FArith(Sub, F0, F0, F1, f64)
		case mir.Mul:
			m.FArith(Mul, F0, F0, F1, f64)
		case mir.Div:
			m.FArith(FDiv, F0, F0, F1, f64)
		case mir.Rem:
			m.Store(8, V1, SP, 0)
			m.Store(8, V2, SP, 8)
			name := "fmod"
			if !f64 {
				name = "fmodf"
			}
			m.CallC(f.g.extern(name), 0, []CArg{{Float: true, F64: f64}, {Float: true, F64: f64}}, true, f64)
			return
		default:
			m.FCompare(V1, floatConds[r.Op], F0, F1, f64)
			return
		}
		m.FMovFrom(V1, F0, f64)
		return
	}
	size := f.scalarSize(ty)
	signed := isSigned(ty)
	if signed {
		m.SignExt(V1, V1, size)
		if r.Op != mir.Shl && r.Op != mir.Shr {
			m.SignExt(V2, V2, size)
		}
	}
	dbg := f.g.opts.Debug
	pos := r.Pos
	ovf := func(what string) Label { return f.panicStub("attempt to "+what+" with overflow", pos) }
	switch r.Op {
	case mir.Add, mir.Sub, mir.Mul:
		what := map[mir.BinOp]string{mir.Add: "add", mir.Sub: "subtract", mir.Mul: "multiply"}[r.Op]
		op := map[mir.BinOp]Op{mir.Add: Add, mir.Sub: Sub, mir.Mul: Mul}[r.Op]
		if size == 8 && dbg {
			m.ArithOvf(op, signed, V1, V1, V2, ovf(what))
			return
		}
		m.Arith(op, V1, V1, V2)
		if dbg {
			f.checkRange(size, signed, ovf(what))
		}
	case mir.Div, mir.Rem:
		m.BranchZero(V2, f.panicStub("attempt to divide by zero", pos))
		if signed {
			skip := m.NewLabel()
			m.CmpImm(V2, -1)
			m.BranchIf(NE, skip)
			m.MovImm(T1, intMin(size))
			m.Cmp(V1, T1)
			m.BranchIf(EQ, f.panicStub("attempt to divide with overflow", pos))
			m.Bind(skip)
		}
		var op Op
		switch {
		case r.Op == mir.Div && signed:
			op = DivS
		case r.Op == mir.Div:
			op = DivU
		case signed:
			op = RemS
		default:
			op = RemU
		}
		m.Arith(op, V1, V1, V2)
	case mir.Shl, mir.Shr:
		m.CmpImm(V2, size*8)
		m.BranchIf(GEU, f.panicStub("attempt to shift with overflow", pos))
		switch {
		case r.Op == mir.Shl:
			m.Arith(Shl, V1, V1, V2)
		case signed:
			m.Arith(ShrS, V1, V1, V2)
		default:
			m.Arith(ShrU, V1, V1, V2)
		}
	case mir.BitAnd:
		m.Arith(And, V1, V1, V2)
	case mir.BitOr:
		m.Arith(Or, V1, V1, V2)
	case mir.BitXor:
		m.Arith(Xor, V1, V1, V2)
	case mir.Offset:
		m.MovImm(T1, f.size(f.reveal(ty.Elem)))
		m.MulAdd(V1, V2, T1, V1)
	default:
		m.Cmp(V1, V2)
		var c Cond
		switch r.Op {
		case mir.Eq:
			c = EQ
		case mir.Ne:
			c = NE
		case mir.Lt:
			c = LTU
			if signed {
				c = LT
			}
		case mir.Le:
			c = LEU
			if signed {
				c = LE
			}
		case mir.Gt:
			c = GTU
			if signed {
				c = GT
			}
		case mir.Ge:
			c = GEU
			if signed {
				c = GE
			}
		}
		m.SetCond(V1, c)
	}
}

// checkRange jumps to l unless V1 fits in size bytes.
func (f *fnGen) checkRange(size int64, signed bool, l Label) {
	if size >= 8 {
		return
	}
	m := f.m
	if signed {
		m.SignExt(T1, V1, size)
		m.Cmp(T1, V1)
		m.BranchIf(NE, l)
	} else {
		m.MovImm(T1, 1<<(8*size)-1)
		m.Cmp(V1, T1)
		m.BranchIf(GTU, l)
	}
}

func (f *fnGen) unop(r mir.Un) {
	m := f.m
	ty := f.conc(r.Type)
	f.loadScalar(r.X, V1)
	switch {
	case ty.Kind == types.KFloat:
		m.FMovTo(F0, V1, ty.F64)
		m.FNeg(F0, F0, ty.F64)
		m.FMovFrom(V1, F0, ty.F64)
	case ty.Kind == types.KBool:
		m.MovImm(V2, 1)
		m.Arith(Xor, V1, V1, V2)
	case r.Op == mir.Not:
		m.Not(V1, V1)
	default:
		size := f.scalarSize(ty)
		if isSigned(ty) && f.g.opts.Debug {
			m.SignExt(V1, V1, size)
			m.MovImm(T1, intMin(size))
			m.Cmp(V1, T1)
			m.BranchIf(EQ, f.panicStub("attempt to negate with overflow", r.Pos))
		}
		m.Neg(V1, V1)
	}
}

func (f *fnGen) cast(r mir.Cast, to *types.Type) {
	m := f.m
	switch r.Kind {
	case mir.CastPtr:
		f.loadVal(r.X, f.size(to))
		return
	case mir.CastPtrInt, mir.CastFnPtr, mir.CastIntPtr:
		f.loadScalar(r.X, V1)
		return
	case mir.CastEnum:
		from := f.operandType(r.X)
		if r.X.Kind != mir.OpCopy {
			f.loadScalar(r.X, V1)
			return
		}
		f.place(r.X.Place)
		l := f.g.lay.Of(from)
		vs := f.g.p.Info.Adts[from.Def].Variants
		if l.Tag != layout.TagInt {
			if len(vs) > 0 {
				m.MovImm(V1, vs[0].Discr)
			}
			return
		}
		m.Load(l.TagSize, V1, RA, l.TagOff)
		for _, v := range vs {
			if v.Discr < 0 {
				m.SignExt(V1, V1, l.TagSize)
				break
			}
		}
		return
	}
	from := f.operandType(r.X)
	f.loadScalar(r.X, V1)
	switch {
	case from.Kind == types.KFloat && to.Kind == types.KFloat:
		if from.F64 == to.F64 {
			return
		}
		m.FMovTo(F0, V1, from.F64)
		m.FloatConv(F0, F0, to.F64)
		m.FMovFrom(V1, F0, to.F64)
	case from.Kind == types.KFloat:
		m.FMovTo(F0, V1, from.F64)
		size := f.scalarSize(to)
		if size == 8 {
			m.FloatToInt(V1, F0, isSigned(to), from.F64)
			return
		}
		m.FloatToInt(V1, F0, true, from.F64)
		lo, hi := intMin(size), -intMin(size)-1
		if !isSigned(to) {
			lo, hi = 0, 1<<(8*size)-1
		}
		m.MovImm(V2, lo)
		m.Cmp(V1, V2)
		m.Select(V1, V2, V1, LT)
		m.MovImm(V2, hi)
		m.Cmp(V1, V2)
		m.Select(V1, V2, V1, GT)
	case to.Kind == types.KFloat:
		if isSigned(from) {
			m.SignExt(V1, V1, f.scalarSize(from))
			m.IntToFloat(F0, V1, true, to.F64)
		} else {
			m.IntToFloat(F0, V1, false, to.F64)
		}
		m.FMovFrom(V1, F0, to.F64)
	default:
		if isSigned(from) {
			m.SignExt(V1, V1, f.scalarSize(from))
		}
	}
}

// --- Terminators -----------------------------------------------------------

func (f *fnGen) term(bi int, t mir.Term) {
	m := f.m
	switch t := t.(type) {
	case mir.Goto:
		if t.Target != bi+1 {
			m.Jump(f.blocks[t.Target])
		}
	case mir.Switch:
		ty := f.conc(t.Type)
		f.loadScalar(t.X, V1)
		if ty != nil && isSigned(ty) {
			m.SignExt(V1, V1, f.scalarSize(ty))
		}
		for i, v := range t.Vals {
			m.CmpImm(V1, v)
			m.BranchIf(EQ, f.blocks[t.Targets[i]])
		}
		m.Jump(f.blocks[t.Else])
	case mir.Return:
		m.Load(8, CTX, FP, slotDefers)
		skip := m.NewLabel()
		m.BranchZero(CTX, skip)
		m.Call("_navi_rundefers")
		m.Bind(skip)
		if size := f.size(f.ltypes[0]); size > 0 {
			m.Load(8, BASE, FP, slotRet)
			f.localAddr(RA, 0)
			f.copyMem(BASE, 0, RA, 0, size)
		}
		m.Return()
	case mir.Unreachable:
		m.Trap()
	case mir.Assert:
		f.loadScalar(t.Cond, V1)
		m.BranchNonZero(V1, f.blocks[t.Target])
		if t.Bounds {
			f.loadScalar(t.Len, V1)
			m.Store(8, V1, SP, 0)
			f.loadScalar(t.Index, V1)
			m.Store(8, V1, SP, 8)
			file, line, col := "<unknown>", 0, 0
			if f.f.File != nil {
				ps := f.f.File.Position(t.Pos)
				file, line, col = ps.File, ps.Line, ps.Col
			}
			f.storeFileLine(16, file, line, col)
			f.callRuntime("panic_bounds")
			m.Trap()
			return
		}
		f.callPanic(t.Msg, t.Pos)
	case mir.Call:
		f.call(t)
	default:
		f.g.errorf("%s: block without terminator", f.f.Name)
	}
}

// passLoc sets LOC to the location passed to a callee.
func (f *fnGen) passLoc(pos syntax.Pos) {
	if f.inst.TrackCaller && !f.f.Closure {
		f.m.Load(8, LOC, FP, slotLoc)
		return
	}
	f.m.AdrSym(LOC, f.g.loc(f.f.File, pos))
}

// storeArgs writes the arguments to the outgoing area. If thinRecv, the
// first argument is a fat pointer of which only the data word is passed.
func (f *fnGen) storeArgs(args []mir.Operand, thinRecv bool) {
	off := int64(0)
	for i, arg := range args {
		if i == 0 && thinRecv {
			f.loadScalar(arg, V1)
			f.m.Store(8, V1, SP, 0)
			off = 8
			continue
		}
		f.storeOperand(arg, SP, off)
		off += round8(f.size(f.operandType(arg)))
	}
}

func (f *fnGen) call(t mir.Call) {
	m := f.m
	dt := f.placeType(t.Dest)
	var target *codegen.Target
	slot := -1
	if t.Fn.Kind == mir.OpConst && t.Fn.Const.Kind == mir.CFn {
		target, slot = f.g.p.SiteTarget(f.inst, t.Fn.Const)
		if target == nil {
			f.g.errorf("%s: unresolved call", f.f.Name)
			return
		}
		if slot < 0 && target.Kind == codegen.TIntrinsic {
			f.intrinsic(target, t, dt)
			return
		}
	}
	switch {
	case target == nil:
		ft := f.operandType(t.Fn)
		if ft.Kind == types.KFn && ft.Abi == "C" {
			f.cCall(t, dt, "", true)
			return
		}
		f.storeArgs(t.Args, false)
		f.loadVal(t.Fn, 16)
		m.Mov(BASE, V1)
		m.Mov(CTX, V2)
		f.passLoc(t.Pos)
		m.AddrOf(RET, FP, f.resOff)
		m.CallReg(BASE)
	case slot >= 0:
		f.storeArgs(t.Args, false)
		m.Load(8, BASE, FP, slotCtx)
		m.Load(8, CTX, BASE, int64(16*slot+8))
		m.Load(8, BASE, BASE, int64(16*slot))
		f.passLoc(t.Pos)
		m.AddrOf(RET, FP, f.resOff)
		m.CallReg(BASE)
	case target.Kind == codegen.TVirtual:
		f.storeArgs(t.Args, true)
		f.loadVal(t.Args[0], 16)
		m.Load(8, CTX, V2, int64(16*target.Method+8))
		m.Load(8, BASE, V2, int64(16*target.Method))
		f.passLoc(t.Pos)
		m.AddrOf(RET, FP, f.resOff)
		m.CallReg(BASE)
	default:
		f.storeArgs(t.Args, false)
		f.passLoc(t.Pos)
		m.AddrOf(RET, FP, f.resOff)
		m.AdrSym(CTX, target.Inst.Dict)
		m.Call(target.Inst.Group.Sym)
	}
	f.afterCall(t, dt)
}

func (f *fnGen) afterCall(t mir.Call, dt *types.Type) {
	m := f.m
	if t.Target < 0 || dt.Kind == types.KNever {
		m.Trap()
		return
	}
	if size := f.size(dt); size > 0 {
		f.place(t.Dest)
		f.copyMem(RA, 0, FP, f.resOff, size)
	}
	m.Jump(f.blocks[t.Target])
}

// cCall calls a C function (sym, or the C function pointer t.Fn when
// viaFn) with the platform's C convention.
func (f *fnGen) cCall(t mir.Call, dt *types.Type, sym string, viaFn bool) {
	m := f.m
	var args []CArg
	for i, arg := range t.Args {
		ty := f.operandType(arg)
		switch {
		case ty.Kind == types.KFloat:
			f.loadScalar(arg, V1)
			args = append(args, CArg{Float: true, F64: ty.F64})
		case f.size(ty) <= 8:
			f.loadScalar(arg, V1)
			if isSigned(ty) {
				m.SignExt(V1, V1, f.scalarSize(ty))
			}
			args = append(args, CArg{})
		default:
			f.g.errorf("%s: unsupported C argument type %s", f.f.Name, ty)
			return
		}
		m.Store(8, V1, SP, int64(8*i))
	}
	floatRet := dt.Kind == types.KFloat
	f64 := floatRet && dt.F64
	if viaFn {
		f.loadScalar(t.Fn, BASE)
		m.CallC("", BASE, args, floatRet, f64)
	} else {
		m.CallC(sym, 0, args, floatRet, f64)
	}
	if t.Target < 0 || dt.Kind == types.KNever {
		m.Trap()
		return
	}
	if size := f.size(dt); size > 0 {
		f.storeRegs(t.Dest, size)
	}
	m.Jump(f.blocks[t.Target])
}

// intrinsic generates an inline intrinsic or a C call.
func (f *fnGen) intrinsic(target *codegen.Target, t mir.Call, dt *types.Type) {
	m := f.m
	size := f.size(dt)
	targ := func(i int) *types.Type {
		if i < len(target.TypeArgs) {
			return target.TypeArgs[i]
		}
		return nil
	}
	switch target.Intrinsic {
	case "extern":
		f.cCall(t, dt, f.g.extern(linkName(target.Def)), false)
		return
	case "unreachable":
		m.Trap()
		return
	case "abort":
		m.CallC(f.g.extern("abort"), 0, nil, false, false)
		m.Trap()
		return
	case "has_pointers":
		if f.noscan(targ(0)) {
			m.MovImm(V1, 0)
		} else {
			m.MovImm(V1, 1)
		}
	case "frame_address":
		m.Mov(V1, FP)
	case "stack_top":
		m.AdrSym(V1, "_navi_stack_top")
		m.Load(8, V1, V1, 0)
	case "data_start":
		m.AdrSym(V1, "_navi_data_start")
	case "data_end":
		m.AdrSym(V1, "_navi_data_end")
	case "run_defers":
		f.loadScalar(t.Args[0], CTX)
		m.Call("_navi_rundefers")
	case "swap_context":
		f.loadScalar(t.Args[0], V1)
		m.Store(8, V1, SP, 0)
		f.loadScalar(t.Args[1], V1)
		m.Store(8, V1, SP, 8)
		m.CallC("_navi_swapctx", 0, []CArg{{}, {}}, false, false)
	case "gstart_address":
		m.AdrSym(V1, "_navi_gstart")
	case "set_stack_limit":
		f.loadScalar(t.Args[0], V1)
		m.AdrSym(I1, "_navi_stack_lo")
		m.Store(8, V1, I1, 0)
	case "size_of":
		m.MovImm(V1, f.size(targ(0)))
	case "align_of":
		m.MovImm(V1, f.g.lay.Of(targ(0)).Align)
	case "ptr_offset":
		f.loadScalar(t.Args[1], V2)
		m.Store(8, V2, FP, f.resOff)
		f.loadScalar(t.Args[0], V1)
		m.Load(8, V2, FP, f.resOff)
		m.MovImm(T1, f.size(targ(0)))
		m.MulAdd(V1, V2, T1, V1)
	case "slice_from_raw", "slice_from_raw_mut", "str_from_raw":
		f.loadScalar(t.Args[1], V2)
		m.Store(8, V2, FP, f.resOff)
		f.loadScalar(t.Args[0], V1)
		m.Load(8, V2, FP, f.resOff)
	case "slice_len":
		f.loadVal(t.Args[0], 16)
		m.Mov(V1, V2)
	case "slice_ptr":
		f.loadScalar(t.Args[0], V1)
	case "str_as_bytes":
		f.loadVal(t.Args[0], 16)
	case "caller_location":
		if f.inst.TrackCaller && !f.f.Closure {
			m.Load(8, BASE, FP, slotLoc)
		} else {
			m.AdrSym(BASE, f.g.loc(f.f.File, t.Pos))
		}
		f.place(t.Dest)
		f.copyMem(RA, 0, BASE, 0, size)
		if t.Target >= 0 {
			m.Jump(f.blocks[t.Target])
		}
		return
	default:
		f.g.errorf("%s: intrinsic %s has no native implementation", f.f.Name, target.Intrinsic)
		return
	}
	f.storeRegs(t.Dest, size)
	if t.Target >= 0 {
		m.Jump(f.blocks[t.Target])
	} else {
		m.Trap()
	}
}

// --- Defer -----------------------------------------------------------------

// pushDefer allocates a record [next, code, ctx, argsize, retsize, loc,
// args...] and links it into the frame's defer list.
func (f *fnGen) pushDefer(s mir.PushDefer) {
	m := f.m
	virtual := false
	var target *codegen.Target
	var retTy *types.Type
	if s.Fn.Kind == mir.OpConst && s.Fn.Const.Kind == mir.CFn {
		target, _ = f.g.p.SiteTarget(f.inst, s.Fn.Const)
		if target == nil {
			f.g.errorf("%s: unresolved deferred call", f.f.Name)
			return
		}
		switch {
		case target.Kind == codegen.TVirtual:
			virtual = true
			if sig := f.g.p.Info.FnSig(target.Def); sig != nil {
				retTy = f.conc(sig.Ret)
			}
		case target.Kind == codegen.TInst:
			retTy = target.Inst.Concrete(f.g.p, target.Inst.Func.Locals[0].Type)
		default:
			f.g.errorf("%s: cannot defer intrinsic %s", f.f.Name, target.Intrinsic)
			return
		}
	} else if ft := f.operandType(s.Fn); ft.Kind == types.KFn {
		retTy = ft.Ret
	}
	argSize := int64(0)
	for i, arg := range s.Args {
		if i == 0 && virtual {
			argSize += 8
			continue
		}
		argSize += round8(f.size(f.operandType(arg)))
	}
	retSize := int64(16)
	if retTy != nil {
		if rs := f.size(retTy); rs > retSize {
			retSize = rs
		}
	}
	retSize = round8(retSize)
	f.callAlloc(48+argSize, false)
	m.Load(8, BASE, FP, f.resOff)
	// Code and context.
	switch {
	case virtual:
		f.loadVal(s.Args[0], 16)
		m.Store(8, V1, BASE, 48)
		m.Load(8, V1, V2, int64(16*target.Method))
		m.Load(8, V2, V2, int64(16*target.Method+8))
	case target != nil:
		f.fnValue(s.Fn.Const, V1, V2)
	default:
		f.loadVal(s.Fn, 16)
	}
	m.Store(8, V1, BASE, 8)
	m.Store(8, V2, BASE, 16)
	m.MovImm(V1, argSize)
	m.Store(8, V1, BASE, 24)
	m.MovImm(V1, retSize)
	m.Store(8, V1, BASE, 32)
	if f.inst.TrackCaller && !f.f.Closure {
		m.Load(8, V1, FP, slotLoc)
	} else {
		m.AdrSym(V1, f.g.loc(f.f.File, noPos))
	}
	m.Store(8, V1, BASE, 40)
	off := int64(48)
	for i, arg := range s.Args {
		if i == 0 && virtual {
			off += 8
			continue
		}
		f.storeOperand(arg, BASE, off)
		off += round8(f.size(f.operandType(arg)))
	}
	m.Load(8, V1, FP, slotDefers)
	m.Store(8, V1, BASE, 0)
	m.Store(8, BASE, FP, slotDefers)
}
