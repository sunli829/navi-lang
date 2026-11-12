package syntax

import (
	"sort"
	"strings"
)

// ParseFile parses one source file. It always returns a SourceFile; errors
// are reported in the returned Errors (Len() == 0 means success).
func ParseFile(f *File) (*SourceFile, *Errors) {
	toks, errs := Tokenize(f)
	p := &parser{file: f, toks: toks, errs: errs}
	sf := &SourceFile{File: f}
	sf.Attrs = p.innerAttrs()
	sf.Items = p.items(EOF)
	sort.SliceStable(errs.Diags, func(i, j int) bool { return errs.Diags[i].Pos < errs.Diags[j].Pos })
	return sf, errs
}

type parser struct {
	file *File
	toks []Token
	i    int
	errs *Errors

	// noStructLit is set while parsing the head of if/while/match/for, where
	// `x {` starts the body rather than a struct literal.
	noStructLit bool
	lastErrPos  Pos
}

func (p *parser) tok() Token       { return p.toks[p.i] }
func (p *parser) kind() Kind       { return p.toks[p.i].Kind }
func (p *parser) pos() Pos         { return p.toks[p.i].Pos }
func (p *parser) peek(n int) Token { return p.toks[min(p.i+n, len(p.toks)-1)] }

func (p *parser) next() Token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *parser) got(k Kind) bool {
	if p.kind() == k {
		p.next()
		return true
	}
	return false
}

func (p *parser) errorf(pos Pos, format string, args ...any) {
	if pos == p.lastErrPos && p.errs.Len() > 0 {
		return // one error per position is enough
	}
	p.lastErrPos = pos
	p.errs.add(pos, format, args...)
}

func (p *parser) describe(t Token) string {
	switch t.Kind {
	case Ident:
		return "identifier " + t.Text
	case EOF:
		return "end of file"
	}
	if t.Text != "" {
		return t.Kind.String() + " " + t.Text
	}
	return "'" + t.Kind.String() + "'"
}

func (p *parser) expect(k Kind) Pos {
	pos := p.pos()
	if !p.got(k) {
		p.errorf(pos, "expected '%s', found %s", k, p.describe(p.tok()))
	}
	return pos
}

// splitRest consumes the first character of the current token and leaves
// the rest as a token of kind rest: >> becomes >, && becomes &, and so on.
func (p *parser) splitRest(rest Kind) {
	t := &p.toks[p.i]
	t.Kind = rest
	t.Pos++
}

// gotGt consumes a '>' that closes generic arguments, splitting >>, >=, >>=.
func (p *parser) gotGt() bool {
	switch p.kind() {
	case Gt:
		p.next()
	case Shr:
		p.splitRest(Gt)
	case Ge:
		p.splitRest(Eq)
	case ShrEq:
		p.splitRest(Ge)
	default:
		return false
	}
	return true
}

func (p *parser) expectGt() {
	if !p.gotGt() {
		p.errorf(p.pos(), "expected '>', found %s", p.describe(p.tok()))
	}
}

// gotAmp consumes a single '&', splitting '&&'.
func (p *parser) gotAmp() bool {
	switch p.kind() {
	case And:
		p.next()
	case AndAnd:
		p.splitRest(And)
	default:
		return false
	}
	return true
}

func (p *parser) name() *Name {
	t := p.tok()
	if t.Kind == Ident {
		p.next()
		return &Name{NamePos: t.Pos, Value: t.Text}
	}
	p.errorf(t.Pos, "expected identifier, found %s", p.describe(t))
	return &Name{NamePos: t.Pos, Value: "_"}
}

// ---------------------------------------------------------------------------
// Attributes

func (p *parser) innerAttrs() []*Attr {
	var attrs []*Attr
	for p.kind() == Pound && p.peek(1).Kind == Not {
		attrs = append(attrs, p.attr(true))
	}
	return attrs
}

func (p *parser) outerAttrs() []*Attr {
	var attrs []*Attr
	for p.kind() == Pound && p.peek(1).Kind == LBracket {
		attrs = append(attrs, p.attr(false))
	}
	return attrs
}

func (p *parser) attr(inner bool) *Attr {
	a := &Attr{Hash: p.next().Pos, Inner: inner}
	if inner {
		p.expect(Not)
	}
	p.expect(LBracket)
	a.Path = p.modPath()
	switch p.kind() {
	case LParen:
		p.next()
		depth := 1
		for {
			t := p.tok()
			if t.Kind == EOF {
				p.errorf(t.Pos, "unterminated attribute")
				return a
			}
			switch t.Kind {
			case LParen, LBracket, LBrace:
				depth++
			case RParen, RBracket, RBrace:
				depth--
			}
			if depth == 0 {
				p.next()
				break
			}
			a.Args = append(a.Args, p.next())
		}
	case Eq:
		p.next()
		a.Value = p.unary()
	}
	p.expect(RBracket)
	return a
}

// ---------------------------------------------------------------------------
// Items

// items parses items until the closing token (EOF or RBrace).
func (p *parser) items(end Kind) []Item {
	var items []Item
	for p.kind() != end && p.kind() != EOF {
		start := p.i
		if it := p.item(); it != nil {
			items = append(items, it)
		}
		if p.i == start {
			p.errorf(p.pos(), "expected item, found %s", p.describe(p.tok()))
			p.syncItem()
			if p.i == start {
				p.next() // always make progress
			}
		}
	}
	return items
}

// syncItem skips tokens until something that can start an item.
func (p *parser) syncItem() {
	depth := 0
	for {
		switch p.kind() {
		case EOF:
			return
		case LBrace:
			depth++
		case RBrace:
			if depth == 0 {
				return
			}
			depth--
			if depth == 0 {
				p.next()
				return
			}
		case Semi:
			if depth == 0 {
				p.next()
				return
			}
		case KwFn, KwStruct, KwEnum, KwImpl, KwTrait, KwMod, KwUse, KwConst, KwStatic, KwType, KwPub, KwExtern, Pound:
			if depth == 0 {
				return
			}
		}
		p.next()
	}
}

func isItemStart(p *parser) bool {
	switch p.kind() {
	case KwFn, KwStruct, KwEnum, KwImpl, KwTrait, KwMod, KwUse, KwStatic, KwType, KwPub, KwExtern:
		return true
	case KwConst:
		return true
	case KwUnsafe:
		k := p.peek(1).Kind
		return k == KwFn || k == KwImpl || k == KwTrait
	case Pound:
		return p.peek(1).Kind == LBracket
	}
	return false
}

