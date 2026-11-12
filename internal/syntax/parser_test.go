package syntax

import (
	"strings"
	"testing"
)

// parseWith parses src with the given entry point and fails on errors or
// leftover tokens.
func parseWith[T any](t *testing.T, src string, entry func(*parser) T) T {
	t.Helper()
	f := NewFile("test.navi", []byte(src))
	toks, errs := Tokenize(f)
	p := &parser{file: f, toks: toks, errs: errs}
	n := entry(p)
	if p.kind() != EOF {
		t.Errorf("%q: unparsed input at %s", src, p.describe(p.tok()))
	}
	if errs.Len() > 0 {
		t.Errorf("%q: %v", src, errs)
	}
	return n
}

func TestExpr(t *testing.T) {
	tests := []struct{ src, want string }{
		{"1 + 2 * 3", "(+ 1 (* 2 3))"},
		{"(1 + 2) * 3", "(* (paren (+ 1 2)) 3)"},
		{"a - b - c", "(- (- a b) c)"},
		{"a = b = c", "(= a (= b c))"},
		{"x += 1", "(+= x 1)"},
		{"a || b && c", "(|| a (&& b c))"},
		{"a == b && c != d", "(&& (== a b) (!= c d))"},
		{"a & b ^ c | d", "(| (^ (& a b) c) d)"},
		{"1 << 2 + 3", "(<< 1 (+ 2 3))"},
		{"-x as u64", "(as (- x) u64)"},
		{"x as i64 * 2", "(* (as x i64) 2)"},
		{"!a.b()", "(! (.b a))"},
		{"*p.x", "(* (.x p))"},
		{"&mut v[i]", "(&mut (index v i))"},
		{"&&x", "(& (& x))"},
		{"f(a, b)?", "(? (call f a b))"},
		{"a.b.c(1).d", "(.d (.c (.b a) 1))"},
		{"t.0.1", "(.1 (.0 t))"},
		{"1.5.max(2.0)", "(.max 1.5 2.0)"},
		{"x.parse::<i64>()", "(.parse<i64> x)"},
		{"Vec::<i32>::new()", "(call Vec<i32>::new)"},
		{"<T as Figure>::SIDES", "<T as Figure>::SIDES"},
		{"a..b", "(.. a b)"},
		{"a..=b", "(..= a b)"},
		{"..b", "(.. _ b)"},
		{"a..", "(.. a _)"},
		{"..", "(.. _ _)"},
		{"0..n + 1", "(.. 0 (+ n 1))"},
		{"Point { x: 1.0, y }", "(struct Point x:1.0 y:y)"},
		{"Point { x: 0.0, ..origin }", "(struct Point x:0.0 ..origin)"},
		{"Shape::Rect(1.0, 2.0)", "(call Shape::Rect 1.0 2.0)"},
		{"[1, 2, 3]", "(array 1 2 3)"},
		{"[0u8; 256]", "(repeat 0u8 256)"},
		{"(a, b)", "(tuple a b)"},
		{"(a,)", "(tuple a)"},
		{"()", "(tuple)"},
		{"|x| x + 1", "(closure (x) (+ x 1))"},
		{"|a: i32, b: i32| -> i32 { a + b }", "(closure (a:i32 b:i32) -> i32 (block (+ a b)))"},
		{"|| heavy(42)", "(closure () (call heavy 42))"},
		{"if x > 0 { 1 } else { -1 }", "(if (> x 0) (block 1) (block (- 1)))"},
		{"if let Some(x) = opt { x } else { 0 }", "(if (let (Some x) opt) (block x) (block 0))"},
		{"match s { Shape::Circle { r } => r, _ => 0.0 }", "(match s (arm (Shape::Circle { r:r }) r) (arm _ 0.0))"},
		{"loop { if done() { break 42; } }", "(loop (block (if (call done) (block (break 42);))))"},
		{"'outer: for i in 0..10 { continue 'outer; }", "(for 'outer i (.. 0 10) (block (continue 'outer);))"},
		{"while let Some(x) = stack.pop() { }", "(while (let (Some x) (.pop stack)) (block))"},
		{"for x in &mut v { *x += 1; }", "(for x (&mut v) (block (+= (* x) 1);))"},
		{"unsafe { *raw_ptr }", "(unsafe (* raw_ptr))"},
		{"return", "(return)"},
		{"`hello ${name}!`", `(template "hello " ${name} "!")`},
		{"`${p:?} ${pi:.3} ${n:>8}`", `(template ${p:?} " " ${pi:.3} " " ${n:>8})`},
		{"`sum = ${a + b}, first = ${v[0]}`", `(template "sum = " ${(+ a b)} ", first = " ${(index v 0)})`},
		{"`${f(`inner ${x}`)}`", `(template ${(call f (template "inner " ${x}))})`},
		{"`${ {1} }`", "(template ${(block 1)})"},
		{"`a \\${b} \\``", "(template \"a \\\\${b} \\\\`\")"},
		{"`${Vec::<i32>::new().len()}`", "(template ${(.len (call Vec<i32>::new))})"},
	}
	for _, tt := range tests {
		got := Dump(parseWith(t, tt.src, (*parser).expr))
		if got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestNoStructLiteralInConditions(t *testing.T) {
	tests := []struct{ src, want string }{
		{"if x == y { 1 } else { 2 }", "(if (== x y) (block 1) (block 2))"},
		{"match x { _ => S { a: 1 } }", "(match x (arm _ (struct S a:1)))"},
		{"for x in v { S { a: x }; }", "(for x v (block (struct S a:x);))"},
		{"if f(S { a: 1 }) { }", "(if (call f (struct S a:1)) (block))"},
	}
	for _, tt := range tests {
		got := Dump(parseWith(t, tt.src, (*parser).expr))
		if got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestPat(t *testing.T) {
	tests := []struct{ src, want string }{
		{"_", "_"},
		{"x", "x"},
		{"mut x", "mut x"},
		{"0", "0"},
		{"-1", "(- 1)"},
		{"1 | 2 | 3", "(| 1 2 3)"},
		{"4..=9", "(..=pat 4 9)"},
		{"'0'..='9'", "(..=pat '0' '9')"},
		{"5..", "(..pat 5 _)"},
		{"n @ 10..=99", "(@ n (..=pat 10 99))"},
		{"&d @ '0'..='9'", "(& (@ d (..=pat '0' '9')))"},
		{"Point { x: 0.0, y }", "(Point { x:0.0, y:y })"},
		{"Point { x, .. }", "(Point { x:x .. })"},
		{"Shape::Rect(w, h)", "(Shape::Rect w h)"},
		{"Shape::Empty", "Shape::Empty"},
		{"(0, _)", "(tuple 0 _)"},
		{"(x)", "x"},
		{"[first, .., last]", "(slice first .. last)"},
		{"[]", "(slice)"},
		{"Some(&Point { x, .. })", "(Some (& (Point { x:x .. })))"},
		{`"admin"`, `"admin"`},
	}
	for _, tt := range tests {
		got := Dump(parseWith(t, tt.src, (*parser).pat))
		if got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestType(t *testing.T) {
	tests := []struct{ src, want string }{
		{"i32", "i32"},
		{"&str", "(& str)"},
		{"&mut Point", "(&mut Point)"},
		{"&&T", "(& (& T))"},
		{"*const u8", "(*const u8)"},
		{"[u8]", "(slice u8)"},
		{"[u8; 4]", "(array u8 4)"},
		{"(i32, &str)", "(tuple i32 (& str))"},
		{"()", "(tuple)"},
		{"(T)", "T"},
		{"fn(i32) -> i32", "(fn i32 i32)"},
		{`extern "C" fn(i32) -> i32`, "(extern-fn i32 i32)"},
		{"&dyn Display", "(& (dyn Display))"},
		{"impl Iterator<Item = i32>", "(impl Iterator<Item=i32>)"},
		{"Vec<Vec<u8>>", "Vec<Vec<u8>>"},
		{"Map<Self, B>", "Map<Self,B>"},
		{"T::Item", "T::Item"},
		{"<T as Figure>::Unit", "<T as Figure>::Unit"},
		{"Result<T, std::io::Error>", "Result<T,std::io::Error>"},
		{"!", "!"},
	}
	for _, tt := range tests {
		got := Dump(parseWith(t, tt.src, (*parser).typ))
		if got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestItems(t *testing.T) {
	tests := []struct{ src, want string }{
		{"pub fn add(a: i32, b: i32) -> i32 { a + b }", "(file (fn add (a:i32 b:i32) -> i32 (block (+ a b))))"},
		{"fn largest<T: PartialOrd>(xs: &[T]) -> &T { xs }", "(file (fn largest <T:PartialOrd> (xs:(& (slice T))) -> (& T) (block xs)))"},
		{"fn p<I>(it: I) where I: Iterator, I::Item: Display {}", "(file (fn p <I> where I:Iterator where I::Item:Display (it:I) (block)))"},
		{"struct Point { x: f64, pub y: f64 }", "(file (struct Point {x:f64 pub y:f64}))"},
		{"struct Meters(f64);", "(file (struct Meters (f64)))"},
		{"struct Unit;", "(file (struct Unit))"},
		{"enum Color { Red = 1, Green = 2 }", "(file (enum Color (Red = 1) (Green = 2)))"},
		{"enum Shape { Circle { r: f64 }, Rect(f64, f64), Empty }", "(file (enum Shape (Circle {r:f64}) (Rect (f64 f64)) (Empty)))"},
		{"type Result<T> = std::result::Result<T, Error>;", "(file (type Result <T> = std::result::Result<T,Error>))"},
		{"const MAX: usize = 1024;", "(file (const MAX usize 1024))"},
		{"static mut COUNTER: i64 = 0;", "(file (static mut COUNTER i64 0))"},
		{"impl<T: Display> ToString for T { fn to_string(&self) -> String { `${self}` } }",
			"(file (impl <T:Display> ToString for T (fn to_string (&self) -> String (block (template ${self})))))"},
		{"trait Figure: Debug { type Unit; const SIDES: u32; fn area(&self) -> f64; }",
			"(file (trait Figure : Debug (type Unit) (const SIDES u32 _) (fn area (&self) -> f64)))"},
		{"mod parser;", "(file (mod parser))"},
		{"mod util { pub fn clamp(x: i32) -> i32 { x } }", "(file (mod util (fn clamp (x:i32) -> i32 (block x))))"},
		{"use std::io::{self, Read, Write as W};", "(file (use std::io::{self,Read,Write as W}))"},
		{"use super::util::*;", "(file (use super::util::*))"},
		{`extern "C" { fn write(fd: i32, buf: *const u8, n: usize) -> isize; }`,
			"(file (extern C (fn write (fd:i32 buf:(*const u8) n:usize) -> isize)))"},
		{`#[export_name = "navi_cb"] extern "C" fn callback(x: i32) -> i32 { x }`,
			"(file (fn callback (x:i32) -> i32 (block x)))"},
		{"#[asm] fn memmove(dst: *mut u8, src: *const u8, n: usize);",
			"(file (fn memmove (dst:(*mut u8) src:(*const u8) n:usize)))"},
		{"const fn make_table() -> [u32; 4] { [0; 4] }", "(file (fn make_table () -> (array u32 4) (block (repeat 0 4))))"},
		{"trait PartialEq<Rhs = Self> { fn eq(&self, other: &Rhs) -> bool; }",
			"(file (trait PartialEq <Rhs=Self> (fn eq (&self other:(& Rhs)) -> bool)))"},
		{"struct S<A, B: Clone = Vec<A>>;", "(file (struct S <A,B:Clone=Vec<A>>))"},
	}
	for _, tt := range tests {
		got := Dump(parseWith(t, tt.src, func(p *parser) *SourceFile {
			return &SourceFile{Attrs: p.innerAttrs(), Items: p.items(EOF)}
		}))
		if got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestBlockStatements(t *testing.T) {
	tests := []struct{ src, want string }{
		{"{ let x = 5; let mut y: i64 = 0; x }", "(block (let x 5) (let mut y i64 0) x)"},
		{"{ let Some(x) = opt else { return; }; }", "(block (let (Some x) opt else (block (return);)))"},
		{"{ defer file.close(); defer { log(1); } }", "(block (defer (.close file)) (defer (block (call log 1);)))"},
		{"{ if a { b() } c }", "(block (if a (block (call b))) c)"},
		{"{ match x { _ => {} }.len() }", "(block (.len (match x (arm _ (block)))))"},
		{"{ fn inner() {} inner() }", "(block (fn inner () (block)) (call inner))"},
		{"{ ;; }", "(block ; ;)"},
		{"{ select { v = rx.recv() => f(v), tx.send(1) => {}, default => {} } }",
			"(block (select (recv v (.recv rx) (call f v)) (case (.send tx 1) (block)) (default (block))))"},
	}
	for _, tt := range tests {
		got := Dump(parseWith(t, tt.src, func(p *parser) Expr { return p.block() }))
		if got != tt.want {
			t.Errorf("%q\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"fn f() { a < b < c }", "comparison operators cannot be chained"},
		{"fn f() { let x = 1 }", "expected ';'"},
		{"fn f() { `${x:q}` }", `invalid format spec "q"`},
		{"fn f() { `${}` }", "empty ${}"},
		{"fn f() { defer x; }", "defer needs a function call or a block"},
		{"fn f() { select { x + 1 => {} } }", "select case must be a recv() or send() call"},
		{`extern "Rust" fn f() {}`, `unsupported ABI "Rust"`},
		{"fn f() { let async = 1; }", `"async" is a reserved word`},
		{"struct { }", "expected identifier"},
		{"fn f() { 1..= }", "inclusive range '..=' needs an upper bound"},
	}
	for _, tt := range tests {
		_, errs := ParseFile(NewFile("e.navi", []byte(tt.src)))
		if errs.Len() == 0 || !strings.Contains(errs.Error(), tt.want) {
			t.Errorf("%q: got errors %q, want one containing %q", tt.src, errs.Error(), tt.want)
		}
	}
}

func TestErrorPosition(t *testing.T) {
	_, errs := ParseFile(NewFile("e.navi", []byte("fn f() {\n    let x = ;\n}\n")))
	if errs.Len() == 0 {
		t.Fatal("expected an error")
	}
	if got := errs.File.Position(errs.Diags[0].Pos).String(); got != "e.navi:2:13" {
		t.Errorf("position = %s, want e.navi:2:13", got)
	}
}

func TestRecovery(t *testing.T) {
	src := "fn a() { let = ; }\nfn b() {}\nstruct S { x: }\nfn c() {}\n"
	sf, errs := ParseFile(NewFile("r.navi", []byte(src)))
	if errs.Len() == 0 {
		t.Fatal("expected errors")
	}
	var names []string
	for _, it := range sf.Items {
		switch it := it.(type) {
		case *FnDecl:
			names = append(names, it.Name.Value)
		case *StructDecl:
			names = append(names, it.Name.Value)
		}
	}
	if got := strings.Join(names, ","); got != "a,b,S,c" {
		t.Errorf("recovered items = %s, want a,b,S,c", got)
	}
}

// TestTerminates checks that error recovery always makes progress.
func TestTerminates(t *testing.T) {
	srcs := []string{
		"fn a() {}\n#![cfg(test)]\nfn b() {}",
		"fn f() { ) }",
		"fn f() { ] ] ] }",
		"impl X { ) }",
		"fn f() { match x { ) } }",
		"#",
		"fn f() { let x = (; }",
		"}}}",
	}
	for _, src := range srcs {
		_, errs := ParseFile(NewFile("t.navi", []byte(src)))
		if errs.Len() == 0 {
			t.Errorf("%q: expected errors", src)
		}
	}
}
