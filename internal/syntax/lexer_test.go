package syntax

import (
	"strings"
	"testing"
)

func lexKinds(t *testing.T, src string) string {
	t.Helper()
	toks, errs := Tokenize(NewFile("t.navi", []byte(src)))
	if errs.Len() > 0 {
		t.Errorf("%q: %v", src, errs)
	}
	var parts []string
	for _, tk := range toks[:len(toks)-1] {
		s := tk.Kind.String()
		if tk.Text != "" {
			s += "(" + tk.Text + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func TestLexer(t *testing.T) {
	tests := []struct{ src, want string }{
		{"let x = 42;", "let identifier(x) = integer literal(42) ;"},
		{"1_000_000 0xff 0o17 0b1010 10u8 7usize", "integer literal(1_000_000) integer literal(0xff) integer literal(0o17) integer literal(0b1010) integer literal(10u8) integer literal(7usize)"},
		{"1.5 2e10 1.0e-3f32 3f64", "float literal(1.5) float literal(2e10) float literal(1.0e-3f32) float literal(3f64)"},
		{"1..2", "integer literal(1) .. integer literal(2)"},
		{"1.max(2)", "integer literal(1) . identifier(max) ( integer literal(2) )"},
		{"t.0.1", "identifier(t) . integer literal(0) . integer literal(1)"},
		{"'a' '\\n' '\\u{1F600}' '中'", "char literal('a') char literal('\\n') char literal('\\u{1F600}') char literal('中')"},
		{"'outer: loop", "label(outer) : loop"},
		{"b'a' b\"abc\"", "byte literal(b'a') byte string literal(b\"abc\")"},
		{`r"C:\path" r#"has "quotes""#`, `raw string literal(r"C:\path") raw string literal(r#"has "quotes""#)`},
		{"r#match", "identifier(match)"},
		{"_ _x", "_ identifier(_x)"},
		{"a::b -> => ..= .. . ::", "identifier(a) :: identifier(b) -> => ..= .. . ::"},
		{">>= <<= >> << >= <= != ==", ">>= <<= >> << >= <= != =="},
		{"&& || &= |= ^=", "&& || &= |= ^="},
		{"a /* x /* nested */ y */ b // c\nd", "identifier(a) identifier(b) identifier(d)"},
		{"`hi ${a + `x${b}`} \\` \\${}`", "template string(`hi ${a + `x${b}`} \\` \\${}`)"},
		{"`${ {} }` x", "template string(`${ {} }`) identifier(x)"},
		{"self Self crate super", "self Self crate super"},
	}
	for _, tt := range tests {
		if got := lexKinds(t, tt.src); got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestLexerErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{`"abc`, "unterminated string literal"},
		{"/* abc", "unterminated block comment"},
		{"`abc", "unterminated template string"},
		{"`${a`", "unterminated"},
		{`"\q"`, `unknown escape sequence \q`},
		{"0x", "missing digits"},
		{"1.5u8", "integer suffix on a float literal"},
		{"0xffu7", `invalid suffix`},
		{"$", "unexpected character"},
		{"yield", "reserved word"},
	}
	for _, tt := range tests {
		_, errs := Tokenize(NewFile("t.navi", []byte(tt.src)))
		if errs.Len() == 0 || !strings.Contains(errs.Error(), tt.want) {
			t.Errorf("%q: got %q, want an error containing %q", tt.src, errs.Error(), tt.want)
		}
	}
}
