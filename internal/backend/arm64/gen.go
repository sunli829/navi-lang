package arm64

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"navi/internal/codegen"
	"navi/internal/layout"
	"navi/internal/mir"
	"navi/internal/obj/macho"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// Internal calling convention (all navi functions, closures and shims):
//
//	x0   context: the dictionary of the instance, or for closures the
//	     environment (whose word at -8 holds the dictionary)
//	x8   address of the result slot
//	x9   caller location (a *Location) for #[track_caller]
//	args on the caller's stack at sp+off, each rounded up to 8 bytes;
//	     the callee sees them at x29+16+off
//
// No register survives a call. Every MIR local lives in the frame:
//
//	x29+16+off  incoming arguments
//	x29-8       context (dictionary)
//	x29-16      result slot address
//	x29-24      caller location
//	x29-32      head of the defer list
//	x29-...     locals, the call result buffer, the aggregate buffer
//	sp+0        outgoing arguments
const (
	slotCtx    = -8
	slotRet    = -16
	slotLoc    = -24
	slotDefers = -32
	fixedFrame = 40
)

// Options control code generation.
type Options struct {
	// Debug enables integer overflow checks.
	Debug bool
}

type gen struct {
	p       *codegen.Plan
	o       *macho.Object
	opts    Options
	lay     *layout.Computer
	locs    map[string]string
	locList []locData
	externs map[string]bool
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

// Generate produces a Mach-O object with the code and data of the plan.
func Generate(p *codegen.Plan, opts Options) (*macho.Object, error) {
	if len(p.Errors) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(p.Errors, "\n"))
	}
	g := &gen{p: p, o: macho.New("arm64"), opts: opts, lay: p.Lay, locs: map[string]string{},
		externs: map[string]bool{}, closure: map[closureKey]string{}, statics: map[*resolve.Def]string{}}
	for i, d := range p.Statics {
		g.statics[d] = fmt.Sprintf("_navi_static%d_%s", i, d.Name)
	}
	for _, grp := range p.Groups {
		g.genGroup(grp)
	}
	g.genMain()
	g.genRunDefers()
	g.genData()
	if len(g.errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(g.errs, "\n"))
	}
	for name := range g.externs {
		g.o.Undefined(name)
	}
	return g.o, nil
}

func (g *gen) errorf(format string, args ...any) {
	g.errs = append(g.errs, fmt.Sprintf(format, args...))
}

// emitFunc places assembled code in the text section.
func (g *gen) emitFunc(sym string, a *Asm, global bool) {
	if err := a.Finish(); err != nil {
		g.errorf("%s: %v", sym, err)
		return
	}
	off := g.o.Define(sym, macho.Text, 4, global)
	g.o.Sects[macho.Text] = append(g.o.Sects[macho.Text], a.Bytes()...)
	for _, r := range a.Relocs {
		g.o.Reloc(macho.Text, uint32(off)+uint32(r.Off), r.Kind, r.Sym)
	}
}