func (p *parser) item() Item {
	attrs := p.outerAttrs()
	base := ItemBase{Attrs: attrs, Start: p.pos()}
	base.Vis = p.visibility()

	switch p.kind() {
	case KwFn:
		return p.fnDecl(base, "")
	case KwConst:
		if k := p.peek(1).Kind; k == KwFn || k == KwUnsafe {
			p.next()
			fn := p.fnDecl(base, "")
			fn.Const = true
			return fn
		}
		return p.constDecl(base)
	case KwUnsafe:
		switch p.peek(1).Kind {
		case KwFn:
			p.next()
			fn := p.fnDecl(base, "")
			fn.Unsafe = true
			return fn
		case KwImpl:
			p.next()
			im := p.implDecl(base)
			im.Unsafe = true
			return im
		case KwTrait:
			p.next()
			tr := p.traitDecl(base)
			tr.Unsafe = true
			return tr
		}
	case KwExtern:
		p.next()
		abi := p.abi()
		if p.kind() == LBrace {
			return p.externBlock(base, abi)
		}
		if p.kind() == KwFn {
			return p.fnDecl(base, abi)
		}
		p.errorf(p.pos(), "expected 'fn' or '{' after extern, found %s", p.describe(p.tok()))
		return nil
	case KwStruct:
		return p.structDecl(base)
	case KwEnum:
		return p.enumDecl(base)
	case KwType:
		return p.typeAlias(base)
	case KwStatic:
		return p.staticDecl(base)
	case KwImpl:
		return p.implDecl(base)
	case KwTrait:
		return p.traitDecl(base)
	case KwMod:
		return p.modDecl(base)
	case KwUse:
		return p.useDecl(base)
	}
	if len(attrs) > 0 || base.Vis != VisPrivate {
		p.errorf(p.pos(), "expected item after attributes or visibility, found %s", p.describe(p.tok()))
	}
	return nil
}

func (p *parser) visibility() VisKind {
	if !p.got(KwPub) {
		return VisPrivate
	}
	if p.kind() == LParen {
		switch p.peek(1).Kind {
		case KwCrate:
			p.next()
			p.next()
			p.expect(RParen)
			return VisCrate
		case KwSuper:
			p.next()
			p.next()
			p.expect(RParen)
			return VisSuper
		}
	}
	return VisPub
}

// abi parses the optional "C" after extern. Missing means "C".
func (p *parser) abi() string {
	if p.kind() != String {
		return "C"
	}
	t := p.next()
	abi := strings.Trim(t.Text, `"`)
	if abi != "C" {
		p.errorf(t.Pos, "unsupported ABI %q (only \"C\" is supported)", abi)
	}
	return abi
}

func (p *parser) fnDecl(base ItemBase, abi string) *FnDecl {
	p.expect(KwFn)
	fn := &FnDecl{ItemBase: base, Abi: abi}
	fn.Name = p.name()
	fn.Generics = p.genericParams()
	p.expect(LParen)
	if p.kind() != RParen {
		fn.Self = p.selfParam()
		if fn.Self == nil || p.got(Comma) {
			for p.kind() != RParen && p.kind() != EOF {
				param := &Param{Pat: p.patNoTopAlt()}
				p.expect(Colon)
				param.Type = p.typ()
				fn.Params = append(fn.Params, param)
				if !p.got(Comma) {
					break
				}
			}
		}
	}
	p.expect(RParen)
	if p.got(RArrow) {
		fn.Result = p.typ()
	}
	p.whereClause(fn.Generics)
	if p.kind() == LBrace {
		fn.Body = p.block()
	} else {
		p.expect(Semi)
	}
	return fn
}

// selfParam parses self, mut self, &self or &mut self if present.
func (p *parser) selfParam() *SelfParam {
	switch {
	case p.kind() == KwSelfValue:
		return &SelfParam{SelfPos: p.next().Pos}
	case p.kind() == KwMut && p.peek(1).Kind == KwSelfValue:
		p.next()
		return &SelfParam{SelfPos: p.next().Pos, Mut: true}
	case p.kind() == And && p.peek(1).Kind == KwSelfValue:
		p.next()
		return &SelfParam{SelfPos: p.next().Pos, Ref: true}
	case p.kind() == And && p.peek(1).Kind == KwMut && p.peek(2).Kind == KwSelfValue:
		p.next()
		p.next()
		return &SelfParam{SelfPos: p.next().Pos, Ref: true, Mut: true}
	}
	return nil
}

func (p *parser) genericParams() *Generics {
	g := &Generics{Lt: NoPos}
	if p.kind() != Lt {
		return g
	}
	g.Lt = p.next().Pos
	for p.kind() != Gt && p.kind() != EOF {
		gp := &GenericParam{Name: p.name()}
		if p.got(Colon) {
			gp.Bounds = p.bounds()
		}
		if p.got(Eq) {
			gp.Default = p.typ()
		}
		g.Params = append(g.Params, gp)
		if !p.got(Comma) {
			break
		}
	}
	p.expectGt()
	return g
}

// bounds parses A + B + C.
func (p *parser) bounds() []*Path {
	var bs []*Path
	for {
		bs = append(bs, p.typePath())
		if !p.got(Plus) {
			return bs
		}
	}
}

func (p *parser) whereClause(g *Generics) {
	if !p.got(KwWhere) {
		return
	}
	for p.kind() != LBrace && p.kind() != Semi && p.kind() != EOF {
		wp := &WherePredicate{Type: p.typ()}
		p.expect(Colon)
		wp.Bounds = p.bounds()
		g.Where = append(g.Where, wp)
		if !p.got(Comma) {
			break
		}
	}
}

func (p *parser) structDecl(base ItemBase) *StructDecl {
	p.expect(KwStruct)
	s := &StructDecl{ItemBase: base, Name: p.name()}
	s.Generics = p.genericParams()
	switch p.kind() {
	case LParen:
		s.Kind = StructTuple
		s.Fields = p.tupleFields()
		p.whereClause(s.Generics)
		p.expect(Semi)
	case Semi:
		s.Kind = StructUnit
		p.next()
	default:
		p.whereClause(s.Generics)
		if p.kind() == Semi {
			s.Kind = StructUnit
			p.next()
			break
		}
		s.Kind = StructNamed
		s.Fields = p.namedFields()
	}
	return s
}

