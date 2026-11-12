package syntax

// Node is implemented by every AST node.
type Node interface {
	Pos() Pos
}

// SourceFile is the AST of one .navi file.
type SourceFile struct {
	File  *File
	Attrs []*Attr // inner attributes (#![...])
	Items []Item
}

// ---------------------------------------------------------------------------
// Common pieces

// Name is an identifier with its position.
type Name struct {
	NamePos Pos
	Value   string
}

func (n *Name) Pos() Pos { return n.NamePos }

// Attr is #[path] / #[path(args)] / #[path = value]. Args keeps the tokens
// inside the parentheses; attributes are interpreted by later phases.
type Attr struct {
	Hash  Pos
	Inner bool
	Path  *Path
	Args  []Token // tokens between ( and ), without the parentheses
	Value Expr    // for #[name = value]
}

func (a *Attr) Pos() Pos { return a.Hash }

// VisKind is the visibility of an item or field.
type VisKind uint8

const (
	VisPrivate VisKind = iota
	VisPub             // pub
	VisCrate           // pub(crate)
	VisSuper           // pub(super)
)

// Path is a::b::<T>::c. Each segment may carry generic arguments.
type Path struct {
	Global   bool // reserved for a leading :: (not allowed yet)
	Segments []*PathSegment
	QSelf    *QSelf // <T as Trait>::rest
}

func (p *Path) Pos() Pos {
	if p.QSelf != nil {
		return p.QSelf.Lt
	}
	return p.Segments[0].Name.NamePos
}

// PathSegment is one name in a path. Keywords such as self, Self, super and
// crate are stored by their spelling.
type PathSegment struct {
	Name *Name
	Args *GenericArgs // nil when absent
}

// QSelf is the <Type as Trait> prefix of a qualified path.
type QSelf struct {
	Lt    Pos
	Type  Type
	Trait *Path // nil for <Type>::name
}

// GenericArgs is <A, B, Item = C>.
type GenericArgs struct {
	Lt       Pos
	Args     []Type
	Bindings []*AssocBinding
}

// AssocBinding is Item = T inside generic arguments.
type AssocBinding struct {
	Name *Name
	Type Type
}

// Generics is the <T: Bound, U> list on an item plus its where clause.
type Generics struct {
	Lt     Pos // NoPos if there is no parameter list
	Params []*GenericParam
	Where  []*WherePredicate
}

// GenericParam is a type parameter T: A + B.
type GenericParam struct {
	Name   *Name
	Bounds []*Path
}

// WherePredicate is Type: A + B in a where clause.
type WherePredicate struct {
	Type   Type
	Bounds []*Path
}

// ---------------------------------------------------------------------------
// Items

// Item is a top-level or nested declaration.
type Item interface {
	Node
	itemNode()
}

// ItemBase holds what every item has.
type ItemBase struct {
	Attrs []*Attr
	Vis   VisKind
	Start Pos
}

func (b *ItemBase) Pos() Pos        { return b.Start }
func (*ItemBase) itemNode()         {}
func (b *ItemBase) Base() *ItemBase { return b }

// FnDecl is a function, method or extern function.
type FnDecl struct {
	ItemBase
	Name     *Name
	Const    bool
	Unsafe   bool
	Abi      string // "C" for extern "C" fn, empty otherwise
	Generics *Generics
	Self     *SelfParam // nil for free functions
	Params   []*Param
	Result   Type       // nil for ()
	Body     *BlockExpr // nil for declarations without a body
}

// SelfParam is self, &self, &mut self or mut self.
type SelfParam struct {
	SelfPos Pos
	Ref     bool
	Mut     bool // &mut self, or mut self when !Ref
}

// Param is a function parameter.
type Param struct {
	Pat  Pat
	Type Type
}

// StructDecl is a struct with named fields, a tuple struct or a unit struct.
type StructDecl struct {
	ItemBase
	Name     *Name
	Generics *Generics
	Kind     StructKind
	Fields   []*Field
}

// StructKind distinguishes the three struct shapes.
type StructKind uint8

