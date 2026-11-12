// Package layout computes the memory layout of navi types: size,
// alignment, field offsets, enum tags and pointer maps for the GC.
package layout

import (
	"fmt"
	"sync"

	"navi/internal/types"
)

// PtrSize is the size of a pointer on all supported targets (64-bit).
const PtrSize = 8

// Layout describes how values of a type are stored.
type Layout struct {
	Size  int64
	Align int64
	// Fields holds field offsets for tuples, structs, arrays (element
	// stride is Size/Len) and the payload of each enum variant.
	Fields []int64
	// For enums: the tag, its offset and size, and each variant's fields.
	Tag      TagKind
	TagOff   int64
	TagSize  int64
	Variants [][]int64 // field offsets per variant
	// Niche: for Option-like enums represented by a null pointer, the
	// index of the variant encoded as null (the other variant holds the
	// pointer at offset 0).
	NicheVariant int
	// Ptrs lists the offsets of words that may hold GC pointers. For enums
	// it is the union over variants, so the GC must check that a candidate
	// word really points into the heap (the GC does not move objects).
	Ptrs []int64
	// Unsized types ([T], str, dyn) have no layout of their own.
	Unsized bool
}

// TagKind says how an enum stores its variant.
type TagKind uint8

const (
	NoTag    TagKind = iota // structs, single-variant enums
	TagInt                  // an integer tag
	TagNiche                // a pointer that is null for NicheVariant
)

// Computer computes and caches layouts.
type Computer struct {
	Info  *types.Info
	mu    sync.Mutex
	cache map[*types.Type]*Layout
}

// New returns a layout computer.
func New(info *types.Info) *Computer {
	return &Computer{Info: info, cache: map[*types.Type]*Layout{}}
}

func align(x, a int64) int64 {
	if a <= 1 {
		return x
	}
	return (x + a - 1) / a * a
}

// IsFat reports whether a pointer to t is a fat pointer (pointer plus
// length or vtable).
func IsFat(t *types.Type) bool {
	switch t.Kind {
	case types.KStr, types.KSlice, types.KDyn:
		return true
	}
	return false
}

// Of returns the layout of a fully concrete type.
func (c *Computer) Of(t *types.Type) *Layout {
	c.mu.Lock()
	if l := c.cache[t]; l != nil {
		c.mu.Unlock()
		return l
	}
	c.mu.Unlock()
	l := c.compute(t)
	c.mu.Lock()
	c.cache[t] = l
	c.mu.Unlock()
	return l
}

func scalar(size int64) *Layout { return &Layout{Size: size, Align: size} }

func intSize(k types.IntKind) int64 {
	switch k {
	case types.I8, types.U8:
		return 1
	case types.I16, types.U16:
		return 2
	case types.I32, types.U32:
		return 4
	}
	return 8
}

// IntSize returns the size in bytes of an integer kind.
func IntSize(k types.IntKind) int64 { return intSize(k) }

func (c *Computer) compute(t *types.Type) *Layout {
	switch t.Kind {
	case types.KBool:
		return scalar(1)
	case types.KChar:
		return scalar(4)
	case types.KInt:
		return scalar(intSize(t.Int))
	case types.KFloat:
		if t.F64 {
			return scalar(8)
		}
		return scalar(4)
	case types.KNever:
		return &Layout{Size: 0, Align: 1}
	case types.KStr, types.KSlice, types.KDyn:
		return &Layout{Unsized: true, Align: 1}
	case types.KRef, types.KPtr:
		if IsFat(t.Elem) {
			return &Layout{Size: 16, Align: 8, Fields: []int64{0, 8}, Ptrs: fatPtrs(t.Elem)}
		}
		return &Layout{Size: 8, Align: 8, Ptrs: []int64{0}}
	case types.KFn:
		if t.Abi == "C" {
			return &Layout{Size: 8, Align: 8}
		}
		// A closure: code pointer and environment pointer.
		return &Layout{Size: 16, Align: 8, Fields: []int64{0, 8}, Ptrs: []int64{8}}
	case types.KTuple:
		return c.record(t.Elems)
	case types.KArray:
		el := c.Of(t.Elem)
		stride := align(el.Size, el.Align)
		l := &Layout{Size: stride * t.Len, Align: el.Align}
		for i := int64(0); i < t.Len && len(el.Ptrs) > 0; i++ {
			for _, p := range el.Ptrs {
				l.Ptrs = append(l.Ptrs, i*stride+p)
			}
		}
		return l
	case types.KAdt:
		return c.adt(t)
	case types.KOpaque:
		if h := c.Info.Hidden[t]; h != nil {
			return c.Of(h)
		}
	}
	panic(fmt.Sprintf("layout of non-concrete type %s", t))
}

