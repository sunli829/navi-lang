package types

import (
	"fmt"
	"strings"

	"navi/internal/resolve"
)

// SigString renders the collected signature of a definition, for tests and
// diagnostics. It returns "" for definitions without a signature.
func (info *Info) SigString(d *resolve.Def) string {
	switch d.Kind {
	case resolve.DefStruct, resolve.DefEnum:
		return info.adtSig(info.Adts[d])
	case resolve.DefFn, resolve.DefAssocFn, resolve.DefExternFn:
		return info.fnSigString(info.Fns[d])
	case resolve.DefTrait:
		ti := info.Traits[d]
		if ti == nil {
			return ""
		}
		var b strings.Builder
		b.WriteString("trait " + d.Name)
		info.writeGenerics(&b, ti.Generics, ti.Generics.SelfTrait)
		if len(ti.Supers) > 0 {
			b.WriteString(": ")
			writeBounds(&b, ti.Supers)
		}
		info.writeWhere(&b, ti.Generics, ti.Generics.SelfTrait)
		return b.String()
	case resolve.DefImpl:
		ii := info.Impls[d]
		if ii == nil {
			return ""
		}
		var b strings.Builder
		b.WriteString("impl")
		info.writeGenerics(&b, ii.Generics, nil)
		b.WriteByte(' ')
		if ii.Trait != nil {
			b.WriteString(ii.Trait.String())
			b.WriteString(" for ")
		}
		b.WriteString(ii.SelfTy.String())
		info.writeWhere(&b, ii.Generics, nil)
		return b.String()
	case resolve.DefTypeAlias:
		ai := info.Aliases[d]
		if ai == nil {
			return ""
		}
		var b strings.Builder
		b.WriteString("type " + d.Name)
		info.writeGenerics(&b, ai.Generics, nil)
		b.WriteString(" = " + ai.Type.String())
		return b.String()
	case resolve.DefAssocType:
		ai := info.Assoc[d]
		if ai == nil {
			return ""
		}
		s := "type " + qualName(d)
		if len(ai.Bounds) > 0 {
			var b strings.Builder
			writeBounds(&b, ai.Bounds)
			s += ": " + b.String()
		}
		if ai.Value != nil {
			s += " = " + ai.Value.String()
		}
		return s
	case resolve.DefConst, resolve.DefAssocConst:
		return "const " + qualName(d) + ": " + info.Consts[d].Type.String()
	case resolve.DefStatic, resolve.DefExternStatic:
		ci := info.Consts[d]
		if ci.Mut {
			return "static mut " + d.Name + ": " + ci.Type.String()
		}
		return "static " + d.Name + ": " + ci.Type.String()
	}
	return ""
}

func qualName(d *resolve.Def) string {
	if d.Parent != nil && d.Parent.Kind == resolve.DefTrait {
		return d.Parent.Name + "::" + d.Name
	}
	return d.Name
}

func (info *Info) adtSig(ai *AdtInfo) string {
	if ai == nil {
		return ""
	}
	var b strings.Builder
	if ai.IsEnum {
		b.WriteString("enum ")
	} else {
		b.WriteString("struct ")
	}
	b.WriteString(ai.Def.Name)
	info.writeGenerics(&b, ai.Generics, nil)
	writeFields := func(kind int, fields []*Field) {
		switch kind {
		case 1:
			b.WriteByte('(')
			for i, f := range fields {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(f.Type.String())
			}
			b.WriteByte(')')
		case 0:
			b.WriteString(" { ")
			for i, f := range fields {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(f.Name + ": " + f.Type.String())
			}
			b.WriteString(" }")
		}
	}
	if !ai.IsEnum {
		writeFields(int(ai.Kind), ai.Fields)
		return b.String()
	}
	b.WriteString(" { ")
	for i, v := range ai.Variants {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(v.Def.Name)
		writeFields(int(v.Kind), v.Fields)
		if v.Kind == 2 {
			fmt.Fprintf(&b, " = %d", v.Discr)
		}
	}
	b.WriteString(" }")
	return b.String()
}

func (info *Info) fnSigString(fs *FnSig) string {
	if fs == nil {
		return ""
	}
	var b strings.Builder
	if fs.Abi != "" {
		fmt.Fprintf(&b, "extern %q ", fs.Abi)
	}
	if fs.Const {
		b.WriteString("const ")
	}
	if fs.Unsafe {
		b.WriteString("unsafe ")
	}
	b.WriteString("fn " + qualName(fs.Def))
	info.writeGenerics(&b, fs.Generics, nil)
	b.WriteByte('(')
	var parts []string
	if fs.Self != nil {
		parts = append(parts, fs.Self.String())
	}
	for _, p := range fs.Params {
		parts = append(parts, p.String())
	}
	b.WriteString(strings.Join(parts, ", "))
	b.WriteByte(')')
	if fs.Ret != nil && fs.Ret != info.Ctx.Unit {
		b.WriteString(" -> " + fs.Ret.String())
	}
	info.writeWhere(&b, fs.Generics, nil)
	return b.String()
}

// writeGenerics writes the item's own parameters with their simple bounds.
func (info *Info) writeGenerics(b *strings.Builder, g *Generics, selfTrait *resolve.Def) {
	if g == nil || len(g.Params) == 0 {
		return
	}
	b.WriteByte('<')
	for i, p := range g.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Def.Name)
		var bounds []*TraitRef
		for _, pr := range g.Preds {
			if pr.Trait.Self == p.Type {
				bounds = append(bounds, pr.Trait)
			}
		}
		if len(bounds) > 0 {
			b.WriteString(": ")
			writeBounds(b, bounds)
		}
		if p.Default != nil {
			b.WriteString(" = " + p.Default.String())
		}
	}
	b.WriteByte('>')
}

// writeWhere writes predicates whose self type is not one of the item's own
// parameters.
func (info *Info) writeWhere(b *strings.Builder, g *Generics, selfTrait *resolve.Def) {
	if g == nil {
		return
	}
	own := map[*Type]bool{}
	for _, p := range g.Params {
		own[p.Type] = true
	}
	var parts []string
	for _, pr := range g.Preds {
		if own[pr.Trait.Self] {
			continue
		}
		parts = append(parts, pr.Trait.Self.String()+": "+pr.Trait.String())
	}
	if len(parts) > 0 {
		b.WriteString(" where " + strings.Join(parts, ", "))
	}
}