func (p *parser) namedFields() []*Field {
	var fields []*Field
	p.expect(LBrace)
	for p.kind() != RBrace && p.kind() != EOF {
		f := &Field{Attrs: p.outerAttrs(), Vis: p.visibility()}
		f.Name = p.name()
		p.expect(Colon)
		f.Type = p.typ()
		fields = append(fields, f)
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return fields
}

func (p *parser) tupleFields() []*Field {
	var fields []*Field
	p.expect(LParen)
	for p.kind() != RParen && p.kind() != EOF {
		f := &Field{Attrs: p.outerAttrs(), Vis: p.visibility()}
		f.Type = p.typ()
		fields = append(fields, f)
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RParen)
	return fields
}

func (p *parser) enumDecl(base ItemBase) *EnumDecl {
	p.expect(KwEnum)
	e := &EnumDecl{ItemBase: base, Name: p.name()}
	e.Generics = p.genericParams()
	p.whereClause(e.Generics)
	p.expect(LBrace)
	for p.kind() != RBrace && p.kind() != EOF {
		v := &Variant{Attrs: p.outerAttrs(), Name: p.name()}
		switch p.kind() {
		case LBrace:
			v.Kind = StructNamed
			v.Fields = p.namedFields()
		case LParen:
			v.Kind = StructTuple
			v.Fields = p.tupleFields()
		default:
			v.Kind = StructUnit
		}
		if p.got(Eq) {
			v.Discriminant = p.expr()
		}
		e.Variants = append(e.Variants, v)
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return e
}

func (p *parser) typeAlias(base ItemBase) *TypeAlias {
	p.expect(KwType)
	t := &TypeAlias{ItemBase: base, Name: p.name()}
	t.Generics = p.genericParams()
	if p.got(Colon) {
		t.Bounds = p.bounds()
	}
	p.whereClause(t.Generics)
	if p.got(Eq) {
		t.Type = p.typ()
	}
	p.expect(Semi)
	return t
}

func (p *parser) constDecl(base ItemBase) *ConstDecl {
	p.expect(KwConst)
	c := &ConstDecl{ItemBase: base}
	if p.kind() == Underscore {
		c.Name = &Name{NamePos: p.next().Pos, Value: "_"}
	} else {
		c.Name = p.name()
	}
	p.expect(Colon)
	c.Type = p.typ()
	if p.got(Eq) {
		c.Value = p.expr()
	}
	p.expect(Semi)
	return c
}

func (p *parser) staticDecl(base ItemBase) *StaticDecl {
	p.expect(KwStatic)
	s := &StaticDecl{ItemBase: base}
	s.Mut = p.got(KwMut)
	s.Name = p.name()
	p.expect(Colon)
	s.Type = p.typ()
	p.expect(Eq)
	s.Value = p.expr()
	p.expect(Semi)
	return s
}

func (p *parser) implDecl(base ItemBase) *ImplDecl {
	p.expect(KwImpl)
	im := &ImplDecl{ItemBase: base}
	// impl<T> ...: a '<' right after impl starts the parameter list.
	im.Generics = p.genericParams()
	first := p.typ()
	if p.got(KwFor) {
		pt, ok := first.(*PathType)
		if !ok {
			p.errorf(first.Pos(), "expected a trait name before 'for'")
		} else {
			im.Trait = pt.Path
		}
		im.Type = p.typ()
	} else {
		im.Type = first
	}
	p.whereClause(im.Generics)
	p.expect(LBrace)
	im.Items = p.items(RBrace)
	p.expect(RBrace)
	return im
}

func (p *parser) traitDecl(base ItemBase) *TraitDecl {
	p.expect(KwTrait)
	t := &TraitDecl{ItemBase: base, Name: p.name()}
	t.Generics = p.genericParams()
	if p.got(Colon) {
		t.Supers = p.bounds()
	}
	p.whereClause(t.Generics)
	p.expect(LBrace)
	t.Items = p.items(RBrace)
	p.expect(RBrace)
	return t
}

func (p *parser) modDecl(base ItemBase) *ModDecl {
	p.expect(KwMod)
	m := &ModDecl{ItemBase: base, Name: p.name()}
	if p.got(Semi) {
		return m
	}
	m.Inline = true
	p.expect(LBrace)
	m.Attrs = append(m.Attrs, p.innerAttrs()...)
	m.Items = p.items(RBrace)
	p.expect(RBrace)
	return m
}

func (p *parser) useDecl(base ItemBase) *UseDecl {
	p.expect(KwUse)
	u := &UseDecl{ItemBase: base, Tree: p.useTree()}
	p.expect(Semi)
	return u
}

func (p *parser) useTree() *UseTree {
	t := &UseTree{}
	if p.kind() == LBrace {
		t.Group = true
		t.Children = p.useGroup()
		return t
	}
	if p.kind() == Star {
		p.next()
		t.Glob = true
		return t
	}
	path := &Path{}
	for {
		path.Segments = append(path.Segments, &PathSegment{Name: p.pathName()})
		if p.kind() != PathSep {
			break
		}
		p.next()
		if p.kind() == Star {
			p.next()
			t.Glob = true
			break
		}
		if p.kind() == LBrace {
			t.Group = true
			t.Children = p.useGroup()
			break
		}
	}
	t.Prefix = path
	if !t.Glob && !t.Group && p.got(KwAs) {
		if p.kind() == Underscore {
			t.Alias = &Name{NamePos: p.next().Pos, Value: "_"}
		} else {
			t.Alias = p.name()
		}
	}
	return t
}

func (p *parser) useGroup() []*UseTree {
	var children []*UseTree
	p.expect(LBrace)
	for p.kind() != RBrace && p.kind() != EOF {
		children = append(children, p.useTree())
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return children
}

func (p *parser) externBlock(base ItemBase, abi string) *ExternBlock {
	b := &ExternBlock{ItemBase: base, Abi: abi}
	p.expect(LBrace)
	for p.kind() != RBrace && p.kind() != EOF {
		attrs := p.outerAttrs()
		ib := ItemBase{Attrs: attrs, Start: p.pos()}
		ib.Vis = p.visibility()
		switch p.kind() {
		case KwFn:
			fn := p.fnDecl(ib, abi)
			if fn.Body != nil {
				p.errorf(fn.Body.Lbrace, "functions in an extern block cannot have a body")
			}
			b.Items = append(b.Items, fn)
		case KwStatic:
			b.Items = append(b.Items, p.externStatic(ib))
		default:
			p.errorf(p.pos(), "expected 'fn' or 'static' in extern block, found %s", p.describe(p.tok()))
			p.syncItem()
		}
	}
	p.expect(RBrace)
	return b
}

func (p *parser) externStatic(base ItemBase) *StaticDecl {
	p.expect(KwStatic)
	s := &StaticDecl{ItemBase: base}
	s.Mut = p.got(KwMut)
	s.Name = p.name()
	p.expect(Colon)
	s.Type = p.typ()
	p.expect(Semi)
	return s
}

// ---------------------------------------------------------------------------
// Paths

// pathName parses one path segment name: an identifier or self/Self/super/crate.
func (p *parser) pathName() *Name {
	t := p.tok()
	switch t.Kind {
	case Ident:
		p.next()
		return &Name{NamePos: t.Pos, Value: t.Text}
	case KwSelfValue, KwSelfType, KwSuper, KwCrate:
		p.next()
		return &Name{NamePos: t.Pos, Value: t.Kind.String()}
	}
	p.errorf(t.Pos, "expected identifier, found %s", p.describe(t))
	return &Name{NamePos: t.Pos, Value: "_"}
}

func isPathStart(k Kind) bool {
	switch k {
	case Ident, KwSelfValue, KwSelfType, KwSuper, KwCrate, Lt:
		return true
	}
	return false
}

// modPath parses a plain path without generic arguments (attributes).
func (p *parser) modPath() *Path {
	path := &Path{}
	for {
		path.Segments = append(path.Segments, &PathSegment{Name: p.pathName()})
		if !p.got(PathSep) {
			return path
		}
	}
}

// typePath parses a path in type position, where generic arguments follow
// the name directly: Vec<T>, Iterator<Item = u8>, T::Item.
func (p *parser) typePath() *Path { return p.path(false) }

// exprPath parses a path in expression or pattern position, where generic
// arguments need a turbofish: Vec::<T>::new.
func (p *parser) exprPath() *Path { return p.path(true) }

func (p *parser) path(turbofish bool) *Path {
	path := &Path{}
	if p.kind() == Lt {
		path.QSelf = p.qself()
		p.expect(PathSep)
	}
	for {
		seg := &PathSegment{Name: p.pathName()}
		path.Segments = append(path.Segments, seg)
		if !turbofish && p.kind() == Lt {
			seg.Args = p.genericArgs()
		}
		if p.kind() != PathSep {
			return path
		}
		if p.peek(1).Kind == Lt {
			p.next()
			seg.Args = p.genericArgs()
			if p.kind() != PathSep {
				return path
			}
		}
		if !isPathStart(p.peek(1).Kind) || p.peek(1).Kind == Lt {
			return path
		}
		p.next()
	}
}

func (p *parser) qself() *QSelf {
	q := &QSelf{Lt: p.expect(Lt)}
	q.Type = p.typ()
	if p.got(KwAs) {
		q.Trait = p.typePath()
	}
	p.expectGt()
	return q
}

func (p *parser) genericArgs() *GenericArgs {
	ga := &GenericArgs{Lt: p.expect(Lt)}
	for p.kind() != Gt && p.kind() != Shr && p.kind() != Ge && p.kind() != ShrEq && p.kind() != EOF {
		if p.kind() == Ident && p.peek(1).Kind == Eq {
			b := &AssocBinding{Name: p.name()}
			p.next()
			b.Type = p.typ()
			ga.Bindings = append(ga.Bindings, b)
		} else {
			ga.Args = append(ga.Args, p.typ())
		}
		if !p.got(Comma) {
			break
		}
	}
	p.expectGt()
	return ga
}

// ---------------------------------------------------------------------------
// Types

func (p *parser) typ() Type {
	t := p.tok()
	switch t.Kind {
	case And, AndAnd:
		pos := t.Pos
		p.gotAmp()
		r := &RefType{Amp: pos}
		r.Mut = p.got(KwMut)
		r.Elem = p.typ()
		return r
	case Star:
		p.next()
		pt := &PtrType{Star: t.Pos}
		switch {
		case p.got(KwMut):
			pt.Mut = true
		case p.got(KwConst):
		default:
			p.errorf(p.pos(), "expected 'const' or 'mut' after '*' in a pointer type")
		}
		pt.Elem = p.typ()
		return pt
	case LBracket:
		p.next()
		elem := p.typ()
		if p.got(Semi) {
			n := p.expr()
			p.expect(RBracket)
			return &ArrayType{Lbrack: t.Pos, Elem: elem, Len: n}
		}
		p.expect(RBracket)
		return &SliceType{Lbrack: t.Pos, Elem: elem}
	case LParen:
		p.next()
		tt := &TupleType{Lparen: t.Pos}
		trailingComma := false
		for p.kind() != RParen && p.kind() != EOF {
			tt.Elems = append(tt.Elems, p.typ())
			trailingComma = p.got(Comma)
			if !trailingComma {
				break
			}
		}
		p.expect(RParen)
		if len(tt.Elems) == 1 && !trailingComma {
			return tt.Elems[0] // (T) is just T
		}
		return tt
	case KwFn:
		return p.fnType(t.Pos, "")
	case KwExtern:
		p.next()
		abi := p.abi()
		return p.fnType(t.Pos, abi)
	case KwDyn:
		p.next()
		return &DynType{Dyn: t.Pos, Bounds: p.bounds()}
	case KwImpl:
		p.next()
		return &ImplType{Impl: t.Pos, Bounds: p.bounds()}
	case Not:
		p.next()
		return &NeverType{Bang: t.Pos}
	case Underscore:
		p.next()
		return &InferType{Underscore: t.Pos}
	}
	if isPathStart(t.Kind) {
		return &PathType{Path: p.typePath()}
	}
	p.errorf(t.Pos, "expected type, found %s", p.describe(t))
	p.next()
	return &InferType{Underscore: t.Pos}
}

func (p *parser) fnType(start Pos, abi string) *FnType {
	p.expect(KwFn)
	ft := &FnType{Start: start, Abi: abi}
	p.expect(LParen)
	for p.kind() != RParen && p.kind() != EOF {
		ft.Params = append(ft.Params, p.typ())
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RParen)
	if p.got(RArrow) {
		ft.Result = p.typ()
	}
	return ft
}

// ---------------------------------------------------------------------------
// Statements and blocks

func (p *parser) block() *BlockExpr {
	b := &BlockExpr{Lbrace: p.expect(LBrace)}
	old := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = old }()
	for p.kind() != RBrace && p.kind() != EOF {
		start := p.i
		if done := p.stmt(b); done {
			break
		}
		if p.i == start {
			p.errorf(p.pos(), "expected statement, found %s", p.describe(p.tok()))
			p.syncStmt()
			if p.i == start {
				p.next() // always make progress
			}
		}
	}
	b.Rbrace = p.expect(RBrace)
	return b
}

// syncStmt skips to the end of the current statement.
func (p *parser) syncStmt() {
	depth := 0
	for {
		switch p.kind() {
		case EOF:
			return
		case LBrace, LParen, LBracket:
			depth++
		case RBrace, RParen, RBracket:
			if depth == 0 {
				return
			}
			depth--
		case Semi:
			if depth == 0 {
				p.next()
				return
			}
		}
		p.next()
	}
}

// stmt parses one statement into b. It returns true when it parsed the
// block's tail expression.
func (p *parser) stmt(b *BlockExpr) bool {
	switch p.kind() {
	case Semi:
		b.Stmts = append(b.Stmts, &EmptyStmt{Semi: p.next().Pos})
		return false
	case KwLet:
		b.Stmts = append(b.Stmts, p.letStmt())
		return false
	case KwDefer:
		b.Stmts = append(b.Stmts, p.deferStmt())
		return false
	}
	if isItemStart(p) && !(p.kind() == KwConst && p.peek(1).Kind == LBrace) {
		if it := p.item(); it != nil {
			b.Stmts = append(b.Stmts, &ItemStmt{Item: it})
		}
		return false
	}

	x, blockLike := p.exprStmtHead()
	switch {
	case p.got(Semi):
		b.Stmts = append(b.Stmts, &ExprStmt{X: x, Semi: true})
	case p.kind() == RBrace:
		b.Tail = x
		return true
	case blockLike:
		b.Stmts = append(b.Stmts, &ExprStmt{X: x})
	default:
		p.errorf(p.pos(), "expected ';' or '}' after expression, found %s", p.describe(p.tok()))
		p.syncStmt()
	}
	return false
}

// exprStmtHead parses an expression in statement position. A block-like
// expression (if, match, loop, a block, ...) ends the statement unless it is
// followed by a method call, field access or '?'.
func (p *parser) exprStmtHead() (Expr, bool) {
	if !p.startsBlockLike() {
		return p.expr(), false
	}
	x := p.blockLikeExpr()
	if k := p.kind(); k == Dot || k == Question {
		x = p.postfix(x)
		return p.exprFrom(x), false
	}
	return x, true
}

func (p *parser) startsBlockLike() bool {
	switch p.kind() {
	case LBrace, KwIf, KwMatch, KwLoop, KwWhile, KwFor, KwSelect:
		return true
	case KwUnsafe:
		return p.peek(1).Kind == LBrace
	case Lifetime:
		return p.peek(1).Kind == Colon
	}
	return false
}

func (p *parser) letStmt() *LetStmt {
	s := &LetStmt{Let: p.next().Pos}
	s.Pat = p.pat()
	if p.got(Colon) {
		s.Type = p.typ()
	}
	if p.got(Eq) {
		s.Init = p.expr()
		if p.kind() == KwElse {
			p.next()
			s.Else = p.block()
		}
	}
	p.expect(Semi)
	return s
}

func (p *parser) deferStmt() *DeferStmt {
	s := &DeferStmt{Defer: p.next().Pos}
	if p.kind() == LBrace {
		s.X = p.block()
		p.got(Semi)
		return s
	}
	s.X = p.expr()
	switch s.X.(type) {
	case *CallExpr, *MethodCallExpr:
	default:
		p.errorf(s.X.Pos(), "defer needs a function call or a block")
	}
	p.expect(Semi)
	return s
}

// ---------------------------------------------------------------------------
// Expressions

// Binary operator precedence, higher binds tighter.
const (
	precOrOr = 1 + iota
	precAndAnd
	precCompare
	precBitOr
	precBitXor
	precBitAnd
	precShift
	precAdd
	precMul
	precCast
)

func binaryPrec(k Kind) int {
	switch k {
	case OrOr:
		return precOrOr
	case AndAnd:
		return precAndAnd
	case EqEq, Ne, Lt, Gt, Le, Ge:
		return precCompare
	case Or:
		return precBitOr
	case Caret:
		return precBitXor
	case And:
		return precBitAnd
	case Shl, Shr:
		return precShift
	case Plus, Minus:
		return precAdd
	case Star, Slash, Percent:
		return precMul
	case KwAs:
		return precCast
	}
	return 0
}

func isAssignOp(k Kind) bool {
	switch k {
	case Eq, PlusEq, MinusEq, StarEq, SlashEq, PercentEq, CaretEq, AndEq, OrEq, ShlEq, ShrEq:
		return true
	}
	return false
}

// expr parses a full expression including assignment.
func (p *parser) expr() Expr {
	return p.assign()
}

// exprNoStruct parses an expression in which `name {` is not a struct literal.
func (p *parser) exprNoStruct() Expr {
	old := p.noStructLit
	p.noStructLit = true
	x := p.expr()
	p.noStructLit = old
	return x
}

func (p *parser) assign() Expr {
	x := p.rangeExpr()
	if isAssignOp(p.kind()) {
		t := p.next()
		y := p.assign()
		return &AssignExpr{Op: t.Kind, OpPos: t.Pos, X: x, Y: y}
	}
	return x
}

// exprFrom continues parsing binary operators, ranges and assignment after an
// already parsed operand.
func (p *parser) exprFrom(x Expr) Expr {
	x = p.binaryFrom(x, 1)
	if k := p.kind(); k == DotDot || k == DotDotEq {
		x = p.rangeFrom(x)
	}
	if isAssignOp(p.kind()) {
		t := p.next()
		y := p.assign()
		return &AssignExpr{Op: t.Kind, OpPos: t.Pos, X: x, Y: y}
	}
	return x
}

func (p *parser) rangeExpr() Expr {
	if k := p.kind(); k == DotDot || k == DotDotEq {
		return p.rangeFrom(nil)
	}
	x := p.binary(1)
	if k := p.kind(); k == DotDot || k == DotDotEq {
		return p.rangeFrom(x)
	}
	return x
}

func (p *parser) rangeFrom(lo Expr) Expr {
	t := p.next()
	r := &RangeExpr{OpPos: t.Pos, Lo: lo, Inclusive: t.Kind == DotDotEq}
	if p.startsExpr() {
		r.Hi = p.binary(1)
	} else if r.Inclusive {
		p.errorf(p.pos(), "inclusive range '..=' needs an upper bound")
	}
	return r
}

// startsExpr reports whether the current token can begin an operand. It is
// used for optional operands (range ends, break/return values).
func (p *parser) startsExpr() bool {
	switch p.kind() {
	case Ident, Int, Float, Char, Byte, String, RawString, ByteString, Template,
		KwTrue, KwFalse, KwSelfValue, KwSelfType, KwSuper, KwCrate,
		LParen, LBracket, Minus, Not, Star, And, AndAnd, Or, OrOr, Lt,
		KwIf, KwMatch, KwLoop, KwWhile, KwFor, KwUnsafe, KwReturn, KwBreak, KwContinue, KwSelect, Lifetime:
		return true
	case LBrace:
		return !p.noStructLit
	}
	return false
}

func (p *parser) binary(minPrec int) Expr {
	return p.binaryFrom(p.unary(), minPrec)
}

func (p *parser) binaryFrom(x Expr, minPrec int) Expr {
	for {
		t := p.tok()
		prec := binaryPrec(t.Kind)
		if prec == 0 || prec < minPrec {
			return x
		}
		p.next()
		if t.Kind == KwAs {
			x = &CastExpr{X: x, Type: p.typ()}
			continue
		}
		y := p.binary(prec + 1)
		x = &BinaryExpr{Op: t.Kind, OpPos: t.Pos, X: x, Y: y}
		if prec == precCompare && binaryPrec(p.kind()) == precCompare {
			p.errorf(p.pos(), "comparison operators cannot be chained; use parentheses")
		}
	}
}

func (p *parser) unary() Expr {
	t := p.tok()
	switch t.Kind {
	case Minus, Not, Star:
		p.next()
		return &UnaryExpr{OpPos: t.Pos, Op: t.Kind, X: p.unary()}
	case And, AndAnd:
		p.gotAmp()
		r := &RefExpr{Amp: t.Pos}
		r.Mut = p.got(KwMut)
		r.X = p.unary()
		return r
	}
	return p.postfix(p.primary())
}

func (p *parser) postfix(x Expr) Expr {
	for {
		switch p.kind() {
		case Dot:
			p.next()
			t := p.tok()
			switch t.Kind {
			case Int:
				p.next()
				x = &FieldExpr{X: x, Name: &Name{NamePos: t.Pos, Value: t.Text}}
				continue
			case Ident:
				p.next()
			default:
				p.errorf(t.Pos, "expected field or method name, found %s", p.describe(t))
				return x
			}
			name := &Name{NamePos: t.Pos, Value: t.Text}
			var targs *GenericArgs
			if p.kind() == PathSep && p.peek(1).Kind == Lt {
				p.next()
				targs = p.genericArgs()
			}
			if p.kind() == LParen {
				x = &MethodCallExpr{Recv: x, Name: name, TypeArgs: targs, Args: p.callArgs()}
			} else {
				if targs != nil {
					p.errorf(targs.Lt, "field access cannot have generic arguments")
				}
				x = &FieldExpr{X: x, Name: name}
			}
		case LParen:
			lp := p.pos()
			x = &CallExpr{Fun: x, Lparen: lp, Args: p.callArgs()}
		case LBracket:
			lb := p.next().Pos
			old := p.noStructLit
			p.noStructLit = false
			idx := p.expr()
			p.noStructLit = old
			p.expect(RBracket)
			x = &IndexExpr{X: x, Lbrack: lb, Index: idx}
		case Question:
			x = &TryExpr{X: x, Question: p.next().Pos}
		default:
			return x
		}
	}
}

func (p *parser) callArgs() []Expr {
	p.expect(LParen)
	old := p.noStructLit
	p.noStructLit = false
	var args []Expr
	for p.kind() != RParen && p.kind() != EOF {
		args = append(args, p.expr())
		if !p.got(Comma) {
			break
		}
	}
	p.noStructLit = old
	p.expect(RParen)
	return args
}

func (p *parser) primary() Expr {
	t := p.tok()
	switch t.Kind {
	case Int:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitInt, Value: t.Text}
	case Float:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitFloat, Value: t.Text}
	case Char:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitChar, Value: t.Text}
	case Byte:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitByte, Value: t.Text}
	case String, RawString:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitString, Value: t.Text}
	case ByteString:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitByteString, Value: t.Text}
	case KwTrue, KwFalse:
		p.next()
		return &LitExpr{ValuePos: t.Pos, Kind: LitBool, Value: t.Kind.String()}
	case Template:
		p.next()
		return p.template(t)
	case LParen:
		return p.parenOrTuple()
	case LBracket:
		return p.array()
	case Or, OrOr:
		return p.closure()
	case KwReturn:
		p.next()
		r := &ReturnExpr{Return: t.Pos}
		if p.startsExpr() {
			r.Value = p.expr()
		}
		return r
	case KwBreak:
		p.next()
		b := &BreakExpr{Break: t.Pos}
		if p.kind() == Lifetime {
			lt := p.next()
			b.Label = &Name{NamePos: lt.Pos, Value: lt.Text}
		}
		if p.startsExpr() {
			b.Value = p.expr()
		}
		return b
	case KwContinue:
		p.next()
		c := &ContinueExpr{Continue: t.Pos}
		if p.kind() == Lifetime {
			lt := p.next()
			c.Label = &Name{NamePos: lt.Pos, Value: lt.Text}
		}
		return c
	}
	if p.startsBlockLike() {
		return p.blockLikeExpr()
	}
	if isPathStart(t.Kind) {
		path := p.exprPath()
		if p.kind() == LBrace && !p.noStructLit {
			return p.structLit(path)
		}
		return &PathExpr{Path: path}
	}
	p.errorf(t.Pos, "expected expression, found %s", p.describe(t))
	if t.Kind != EOF && t.Kind != RBrace && t.Kind != RParen && t.Kind != RBracket && t.Kind != Semi {
		p.next()
	}
	return &BadExpr{From: t.Pos}
}

