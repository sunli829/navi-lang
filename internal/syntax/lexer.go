package syntax

import (
	"unicode/utf8"
)

// lexer turns source bytes into tokens.
type lexer struct {
	src  []byte
	off  int // offset of the next unread byte
	base Pos // position of src[0] in the file (non-zero when lexing a template hole)
	errs *Errors
	prev Kind // kind of the previously returned token
}

func newLexer(src []byte, base Pos, errs *Errors) *lexer {
	return &lexer{src: src, base: base, errs: errs, prev: EOF}
}

// Tokenize lexes a whole file. It is mainly useful for tests and tools; the
// parser lexes on demand.
func Tokenize(f *File) ([]Token, *Errors) {
	errs := &Errors{File: f}
	l := newLexer(f.Src, 0, errs)
	var toks []Token
	for {
		t := l.next()
		toks = append(toks, t)
		if t.Kind == EOF {
			return toks, errs
		}
	}
}

func (l *lexer) pos(off int) Pos { return l.base + Pos(off) }

func (l *lexer) errorf(off int, format string, args ...any) {
	l.errs.add(l.pos(off), format, args...)
}

func (l *lexer) peekByte(n int) byte {
	if l.off+n < len(l.src) {
		return l.src[l.off+n]
	}
	return 0
}

func (l *lexer) next() Token {
	t := l.scan()
	l.prev = t.Kind
	return t
}

func (l *lexer) scan() Token {
	l.skipSpaceAndComments()
	start := l.off
	tok := func(k Kind, n int) Token {
		l.off += n
		return Token{Kind: k, Pos: l.pos(start), End: l.pos(l.off)}
	}
	if l.off >= len(l.src) {
		return Token{Kind: EOF, Pos: l.pos(start), End: l.pos(start)}
	}
	c := l.src[l.off]
	switch {
	case c == 'r' && l.peekByte(1) == '#' && isIdentStart(l.peekByte(2)):
		return l.rawIdent()
	case c == 'r' && (l.peekByte(1) == '"' || l.peekByte(1) == '#'):
		return l.rawString(start, 1, RawString)
	case c == 'b' && l.peekByte(1) == 'r' && (l.peekByte(2) == '"' || l.peekByte(2) == '#'):
		return l.rawString(start, 2, ByteString)
	case c == 'b' && l.peekByte(1) == '\'':
		l.off++
		t := l.charLit(start)
		t.Kind = Byte
		return t
	case c == 'b' && l.peekByte(1) == '"':
		l.off++
		t := l.stringLit(start)
		t.Kind = ByteString
		return t
	case isIdentStart(c):
		return l.ident()
	case isDigit(c):
		return l.number()
	case c == '"':
		return l.stringLit(start)
	case c == '\'':
		return l.quote()
	case c == '`':
		return l.template()
	}

	two := func(next byte, k2 Kind, k1 Kind) Token {
		if l.peekByte(1) == next {
			return tok(k2, 2)
		}
		return tok(k1, 1)
	}
	switch c {
	case '+':
		return two('=', PlusEq, Plus)
	case '-':
		if l.peekByte(1) == '>' {
			return tok(RArrow, 2)
		}
		return two('=', MinusEq, Minus)
	case '*':
		return two('=', StarEq, Star)
	case '/':
		return two('=', SlashEq, Slash)
	case '%':
		return two('=', PercentEq, Percent)
	case '^':
		return two('=', CaretEq, Caret)
	case '!':
		return two('=', Ne, Not)
	case '&':
		switch l.peekByte(1) {
		case '&':
			return tok(AndAnd, 2)
		case '=':
			return tok(AndEq, 2)
		}
		return tok(And, 1)
	case '|':
		switch l.peekByte(1) {
		case '|':
			return tok(OrOr, 2)
		case '=':
			return tok(OrEq, 2)
		}
		return tok(Or, 1)
	case '<':
		if l.peekByte(1) == '<' {
			if l.peekByte(2) == '=' {
				return tok(ShlEq, 3)
			}
			return tok(Shl, 2)
		}
		return two('=', Le, Lt)
	case '>':
		if l.peekByte(1) == '>' {
			if l.peekByte(2) == '=' {
				return tok(ShrEq, 3)
			}
			return tok(Shr, 2)
		}
		return two('=', Ge, Gt)
	case '=':
		switch l.peekByte(1) {
		case '=':
			return tok(EqEq, 2)
		case '>':
			return tok(FatArrow, 2)
		}
		return tok(Eq, 1)
	case '@':
		return tok(At, 1)
	case '.':
		if l.peekByte(1) == '.' {
			if l.peekByte(2) == '=' {
				return tok(DotDotEq, 3)
			}
			return tok(DotDot, 2)
		}
		return tok(Dot, 1)
	case ',':
		return tok(Comma, 1)
	case ';':
		return tok(Semi, 1)
	case ':':
		return two(':', PathSep, Colon)
	case '#':
		return tok(Pound, 1)
	case '?':
		return tok(Question, 1)
	case '(':
		return tok(LParen, 1)
	case ')':
		return tok(RParen, 1)
	case '[':
		return tok(LBracket, 1)
	case ']':
		return tok(RBracket, 1)
	case '{':
		return tok(LBrace, 1)
	case '}':
		return tok(RBrace, 1)
	}
	r, size := utf8.DecodeRune(l.src[l.off:])
	l.errorf(start, "unexpected character %q", r)
	return tok(Illegal, size)
}