const (
	StructNamed StructKind = iota // struct S { a: T }
	StructTuple                   // struct S(T);
	StructUnit                    // struct S;
)

// Field is a struct or variant field. Name is nil for tuple fields.
type Field struct {
	Attrs []*Attr
	Vis   VisKind
	Name  *Name
	Type  Type
}

// EnumDecl is an enum.
type EnumDecl struct {
	ItemBase
	Name     *Name
	Generics *Generics
	Variants []*Variant
}

// Variant is one enum variant.
type Variant struct {
	Attrs        []*Attr
	Name         *Name
	Kind         StructKind
	Fields       []*Field
	Discriminant Expr // = value, or nil
}

// TypeAlias is type Name<T> = Type; In a trait it may have no Type.
type TypeAlias struct {
	ItemBase
	Name     *Name
	Generics *Generics
	Bounds   []*Path // type Item: Bound; (in traits)
	Type     Type    // nil for an associated type declaration
}

// ConstDecl is const NAME: T = value;
type ConstDecl struct {
	ItemBase
	Name  *Name
	Type  Type
	Value Expr // nil for an associated const declaration in a trait
}

// StaticDecl is static [mut] NAME: T = value;
type StaticDecl struct {
	ItemBase
	Name  *Name
	Mut   bool
	Type  Type
	Value Expr
}

// ImplDecl is impl<T> [Trait for] Type { ... }
type ImplDecl struct {
	ItemBase
	Unsafe   bool
	Generics *Generics
	Trait    *Path // nil for an inherent impl
	Type     Type
	Items    []Item
}

// TraitDecl is trait Name<T>: Super { ... }
type TraitDecl struct {
	ItemBase
	Unsafe   bool
	Name     *Name
	Generics *Generics
	Supers   []*Path
	Items    []Item
}

// ModDecl is mod name; or mod name { ... }.
type ModDecl struct {
	ItemBase
	Name   *Name
	Inline bool
	Items  []Item
}

// UseDecl is use tree;
type UseDecl struct {
	ItemBase
	Tree *UseTree
}

// UseTree is a::b, a::b as c, a::*, a::{x, y}.
type UseTree struct {
	Prefix   *Path // may be nil for {a, b} at the top level of a group
	Alias    *Name // as name
	Glob     bool  // ::*
	Children []*UseTree
	Group    bool // ::{...}
}

// ExternBlock is extern "C" { fn ...; }
type ExternBlock struct {
	ItemBase
	Abi   string
	Items []Item
}

// ---------------------------------------------------------------------------
// Types

// Type is a type expression.
type Type interface {
	Node
	typeNode()
}

type (
	// PathType is a named type such as Vec<T> or T::Item.
	PathType struct{ Path *Path }

	// RefType is &T or &mut T.
	RefType struct {
		Amp  Pos
		Mut  bool
		Elem Type
	}

	// PtrType is *const T or *mut T.
	PtrType struct {
		Star Pos
		Mut  bool
		Elem Type
	}

	// SliceType is [T].
	SliceType struct {
		Lbrack Pos
		Elem   Type
	}

	// ArrayType is [T; N].
	ArrayType struct {
		Lbrack Pos
		Elem   Type
		Len    Expr
	}

	// TupleType is (A, B); () is the unit type.
	TupleType struct {
		Lparen Pos
		Elems  []Type
	}

	// FnType is fn(A) -> B or extern "C" fn(A) -> B.
	FnType struct {
		Start  Pos
		Abi    string
		Params []Type
		Result Type // nil for ()
	}

	// DynType is dyn A + B.
	DynType struct {
		Dyn    Pos
		Bounds []*Path
	}

	// ImplType is impl A + B.
	ImplType struct {
		Impl   Pos
		Bounds []*Path
	}

	// NeverType is !.
	NeverType struct{ Bang Pos }

	// InferType is _.
	InferType struct{ Underscore Pos }
)