func (p *parser) blockLikeExpr() Expr {
	var label *Name
	if p.kind() == Lifetime {
		lt := p.next()
		label = &Name{NamePos: lt.Pos, Value: lt.Text}
		p.expect(Colon)
	}
	t := p.tok()
	switch t.Kind {
	case LBrace:
		if label != nil {
			p.errorf(label.NamePos, "labels are only allowed on loops")
		}
		return p.block()
	case KwUnsafe:
		p.next()
		b := p.block()
		b.Unsafe = true
		return b
	case KwIf:
		return p.ifExpr()
	case KwMatch:
		return p.matchExpr()
	case KwLoop:
		p.next()
		return &LoopExpr{Label: label, Loop: t.Pos, Body: p.block()}
	case KwWhile:
		p.next()
		w := &WhileExpr{Label: label, While: t.Pos}
		w.Cond = p.cond()
		w.Body = p.block()
		return w
	case KwFor:
		p.next()
		f := &ForExpr{Label: label, For: t.Pos}
		f.Pat = p.pat()
		p.expect(KwIn)
		f.Iter = p.exprNoStruct()
		f.Body = p.block()
		return f
	case KwSelect:
		return p.selectExpr()
	}
	p.errorf(t.Pos, "expected loop after label, found %s", p.describe(t))
	return &BadExpr{From: t.Pos}
}