func (l *lexer) skipSpaceAndComments() {
	for l.off < len(l.src) {
		switch c := l.src[l.off]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			l.off++
		case c == '/' && l.peekByte(1) == '/':
			for l.off < len(l.src) && l.src[l.off] != '\n' {
				l.off++
			}
		case c == '/' && l.peekByte(1) == '*':
			start := l.off
			l.off += 2
			depth := 1
			for depth > 0 {
				if l.off >= len(l.src) {
					l.errorf(start, "unterminated block comment")
					return
				}
				switch {
				case l.src[l.off] == '/' && l.peekByte(1) == '*':
					depth++
					l.off += 2
				case l.src[l.off] == '*' && l.peekByte(1) == '/':
					depth--
					l.off += 2
				default:
					l.off++
				}
			}
		default:
			return
		}
	}
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentCont(c byte) bool { return isIdentStart(c) || isDigit(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func (l *lexer) ident() Token {
	start := l.off
	for l.off < len(l.src) && isIdentCont(l.src[l.off]) {
		l.off++
	}
	text := string(l.src[start:l.off])
	t := Token{Kind: Ident, Pos: l.pos(start), End: l.pos(l.off), Text: text}
	if text == "_" {
		t.Kind, t.Text = Underscore, ""
	} else if k, ok := keywords[text]; ok {
		t.Kind, t.Text = k, ""
	} else if reserved[text] {
		l.errorf(start, "%q is a reserved word", text)
	}
	return t
}

func (l *lexer) rawIdent() Token {
	start := l.off
	l.off += 2
	nameStart := l.off
	for l.off < len(l.src) && isIdentCont(l.src[l.off]) {
		l.off++
	}
	return Token{Kind: Ident, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[nameStart:l.off])}
}

func (l *lexer) number() Token {
	start := l.off
	kind := Int
	digits := isDigit
	decimal := true
	if l.src[l.off] == '0' {
		switch l.peekByte(1) {
		case 'x':
			digits, decimal = isHexDigit, false
			l.off += 2
		case 'o':
			digits, decimal = func(c byte) bool { return c >= '0' && c <= '7' }, false
			l.off += 2
		case 'b':
			digits, decimal = func(c byte) bool { return c == '0' || c == '1' }, false
			l.off += 2
		}
	}
	n := 0
	for l.off < len(l.src) && (digits(l.src[l.off]) || l.src[l.off] == '_') {
		if l.src[l.off] != '_' {
			n++
		}
		l.off++
	}
	if n == 0 {
		l.errorf(start, "missing digits in number")
	}
	// After a '.' (tuple index like t.0.1) only an integer is lexed.
	if decimal && l.prev != Dot {
		// Fraction: a '.' followed by a digit. "1..2" and "1.foo()" stay integers.
		if l.peekByte(0) == '.' && isDigit(l.peekByte(1)) {
			kind = Float
			l.off++
			for l.off < len(l.src) && (isDigit(l.src[l.off]) || l.src[l.off] == '_') {
				l.off++
			}
		}
		if c := l.peekByte(0); c == 'e' || c == 'E' {
			save := l.off
			l.off++
			if c := l.peekByte(0); c == '+' || c == '-' {
				l.off++
			}
			if isDigit(l.peekByte(0)) {
				kind = Float
				for l.off < len(l.src) && (isDigit(l.src[l.off]) || l.src[l.off] == '_') {
					l.off++
				}
			} else {
				l.off = save
			}
		}
	}
	// Type suffix such as u8, i64, f32, usize.
	if l.off < len(l.src) && isIdentStart(l.src[l.off]) {
		sufStart := l.off
		for l.off < len(l.src) && isIdentCont(l.src[l.off]) {
			l.off++
		}
		switch string(l.src[sufStart:l.off]) {
		case "f32", "f64":
			if decimal {
				kind = Float
			} else {
				l.errorf(sufStart, "float suffix on a non-decimal literal")
			}
		case "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize":
			if kind == Float {
				l.errorf(sufStart, "integer suffix on a float literal")
			}
		default:
			l.errorf(sufStart, "invalid suffix %q on number", l.src[sufStart:l.off])
		}
	}
	return Token{Kind: kind, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start:l.off])}
}

