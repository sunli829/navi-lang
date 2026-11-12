// Package interp runs MIR directly. Memory is modelled like Miri: each
// allocation is a byte array plus a table of pointers stored in it, so that
// raw-pointer code in std behaves as it will in native code.
package interp

import (
	"encoding/binary"
	"math"
)

// Alloc is one allocation (a local, a heap object, a static, a string
// literal, a function or a vtable).
type Alloc struct {
	id   int
	b    []byte
	ptrs map[int64]Ptr // pointers stored at these offsets (8-byte words)
	fn   *fnInst       // for function allocations
	vt   *vtable       // for vtables
	name string        // for diagnostics
}

// Ptr is a pointer: an allocation and an offset. A nil allocation is a
// null pointer or an integer cast to a pointer (Off is the address).
type Ptr struct {
	A   *Alloc
	Off int64
}

func (p Ptr) add(n int64) Ptr { return Ptr{A: p.A, Off: p.Off + n} }

func (p Ptr) isNull() bool { return p.A == nil && p.Off == 0 }

// val is a value in transit: bytes plus the pointers inside them.
type val struct {
	b    []byte
	ptrs map[int64]Ptr
}

func newVal(size int64) val { return val{b: make([]byte, size)} }

func (v val) setPtr(off int64, p Ptr) {
	if p.A == nil {
		binary.LittleEndian.PutUint64(v.b[off:], uint64(p.Off))
		delete(v.ptrs, off)
		return
	}
	binary.LittleEndian.PutUint64(v.b[off:], uint64(p.A.id)<<32|uint64(p.Off)&0xffffffff)
	v.ptrs[off] = p
}

func (v *val) ensurePtrs() {
	if v.ptrs == nil {
		v.ptrs = map[int64]Ptr{}
	}
}

func (v val) ptrAt(off int64) Ptr {
	if p, ok := v.ptrs[off]; ok {
		return p
	}
	return Ptr{Off: int64(binary.LittleEndian.Uint64(v.b[off:]))}
}

func (v val) u64(off int64) uint64 { return binary.LittleEndian.Uint64(v.b[off:]) }

// read copies size bytes starting at p.
func (m *Machine) read(p Ptr, size int64) val {
	v := newVal(size)
	if size == 0 {
		return v
	}
	if p.A == nil {
		m.fatal("null pointer dereference")
	}
	if p.Off < 0 || p.Off+size > int64(len(p.A.b)) {
		m.fatal("out-of-bounds memory access in %s (offset %d, size %d, allocation size %d)", p.A.name, p.Off, size, len(p.A.b))
	}
	copy(v.b, p.A.b[p.Off:p.Off+size])
	for off, q := range p.A.ptrs {
		if off >= p.Off && off < p.Off+size {
			v.ensurePtrs()
			v.ptrs[off-p.Off] = q
		}
	}
	return v
}

// write stores v at p.
func (m *Machine) write(p Ptr, v val) {
	size := int64(len(v.b))
	if size == 0 {
		return
	}
	if p.A == nil {
		m.fatal("null pointer dereference")
	}
	if p.Off < 0 || p.Off+size > int64(len(p.A.b)) {
		m.fatal("out-of-bounds memory write in %s (offset %d, size %d, allocation size %d)", p.A.name, p.Off, size, len(p.A.b))
	}
	copy(p.A.b[p.Off:], v.b)
	for off := range p.A.ptrs {
		if off >= p.Off && off < p.Off+size {
			delete(p.A.ptrs, off)
		}
	}
	for off, q := range v.ptrs {
		if p.A.ptrs == nil {
			p.A.ptrs = map[int64]Ptr{}
		}
		p.A.ptrs[p.Off+off] = q
	}
}

func (m *Machine) newAlloc(size int64, name string) *Alloc {
	m.nextID++
	return &Alloc{id: m.nextID, b: make([]byte, size), name: name}
}

// Scalar helpers.

func intVal(size int64, x int64) val {
	v := newVal(size)
	switch size {
	case 1:
		v.b[0] = byte(x)
	case 2:
		binary.LittleEndian.PutUint16(v.b, uint16(x))
	case 4:
		binary.LittleEndian.PutUint32(v.b, uint32(x))
	case 8:
		binary.LittleEndian.PutUint64(v.b, uint64(x))
	}
	return v
}

// readInt reads a size-byte integer at off; signed sign-extends.
func readInt(b []byte, size int64, signed bool) int64 {
	switch size {
	case 1:
		if signed {
			return int64(int8(b[0]))
		}
		return int64(b[0])
	case 2:
		x := binary.LittleEndian.Uint16(b)
		if signed {
			return int64(int16(x))
		}
		return int64(x)
	case 4:
		x := binary.LittleEndian.Uint32(b)
		if signed {
			return int64(int32(x))
		}
		return int64(x)
	case 8:
		return int64(binary.LittleEndian.Uint64(b))
	}
	return 0
}

func floatVal(f64 bool, x float64) val {
	if f64 {
		v := newVal(8)
		binary.LittleEndian.PutUint64(v.b, math.Float64bits(x))
		return v
	}
	v := newVal(4)
	binary.LittleEndian.PutUint32(v.b, math.Float32bits(float32(x)))
	return v
}

func readFloat(b []byte, f64 bool) float64 {
	if f64 {
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
}

func ptrVal(p Ptr) val {
	v := newVal(8)
	v.ensurePtrs()
	v.setPtr(0, p)
	return v
}

func fatVal(p Ptr, meta Ptr) val {
	v := newVal(16)
	v.ensurePtrs()
	v.setPtr(0, p)
	v.setPtr(8, meta)
	return v
}