// cond parses the condition of if/while, which may be `let pat = expr`.
func (p *parser) cond() Expr {
	if p.kind() == KwLet {
		l := &LetExpr{Let: p.next().Pos}
		l.Pat = p.pat()
		p.expect(Eq)
		l.X = p.exprNoStruct()
		return l
	}
	return p.exprNoStruct()
}

func (p *parser) ifExpr() *IfExpr {
	x := &IfExpr{If: p.expect(KwIf)}
	x.Cond = p.cond()
	x.Then = p.block()
	if p.got(KwElse) {
		if p.kind() == KwIf {
			x.Else = p.ifExpr()
		} else {
			x.Else = p.block()
		}
	}
	return x
}

func (p *parser) matchExpr() *MatchExpr {
	m := &MatchExpr{Match: p.expect(KwMatch)}
	m.X = p.exprNoStruct()
	p.expect(LBrace)
	old := p.noStructLit
	p.noStructLit = false
	for p.kind() != RBrace && p.kind() != EOF {
		arm := &MatchArm{}
		p.got(Or) // leading |
		arm.Pat = p.pat()
		if p.got(KwIf) {
			arm.Guard = p.expr()
		}
		p.expect(FatArrow)
		body, blockLike := p.exprStmtHead()
		arm.Body = body
		m.Arms = append(m.Arms, arm)
		if !p.got(Comma) && !blockLike && p.kind() != RBrace {
			p.errorf(p.pos(), "expected ',' after match arm, found %s", p.describe(p.tok()))
			break
		}
	}
	p.noStructLit = old
	p.expect(RBrace)
	return m
}