func (t *PathType) Pos() Pos  { return t.Path.Pos() }
func (t *RefType) Pos() Pos   { return t.Amp }
func (t *PtrType) Pos() Pos   { return t.Star }
func (t *SliceType) Pos() Pos { return t.Lbrack }
func (t *ArrayType) Pos() Pos { return t.Lbrack }
func (t *TupleType) Pos() Pos { return t.Lparen }
func (t *FnType) Pos() Pos    { return t.Start }
func (t *DynType) Pos() Pos   { return t.Dyn }
func (t *ImplType) Pos() Pos  { return t.Impl }
func (t *NeverType) Pos() Pos { return t.Bang }
func (t *InferType) Pos() Pos { return t.Underscore }

func (*PathType) typeNode()  {}
func (*RefType) typeNode()   {}
func (*PtrType) typeNode()   {}
func (*SliceType) typeNode() {}
func (*ArrayType) typeNode() {}
func (*TupleType) typeNode() {}
func (*FnType) typeNode()    {}
func (*DynType) typeNode()   {}
func (*ImplType) typeNode()  {}
func (*NeverType) typeNode() {}
func (*InferType) typeNode() {}

// ---------------------------------------------------------------------------
// Expressions

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// LitKind is the kind of a literal expression.
type LitKind uint8

const (
	LitInt LitKind = iota
	LitFloat
	LitChar
	LitByte
	LitString
	LitByteString
	LitBool
)

type (
	// LitExpr is a literal. Value is the source text (true/false for bools).
	LitExpr struct {
		ValuePos Pos
		Kind     LitKind
		Value    string
	}

	// TemplateExpr is `text ${expr:spec} text`.
	TemplateExpr struct {
		Backtick Pos
		Parts    []*TemplatePart
	}

	// PathExpr is a variable, function, constant or unit struct/variant.
	PathExpr struct{ Path *Path }

	// UnaryExpr is -x, !x, *x.
	UnaryExpr struct {
		OpPos Pos
		Op    Kind // Minus, Not, Star
		X     Expr
	}

	// RefExpr is &x or &mut x.
	RefExpr struct {
		Amp Pos
		Mut bool
		X   Expr
	}

	// BinaryExpr is x op y, including && and ||.
	BinaryExpr struct {
		Op    Kind
		OpPos Pos
		X, Y  Expr
	}

	// AssignExpr is x = y or x op= y.
	AssignExpr struct {
		Op    Kind // Eq, PlusEq, ...
		OpPos Pos
		X, Y  Expr
	}

	// CastExpr is x as T.
	CastExpr struct {
		X    Expr
		Type Type
	}

	// RangeExpr is a..b, a..=b, ..b, a.., ..
	RangeExpr struct {
		OpPos     Pos
		Lo, Hi    Expr // either may be nil
		Inclusive bool
	}

	// CallExpr is f(args).
	CallExpr struct {
		Fun    Expr
		Lparen Pos
		Args   []Expr
	}

	// MethodCallExpr is x.name::<T>(args).
	MethodCallExpr struct {
		Recv     Expr
		Name     *Name
		TypeArgs *GenericArgs
		Args     []Expr
	}

	// FieldExpr is x.name or x.0.
	FieldExpr struct {
		X    Expr
		Name *Name // the field name or tuple index as text
	}

	// IndexExpr is x[i].
	IndexExpr struct {
		X      Expr
		Lbrack Pos
		Index  Expr
	}

	// TryExpr is x?.
	TryExpr struct {
		X        Expr
		Question Pos
	}

	// StructLit is Path { a: x, b, ..base }.
	StructLit struct {
		Path   *Path
		Fields []*FieldInit
		Base   Expr // ..base, or nil
	}

	// TupleExpr is (a, b) or (); a parenthesized expression is ParenExpr.
	TupleExpr struct {
		Lparen Pos
		Elems  []Expr
	}

	// ParenExpr is (x).
	ParenExpr struct {
		Lparen Pos
		X      Expr
	}

	// ArrayExpr is [a, b, c].
	ArrayExpr struct {
		Lbrack Pos
		Elems  []Expr
	}

	// ArrayRepeatExpr is [x; n].
	ArrayRepeatExpr struct {
		Lbrack Pos
		Value  Expr
		Count  Expr
	}

	// BlockExpr is { stmts; tail } or unsafe { ... }.
	BlockExpr struct {
		Lbrace Pos
		Rbrace Pos
		Unsafe bool
		Stmts  []Stmt
		Tail   Expr // final expression without a semicolon, or nil
	}

	// IfExpr is if cond { } else ... ; Cond is a LetExpr for if let.
	IfExpr struct {
		If   Pos
		Cond Expr
		Then *BlockExpr
		Else Expr // *BlockExpr, *IfExpr or nil
	}

	// LetExpr is let pat = x, only valid as the condition of if/while.
	LetExpr struct {
		Let Pos
		Pat Pat
		X   Expr
	}

	// MatchExpr is match x { arms }.
	MatchExpr struct {
		Match Pos
		X     Expr
		Arms  []*MatchArm
	}

	// LoopExpr is 'label: loop { }.
	LoopExpr struct {
		Label *Name
		Loop  Pos
		Body  *BlockExpr
	}

	// WhileExpr is 'label: while cond { }; Cond is a LetExpr for while let.
	WhileExpr struct {
		Label *Name
		While Pos
		Cond  Expr
		Body  *BlockExpr
	}

	// ForExpr is 'label: for pat in x { }.
	ForExpr struct {
		Label *Name
		For   Pos
		Pat   Pat
		Iter  Expr
		Body  *BlockExpr
	}

	// BreakExpr is break 'label value.
	BreakExpr struct {
		Break Pos
		Label *Name
		Value Expr
	}

	// ContinueExpr is continue 'label.
	ContinueExpr struct {
		Continue Pos
		Label    *Name
	}

	// ReturnExpr is return value.
	ReturnExpr struct {
		Return Pos
		Value  Expr
	}

	// ClosureExpr is |params| -> T { body } or |params| expr.
	ClosureExpr struct {
		Or     Pos
		Params []*Param // Type may be nil
		Result Type
		Body   Expr
	}

	// SelectExpr is select { arms }.
	SelectExpr struct {
		Select Pos
		Arms   []*SelectArm
	}

	// BadExpr stands in for an expression that failed to parse.
	BadExpr struct{ From Pos }
)