func fatPtrs(elem *types.Type) []int64 {
	if elem.Kind == types.KDyn {
		return []int64{0, 8} // data pointer and vtable
	}
	return []int64{0}
}

// record lays out fields in declaration order with natural alignment.
func (c *Computer) record(fields []*types.Type) *Layout {
	l := &Layout{Align: 1}
	off := int64(0)
	for _, f := range fields {
		fl := c.Of(f)
		off = align(off, fl.Align)
		l.Fields = append(l.Fields, off)
		for _, p := range fl.Ptrs {
			l.Ptrs = append(l.Ptrs, off+p)
		}
		off += fl.Size
		if fl.Align > l.Align {
			l.Align = fl.Align
		}
	}
	l.Size = align(off, l.Align)
	return l
}

// FieldTypes returns the field types of an ADT variant (variant -1 for
// structs) with the ADT's arguments substituted.
func (c *Computer) FieldTypes(t *types.Type, variant int) []*types.Type {
	ai := c.Info.Adts[t.Def]
	fields := ai.Fields
	if variant >= 0 {
		fields = ai.Variants[variant].Fields
	}
	out := make([]*types.Type, len(fields))
	sub := types.ParamMap(ai.Generics, t.Args)
	for i, f := range fields {
		out[i] = types.Subst(c.Info.Ctx, f.Type, sub, nil)
	}
	return out
}

func (c *Computer) adt(t *types.Type) *Layout {
	ai := c.Info.Adts[t.Def]
	if !ai.IsEnum {
		return c.record(c.FieldTypes(t, -1))
	}
	n := len(ai.Variants)
	if n == 0 {
		return &Layout{Size: 0, Align: 1}
	}
	// Option-like enums: one unit variant and one variant holding a single
	// non-null pointer use the null pointer for the unit variant.
	if n == 2 {
		for unit := 0; unit < 2; unit++ {
			other := 1 - unit
			if len(ai.Variants[unit].Fields) != 0 || len(ai.Variants[other].Fields) != 1 {
				continue
			}
			ft := c.FieldTypes(t, other)[0]
			if ft.Kind == types.KRef || ft.Kind == types.KFn && ft.Abi == "" {
				fl := c.Of(ft)
				l := &Layout{Size: fl.Size, Align: fl.Align, Tag: TagNiche, NicheVariant: unit,
					Variants: [][]int64{nil, nil}, Ptrs: fl.Ptrs}
				l.Variants[other] = []int64{0}
				return l
			}
		}
	}
	// Tag: the smallest integer that holds every discriminant.
	minD, maxD := ai.Variants[0].Discr, ai.Variants[0].Discr
	for _, v := range ai.Variants {
		if v.Discr < minD {
			minD = v.Discr
		}
		if v.Discr > maxD {
			maxD = v.Discr
		}
	}
	tagSize := int64(8)
	switch {
	case minD >= 0 && maxD <= 0xff, minD >= -128 && maxD <= 127:
		tagSize = 1
	case minD >= -1<<15 && maxD <= 0xffff:
		tagSize = 2
	case minD >= -1<<31 && maxD <= 0xffffffff:
		tagSize = 4
	}
	l := &Layout{Tag: TagInt, TagOff: 0, TagSize: tagSize, Align: tagSize}
	maxEnd := tagSize
	for i := range ai.Variants {
		fields := c.FieldTypes(t, i)
		off := tagSize
		var offs []int64
		for _, f := range fields {
			fl := c.Of(f)
			off = align(off, fl.Align)
			offs = append(offs, off)
			for _, p := range fl.Ptrs {
				l.Ptrs = appendUnique(l.Ptrs, off+p)
			}
			off += fl.Size
			if fl.Align > l.Align {
				l.Align = fl.Align
			}
		}
		l.Variants = append(l.Variants, offs)
		if off > maxEnd {
			maxEnd = off
		}
	}
	l.Size = align(maxEnd, l.Align)
	return l
}

func appendUnique(xs []int64, x int64) []int64 {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}