func (p *parser) selectExpr() *SelectExpr {
	s := &SelectExpr{Select: p.expect(KwSelect)}
	p.expect(LBrace)
	old := p.noStructLit
	p.noStructLit = false
	for p.kind() != RBrace && p.kind() != EOF {
		arm := &SelectArm{}
		switch {
		case p.kind() == Ident && p.tok().Text == "default" && p.peek(1).Kind == FatArrow:
			p.next()
			arm.Default = true
		case p.selectBinding():
			arm.Pat = p.patNoTopAlt()
			p.expect(Eq)
			arm.Op = p.expr()
		default:
			arm.Op = p.expr()
		}
		if arm.Op != nil {
			if _, ok := arm.Op.(*MethodCallExpr); !ok {
				p.errorf(arm.Op.Pos(), "select case must be a recv() or send() call")
			}
		}
		p.expect(FatArrow)
		body, blockLike := p.exprStmtHead()
		arm.Body = body
		s.Arms = append(s.Arms, arm)
		if !p.got(Comma) && !blockLike && p.kind() != RBrace {
			p.errorf(p.pos(), "expected ',' after select case, found %s", p.describe(p.tok()))
			break
		}
	}
	p.noStructLit = old
	p.expect(RBrace)
	return s
}

// selectBinding reports whether a select case starts with `pat =`.
func (p *parser) selectBinding() bool {
	switch p.kind() {
	case Ident, Underscore:
		return p.peek(1).Kind == Eq
	case KwMut:
		return p.peek(1).Kind == Ident && p.peek(2).Kind == Eq
	}
	return false
}