// extern returns the symbol of a C function, declaring it undefined.
func (g *gen) extern(name string) string {
	sym := "_" + name
	g.externs[sym] = true
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
		off := o.Define(l.sym, macho.DataConst, 8, false)
		buf := make([]byte, ll.Size)
		putU64(buf[ll.Fields[0]+8:], uint64(len(l.file)))
		putU32(buf[ll.Fields[1]:], uint32(l.line))
		putU32(buf[ll.Fields[2]:], uint32(l.col))
		o.Sects[macho.DataConst] = append(o.Sects[macho.DataConst], buf...)
		o.Reloc(macho.DataConst, uint32(off+uint64(ll.Fields[0])), macho.Abs64, g.p.Str(l.file))
	}
	// Dictionaries: [code, ctx] per slot (vtables: [vtable, 0]).
	for _, grp := range g.p.Groups {
		for _, in := range grp.Insts {
			off := o.Define(in.Dict, macho.DataConst, 8, false)
			n := grp.NSlots
			if n == 0 {
				o.Sects[macho.DataConst] = append(o.Sects[macho.DataConst], make([]byte, 8)...)
				continue
			}
			o.Sects[macho.DataConst] = append(o.Sects[macho.DataConst], make([]byte, 16*n)...)
			for i, e := range g.p.DictEntries(in) {
				at := uint32(off) + uint32(16*i)
				switch e := e.(type) {
				case *codegen.Target:
					g.targetWords(at, e)
				case *codegen.Vtable:
					o.Reloc(macho.DataConst, at, macho.Abs64, e.Sym)
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
		off := o.Define(v.Sym, macho.DataConst, 8, false)
		o.Sects[macho.DataConst] = append(o.Sects[macho.DataConst], make([]byte, 16*len(v.Methods)+8)...)
		for i, t := range v.Methods {
			g.targetWords(uint32(off)+uint32(16*i), t)
		}
	}
	// Statics (zero-initialized; _main runs the initializers).
	for _, d := range g.p.Statics {
		ty := g.p.Info.Consts[d].Type
		size := int64(8)
		if l := g.lay.Of(ty); l.Size > 0 {
			size = l.Size
		}
		o.Define(g.statics[d], macho.Data, 8, false)
		o.Sects[macho.Data] = append(o.Sects[macho.Data], make([]byte, size)...)
	}
	// Strings last: code generation adds file names.
	type str struct{ sym, s string }
	var strs []str
	for s, sym := range g.p.Strings {
		strs = append(strs, str{sym, s})
	}
	sort.Slice(strs, func(i, j int) bool { return strs[i].sym < strs[j].sym })
	for _, s := range strs {
		o.Define(s.sym, macho.RoData, 1, false)
		o.Sects[macho.RoData] = append(o.Sects[macho.RoData], s.s...)
		o.Sects[macho.RoData] = append(o.Sects[macho.RoData], 0)
	}
}

func (g *gen) targetWords(at uint32, t *codegen.Target) {
	switch t.Kind {
	case codegen.TInst:
		g.o.Reloc(macho.DataConst, at, macho.Abs64, t.Inst.Group.Sym)
		g.o.Reloc(macho.DataConst, at+8, macho.Abs64, t.Inst.Dict)
	case codegen.TIntrinsic:
		if t.Intrinsic == "extern" {
			g.o.Reloc(macho.DataConst, at, macho.Abs64, g.extern(linkName(t.Def)))
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

// genMain writes the C entry point: initialize statics, call main.
func (g *gen) genMain() {
	a := &Asm{}
	a.StpPre(FP, LR, SP, -16)
	a.MovSP(FP, SP)
	main := g.p.Main
	retSize := int64(16)
	if s := g.sizeOf(main.Concrete(g.p, main.Func.Locals[0].Type)); s > retSize {
		retSize = s
	}
	frame := (retSize + 15) &^ 15
	a.MovImm(X16, frame)
	a.SubExt(SP, SP, X16)
	mainLoc := g.locAt("<main>", 0, 0)
	for i := len(g.p.Statics) - 1; i >= 0; i-- {
		d := g.p.Statics[i]
		in := g.p.StaticInit[d]
		if in == nil {
			continue
		}
		a.AdrSym(X8, g.statics[d])
		a.AdrSym(X9, mainLoc)
		a.AdrSym(X0, in.Dict)
		a.Bl(in.Group.Sym)
	}
	a.MovSP(X8, SP)
	a.AdrSym(X9, mainLoc)
	a.AdrSym(X0, main.Dict)
	a.Bl(main.Group.Sym)
	a.MovImm(X0, 0)
	a.MovSP(SP, FP)
	a.LdpPost(FP, LR, SP, 16)
	a.Ret()
	g.emitFunc("_main", a, true)
}

// genRunDefers writes _navi_rundefers(x0 = list head): calls each
// deferred record [next, code, ctx, argsize, retsize, loc, args...].
func (g *gen) genRunDefers() {
	a := &Asm{}
	a.StpPre(FP, LR, SP, -16)
	a.MovSP(FP, SP)
	a.StpPre(19, 20, SP, -16)
	a.Mov(19, X0)
	loop, done, copyLoop, copied := a.NewLabel(), a.NewLabel(), a.NewLabel(), a.NewLabel()
	a.Bind(loop)
	a.Cbz(19, done)
	a.Ldr(8, X10, 19, 24)
	a.Ldr(8, X11, 19, 32)
	a.Add(X12, X10, X11)
	a.AddImm(X12, X12, 15)
	a.LsrImm(X12, X12, 4)
	a.LslImm(X12, X12, 4)
	a.SubExt(SP, SP, X12)
	a.MovSP(X17, SP)
	a.AddImm(X16, 19, 48)
	a.LsrImm(X13, X10, 3)
	a.Cbz(X13, copied)
	a.Bind(copyLoop)
	a.LdrPost(X14, X16, 8)
	a.StrPost(X14, X17, 8)
	a.SubsImm(X13, X13, 1)
	a.BCond(NE, copyLoop)
	a.Bind(copied)
	a.AddExt(X8, SP, X10)
	a.Ldr(8, X9, 19, 40)
	a.Ldr(8, X0, 19, 16)
	a.Ldr(8, X16, 19, 8)
	a.Ldr(8, 20, 19, 0)
	a.Blr(X16)
	a.SubImm(SP, FP, 16)
	a.Mov(19, 20)
	a.B(loop)
	a.Bind(done)
	a.LdpPost(19, 20, SP, 16)
	a.LdpPost(FP, LR, SP, 16)
	a.Ret()
	g.emitFunc("_navi_rundefers", a, false)
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
	a      *Asm
	grp    *codegen.Group
	inst   *codegen.Inst
	f      *mir.Func
	ltypes []*types.Type
	loff   []int64 // x29-relative offsets of locals
	// heap marks locals whose address is taken: they live in a heap cell
	// whose pointer is in the frame slot (M3 has no escape analysis).
	heap   []bool
	blocks []Label
	resOff int64 // call result buffer
	tmpOff int64 // aggregate buffer
	stubs  map[string]Label
	stubFn []func()
}

func (g *gen) genFunc(grp *codegen.Group, f *mir.Func, sym string) {
	fg := &fnGen{g: g, a: &Asm{}, grp: grp, inst: grp.Rep, f: f, stubs: map[string]Label{}}
	fg.gen()
	g.emitFunc(sym, fg.a, false)
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
// environment in x0).
func (f *fnGen) firstArg() int {
	if f.f.Closure {
		return 2
	}
	return 1
}

func (f *fnGen) gen() {
	a := f.a
	fn := f.f
	f.ltypes = make([]*types.Type, len(fn.Locals))
	f.loff = make([]int64, len(fn.Locals))
	f.heap = make([]bool, len(fn.Locals))
	for i, l := range fn.Locals {
		f.ltypes[i] = f.conc(l.Type)
	}
	for _, b := range fn.Blocks {
		for _, st := range b.Stmts {
			if as, ok := st.(mir.Assign); ok {
				if r, ok := as.Rv.(mir.Ref); ok && (len(r.Place.Proj) == 0 || r.Place.Proj[0].Kind != mir.PDeref) {
					f.heap[r.Place.Local] = true
				}
			}
		}
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
		if f.heap[i] {
			size, align = 8, 8
		}
		f.loff[i] = alloc(size, align)
	}
	// Heap cells of address-taken arguments get a frame slot too.
	argCell := map[int]int64{}
	for i := f.firstArg(); i <= fn.NArgs; i++ {
		if f.heap[i] {
			argCell[i] = alloc(8, 8)
		}
	}
	// Buffers and the outgoing argument area.
	maxRes, maxTmp, maxOut := int64(16), int64(16), int64(48)
	var scan func(fn *mir.Func)
	scan = func(fn *mir.Func) {
		for _, b := range fn.Blocks {
			for _, s := range b.Stmts {
				as, ok := s.(mir.Assign)
				if !ok {
					continue
				}
				// Aggregates and large copies are staged in the buffer.
				if s := f.size(f.placeType(as.Place)); s > maxTmp {
					maxTmp = s
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
	}
	scan(fn)
	f.resOff = alloc(maxRes, 8)
	f.tmpOff = alloc(maxTmp, 8)
	frame := (cur+15)&^15 + (maxOut+15)&^15

	// Prologue.
	a.StpPre(FP, LR, SP, -16)
	a.MovSP(FP, SP)
	if frame < 4096 {
		a.SubImm(SP, SP, frame)
	} else {
		a.MovImm(X16, frame)
		a.SubExt(SP, SP, X16)
	}
	if fn.Closure {
		a.Str(8, X0, FP, f.loff[1])
		a.Ldr(8, X16, X0, -8)
		a.Str(8, X16, FP, slotCtx)
	} else {
		a.Str(8, X0, FP, slotCtx)
	}
	a.Str(8, X8, FP, slotRet)
	a.Str(8, X9, FP, slotLoc)
	if fn.HasDefer {
		a.Str(8, ZR, FP, slotDefers)
	}
	for i := range fn.Locals {
		if !f.heap[i] {
			continue
		}
		size := f.size(f.ltypes[i])
		f.callAlloc(size)
		a.Ldr(8, X15, FP, f.resOff)
		if off, ok := argCell[i]; ok {
			a.AddrOf(X10, FP, f.loff[i])
			f.copyMem(X15, 0, X10, 0, size)
			f.loff[i] = off
		}
		a.Str(8, X15, FP, f.loff[i])
	}

	f.blocks = make([]Label, len(fn.Blocks))
	for i := range f.blocks {
		f.blocks[i] = a.NewLabel()
	}
	for bi, b := range fn.Blocks {
		a.Bind(f.blocks[bi])
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

func (f *fnGen) localAddr(r Reg, l int) {
	if f.heap[l] {
		f.a.Ldr(8, r, FP, f.loff[l])
		return
	}
	f.a.AddrOf(r, FP, f.loff[l])
}

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

// place computes the address of a place into X10 (and the metadata of an
// unsized place into X11). Clobbers X16, X17.
func (f *fnGen) place(pl mir.Place) *types.Type {
	a := f.a
	f.localAddr(X10, pl.Local)
	ty := f.ltypes[pl.Local]
	variant := -1
	for _, pr := range pl.Proj {
		switch pr.Kind {
		case mir.PDeref:
			elem := f.reveal(ty.Elem)
			if layout.IsFat(elem) {
				a.Ldr(8, X11, X10, 8)
			}
			a.Ldr(8, X10, X10, 0)
			ty, variant = elem, -1
		case mir.PDowncast:
			variant = pr.Variant
		case mir.PField:
			var off int64
			off, ty = f.fieldOf(ty, variant, pr.Field)
			variant = -1
			f.addConst(X10, off)
		case mir.PIndex:
			elem := f.reveal(ty.Elem)
			f.localAddr(X16, pr.Index)
			a.Ldr(8, X16, X16, 0)
			a.MovImm(X17, f.size(elem))
			a.Madd(X10, X16, X17, X10)
			ty = elem
		}
	}
	return ty
}

// addConst adds a constant to r (clobbers X16 for large values).
func (f *fnGen) addConst(r Reg, off int64) {
	switch {
	case off == 0:
	case off > 0 && off < 4096:
		f.a.AddImm(r, r, off)
	default:
		f.a.MovImm(X16, off)
		f.a.Add(r, r, X16)
	}
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
// (fat values: their first word). Clobbers X10, X11, X16, X17.
func (f *fnGen) loadScalar(op mir.Operand, r Reg) *types.Type {
	a := f.a
	if op.Kind == mir.OpCopy {
		ty := f.place(op.Place)
		size := f.size(ty)
		if size > 8 {
			size = 8
		}
		switch size {
		case 0:
			a.MovImm(r, 0)
		case 1, 2, 4, 8:
			a.Ldr(size, r, X10, 0)
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
		a.MovImm(r, v)
	case mir.CFloat:
		if ty == nil || ty.F64 {
			a.MovImm(r, int64(math.Float64bits(c.Float)))
		} else {
			a.MovImm(r, int64(math.Float32bits(float32(c.Float))))
		}
	case mir.CStatic:
		a.AdrSym(r, f.g.statics[c.Static])
	case mir.CStr, mir.CBytes:
		a.AdrSym(r, f.g.p.Str(c.Str))
	case mir.CFn:
		f.fnValue(c, r, X14)
	default:
		a.MovImm(r, 0)
	}
	return ty
}

// loadVal loads a value of 1, 2, 4, 8 or 16 bytes into X12 (and X13).
func (f *fnGen) loadVal(op mir.Operand, size int64) {
	a := f.a
	if size <= 8 {
		f.loadScalar(op, X12)
		return
	}
	if op.Kind == mir.OpCopy {
		f.place(op.Place)
		a.Ldr(8, X12, X10, 0)
		a.Ldr(8, X13, X10, 8)
		return
	}
	c := op.Const
	switch c.Kind {
	case mir.CStr, mir.CBytes:
		a.AdrSym(X12, f.g.p.Str(c.Str))
		a.MovImm(X13, int64(len(c.Str)))
	case mir.CFn:
		f.fnValue(c, X12, X13)
	default:
		a.MovImm(X12, 0)
		a.MovImm(X13, 0)
	}
}

// fnValue materializes a function constant as (code, ctx).
func (f *fnGen) fnValue(c *mir.Const, code, ctx Reg) {
	a := f.a
	t, slot := f.g.p.SiteTarget(f.inst, c)
	if t == nil {
		f.g.errorf("%s: unresolved function constant", f.f.Name)
		return
	}
	if slot >= 0 {
		a.Ldr(8, X16, FP, slotCtx)
		a.Ldr(8, code, X16, int64(16*slot))
		a.Ldr(8, ctx, X16, int64(16*slot+8))
		return
	}
	switch t.Kind {
	case codegen.TInst:
		a.AdrSym(code, t.Inst.Group.Sym)
		a.AdrSym(ctx, t.Inst.Dict)
	case codegen.TIntrinsic:
		if t.Intrinsic == "extern" {
			a.AdrSym(code, f.g.extern(linkName(t.Def)))
			a.MovImm(ctx, 0)
			return
		}
		f.g.errorf("%s: intrinsic %s used as a value", f.f.Name, t.Intrinsic)
	default:
		f.g.errorf("%s: dynamic method %s used as a value", f.f.Name, t.Def.Name)
	}
}

// storeOperand writes an operand to [dst + off]; dst must be X15, FP or
// SP. Clobbers X10-X14, X16, X17.
func (f *fnGen) storeOperand(op mir.Operand, dst Reg, off int64) {
	a := f.a
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
		f.copyMem(dst, off, X10, 0, size)
		return
	}
	switch op.Const.Kind {
	case mir.CZero, mir.CUnit:
		f.zeroMem(dst, off, size)
		return
	}
	switch size {
	case 1, 2, 4, 8:
		f.loadScalar(op, X12)
		a.Str(size, X12, dst, off)
	case 16:
		f.loadVal(op, 16)
		a.Str(8, X12, dst, off)
		a.Str(8, X13, dst, off+8)
	default:
		f.g.errorf("%s: constant of %d bytes", f.f.Name, size)
	}
}

// copyMem copies size bytes; clobbers X13, X14, X16, X17.
func (f *fnGen) copyMem(dst Reg, dOff int64, src Reg, sOff int64, size int64) {
	a := f.a
	if size > 256 {
		a.AddrOf(X16, src, sOff)
		a.AddrOf(X17, dst, dOff)
		a.MovImm(X13, size/8)
		loop := a.NewLabel()
		a.Bind(loop)
		a.LdrPost(X14, X16, 8)
		a.StrPost(X14, X17, 8)
		a.SubsImm(X13, X13, 1)
		a.BCond(NE, loop)
		rest := size % 8
		f.copySmall(X17, 0, X16, 0, rest)
		return
	}
	f.copySmall(dst, dOff, src, sOff, size)
}

func (f *fnGen) copySmall(dst Reg, dOff int64, src Reg, sOff int64, size int64) {
	a := f.a
	pos := int64(0)
	for _, chunk := range []int64{8, 4, 2, 1} {
		for size-pos >= chunk {
			a.Ldr(chunk, X14, src, sOff+pos)
			a.Str(chunk, X14, dst, dOff+pos)
			pos += chunk
		}
	}
}

func (f *fnGen) zeroMem(dst Reg, off, size int64) {
	pos := int64(0)
	for _, chunk := range []int64{8, 4, 2, 1} {
		for size-pos >= chunk {
			f.a.Str(chunk, ZR, dst, off+pos)
			pos += chunk
		}
	}
}

// storeReg stores X12 (and X13) into the place; sizes 1, 2, 4, 8, 16.
func (f *fnGen) storeRegs(pl mir.Place, size int64) {
	a := f.a
	f.place(pl)
	switch size {
	case 0:
	case 1, 2, 4, 8:
		a.Str(size, X12, X10, 0)
	case 16:
		a.Str(8, X12, X10, 0)
		a.Str(8, X13, X10, 8)
	default:
		f.g.errorf("%s: register store of %d bytes", f.f.Name, size)
	}
}

// --- Statements ------------------------------------------------------------

func (f *fnGen) stmt(bi, si int, s mir.Stmt) {
	a := f.a
	switch s := s.(type) {
	case mir.Assign:
		f.assign(bi, si, s)
	case mir.SetDiscr:
		ty := f.place(s.Place)
		f.writeTag(X10, ty, s.Variant)
	case mir.Box:
		ct := f.conc(s.Type)
		l := f.g.lay.Of(ct)
		f.callAlloc(l.Size)
		a.Ldr(8, X12, FP, f.resOff)
		f.localAddr(X10, s.Local)
		a.Str(8, X12, X10, 0)
	case mir.PushDefer:
		f.pushDefer(s)
	}
}

// callAlloc calls std::rt::native::alloc(size, 8); the pointer is left
// in the result buffer.
func (f *fnGen) callAlloc(size int64) {
	a := f.a
	a.MovImm(X12, size)
	a.Str(8, X12, SP, 0)
	a.MovImm(X12, 8)
	a.Str(8, X12, SP, 8)
	f.callRuntime("alloc")
}

func (f *fnGen) callRuntime(name string) {
	a := f.a
	in := f.g.p.Runtime[name]
	a.AddrOf(X8, FP, f.resOff)
	a.AdrSym(X9, f.g.locAt("<runtime>", 0, 0))
	a.AdrSym(X0, in.Dict)
	a.Bl(in.Group.Sym)
}

func (f *fnGen) writeTag(base Reg, ty *types.Type, variant int) {
	a := f.a
	l := f.g.lay.Of(ty)
	switch l.Tag {
	case layout.TagInt:
		a.MovImm(X12, f.g.p.Info.Adts[ty.Def].Variants[variant].Discr)
		a.Str(l.TagSize, X12, base, l.TagOff)
	case layout.TagNiche:
		if variant == l.NicheVariant {
			a.Str(8, ZR, base, 0)
		}
	}
}

// readVariant leaves the variant index of the enum at X10 in X12.
func (f *fnGen) readVariant(ty *types.Type) {
	a := f.a
	l := f.g.lay.Of(ty)
	switch l.Tag {
	case layout.TagInt:
		a.Ldr(l.TagSize, X12, X10, l.TagOff)
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
		a.MovImm(X13, 0)
		for i, v := range vs {
			a.MovImm(X14, int64(uint64(v.Discr)&mask))
			a.Cmp(X12, X14)
			a.MovImm(X16, int64(i))
			a.Csel(X13, X16, X13, EQ)
		}
		a.Mov(X12, X13)
	case layout.TagNiche:
		a.Ldr(8, X12, X10, 0)
		a.CmpImm(X12, 0)
		if l.NicheVariant == 0 {
			a.Cset(X12, NE)
		} else {
			a.Cset(X12, EQ)
		}
	default:
		a.MovImm(X12, 0)
	}
}

func (f *fnGen) assign(bi, si int, s mir.Assign) {
	a := f.a
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
		a.AddrOf(X15, FP, f.tmpOff)
		f.storeOperand(r.X, X15, 0)
		f.place(s.Place)
		f.copyMem(X10, 0, X15, 0, size)
	case mir.Ref:
		pt := f.place(r.Place)
		a.Mov(X12, X10)
		if layout.IsFat(pt) {
			a.Mov(X13, X11)
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
			a.MovImm(X12, pt.Len)
		} else {
			f.place(r.Place)
			a.Mov(X12, X11)
		}
		f.storeRegs(s.Place, size)
	case mir.Unsize:
		f.loadScalar(r.X, X12)
		a.MovImm(X13, r.Len)
		f.storeRegs(s.Place, size)
	case mir.MakeDyn:
		f.loadScalar(r.X, X12)
		vt, slot := f.g.p.SiteVtable(f.inst, codegen.DynSite{F: f.f, B: bi, S: si})
		if slot >= 0 {
			a.Ldr(8, X16, FP, slotCtx)
			a.Ldr(8, X13, X16, int64(16*slot))
		} else if vt != nil {
			a.AdrSym(X13, vt.Sym)
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
		a.AddrOf(X15, FP, f.tmpOff)
		ty := dt
		if r.Type != nil {
			ty = f.conc(r.Type)
		}
		l := f.g.lay.Of(ty)
		switch r.Kind {
		case mir.AggTuple:
			for i, op := range r.Ops {
				f.storeOperand(op, X15, l.Fields[i])
			}
		case mir.AggArray:
			if len(r.Ops) > 0 {
				stride := l.Size / int64(len(r.Ops))
				for i, op := range r.Ops {
					f.storeOperand(op, X15, int64(i)*stride)
				}
			}
		case mir.AggAdt:
			if !f.g.p.Info.Adts[ty.Def].IsEnum {
				for i, op := range r.Ops {
					f.storeOperand(op, X15, l.Fields[i])
				}
				break
			}
			for i, op := range r.Ops {
				f.storeOperand(op, X15, l.Variants[r.Variant][i])
			}
			f.writeTag(X15, ty, r.Variant)
		}
		f.place(s.Place)
		f.copyMem(X10, 0, X15, 0, size)
	default:
		f.g.errorf("%s: unsupported rvalue %T", f.f.Name, s.Rv)
	}
}

// closure allocates the environment [dict, captures...] and leaves the
// closure value (code, env+8) in X12, X13.
func (f *fnGen) closure(r mir.Aggregate) {
	a := f.a
	f.callAlloc(int64(8 + 8*len(r.Ops)))
	a.Ldr(8, X15, FP, f.resOff)
	a.Ldr(8, X14, FP, slotCtx)
	a.Str(8, X14, X15, 0)
	for i, op := range r.Ops {
		f.storeOperand(op, X15, int64(8+8*i))
	}
	a.AdrSym(X12, f.g.closureSym(f.grp, r.Closure))
	a.AddImm(X13, X15, 8)
}

// --- Arithmetic ------------------------------------------------------------

func intMin(size int64) int64 { return -1 << (size*8 - 1) }

func (f *fnGen) panicStub(msg string, pos syntax.Pos) Label {
	key := fmt.Sprintf("%s@%d", msg, pos)
	if l, ok := f.stubs[key]; ok {
		return l
	}
	a := f.a
	l := a.NewLabel()
	f.stubs[key] = l
	f.stubFn = append(f.stubFn, func() {
		a.Bind(l)
		f.callPanic(msg, pos)
	})
	return l
}

// callPanic calls panic_raw(msg, file, line, col). Compiler-generated
// impls report their caller's location.
func (f *fnGen) callPanic(msg string, pos syntax.Pos) {
	a := f.a
	a.AdrSym(X12, f.g.p.Str(msg))
	a.Str(8, X12, SP, 0)
	a.MovImm(X12, int64(len(msg)))
	a.Str(8, X12, SP, 8)
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
	a.Brk(1)
}

// storeCallerLoc writes the received caller location as (file, line,
// col) arguments at sp+off.
func (f *fnGen) storeCallerLoc(off int64) {
	a := f.a
	ll := f.g.lay.Of(f.g.p.Info.Ctx.Adt(f.g.p.Info.Lang["Location"], nil))
	a.Ldr(8, X15, FP, slotLoc)
	a.Ldr(8, X12, X15, ll.Fields[0])
	a.Str(8, X12, SP, off)
	a.Ldr(8, X12, X15, ll.Fields[0]+8)
	a.Str(8, X12, SP, off+8)
	a.Ldr(4, X12, X15, ll.Fields[1])
	a.Str(4, X12, SP, off+16)
	a.Ldr(4, X12, X15, ll.Fields[2])
	a.Str(4, X12, SP, off+24)
}

func (f *fnGen) storeFileLine(off int64, file string, line, col int) {
	a := f.a
	a.AdrSym(X12, f.g.p.Str(file))
	a.Str(8, X12, SP, off)
	a.MovImm(X12, int64(len(file)))
	a.Str(8, X12, SP, off+8)
	a.MovImm(X12, int64(line))
	a.Str(4, X12, SP, off+16)
	a.MovImm(X12, int64(col))
	a.Str(4, X12, SP, off+24)
}

// noPos is the position used for arithmetic panics: MIR statements carry
// no positions yet.
var noPos syntax.Pos

// binop leaves the result in X12.
func (f *fnGen) binop(r mir.Bin) {
	a := f.a
	ty := f.conc(r.Type)
	f.loadScalar(r.X, X12)
	yt := f.loadScalar(r.Y, X13)
	if ty.Kind == types.KFloat {
		f64 := ty.F64
		a.FmovToF(0, X12, f64)
		a.FmovToF(1, X13, f64)
		switch r.Op {
		case mir.Add:
			a.Fadd(0, 0, 1, f64)
		case mir.Sub:
			a.Fsub(0, 0, 1, f64)
		case mir.Mul:
			a.Fmul(0, 0, 1, f64)
		case mir.Div:
			a.Fdiv(0, 0, 1, f64)
		case mir.Rem:
			if f64 {
				a.Bl(f.g.extern("fmod"))
			} else {
				a.Bl(f.g.extern("fmodf"))
			}
		default:
			a.Fcmp(0, 1, f64)
			a.Cset(X12, map[mir.BinOp]Cond{mir.Eq: EQ, mir.Ne: NE, mir.Lt: MI, mir.Le: LS, mir.Gt: GT, mir.Ge: GE}[r.Op])
			return
		}
		a.FmovFromF(X12, 0, f64)
		return
	}
	size := f.scalarSize(ty)
	signed := isSigned(ty)
	if signed {
		a.SignExt(X12, X12, size)
		if r.Op != mir.Shl && r.Op != mir.Shr {
			a.SignExt(X13, X13, size)
		}
	}
	dbg := f.g.opts.Debug
	pos := r.Pos
	ovf := func(what string) Label { return f.panicStub("attempt to "+what+" with overflow", pos) }
	switch r.Op {
	case mir.Add, mir.Sub:
		what := "add"
		if r.Op == mir.Sub {
			what = "subtract"
		}
		if size == 8 && dbg {
			if r.Op == mir.Add {
				a.Adds(X12, X12, X13)
			} else {
				a.Subs(X12, X12, X13)
			}
			switch {
			case signed:
				a.BCond(VS, ovf(what))
			case r.Op == mir.Add:
				a.BCond(HS, ovf(what))
			default:
				a.BCond(LO, ovf(what))
			}
			return
		}
		if r.Op == mir.Add {
			a.Add(X12, X12, X13)
		} else {
			a.Sub(X12, X12, X13)
		}
		if dbg {
			f.checkRange(size, signed, ovf(what))
		}
	case mir.Mul:
		if size == 8 && dbg {
			a.Mul(X14, X12, X13)
			if signed {
				a.Smulh(X15, X12, X13)
				a.CmpAsr63(X15, X14)
				a.BCond(NE, ovf("multiply"))
			} else {
				a.Umulh(X15, X12, X13)
				a.Cbnz(X15, ovf("multiply"))
			}
			a.Mov(X12, X14)
			return
		}
		a.Mul(X12, X12, X13)
		if dbg {
			f.checkRange(size, signed, ovf("multiply"))
		}
	case mir.Div, mir.Rem:
		a.Cbz(X13, f.panicStub("attempt to divide by zero", pos))
		if signed {
			skip := a.NewLabel()
			a.CmnImm(X13, 1)
			a.BCond(NE, skip)
			a.MovImm(X14, intMin(size))
			a.Cmp(X12, X14)
			a.BCond(EQ, f.panicStub("attempt to divide with overflow", pos))
			a.Bind(skip)
			a.Sdiv(X14, X12, X13)
		} else {
			a.Udiv(X14, X12, X13)
		}
		if r.Op == mir.Div {
			a.Mov(X12, X14)
		} else {
			a.Msub(X12, X14, X13, X12)
		}
	case mir.Shl, mir.Shr:
		_ = yt
		if dbg || true {
			a.CmpImm(X13, size*8)
			a.BCond(HS, f.panicStub("attempt to shift with overflow", pos))
		}
		switch {
		case r.Op == mir.Shl:
			a.Lslv(X12, X12, X13)
		case signed:
			a.Asrv(X12, X12, X13)
		default:
			a.Lsrv(X12, X12, X13)
		}
	case mir.BitAnd:
		a.And(X12, X12, X13)
	case mir.BitOr:
		a.Orr(X12, X12, X13)
	case mir.BitXor:
		a.Eor(X12, X12, X13)
	case mir.Offset:
		a.MovImm(X14, f.size(f.reveal(ty.Elem)))
		a.Madd(X12, X13, X14, X12)
	default:
		a.Cmp(X12, X13)
		var c Cond
		switch r.Op {
		case mir.Eq:
			c = EQ
		case mir.Ne:
			c = NE
		case mir.Lt:
			c = LO
			if signed {
				c = LT
			}
		case mir.Le:
			c = LS
			if signed {
				c = LE
			}
		case mir.Gt:
			c = HI
			if signed {
				c = GT
			}
		case mir.Ge:
			c = HS
			if signed {
				c = GE
			}
		}
		a.Cset(X12, c)
	}
}

// checkRange branches to l unless X12 fits in size bytes.
func (f *fnGen) checkRange(size int64, signed bool, l Label) {
	if size >= 8 {
		return
	}
	a := f.a
	if signed {
		a.SignExt(X14, X12, size)
		a.Cmp(X14, X12)
		a.BCond(NE, l)
	} else {
		a.LsrImm(X14, X12, uint32(size*8))
		a.Cbnz(X14, l)
	}
}

func (f *fnGen) unop(r mir.Un) {
	a := f.a
	ty := f.conc(r.Type)
	f.loadScalar(r.X, X12)
	switch {
	case ty.Kind == types.KFloat:
		a.FmovToF(0, X12, ty.F64)
		a.Fneg(0, 0, ty.F64)
		a.FmovFromF(X12, 0, ty.F64)
	case ty.Kind == types.KBool:
		a.MovImm(X13, 1)
		a.Eor(X12, X12, X13)
	case r.Op == mir.Not:
		a.Mvn(X12, X12)
	default:
		size := f.scalarSize(ty)
		if isSigned(ty) && f.g.opts.Debug {
			a.SignExt(X12, X12, size)
			a.MovImm(X14, intMin(size))
			a.Cmp(X12, X14)
			a.BCond(EQ, f.panicStub("attempt to negate with overflow", r.Pos))
		}
		a.Sub(X12, ZR, X12)
	}
}

func (f *fnGen) cast(r mir.Cast, to *types.Type) {
	a := f.a
	switch r.Kind {
	case mir.CastPtr:
		f.loadVal(r.X, f.size(to))
		return
	case mir.CastPtrInt, mir.CastFnPtr, mir.CastIntPtr:
		f.loadScalar(r.X, X12)
		return
	case mir.CastEnum:
		from := f.operandType(r.X)
		if r.X.Kind != mir.OpCopy {
			f.loadScalar(r.X, X12)
			return
		}
		f.place(r.X.Place)
		l := f.g.lay.Of(from)
		vs := f.g.p.Info.Adts[from.Def].Variants
		if l.Tag != layout.TagInt {
			if len(vs) > 0 {
				a.MovImm(X12, vs[0].Discr)
			}
			return
		}
		a.Ldr(l.TagSize, X12, X10, l.TagOff)
		for _, v := range vs {
			if v.Discr < 0 {
				a.SignExt(X12, X12, l.TagSize)
				break
			}
		}
		return
	}
	from := f.operandType(r.X)
	f.loadScalar(r.X, X12)
	switch {
	case from.Kind == types.KFloat && to.Kind == types.KFloat:
		if from.F64 == to.F64 {
			return
		}
		a.FmovToF(0, X12, from.F64)
		if to.F64 {
			a.FcvtDS(0, 0)
		} else {
			a.FcvtSD(0, 0)
		}
		a.FmovFromF(X12, 0, to.F64)
	case from.Kind == types.KFloat:
		a.FmovToF(0, X12, from.F64)
		size := f.scalarSize(to)
		if size == 8 {
			if isSigned(to) {
				a.Fcvtzs(X12, 0, from.F64)
			} else {
				a.Fcvtzu(X12, 0, from.F64)
			}
			return
		}
		a.Fcvtzs(X12, 0, from.F64)
		lo, hi := intMin(size), -intMin(size)-1
		if !isSigned(to) {
			lo, hi = 0, 1<<(8*size)-1
		}
		a.MovImm(X13, lo)
		a.Cmp(X12, X13)
		a.Csel(X12, X13, X12, LT)
		a.MovImm(X13, hi)
		a.Cmp(X12, X13)
		a.Csel(X12, X13, X12, GT)
	case to.Kind == types.KFloat:
		if isSigned(from) {
			a.SignExt(X12, X12, f.scalarSize(from))
			a.Scvtf(0, X12, to.F64)
		} else {
			a.Ucvtf(0, X12, to.F64)
		}
		a.FmovFromF(X12, 0, to.F64)
	default:
		if isSigned(from) {
			a.SignExt(X12, X12, f.scalarSize(from))
		}
	}
}

// --- Terminators -----------------------------------------------------------

func (f *fnGen) term(bi int, t mir.Term) {
	a := f.a
	switch t := t.(type) {
	case mir.Goto:
		if t.Target != bi+1 {
			a.B(f.blocks[t.Target])
		}
	case mir.Switch:
		ty := f.conc(t.Type)
		f.loadScalar(t.X, X12)
		if ty != nil && isSigned(ty) {
			a.SignExt(X12, X12, f.scalarSize(ty))
		}
		for i, v := range t.Vals {
			if v >= 0 && v < 4096 {
				a.CmpImm(X12, v)
			} else {
				a.MovImm(X13, v)
				a.Cmp(X12, X13)
			}
			a.BCond(EQ, f.blocks[t.Targets[i]])
		}
		a.B(f.blocks[t.Else])
	case mir.Return:
		if f.f.HasDefer {
			a.Ldr(8, X0, FP, slotDefers)
			a.Bl("_navi_rundefers")
		}
		if size := f.size(f.ltypes[0]); size > 0 {
			a.Ldr(8, X15, FP, slotRet)
			f.localAddr(X10, 0)
			f.copyMem(X15, 0, X10, 0, size)
		}
		a.MovSP(SP, FP)
		a.LdpPost(FP, LR, SP, 16)
		a.Ret()
	case mir.Unreachable:
		a.Brk(1)
	case mir.Assert:
		f.loadScalar(t.Cond, X12)
		a.Cbnz(X12, f.blocks[t.Target])
		if t.Bounds {
			f.loadScalar(t.Len, X12)
			a.Str(8, X12, SP, 0)
			f.loadScalar(t.Index, X12)
			a.Str(8, X12, SP, 8)
			file, line, col := "<unknown>", 0, 0
			if f.f.File != nil {
				ps := f.f.File.Position(t.Pos)
				file, line, col = ps.File, ps.Line, ps.Col
			}
			f.storeFileLine(16, file, line, col)
			f.callRuntime("panic_bounds")
			a.Brk(1)
			return
		}
		f.callPanic(t.Msg, t.Pos)
	case mir.Call:
		f.call(t)
	default:
		f.g.errorf("%s: block without terminator", f.f.Name)
	}
}

// passLoc sets X9 to the location passed to a callee.
func (f *fnGen) passLoc(pos syntax.Pos) {
	if f.inst.TrackCaller && !f.f.Closure {
		f.a.Ldr(8, X9, FP, slotLoc)
		return
	}
	f.a.AdrSym(X9, f.g.loc(f.f.File, pos))
}

// storeArgs writes the arguments to the outgoing area. If thinRecv, the
// first argument is a fat pointer of which only the data word is passed.
func (f *fnGen) storeArgs(args []mir.Operand, thinRecv bool) {
	off := int64(0)
	for i, arg := range args {
		if i == 0 && thinRecv {
			f.loadScalar(arg, X12)
			f.a.Str(8, X12, SP, 0)
			off = 8
			continue
		}
		f.storeOperand(arg, SP, off)
		off += round8(f.size(f.operandType(arg)))
	}
}

func (f *fnGen) call(t mir.Call) {
	a := f.a
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
			f.loadScalar(t.Fn, X16)
			a.Mov(X15, X16)
			f.cCall(t, dt, func() { a.Blr(X15) })
			return
		}
		f.storeArgs(t.Args, false)
		f.loadVal(t.Fn, 16)
		a.Mov(X16, X12)
		a.Mov(X0, X13)
		f.passLoc(t.Pos)
		a.AddrOf(X8, FP, f.resOff)
		a.Blr(X16)
	case slot >= 0:
		f.storeArgs(t.Args, false)
		a.Ldr(8, X16, FP, slotCtx)
		a.Ldr(8, X0, X16, int64(16*slot+8))
		a.Ldr(8, X16, X16, int64(16*slot))
		f.passLoc(t.Pos)
		a.AddrOf(X8, FP, f.resOff)
		a.Blr(X16)
	case target.Kind == codegen.TVirtual:
		f.storeArgs(t.Args, true)
		f.loadVal(t.Args[0], 16)
		a.Ldr(8, X0, X13, int64(16*target.Method+8))
		a.Ldr(8, X16, X13, int64(16*target.Method))
		f.passLoc(t.Pos)
		a.AddrOf(X8, FP, f.resOff)
		a.Blr(X16)
	default:
		f.storeArgs(t.Args, false)
		f.passLoc(t.Pos)
		a.AddrOf(X8, FP, f.resOff)
		a.AdrSym(X0, target.Inst.Dict)
		a.Bl(target.Inst.Group.Sym)
	}
	f.afterCall(t, dt)
}

func (f *fnGen) afterCall(t mir.Call, dt *types.Type) {
	a := f.a
	if t.Target < 0 || dt.Kind == types.KNever {
		a.Brk(1)
		return
	}
	if size := f.size(dt); size > 0 {
		f.place(t.Dest)
		f.copyMem(X10, 0, FP, f.resOff, size)
	}
	if t.Target != -1 {
		a.B(f.blocks[t.Target])
	}
}

// cCall calls a C function with the AAPCS64 convention.
func (f *fnGen) cCall(t mir.Call, dt *types.Type, emit func()) {
	a := f.a
	ints, floats := 0, 0
	// Stash the code pointer: argument loads clobber X10-X17 but not X15
	// unless an argument is a constant function.
	for _, arg := range t.Args {
		ty := f.operandType(arg)
		switch {
		case ty.Kind == types.KFloat:
			f.loadScalar(arg, X12)
			a.FmovToF(Reg(floats), X12, ty.F64)
			floats++
		case f.size(ty) <= 8:
			if ints >= 8 {
				f.g.errorf("%s: too many arguments for a C call", f.f.Name)
				return
			}
			f.loadScalar(arg, Reg(ints))
			if isSigned(ty) {
				a.SignExt(Reg(ints), Reg(ints), f.scalarSize(ty))
			}
			ints++
		default:
			f.g.errorf("%s: unsupported C argument type %s", f.f.Name, ty)
			return
		}
	}
	emit()
	if t.Target < 0 || dt.Kind == types.KNever {
		a.Brk(1)
		return
	}
	size := f.size(dt)
	if size > 0 {
		if dt.Kind == types.KFloat {
			a.FmovFromF(X12, 0, dt.F64)
		} else {
			a.Mov(X12, X0)
		}
		f.storeRegs(t.Dest, size)
	}
	a.B(f.blocks[t.Target])
}

// intrinsic generates an inline intrinsic or a C call.
func (f *fnGen) intrinsic(target *codegen.Target, t mir.Call, dt *types.Type) {
	a := f.a
	size := f.size(dt)
	targ := func(i int) *types.Type {
		if i < len(target.TypeArgs) {
			return target.TypeArgs[i]
		}
		return nil
	}
	switch target.Intrinsic {
	case "extern":
		sym := f.g.extern(linkName(target.Def))
		f.cCall(t, dt, func() { a.Bl(sym) })
		return
	case "unreachable":
		a.Brk(1)
		return
	case "abort":
		a.Bl(f.g.extern("abort"))
		a.Brk(1)
		return
	case "size_of":
		a.MovImm(X12, f.size(targ(0)))
	case "align_of":
		a.MovImm(X12, f.g.lay.Of(targ(0)).Align)
	case "ptr_offset":
		f.loadScalar(t.Args[1], X13)
		a.Str(8, X13, FP, f.resOff)
		f.loadScalar(t.Args[0], X12)
		a.Ldr(8, X13, FP, f.resOff)
		a.MovImm(X14, f.size(targ(0)))
		a.Madd(X12, X13, X14, X12)
	case "slice_from_raw", "slice_from_raw_mut", "str_from_raw":
		f.loadScalar(t.Args[1], X13)
		a.Str(8, X13, FP, f.resOff)
		f.loadScalar(t.Args[0], X12)
		a.Ldr(8, X13, FP, f.resOff)
	case "slice_len":
		f.loadVal(t.Args[0], 16)
		a.Mov(X12, X13)
	case "slice_ptr":
		f.loadScalar(t.Args[0], X12)
	case "str_as_bytes":
		f.loadVal(t.Args[0], 16)
	case "caller_location":
		if f.inst.TrackCaller && !f.f.Closure {
			a.Ldr(8, X15, FP, slotLoc)
		} else {
			a.AdrSym(X15, f.g.loc(f.f.File, t.Pos))
		}
		f.place(t.Dest)
		f.copyMem(X10, 0, X15, 0, size)
		if t.Target >= 0 {
			a.B(f.blocks[t.Target])
		}
		return
	default:
		f.g.errorf("%s: intrinsic %s has no native implementation", f.f.Name, target.Intrinsic)
		return
	}
	f.storeRegs(t.Dest, size)
	if t.Target >= 0 {
		a.B(f.blocks[t.Target])
	} else {
		a.Brk(1)
	}
}

// --- Defer -----------------------------------------------------------------

// pushDefer allocates a record [next, code, ctx, argsize, retsize, loc,
// args...] and links it into the frame's defer list.
func (f *fnGen) pushDefer(s mir.PushDefer) {
	a := f.a
	virtual := false
	var target *codegen.Target
	slot := -1
	var retTy *types.Type
	if s.Fn.Kind == mir.OpConst && s.Fn.Const.Kind == mir.CFn {
		target, slot = f.g.p.SiteTarget(f.inst, s.Fn.Const)
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
	f.callAlloc(48 + argSize)
	a.Ldr(8, X15, FP, f.resOff)
	// Code and context.
	switch {
	case virtual:
		f.loadVal(s.Args[0], 16)
		a.Str(8, X12, X15, 48)
		a.Ldr(8, X12, X13, int64(16*target.Method))
		a.Ldr(8, X13, X13, int64(16*target.Method+8))
	case target != nil:
		f.fnValue(s.Fn.Const, X12, X13)
		_ = slot
	default:
		f.loadVal(s.Fn, 16)
	}
	a.Str(8, X12, X15, 8)
	a.Str(8, X13, X15, 16)
	a.MovImm(X12, argSize)
	a.Str(8, X12, X15, 24)
	a.MovImm(X12, retSize)
	a.Str(8, X12, X15, 32)
	if f.inst.TrackCaller && !f.f.Closure {
		a.Ldr(8, X12, FP, slotLoc)
	} else {
		a.AdrSym(X12, f.g.loc(f.f.File, noPos))
	}
	a.Str(8, X12, X15, 40)
	off := int64(48)
	for i, arg := range s.Args {
		if i == 0 && virtual {
			off += 8
			continue
		}
		f.storeOperand(arg, X15, off)
		off += round8(f.size(f.operandType(arg)))
	}
	a.Ldr(8, X12, FP, slotDefers)
	a.Str(8, X12, X15, 0)
	a.Str(8, X15, FP, slotDefers)
}
