package interp

import (
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"navi/internal/layout"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

// The runtime half of std (std::rt and std::intrinsics) for the
// interpreter. Threads are goroutines; a global interpreter lock (gil)
// makes sure only one runs navi code at a time, and is released while a
// thread blocks.

type threadHandle struct {
	done   chan struct{}
	result val
}

type channel struct {
	elem       *types.Type
	cap        int // -1: unbounded
	buf        []val
	closed     bool
	sendSeq    int64
	recvSeq    int64
	recvWaiter int
}

type rtState struct {
	chMu    sync.Mutex
	chCond  *sync.Cond
	handles map[*Alloc]any
	killed  atomic.Bool
}

func (m *Machine) rt() *rtState {
	if m.rts == nil {
		m.rts = &rtState{handles: map[*Alloc]any{}}
		m.rts.chCond = sync.NewCond(&m.rts.chMu)
	}
	return m.rts
}

// block runs f with the interpreter lock released.
func (m *Machine) block(f func()) {
	m.gil.Unlock()
	defer m.gil.Lock()
	f()
}

func (t *thread) arg(args []val, i int) val { return args[i] }

// u64arg reads an integer argument of any size (zero-extended).
func u64arg(v val) uint64 {
	if len(v.b) >= 8 {
		return v.u64(0)
	}
	return uint64(readInt(v.b, int64(len(v.b)), false))
}

func (t *thread) intrinsic(inst *fnInst, args []val) val {
	m := t.m
	targ := func(i int) *types.Type {
		if i < len(inst.shimArgs) {
			return inst.shimArgs[i]
		}
		return m.ctx.Err
	}
	switch inst.intrinsic {
	case "size_of":
		return intVal(8, m.sizeOf(targ(0)))
	case "wrapping_add", "wrapping_sub", "wrapping_mul":
		size := m.sizeOf(targ(0))
		a, b := readInt(args[0].b, size, false), readInt(args[1].b, size, false)
		switch inst.intrinsic {
		case "wrapping_add":
			return intVal(size, a+b)
		case "wrapping_sub":
			return intVal(size, a-b)
		}
		return intVal(size, a*b)
	case "monotonic_nanos":
		return intVal(8, time.Since(processStart).Nanoseconds())
	case "is_copy_clone":
		// Conservative: navic's native code copies bitwise when true.
		return intVal(1, 0)
	case "os_arg_count":
		return intVal(8, int64(len(m.Args)))
	case "os_arg":
		return fatVal(Ptr{A: m.strAlloc(m.Args[u64arg(args[0])])}, Ptr{Off: int64(len(m.Args[u64arg(args[0])]))})
	case "os_getenv":
		v, ok := os.LookupEnv(m.readStr(args[0]))
		strTy := m.ctx.Ref(false, m.ctx.Str)
		return m.optionVal(strTy, fatVal(Ptr{A: m.strAlloc(v)}, Ptr{Off: int64(len(v))}), ok)
	case "os_system":
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", m.readStr(args[0]))
		} else {
			cmd = exec.Command("/bin/sh", "-c", m.readStr(args[0]))
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, m.Stdout, m.Stderr
		code := 0
		m.block(func() {
			if err := cmd.Run(); err != nil {
				code = 127
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				}
			}
		})
		return intVal(4, int64(code))
	case "fs_open":
		path := m.readStr(args[0])
		var f *os.File
		var err error
		switch args[1].b[0] {
		case 0:
			f, err = os.Open(path)
		case 1:
			f, err = os.Create(path)
		default:
			f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		}
		if err != nil {
			return intVal(8, 0)
		}
		m.files = append(m.files, f)
		return intVal(8, int64(len(m.files)))
	case "fs_read":
		f := m.files[u64arg(args[0])-1]
		n := int64(u64arg(args[2]))
		buf := make([]byte, n)
		k, _ := io.ReadFull(f, buf)
		if k > 0 {
			m.write(args[1].ptrAt(0), val{b: buf[:k]})
		}
		return intVal(8, int64(k))
	case "fs_write":
		f := m.files[u64arg(args[0])-1]
		n := int64(u64arg(args[2]))
		if n == 0 {
			return intVal(8, 0)
		}
		k, _ := f.Write(m.read(args[1].ptrAt(0), n).b)
		return intVal(8, int64(k))
	case "fs_close":
		if i := u64arg(args[0]); i > 0 && m.files[i-1] != nil {
			m.files[i-1].Close()
			m.files[i-1] = nil
		}
		return newVal(0)
	case "fs_kind":
		st, err := os.Stat(m.readStr(args[0]))
		switch {
		case err != nil:
			return intVal(1, 0)
		case st.IsDir():
			return intVal(1, 2)
		}
		return intVal(1, 1)
	case "fs_mkdir":
		return boolVal(os.Mkdir(m.readStr(args[0]), 0o777) == nil)
	case "fs_remove":
		return boolVal(os.Remove(m.readStr(args[0])) == nil)
	case "float_op":
		x := readFloat(args[1].b, true)
		switch args[0].b[0] {
		case 0:
			x = math.Floor(x)
		case 1:
			x = math.Ceil(x)
		case 2:
			x = math.Trunc(x)
		case 3:
			x = math.Round(x)
		default:
			x = math.Sqrt(x)
		}
		return floatVal(true, x)
	case "align_of":
		return intVal(8, m.layoutOf(targ(0)).Align)
	case "abort":
		panic(exitErr{code: 134})
	case "exit":
		panic(exitErr{code: int(int32(u64arg(args[0])))})
	case "caller_location":
		return t.callerLocation()
	case "alloc":
		a := m.newAlloc(int64(u64arg(args[0])), "heap")
		return ptrVal(Ptr{A: a})
	case "alloc_array":
		n := int64(u64arg(args[0]))
		a := m.newAlloc(n*m.sizeOf(targ(0)), "heap array")
		return ptrVal(Ptr{A: a})
	case "copy":
		src, dst := args[0].ptrAt(0), args[1].ptrAt(0)
		n := int64(u64arg(args[2])) * m.sizeOf(targ(0))
		if n > 0 {
			v := m.read(src, n)
			m.write(dst, v)
		}
		return newVal(0)
	case "ptr_offset":
		return ptrVal(args[0].ptrAt(0).add(int64(u64arg(args[1])) * m.sizeOf(targ(0))))
	case "slice_from_raw", "slice_from_raw_mut", "str_from_raw":
		return fatVal(args[0].ptrAt(0), Ptr{Off: int64(u64arg(args[1]))})
	case "slice_len":
		return intVal(8, args[0].ptrAt(8).Off)
	case "slice_ptr":
		return ptrVal(args[0].ptrAt(0))
	case "str_as_bytes":
		return args[0]
	case "write":
		fd := int32(u64arg(args[0]))
		p, n := args[1].ptrAt(0), int64(u64arg(args[2]))
		data := m.read(p, n).b
		if fd == 2 {
			m.Stderr.Write(data)
		} else {
			m.Stdout.Write(data)
		}
		return newVal(0)
	case "panic_raw":
		msg := m.readStr(args[0])
		file := m.readStr(args[1])
		line := u64arg(args[2]) & 0xffffffff
		col := u64arg(args[3]) & 0xffffffff
		panic(navPanic{msg: file + ":" + strconv.FormatUint(line, 10) + ":" + strconv.FormatUint(col, 10) + ":\n" + msg})
	case "spawn":
		return t.spawn(args[0])
	case "try_call":
		// The interpreter does not catch panics: one ends the process.
		t.callValue(args[0], nil)
		return ptrVal(Ptr{})
	case "join":
		h := m.rt().handles[args[0].ptrAt(0).A].(*threadHandle)
		m.block(func() { <-h.done })
		return h.result
	case "yield_now":
		m.block(func() {})
		return newVal(0)
	case "chan_new":
		c := &channel{elem: targ(0), cap: int(int64(u64arg(args[0])))}
		if u64arg(args[0]) == math.MaxUint64 {
			c.cap = -1
		}
		a := m.newAlloc(0, "channel")
		m.rt().handles[a] = c
		return ptrVal(Ptr{A: a})
	case "chan_send":
		return boolVal(t.chanSend(m.channelOf(args[0]), args[1]))
	case "chan_recv":
		v, ok := t.chanRecv(m.channelOf(args[0]), true)
		return m.optionVal(targ(0), v, ok)
	case "chan_try_recv":
		v, ok := t.chanRecv(m.channelOf(args[0]), false)
		return m.optionVal(targ(0), v, ok)
	case "chan_close":
		rs := m.rt()
		m.block(func() {
			rs.chMu.Lock()
			m.channelOf(args[0]).closed = true
			rs.chCond.Broadcast()
			rs.chMu.Unlock()
		})
		return newVal(0)
	case "chan_closed":
		c := m.channelOf(args[0])
		var closed bool
		m.block(func() {
			m.rt().chMu.Lock()
			closed = c.closed && len(c.buf) == 0
			m.rt().chMu.Unlock()
		})
		return boolVal(closed)
	case "select_cases":
		return intVal(8, t.selectCases(args[0], args[1].b[0] != 0))
	case "fmt_float":
		x := readFloat(args[0].b, true)
		prec := int(u64arg(args[1]))
		hasPrec := args[2].b[0] != 0
		style := args[3].b[0]
		bits := 64
		if style&16 != 0 {
			bits, style = 32, style&^16
		}
		s := formatFloatBits(x, prec, hasPrec, style, bits)
		m.write(args[4].ptrAt(0), val{b: []byte(s)})
		return intVal(8, int64(len(s)))
	}
	m.fatal("unknown intrinsic %s", inst.intrinsic)
	return val{}
}