func (p *parser) parenOrTuple() Expr {
	lp := p.expect(LParen)
	old := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = old }()
	if p.got(RParen) {
		return &TupleExpr{Lparen: lp}
	}
	first := p.expr()
	if p.got(RParen) {
		return &ParenExpr{Lparen: lp, X: first}
	}
	t := &TupleExpr{Lparen: lp, Elems: []Expr{first}}
	for p.got(Comma) && p.kind() != RParen {
		t.Elems = append(t.Elems, p.expr())
	}
	p.expect(RParen)
	return t
}

func (p *parser) array() Expr {
	lb := p.expect(LBracket)
	old := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = old }()
	if p.got(RBracket) {
		return &ArrayExpr{Lbrack: lb}
	}
	first := p.expr()
	if p.got(Semi) {
		count := p.expr()
		p.expect(RBracket)
		return &ArrayRepeatExpr{Lbrack: lb, Value: first, Count: count}
	}
	a := &ArrayExpr{Lbrack: lb, Elems: []Expr{first}}
	for p.got(Comma) && p.kind() != RBracket {
		a.Elems = append(a.Elems, p.expr())
	}
	p.expect(RBracket)
	return a
}

func (p *parser) structLit(path *Path) Expr {
	s := &StructLit{Path: path}
	p.expect(LBrace)
	for p.kind() != RBrace && p.kind() != EOF {
		if p.got(DotDot) {
			s.Base = p.expr()
			break
		}
		var name *Name
		if t := p.tok(); t.Kind == Int {
			p.next()
			name = &Name{NamePos: t.Pos, Value: t.Text}
		} else {
			name = p.name()
		}
		fi := &FieldInit{Name: name}
		if p.got(Colon) {
			fi.Value = p.expr()
		} else {
			fi.Value = &PathExpr{Path: &Path{Segments: []*PathSegment{{Name: name}}}}
		}
		s.Fields = append(s.Fields, fi)
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return s
}

func (p *parser) closure() Expr {
	c := &ClosureExpr{Or: p.pos()}
	if !p.got(OrOr) {
		p.expect(Or)
		for p.kind() != Or && p.kind() != EOF {
			param := &Param{Pat: p.patNoTopAlt()}
			if p.got(Colon) {
				param.Type = p.typ()
			}
			c.Params = append(c.Params, param)
			if !p.got(Comma) {
				break
			}
		}
		p.expect(Or)
	}
	if p.got(RArrow) {
		c.Result = p.typ()
		if p.kind() != LBrace {
			p.errorf(p.pos(), "closure with a return type needs a block body")
		}
		c.Body = p.block()
		return c
	}
	c.Body = p.expr()
	return c
}

// ---------------------------------------------------------------------------
// Template strings

// template splits a template token into literal text and ${...} holes.
func (p *parser) template(t Token) Expr {
	x := &TemplateExpr{Backtick: t.Pos}
	src := p.file.Src
	end := int(t.End)
	if end > int(t.Pos)+1 && src[end-1] == '`' {
		end--
	}
	var text strings.Builder
	i := int(t.Pos) + 1
	for i < end {
		c := src[i]
		if c == '\\' && i+1 < end {
			text.WriteByte(c)
			text.WriteByte(src[i+1])
			i += 2
			continue
		}
		if c == '$' && i+1 < end && src[i+1] == '{' {
			if text.Len() > 0 {
				x.Parts = append(x.Parts, &TemplatePart{Text: text.String()})
				text.Reset()
			}
			part, next := p.templateHole(i)
			x.Parts = append(x.Parts, part)
			i = next
			continue
		}
		text.WriteByte(c)
		i++
	}
	if text.Len() > 0 {
		x.Parts = append(x.Parts, &TemplatePart{Text: text.String()})
	}
	return x
}

// templateHole parses the hole starting at src[start] == '$' and returns the
// part plus the offset just past its closing '}'.
func (p *parser) templateHole(start int) (*TemplatePart, int) {
	sub := &Errors{File: p.file}
	l := newLexer(p.file.Src, 0, sub)
	l.off = start + 2
	var toks []Token
	depth := 0
	colon := -1
	for {
		tk := l.next()
		if tk.Kind == EOF {
			p.errorf(Pos(start), "unterminated ${ in template string")
			return &TemplatePart{X: &BadExpr{From: Pos(start)}}, len(p.file.Src)
		}
		switch tk.Kind {
		case LParen, LBracket, LBrace:
			depth++
		case RParen, RBracket:
			depth--
		case RBrace:
			if depth == 0 {
				part := &TemplatePart{}
				exprToks := toks
				if colon >= 0 {
					exprToks = toks[:colon]
					part.Spec = strings.TrimSpace(string(p.file.Src[toks[colon].End:tk.Pos]))
					p.checkSpec(toks[colon].End, part.Spec)
				}
				part.X = p.subExpr(Pos(start), exprToks, tk.Pos)
				return part, int(tk.End)
			}
			depth--
		case Colon:
			if depth == 0 && colon < 0 {
				colon = len(toks)
			}
		}
		toks = append(toks, tk)
	}
}

// subExpr parses toks as a single expression.
func (p *parser) subExpr(start Pos, toks []Token, end Pos) Expr {
	if len(toks) == 0 {
		p.errorf(start, "empty ${} in template string")
		return &BadExpr{From: start}
	}
	sp := &parser{file: p.file, errs: p.errs}
	sp.toks = append(append([]Token(nil), toks...), Token{Kind: EOF, Pos: end, End: end})
	x := sp.expr()
	if sp.kind() != EOF {
		p.errorf(sp.pos(), "unexpected %s in ${...}", sp.describe(sp.tok()))
	}
	return x
}

// checkSpec validates a format spec: [[fill]align][+]['#']['0'][width]['.' precision][type].
func (p *parser) checkSpec(pos Pos, spec string) {
	s := spec
	isAlign := func(c byte) bool { return c == '<' || c == '^' || c == '>' }
	if len(s) >= 2 && isAlign(s[1]) {
		s = s[2:]
	} else if len(s) >= 1 && isAlign(s[0]) {
		s = s[1:]
	}
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimPrefix(s, "#")
	s = strings.TrimPrefix(s, "0")
	for len(s) > 0 && isDigit(s[0]) {
		s = s[1:]
	}
	if strings.HasPrefix(s, ".") {
		s = s[1:]
		n := 0
		for len(s) > 0 && isDigit(s[0]) {
			s = s[1:]
			n++
		}
		if n == 0 {
			p.errorf(pos, "format spec %q: missing precision after '.'", spec)
			return
		}
	}
	switch s {
	case "", "?", "x", "X", "o", "b", "e", "E":
	default:
		p.errorf(pos, "invalid format spec %q", spec)
	}
}

// ---------------------------------------------------------------------------
// Patterns

// pat parses a pattern including top-level alternatives a | b.
func (p *parser) pat() Pat {
	first := p.patNoTopAlt()
	if p.kind() != Or {
		return first
	}
	or := &OrPat{Alts: []Pat{first}}
	for p.got(Or) {
		or.Alts = append(or.Alts, p.patNoTopAlt())
	}
	return or
}

func (p *parser) patNoTopAlt() Pat {
	t := p.tok()
	switch t.Kind {
	case Underscore:
		p.next()
		return &WildPat{Underscore: t.Pos}
	case DotDot:
		p.next()
		return &RestPat{DotDot: t.Pos}
	case DotDotEq:
		p.next()
		return &RangePat{Hi: p.patRangeEnd(), Inclusive: true, OpPos: t.Pos}
	case And, AndAnd:
		p.gotAmp()
		return &RefPat{Amp: t.Pos, Pat: p.patNoTopAlt()}
	case LParen:
		p.next()
		tp := &TuplePat{Lparen: t.Pos}
		trailingComma := false
		for p.kind() != RParen && p.kind() != EOF {
			tp.Elems = append(tp.Elems, p.pat())
			trailingComma = p.got(Comma)
			if !trailingComma {
				break
			}
		}
		p.expect(RParen)
		if len(tp.Elems) == 1 && !trailingComma {
			if _, rest := tp.Elems[0].(*RestPat); !rest {
				return tp.Elems[0] // (p) is just p
			}
		}
		return tp
	case LBracket:
		p.next()
		sp := &SlicePat{Lbrack: t.Pos}
		for p.kind() != RBracket && p.kind() != EOF {
			sp.Elems = append(sp.Elems, p.pat())
			if !p.got(Comma) {
				break
			}
		}
		p.expect(RBracket)
		return sp
	case KwMut:
		p.next()
		ip := &IdentPat{Mut: true, Name: p.name()}
		if p.got(At) {
			ip.Sub = p.patNoTopAlt()
		}
		return ip
	case Minus, Int, Float, Char, Byte, String, RawString, ByteString, KwTrue, KwFalse:
		lit := p.patLit()
		if k := p.kind(); k == DotDot || k == DotDotEq {
			return p.patRange(lit)
		}
		return &LitPat{X: lit}
	}
	if isPathStart(t.Kind) {
		// A lone identifier is a binding (the resolver turns it into a
		// constant or unit variant if that is what the name refers to).
		if t.Kind == Ident && p.peek(1).Kind != PathSep && p.peek(1).Kind != LParen &&
			p.peek(1).Kind != LBrace && p.peek(1).Kind != DotDot && p.peek(1).Kind != DotDotEq {
			p.next()
			ip := &IdentPat{Name: &Name{NamePos: t.Pos, Value: t.Text}}
			if p.got(At) {
				ip.Sub = p.patNoTopAlt()
			}
			return ip
		}
		path := p.exprPath()
		switch p.kind() {
		case LParen:
			p.next()
			tsp := &TupleStructPat{Path: path}
			for p.kind() != RParen && p.kind() != EOF {
				tsp.Elems = append(tsp.Elems, p.pat())
				if !p.got(Comma) {
					break
				}
			}
			p.expect(RParen)
			return tsp
		case LBrace:
			return p.structPat(path)
		case DotDot, DotDotEq:
			return p.patRange(&PathExpr{Path: path})
		}
		return &PathPat{Path: path}
	}
	p.errorf(t.Pos, "expected pattern, found %s", p.describe(t))
	if t.Kind != EOF && t.Kind != RBrace {
		p.next()
	}
	return &WildPat{Underscore: t.Pos}
}

func (p *parser) patLit() Expr {
	t := p.tok()
	if t.Kind == Minus {
		p.next()
		lit := p.patLit()
		return &UnaryExpr{OpPos: t.Pos, Op: Minus, X: lit}
	}
	switch t.Kind {
	case Int, Float, Char, Byte, String, RawString, ByteString, KwTrue, KwFalse:
		return p.primary()
	}
	p.errorf(t.Pos, "expected literal, found %s", p.describe(t))
	return &BadExpr{From: t.Pos}
}

func (p *parser) patRange(lo Expr) Pat {
	t := p.next()
	r := &RangePat{Lo: lo, Inclusive: t.Kind == DotDotEq, OpPos: t.Pos}
	switch p.kind() {
	case Minus, Int, Float, Char, Byte, Ident, KwSelfType, KwCrate, KwSuper:
		r.Hi = p.patRangeEnd()
	default:
		if r.Inclusive {
			p.errorf(p.pos(), "inclusive range pattern needs an upper bound")
		}
	}
	return r
}

func (p *parser) patRangeEnd() Expr {
	if isPathStart(p.kind()) {
		return &PathExpr{Path: p.exprPath()}
	}
	return p.patLit()
}

func (p *parser) structPat(path *Path) Pat {
	sp := &StructPat{Path: path}
	p.expect(LBrace)
	for p.kind() != RBrace && p.kind() != EOF {
		if p.got(DotDot) {
			sp.Rest = true
			break
		}
		fp := &FieldPat{}
		if p.kind() == KwMut {
			p.next()
			fp.Name = p.name()
			fp.Pat = &IdentPat{Mut: true, Name: fp.Name}
		} else {
			if t := p.tok(); t.Kind == Int {
				p.next()
				fp.Name = &Name{NamePos: t.Pos, Value: t.Text}
			} else {
				fp.Name = p.name()
			}
			if p.got(Colon) {
				fp.Pat = p.pat()
			} else {
				fp.Pat = &IdentPat{Name: fp.Name}
			}
		}
		sp.Fields = append(sp.Fields, fp)
		if !p.got(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return sp
}
