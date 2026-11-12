// Package syntax implements the lexer, AST and parser for navi source files.
package syntax

import "fmt"

// Pos is a byte offset into a source file.
type Pos int32

// NoPos marks a node or token without a source position.
const NoPos Pos = -1

// Kind identifies a token.
type Kind uint8

const (
	EOF Kind = iota
	Illegal

	// Literals and names.
	Ident
	Lifetime // 'label (only used for loop labels)
	Int
	Float
	Char
	Byte
	String
	RawString
	ByteString
	Template // `...${expr}...`

	// Punctuation.
	Plus       // +
	Minus      // -
	Star       // *
	Slash      // /
	Percent    // %
	Caret      // ^
	Not        // !
	And        // &
	Or         // |
	AndAnd     // &&
	OrOr       // ||
	Shl        // <<
	Shr        // >>
	PlusEq     // +=
	MinusEq    // -=
	StarEq     // *=
	SlashEq    // /=
	PercentEq  // %=
	CaretEq    // ^=
	AndEq      // &=
	OrEq       // |=
	ShlEq      // <<=
	ShrEq      // >>=
	Eq         // =
	EqEq       // ==
	Ne         // !=
	Gt         // >
	Lt         // <
	Ge         // >=
	Le         // <=
	At         // @
	Underscore // _
	Dot        // .
	DotDot     // ..
	DotDotEq   // ..=
	Comma      // ,
	Semi       // ;
	Colon      // :
	PathSep    // ::
	RArrow     // ->
	FatArrow   // =>
	Pound      // #
	Question   // ?
	LParen     // (
	RParen     // )
	LBracket   // [
	RBracket   // ]
	LBrace     // {
	RBrace     // }

	keywordStart
	KwAs
	KwBreak
	KwConst
	KwContinue
	KwCrate
	KwDefer
	KwDyn
	KwElse
	KwEnum
	KwExtern
	KwFalse
	KwFn
	KwFor
	KwIf
	KwImpl
	KwIn
	KwLet
	KwLoop
	KwMatch
	KwMod
	KwMut
	KwPub
	KwReturn
	KwSelect
	KwSelfValue // self
	KwSelfType  // Self
	KwStatic
	KwStruct
	KwSuper
	KwTrait
	KwTrue
	KwType
	KwUnsafe
	KwUse
	KwWhere
	KwWhile
	keywordEnd
)

var kindNames = [...]string{
	EOF:         "end of file",
	Illegal:     "illegal token",
	Ident:       "identifier",
	Lifetime:    "label",
	Int:         "integer literal",
	Float:       "float literal",
	Char:        "char literal",
	Byte:        "byte literal",
	String:      "string literal",
	RawString:   "raw string literal",
	ByteString:  "byte string literal",
	Template:    "template string",
	Plus:        "+",
	Minus:       "-",
	Star:        "*",
	Slash:       "/",
	Percent:     "%",
	Caret:       "^",
	Not:         "!",
	And:         "&",
	Or:          "|",
	AndAnd:      "&&",
	OrOr:        "||",
	Shl:         "<<",
	Shr:         ">>",
	PlusEq:      "+=",
	MinusEq:     "-=",
	StarEq:      "*=",
	SlashEq:     "/=",
	PercentEq:   "%=",
	CaretEq:     "^=",
	AndEq:       "&=",
	OrEq:        "|=",
	ShlEq:       "<<=",
	ShrEq:       ">>=",
	Eq:          "=",
	EqEq:        "==",
	Ne:          "!=",
	Gt:          ">",
	Lt:          "<",
	Ge:          ">=",
	Le:          "<=",
	At:          "@",
	Underscore:  "_",
	Dot:         ".",
	DotDot:      "..",
	DotDotEq:    "..=",
	Comma:       ",",
	Semi:        ";",
	Colon:       ":",
	PathSep:     "::",
	RArrow:      "->",
	FatArrow:    "=>",
	Pound:       "#",
	Question:    "?",
	LParen:      "(",
	RParen:      ")",
	LBracket:    "[",
	RBracket:    "]",
	LBrace:      "{",
	RBrace:      "}",
	KwAs:        "as",
	KwBreak:     "break",
	KwConst:     "const",
	KwContinue:  "continue",
	KwCrate:     "crate",
	KwDefer:     "defer",
	KwDyn:       "dyn",
	KwElse:      "else",
	KwEnum:      "enum",
	KwExtern:    "extern",
	KwFalse:     "false",
	KwFn:        "fn",
	KwFor:       "for",
	KwIf:        "if",
	KwImpl:      "impl",
	KwIn:        "in",
	KwLet:       "let",
	KwLoop:      "loop",
	KwMatch:     "match",
	KwMod:       "mod",
	KwMut:       "mut",
	KwPub:       "pub",
	KwReturn:    "return",
	KwSelect:    "select",
	KwSelfValue: "self",
	KwSelfType:  "Self",
	KwStatic:    "static",
	KwStruct:    "struct",
	KwSuper:     "super",
	KwTrait:     "trait",
	KwTrue:      "true",
	KwType:      "type",
	KwUnsafe:    "unsafe",
	KwUse:       "use",
	KwWhere:     "where",
	KwWhile:     "while",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", k)
}

// IsKeyword reports whether k is a keyword.
func (k Kind) IsKeyword() bool { return k > keywordStart && k < keywordEnd }

var keywords = func() map[string]Kind {
	m := make(map[string]Kind, keywordEnd-keywordStart)
	for k := keywordStart + 1; k < keywordEnd; k++ {
		m[kindNames[k]] = k
	}
	return m
}()

// reserved words are not keywords yet but cannot be used as identifiers.
var reserved = map[string]bool{
	"async": true, "await": true, "box": true, "macro": true,
	"move": true, "try": true, "union": true, "yield": true,
}

// Token is a lexed token. Text holds the source text for literals, names
// and template strings; it is empty for punctuation and keywords.
type Token struct {
	Kind Kind
	Pos  Pos
	End  Pos
	Text string
}