// formatFloat renders a float like Rust: style 0 Display, 1 Debug, 2 {:e},
// 3 {:E}.
func formatFloat(x float64, prec int, hasPrec bool, style byte) string {
	return formatFloatBits(x, prec, hasPrec, style, 64)
}

func formatFloatBits(x float64, prec int, hasPrec bool, style byte, bits int) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	}
	if style >= 2 {
		p := -1
		if hasPrec {
			p = prec
		}
		s := strconv.FormatFloat(x, 'e', p, bits)
		mant, exp, _ := strings.Cut(s, "e")
		e, _ := strconv.Atoi(exp)
		out := mant + "e" + strconv.Itoa(e)
		if style == 3 {
			out = strings.ToUpper(out)
		}
		return out
	}
	if hasPrec {
		return strconv.FormatFloat(x, 'f', prec, bits)
	}
	s := strconv.FormatFloat(x, 'f', -1, bits)
	if style == 1 && !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

func (m *Machine) readStr(v val) string {
	p, n := v.ptrAt(0), int64(v.u64(8))
	return string(m.read(p, n).b)
}

func (m *Machine) channelOf(v val) *channel {
	c, ok := m.rt().handles[v.ptrAt(0).A].(*channel)
	if !ok {
		m.fatal("invalid channel")
	}
	return c
}