// TemplatePart is either literal text or an interpolated expression.
type TemplatePart struct {
	Text string // literal text with escapes still in source form
	X    Expr   // non-nil for ${...}
	Spec string // format spec after the top-level ':', e.g. "?", ">8.3"
}

// FieldInit is name: value or a shorthand name in a struct literal.
type FieldInit struct {
	Name  *Name
	Value Expr // for shorthand this is a PathExpr of Name
}

// MatchArm is pat if guard => body.
type MatchArm struct {
	Pat   Pat
	Guard Expr
	Body  Expr
}

// SelectArm is one select case: pat = rx.recv() => body, tx.send(v) => body,
// or default => body.
type SelectArm struct {
	Pat     Pat  // binding for a receive, or nil
	Op      Expr // the recv/send call; nil for default
	Default bool
	Body    Expr
}

func (x *LitExpr) Pos() Pos         { return x.ValuePos }
func (x *TemplateExpr) Pos() Pos    { return x.Backtick }
func (x *PathExpr) Pos() Pos        { return x.Path.Pos() }
func (x *UnaryExpr) Pos() Pos       { return x.OpPos }
func (x *RefExpr) Pos() Pos         { return x.Amp }
func (x *BinaryExpr) Pos() Pos      { return x.X.Pos() }
func (x *AssignExpr) Pos() Pos      { return x.X.Pos() }
func (x *CastExpr) Pos() Pos        { return x.X.Pos() }
func (x *CallExpr) Pos() Pos        { return x.Fun.Pos() }
func (x *MethodCallExpr) Pos() Pos  { return x.Recv.Pos() }
func (x *FieldExpr) Pos() Pos       { return x.X.Pos() }
func (x *IndexExpr) Pos() Pos       { return x.X.Pos() }
func (x *TryExpr) Pos() Pos         { return x.X.Pos() }
func (x *StructLit) Pos() Pos       { return x.Path.Pos() }
func (x *TupleExpr) Pos() Pos       { return x.Lparen }
func (x *ParenExpr) Pos() Pos       { return x.Lparen }
func (x *ArrayExpr) Pos() Pos       { return x.Lbrack }
func (x *ArrayRepeatExpr) Pos() Pos { return x.Lbrack }
func (x *BlockExpr) Pos() Pos       { return x.Lbrace }
func (x *IfExpr) Pos() Pos          { return x.If }
func (x *LetExpr) Pos() Pos         { return x.Let }
func (x *MatchExpr) Pos() Pos       { return x.Match }
func (x *BreakExpr) Pos() Pos       { return x.Break }
func (x *ContinueExpr) Pos() Pos    { return x.Continue }
func (x *ReturnExpr) Pos() Pos      { return x.Return }
func (x *ClosureExpr) Pos() Pos     { return x.Or }
func (x *SelectExpr) Pos() Pos      { return x.Select }
func (x *BadExpr) Pos() Pos         { return x.From }