// escape scans one escape sequence after a backslash; l.off points at the
// byte after '\'. extra lists additional escapable bytes (for templates).
func (l *lexer) escape(extra string) {
	start := l.off - 1
	if l.off >= len(l.src) {
		l.errorf(start, "unterminated escape sequence")
		return
	}
	c := l.src[l.off]
	l.off++
	switch c {
	case 'n', 'r', 't', '\\', '0', '\'', '"':
	case 'x':
		if !(isHexDigit(l.peekByte(0)) && isHexDigit(l.peekByte(1))) {
			l.errorf(start, "\\x escape needs two hex digits")
			return
		}
		l.off += 2
	case 'u':
		if l.peekByte(0) != '{' {
			l.errorf(start, "\\u escape must be written \\u{...}")
			return
		}
		l.off++
		n := 0
		for isHexDigit(l.peekByte(0)) {
			l.off++
			n++
		}
		if l.peekByte(0) != '}' || n == 0 || n > 6 {
			l.errorf(start, "invalid \\u{...} escape")
			return
		}
		l.off++
	default:
		for i := 0; i < len(extra); i++ {
			if c == extra[i] {
				return
			}
		}
		l.errorf(start, "unknown escape sequence \\%c", c)
	}
}

func (l *lexer) stringLit(start int) Token {
	l.off++ // opening quote
	for {
		if l.off >= len(l.src) {
			l.errorf(start, "unterminated string literal")
			break
		}
		c := l.src[l.off]
		l.off++
		if c == '"' {
			break
		}
		if c == '\\' {
			l.escape("")
		}
	}
	return Token{Kind: String, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start:l.off])}
}

// rawString scans r"..." / r#"..."# (prefix is 1) or br"..." (prefix is 2).
func (l *lexer) rawString(start, prefix int, kind Kind) Token {
	l.off += prefix
	hashes := 0
	for l.peekByte(0) == '#' {
		hashes++
		l.off++
	}
	if l.peekByte(0) != '"' {
		l.errorf(start, "expected '\"' to start raw string")
		return Token{Kind: Illegal, Pos: l.pos(start), End: l.pos(l.off)}
	}
	l.off++
	for {
		if l.off >= len(l.src) {
			l.errorf(start, "unterminated raw string literal")
			break
		}
		c := l.src[l.off]
		l.off++
		if c == '"' {
			n := 0
			for n < hashes && l.peekByte(0) == '#' {
				n++
				l.off++
			}
			if n == hashes {
				break
			}
		}
	}
	return Token{Kind: kind, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start:l.off])}
}

// quote handles '\” which starts either a char literal or a loop label.
func (l *lexer) quote() Token {
	start := l.off
	if l.peekByte(1) != '\\' {
		r, size := utf8.DecodeRune(l.src[l.off+1:])
		after := l.off + 1 + size
		isChar := after < len(l.src) && l.src[after] == '\''
		if !isChar && r < utf8.RuneSelf && isIdentStart(byte(r)) {
			l.off++
			for l.off < len(l.src) && isIdentCont(l.src[l.off]) {
				l.off++
			}
			return Token{Kind: Lifetime, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start+1 : l.off])}
		}
	}
	return l.charLit(start)
}

func (l *lexer) charLit(start int) Token {
	l.off++ // opening quote
	if l.off >= len(l.src) {
		l.errorf(start, "unterminated char literal")
		return Token{Kind: Illegal, Pos: l.pos(start), End: l.pos(l.off)}
	}
	if l.src[l.off] == '\\' {
		l.off++
		l.escape("")
	} else {
		_, size := utf8.DecodeRune(l.src[l.off:])
		l.off += size
	}
	if l.peekByte(0) != '\'' {
		l.errorf(start, "unterminated char literal")
		return Token{Kind: Illegal, Pos: l.pos(start), End: l.pos(l.off)}
	}
	l.off++
	return Token{Kind: Char, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start:l.off])}
}

// template scans a whole template string including its ${...} holes. Holes
// are lexed with the normal rules so that braces, strings and nested
// templates inside them are handled correctly; the parser splits the text
// into parts later.
func (l *lexer) template() Token {
	start := l.off
	l.off++ // opening backtick
	for {
		if l.off >= len(l.src) {
			l.errorf(start, "unterminated template string")
			break
		}
		c := l.src[l.off]
		if c == '`' {
			l.off++
			break
		}
		if c == '\\' {
			l.off++
			l.escape("`$")
			continue
		}
		if c == '$' && l.peekByte(1) == '{' {
			holeStart := l.off
			l.off += 2
			depth := 1
			for depth > 0 {
				t := l.scan()
				switch t.Kind {
				case LBrace:
					depth++
				case RBrace:
					depth--
				case EOF:
					l.errorf(holeStart, "unterminated ${ in template string")
					return Token{Kind: Template, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start:l.off])}
				}
			}
			continue
		}
		l.off++
	}
	return Token{Kind: Template, Pos: l.pos(start), End: l.pos(l.off), Text: string(l.src[start:l.off])}
}