// optionVal builds Option<T>: Some(v) when ok, else None.
func (m *Machine) optionVal(elem *types.Type, v val, ok bool) val {
	opt := m.ctx.Adt(m.info.Lang["Option"], []*types.Type{elem})
	l := m.layoutOf(opt)
	tmp := m.newAlloc(l.Size, "option")
	some, none := 0, 0
	for i, vr := range m.info.Adts[opt.Def].Variants {
		if vr.Def.Name == "Some" {
			some = i
		} else {
			none = i
		}
	}
	if ok {
		m.write(Ptr{A: tmp, Off: l.Variants[some][0]}, v)
		m.writeTag(Ptr{A: tmp}, opt, some)
	} else {
		m.writeTag(Ptr{A: tmp}, opt, none)
	}
	return m.read(Ptr{A: tmp}, l.Size)
}

// callerLocation returns the Location of the call site that entered the
// current chain of #[track_caller] functions.
func (t *thread) callerLocation() val {
	m := t.m
	j := len(t.stack) - 1
	for j > 0 && (t.stack[j].fn.Shim || t.stack[j].fn.Def != nil && hasAttr(t.stack[j].fn.Def, "track_caller")) {
		j--
	}
	fr := t.stack[j]
	pos := syntax.Position{File: "<unknown>"}
	if fr.fn.File != nil {
		pos = fr.fn.File.Position(fr.pos)
	}
	loc := m.ctx.Adt(m.info.Lang["Location"], nil)
	l := m.layoutOf(loc)
	tmp := m.newAlloc(l.Size, "location")
	m.write(Ptr{A: tmp, Off: l.Fields[0]}, fatVal(Ptr{A: m.strAlloc(pos.File)}, Ptr{Off: int64(len(pos.File))}))
	m.write(Ptr{A: tmp, Off: l.Fields[1]}, intVal(4, int64(pos.Line)))
	m.write(Ptr{A: tmp, Off: l.Fields[2]}, intVal(4, int64(pos.Col)))
	return m.read(Ptr{A: tmp}, l.Size)
}

// spawn starts a green thread running the closure f.
func (t *thread) spawn(f val) val {
	m := t.m
	h := &threadHandle{done: make(chan struct{})}
	a := m.newAlloc(0, "thread")
	m.rt().handles[a] = h
	go func() {
		m.gil.Lock()
		defer m.gil.Unlock()
		defer close(h.done)
		code := m.runThread(func(nt *thread) { h.result = nt.callValue(f, nil) }, "<unnamed>")
		if code != 0 {
			// A panic in any thread ends the process.
			m.rt().killed.Store(true)
		}
	}()
	return ptrVal(Ptr{A: a})
}

func (t *thread) chanSend(c *channel, v val) bool {
	m := t.m
	rs := m.rt()
	ok := true
	m.block(func() {
		rs.chMu.Lock()
		defer rs.chMu.Unlock()
		for {
			if c.closed {
				ok = false
				return
			}
			if c.cap < 0 || len(c.buf) < c.cap || c.cap == 0 {
				break
			}
			rs.chCond.Wait()
		}
		c.buf = append(c.buf, v)
		c.sendSeq++
		seq := c.sendSeq
		rs.chCond.Broadcast()
		if c.cap == 0 {
			for c.recvSeq < seq && !c.closed {
				rs.chCond.Wait()
			}
		}
	})
	return ok
}