func (x *RangeExpr) Pos() Pos {
	if x.Lo != nil {
		return x.Lo.Pos()
	}
	return x.OpPos
}

func (x *LoopExpr) Pos() Pos {
	if x.Label != nil {
		return x.Label.NamePos
	}
	return x.Loop
}

func (x *WhileExpr) Pos() Pos {
	if x.Label != nil {
		return x.Label.NamePos
	}
	return x.While
}

func (x *ForExpr) Pos() Pos {
	if x.Label != nil {
		return x.Label.NamePos
	}
	return x.For
}

func (*LitExpr) exprNode()         {}
func (*TemplateExpr) exprNode()    {}
func (*PathExpr) exprNode()        {}
func (*UnaryExpr) exprNode()       {}
func (*RefExpr) exprNode()         {}
func (*BinaryExpr) exprNode()      {}
func (*AssignExpr) exprNode()      {}
func (*CastExpr) exprNode()        {}
func (*RangeExpr) exprNode()       {}
func (*CallExpr) exprNode()        {}
func (*MethodCallExpr) exprNode()  {}
func (*FieldExpr) exprNode()       {}
func (*IndexExpr) exprNode()       {}
func (*TryExpr) exprNode()         {}
func (*StructLit) exprNode()       {}
func (*TupleExpr) exprNode()       {}
func (*ParenExpr) exprNode()       {}
func (*ArrayExpr) exprNode()       {}
func (*ArrayRepeatExpr) exprNode() {}
func (*BlockExpr) exprNode()       {}
func (*IfExpr) exprNode()          {}
func (*LetExpr) exprNode()         {}
func (*MatchExpr) exprNode()       {}
func (*LoopExpr) exprNode()        {}
func (*WhileExpr) exprNode()       {}
func (*ForExpr) exprNode()         {}
func (*BreakExpr) exprNode()       {}
func (*ContinueExpr) exprNode()    {}
func (*ReturnExpr) exprNode()      {}
func (*ClosureExpr) exprNode()     {}
func (*SelectExpr) exprNode()      {}
func (*BadExpr) exprNode()         {}

// ---------------------------------------------------------------------------
// Statements

// Stmt is a statement inside a block.
type Stmt interface {
	Node
	stmtNode()
}

type (
	// LetStmt is let pat: T = init else { ... };
	LetStmt struct {
		Let  Pos
		Pat  Pat
		Type Type
		Init Expr
		Else *BlockExpr
	}

	// ExprStmt is an expression used as a statement. Semi records whether it
	// ended with a semicolon.
	ExprStmt struct {
		X    Expr
		Semi bool
	}

	// ItemStmt is an item declared inside a block.
	ItemStmt struct{ Item Item }

	// DeferStmt is defer call; or defer { block }.
	DeferStmt struct {
		Defer Pos
		X     Expr // a call/method call, or a *BlockExpr
	}

	// EmptyStmt is a lone ;
	EmptyStmt struct{ Semi Pos }
)