func (t *thread) chanRecv(c *channel, wait bool) (val, bool) {
	m := t.m
	rs := m.rt()
	var v val
	ok := false
	m.block(func() {
		rs.chMu.Lock()
		defer rs.chMu.Unlock()
		c.recvWaiter++
		rs.chCond.Broadcast()
		for len(c.buf) == 0 && !c.closed && wait {
			rs.chCond.Wait()
		}
		c.recvWaiter--
		if len(c.buf) > 0 {
			v, c.buf = c.buf[0], c.buf[1:]
			c.recvSeq++
			ok = true
			rs.chCond.Broadcast()
		}
	})
	return v, ok
}

// selectCases waits until one case of a select can proceed and performs
// it, returning its index (or len(cases) for the default case).
func (t *thread) selectCases(cases val, hasDefault bool) int64 {
	m := t.m
	rs := m.rt()
	p, n := cases.ptrAt(0), int64(cases.u64(8))
	caseTy := m.selectCaseType()
	l := m.layoutOf(caseTy)
	type sc struct {
		send bool
		c    *channel
		slot Ptr
	}
	var cs []sc
	for i := int64(0); i < n; i++ {
		v := m.read(p.add(i*l.Size), l.Size)
		// The case points at the Sender/Receiver, whose first field is the
		// channel handle.
		handle := m.read(v.ptrAt(l.Fields[1]), 8)
		cs = append(cs, sc{send: v.b[l.Fields[0]] != 0, c: m.channelOf(handle), slot: v.ptrAt(l.Fields[2])})
	}
	chosen := int64(-1)
	var got val
	var gotOK bool
	m.block(func() {
		rs.chMu.Lock()
		defer rs.chMu.Unlock()
		for {
			for i, c := range cs {
				if c.send {
					if c.c.closed {
						continue
					}
					ready := c.c.cap < 0 || len(c.c.buf) < c.c.cap || c.c.cap == 0 && c.c.recvWaiter > len(c.c.buf)
					if !ready {
						continue
					}
					c.c.buf = append(c.c.buf, m.read(c.slot, m.sizeOf(c.c.elem)))
					c.c.sendSeq++
					rs.chCond.Broadcast()
					chosen = int64(i)
					return
				}
				if len(c.c.buf) > 0 || c.c.closed {
					if len(c.c.buf) > 0 {
						got, c.c.buf = c.c.buf[0], c.c.buf[1:]
						c.c.recvSeq++
						gotOK = true
						rs.chCond.Broadcast()
					}
					chosen = int64(i)
					return
				}
			}
			if hasDefault {
				chosen = int64(len(cs))
				return
			}
			rs.chCond.Wait()
		}
	})
	if chosen < int64(len(cs)) && !cs[chosen].send {
		c := cs[chosen]
		m.write(c.slot, m.recvResult(c.c.elem, got, gotOK))
	}
	return chosen
}

func (m *Machine) selectCaseType() *types.Type {
	for _, c := range m.prog.Res.Crates {
		if !c.IsStd {
			continue
		}
		b := c.Root.Lookup("sync", resolve.TypeNS)
		b = b.Res.Def.Module.Lookup("mpmc", resolve.TypeNS)
		b = b.Res.Def.Module.Lookup("SelectCase", resolve.TypeNS)
		return m.ctx.Adt(b.Res.Def, nil)
	}
	return nil
}

// recvResult builds Result<T, RecvError>.
func (m *Machine) recvResult(elem *types.Type, v val, ok bool) val {
	errTy := m.ctx.Adt(m.info.Lang["RecvError"], nil)
	res := m.ctx.Adt(m.info.Lang["Result"], []*types.Type{elem, errTy})
	l := m.layoutOf(res)
	tmp := m.newAlloc(l.Size, "result")
	okIdx, errIdx := 0, 1
	for i, vr := range m.info.Adts[res.Def].Variants {
		if vr.Def.Name == "Ok" {
			okIdx = i
		} else {
			errIdx = i
		}
	}
	if ok {
		m.write(Ptr{A: tmp, Off: l.Variants[okIdx][0]}, v)
		m.writeTag(Ptr{A: tmp}, res, okIdx)
	} else {
		m.writeTag(Ptr{A: tmp}, res, errIdx)
	}
	return m.read(Ptr{A: tmp}, l.Size)
}

var _ = layout.PtrSize

// processStart anchors monotonic_nanos.
var processStart = time.Now()