func (s *LetStmt) Pos() Pos   { return s.Let }
func (s *ExprStmt) Pos() Pos  { return s.X.Pos() }
func (s *ItemStmt) Pos() Pos  { return s.Item.Pos() }
func (s *DeferStmt) Pos() Pos { return s.Defer }
func (s *EmptyStmt) Pos() Pos { return s.Semi }

func (*LetStmt) stmtNode()   {}
func (*ExprStmt) stmtNode()  {}
func (*ItemStmt) stmtNode()  {}
func (*DeferStmt) stmtNode() {}
func (*EmptyStmt) stmtNode() {}

// ---------------------------------------------------------------------------
// Patterns

// Pat is a pattern.
type Pat interface {
	Node
	patNode()
}

type (
	// WildPat is _.
	WildPat struct{ Underscore Pos }

	// RestPat is .. inside a tuple or slice pattern.
	RestPat struct{ DotDot Pos }

	// IdentPat is a binding: [mut] name [@ sub].
	IdentPat struct {
		Mut  bool
		Name *Name
		Sub  Pat
	}

	// LitPat is a literal, possibly negated (-1).
	LitPat struct{ X Expr }

	// RangePat is lo..=hi or lo..hi; either end may be nil.
	RangePat struct {
		Lo, Hi    Expr
		Inclusive bool
		OpPos     Pos
	}

	// PathPat is a unit struct, unit variant or constant: None, Color::Red.
	PathPat struct{ Path *Path }

	// TuplePat is (a, b, ..).
	TuplePat struct {
		Lparen Pos
		Elems  []Pat
	}

	// TupleStructPat is Some(x), Shape::Rect(w, h).
	TupleStructPat struct {
		Path  *Path
		Elems []Pat
	}

	// StructPat is Point { x, y: 0, .. }.
	StructPat struct {
		Path   *Path
		Fields []*FieldPat
		Rest   bool
	}

	// SlicePat is [first, .., last].
	SlicePat struct {
		Lbrack Pos
		Elems  []Pat
	}

	// RefPat is &pat.
	RefPat struct {
		Amp Pos
		Pat Pat
	}

	// OrPat is a | b | c.
	OrPat struct{ Alts []Pat }
)

// FieldPat is name: pat or the shorthand [mut] name.
type FieldPat struct {
	Name *Name
	Pat  Pat
}

func (p *WildPat) Pos() Pos        { return p.Underscore }
func (p *RestPat) Pos() Pos        { return p.DotDot }
func (p *IdentPat) Pos() Pos       { return p.Name.NamePos }
func (p *LitPat) Pos() Pos         { return p.X.Pos() }
func (p *PathPat) Pos() Pos        { return p.Path.Pos() }
func (p *TuplePat) Pos() Pos       { return p.Lparen }
func (p *TupleStructPat) Pos() Pos { return p.Path.Pos() }
func (p *StructPat) Pos() Pos      { return p.Path.Pos() }
func (p *SlicePat) Pos() Pos       { return p.Lbrack }
func (p *RefPat) Pos() Pos         { return p.Amp }
func (p *OrPat) Pos() Pos          { return p.Alts[0].Pos() }

func (p *RangePat) Pos() Pos {
	if p.Lo != nil {
		return p.Lo.Pos()
	}
	return p.OpPos
}

func (*WildPat) patNode()        {}
func (*RestPat) patNode()        {}
func (*IdentPat) patNode()       {}
func (*LitPat) patNode()         {}
func (*RangePat) patNode()       {}
func (*PathPat) patNode()        {}
func (*TuplePat) patNode()       {}
func (*TupleStructPat) patNode() {}
func (*StructPat) patNode()      {}
func (*SlicePat) patNode()       {}
func (*RefPat) patNode()         {}
func (*OrPat) patNode()          {}
