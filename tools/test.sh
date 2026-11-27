#!/bin/sh
# Runs the tests with a self-hosted navi: tests/run (navi test), tests/ui
# (diagnostics), navi test / bench on tests/harness, fmt, lsp, the front
# end's tests and dependencies.
#
#   tools/test.sh path/to/navi
set -e
navi=${1:?usage: tools/test.sh path/to/navi}
# An absolute path: some tests run it from other directories.
case "$navi" in
/* | ?:*) ;;
*) navi=$(pwd)/$navi ;;
esac
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*) os=windows ;;
*) os=unix ;;
esac
# Diagnostic annotations, as rustc's ui tests: "//~ ERROR text" (or WARN)
# expects a diagnostic on that line whose message contains text; "//~^"
# is the line above (one ^ a line), "//~|" the previous annotation's line.
# Prints "line<TAB>KIND<TAB>text" lines.
annotations() {
	awk '{
		i = index($0, "//~"); if (i == 0) next
		r = substr($0, i + 3); t = NR
		if (substr(r, 1, 1) == "|") { t = last; r = substr(r, 2) }
		else while (substr(r, 1, 1) == "^") { t--; r = substr(r, 2) }
		last = t
		sub(/^ +/, "", r); k = r; sub(/ .*/, "", k); m = substr(r, length(k) + 1); sub(/^ +/, "", m)
		if (k == "WARNING") k = "WARN"
		print t "\t" k "\t" m
	}' "$1"
}
# The diagnostics of navi's output (path:line:col: [warning: ]message) in
# annotations' format.
diags() {
	awk '{
		if (!match($0, /:[0-9]+:[0-9]+: /)) next
		split(substr($0, RSTART + 1, RLENGTH - 3), at, ":")
		m = substr($0, RSTART + RLENGTH); k = "ERROR"
		if (substr(m, 1, 9) == "warning: ") { k = "WARN"; m = substr(m, 10) }
		print at[1] "\t" k "\t" m
	}'
}
# Matches $tmp/diag.got against $tmp/diag.want (both from annotations'
# format): each diagnostic needs an annotation of its line and kind whose
# text it contains, and each annotation a diagnostic.
match_diags() {
	awk -F'\t' 'FILENAME == ARGV[1] { line[NR] = $1; kind[NR] = $2; text[NR] = $3; n = NR; next }
		{ for (i = 1; i <= n; i++) if (!used[i] && line[i] == $1 && kind[i] == $2 && index($3, text[i]) > 0) { used[i] = 1; next }
		  print "unexpected: " $1 ": " $2 " " $3; bad = 1 }
		END { for (i = 1; i <= n; i++) if (!used[i]) { print "missing: " line[i] ": " kind[i] " " text[i]; bad = 1 } exit bad }' \
		"$tmp/diag.want" "$tmp/diag.got" > "$tmp/diag.diff"
}
# tests/run: files of #[test] functions; navi test runs them (in parallel,
# each file's output printed when it ends), all must pass. A hung test
# fails after 5 minutes.
"$navi" test --timeout 5m tests/run/*.navi || fail=1
# tests/ui: navi build reports exactly the annotated errors and warnings;
# a file without ERROR annotations builds.
for f in tests/ui/*.navi; do
	n=$(basename "$f" .navi)
	set +e
	"$navi" build -o "$tmp/$n" "$f" > "$tmp/$n.build" 2>&1
	code=$?
	set -e
	annotations "$f" > "$tmp/diag.want"
	diags < "$tmp/$n.build" > "$tmp/diag.got"
	if awk -F'\t' '$2 == "ERROR" { e = 1 } END { exit !e }' "$tmp/diag.want"; then want_fail=1; else want_fail=0; fi
	if [ "$want_fail" = 1 ] && [ "$code" = 0 ]; then
		echo "FAIL $f (built)"; fail=1
	elif [ "$want_fail" = 0 ] && [ "$code" != 0 ]; then
		echo "FAIL $f (build)"; cat "$tmp/$n.build"; fail=1
	elif match_diags; then
		echo "ok   $f"
	else
		echo "FAIL $f (diagnostics)"; head -20 "$tmp/diag.diff"; fail=1
	fi
done
# Derive macros (docs/derive.md): tests/derive/macros's, used by app (its
# output) and bad (exactly the annotated errors).
set +e
(cd tests/derive/app && "$navi" build -o "$tmp/derive-app" > "$tmp/derive-app.build" 2>&1) && "$tmp/derive-app" > "$tmp/derive-app.out" 2>&1
code=$?
(cd tests/derive/bad && "$navi" build -o "$tmp/derive-bad" > "$tmp/derive-bad.build" 2>&1)
bad=$?
set -e
if [ "$code" = 0 ] && cmp -s "$tmp/derive-app.out" tests/derive/app/expected.out; then
	echo "ok   derive macros (tests/derive/app)"
else
	echo "FAIL derive macros (tests/derive/app)"; cat "$tmp/derive-app.build" "$tmp/derive-app.out"; fail=1
fi
annotations tests/derive/bad/src/main.navi > "$tmp/diag.want"
diags < "$tmp/derive-bad.build" > "$tmp/diag.got"
if [ "$bad" != 0 ] && match_diags; then
	echo "ok   derive macro errors (tests/derive/bad)"
else
	echo "FAIL derive macro errors (tests/derive/bad)"; head -20 "$tmp/diag.diff"; fail=1
fi
# The assembler (compiler/src/asm) against clang's, where there is one
# that targets both (macOS).
if [ "$(uname -s)" = Darwin ] && command -v clang > /dev/null && command -v objdump > /dev/null && command -v python3 > /dev/null; then
	for arch in arm64 amd64; do
		if python3 tools/asmcheck.py $arch "$navi" tests/asm/$arch.txt "$tmp/asm-$arch" > "$tmp/asm.out" 2>&1; then
			echo "ok   assembler $arch (tests/asm/$arch.txt)"
		else
			echo "FAIL assembler $arch"; head -20 "$tmp/asm.out"; fail=1
		fi
	done
fi
# navi test and navi bench on a crate with passing and failing tests.
set +e
"$navi" test tests/harness > "$tmp/harness.out" 2>/dev/null
code=$?
# (A failed test's panic message names its file by its full path.)
got_out=$(grep -v ' panicked at ' "$tmp/harness.out")
set -e
if [ "$got_out" = "$(cat tests/harness/expected.out)" ] && [ "$code" = 1 ]; then
	echo "ok   navi test tests/harness"
else
	echo "FAIL navi test tests/harness (exit $code)"; echo "$got_out"; fail=1
fi
if [ "$("$navi" bench tests/harness 2>&1 | grep -c ' ns/iter ')" = 2 ]; then
	echo "ok   navi bench tests/harness"
else
	echo "FAIL navi bench tests/harness"; fail=1
fi
# std and the compiler stay formatted (navi fmt, as nightly rustfmt).
for d in lib/std compiler; do
	if "$navi" fmt --check $d > "$tmp/stdfmt.out" 2>&1; then echo "ok   navi fmt --check $d"; else echo "FAIL navi fmt --check $d"; head -20 "$tmp/stdfmt.out"; fail=1; fi
done
# navi fmt on a module's file finds its modules where the compiler does
# (src/a.navi's mod b: src/a/b.navi), and on a directory that is no
# crate's, formats every file in it.
fp=$tmp/fmtp
mkdir -p "$fp/src/a" "$fp/loose/sub"
printf '[package]\nname = "p"\n' > "$fp/navi.toml"
printf 'mod a;\n' > "$fp/src/lib.navi"
printf 'mod b;\n' > "$fp/src/a.navi"
printf 'fn  inner( ) {}\n' > "$fp/src/a/b.navi"
printf 'fn  decoy( ) {}\n' > "$fp/src/b.navi"
printf 'fn  y( ) {}\n' > "$fp/loose/sub/y.navi"
r1=$(cd "$fp" && "$navi" fmt --check src/a.navi 2>&1 | grep '^Diff in' | tr -d '\r')
r2=$(cd "$fp" && "$navi" fmt --check loose 2>&1 | grep '^Diff in' | tr -d '\r')
if [ "$r1" = "Diff in src/a/b.navi:" ] && [ "$r2" = "Diff in loose/sub/y.navi:" ]; then echo "ok   navi fmt module files and directories"; else echo "FAIL navi fmt module files and directories: $r1 / $r2"; fail=1; fi
# navi fmt: tests/fmt/x.navi formats to x.out, which stays as it is;
# --check reports a file that would change and passes one that would not.
for f in tests/fmt/*.navi; do
	want="${f%.navi}.out"
	if "$navi" fmt - < "$f" > "$tmp/fmt.out" 2>&1 && cmp -s "$tmp/fmt.out" "$want" \
		&& "$navi" fmt - < "$want" > "$tmp/fmt2.out" 2>&1 && cmp -s "$tmp/fmt2.out" "$want"; then
		echo "ok   navi fmt $f"
	else
		echo "FAIL navi fmt $f"; diff "$want" "$tmp/fmt.out" | head -20; fail=1
	fi
done
cp tests/fmt/exprs.navi "$tmp/check.navi"
set +e
"$navi" fmt --check "$tmp/check.navi" > /dev/null 2>&1; c1=$?
"$navi" fmt "$tmp/check.navi" > /dev/null 2>&1; c2=$?
"$navi" fmt --check "$tmp/check.navi" > /dev/null 2>&1; c3=$?
set -e
if [ "$c1" = 1 ] && [ "$c2" = 0 ] && [ "$c3" = 0 ] && cmp -s "$tmp/check.navi" tests/fmt/exprs.out; then
	echo "ok   navi fmt --check"
else
	echo "FAIL navi fmt --check ($c1 $c2 $c3)"; fail=1
fi
# comment_width from navi.toml's [fmt] table, and --comment-width over it.
mkdir -p "$tmp/cw/src"
printf '[package]\nname = "cw"\n\n[fmt]\ncomment_width = 40\n' > "$tmp/cw/navi.toml"
long='// one two three four five six seven eight nine ten'
printf '%s\nfn main() {}\n' "$long" > "$tmp/cw/src/main.navi"
"$navi" fmt --comment-width 100 "$tmp/cw" > /dev/null 2>&1
w100=$(cat "$tmp/cw/src/main.navi")
"$navi" fmt "$tmp/cw" > /dev/null 2>&1
w40=$(cat "$tmp/cw/src/main.navi")
if [ "$w100" = "$(printf '%s\nfn main() {}' "$long")" ] \
	&& [ "$w40" = "$(printf '// one two three four five six seven\n// eight nine ten\nfn main() {}')" ]; then
	echo "ok   navi fmt comment_width"
else
	echo "FAIL navi fmt comment_width"; echo "$w100"; echo "$w40"; fail=1
fi
# navi lsp: the recorded session tests/lsp/session.jsonl (one message a
# line, the repository's path as @ROOT@) answers as tests/lsp/session.out.
root=$(pwd)
# Windows: navi.exe sees C:/dir, which a URI writes as file:///C:/dir.
uroot=$root
if [ "$os" = windows ]; then root=$(pwd -W); uroot=/$root; fi
sed "s#@ROOT@#$uroot#g" tests/lsp/session.jsonl | LC_ALL=C awk '{ printf "Content-Length: %d\r\n\r\n%s", length($0), $0 }' \
	| NAVI_STD="$root/lib/std" "$navi" lsp 2>/dev/null | perl -0777 -pe 's/Content-Length: \d+\r\n\r\n/\n/g' \
	| sed "s#$uroot#@ROOT@#g" | sed '/^$/d' > "$tmp/lsp.out"
if cmp -s "$tmp/lsp.out" tests/lsp/session.out; then
	echo "ok   navi lsp tests/lsp/session.jsonl"
else
	echo "FAIL navi lsp tests/lsp/session.jsonl"; diff tests/lsp/session.out "$tmp/lsp.out" | head -20; fail=1
fi
# The same for derives (tests/derive/session.jsonl): hovering a derive
# shows its code, a derive macro's name goes to its function.
sed "s#@ROOT@#$uroot#g" tests/derive/session.jsonl | LC_ALL=C awk '{ printf "Content-Length: %d\r\n\r\n%s", length($0), $0 }' \
	| NAVI_STD="$root/lib/std" "$navi" lsp 2>/dev/null | perl -0777 -pe 's/Content-Length: \d+\r\n\r\n/\n/g' \
	| sed "s#$uroot#@ROOT@#g" | sed '/^$/d' > "$tmp/lsp.out"
if cmp -s "$tmp/lsp.out" tests/derive/session.out; then
	echo "ok   navi lsp tests/derive/session.jsonl"
else
	echo "FAIL navi lsp tests/derive/session.jsonl"; diff tests/derive/session.out "$tmp/lsp.out" | head -20; fail=1
fi
# The front end: tests/syntax parses; in tests/resolve (name resolution),
# tests/types and tests/check (type checking) the files of ok/ and sigs/
# have no errors and those of err/ exactly the errors annotated with
# "//~ ERROR text" (see annotations).
front() {
	"$navi" "$1" "$2" > "$tmp/front.out" 2>&1
	grep '^error ' "$tmp/front.out" | diags > "$tmp/diag.got"
	annotations "$2" > "$tmp/diag.want"
	if match_diags; then
		echo "ok   $2"
	else
		echo "FAIL $2 ($1)"; head -20 "$tmp/diag.diff"; fail=1
	fi
}
for f in tests/syntax/*.navi; do
	if "$navi" parse "$f" > "$tmp/parse.out" 2>&1; then echo "ok   $f"; else echo "FAIL $f (parse)"; head -5 "$tmp/parse.out"; fail=1; fi
done
for f in tests/resolve/*/*.navi; do front resolve-dump "$f"; done
# Dependencies (see compiler/src/deps.navi): app needs foo (a path, as f)
# and bar v1.0.0 (git); foo needs util (a path) and bar v1.1.0, so both
# versions of bar are in app. navi.lock keeps a tag that moves; navi update
# follows it.
d=$tmp/deps
mkdir -p "$d/bar/src" "$d/util/src" "$d/foo/src" "$d/app/src"
dd=$d
gu=file://$d
if [ "$os" = windows ]; then dd=$(cygpath -m "$d"); gu=file:///$dd; fi
g() { git -C "$d/bar" -c user.email=t@t -c user.name=t "$@" > /dev/null; }
printf '[package]\nname = "bar"\n' > "$d/bar/navi.toml"
printf 'pub struct Version { pub n: i32 }\npub fn version() -> Version { Version { n: %s } }\n' 100 > "$d/bar/src/lib.navi"
g init -q && g add -A && g commit -qm one && g tag v1.0.0
printf 'pub struct Version { pub n: i32 }\npub fn version() -> Version { Version { n: %s } }\n' 110 > "$d/bar/src/lib.navi"
g commit -qam two && g tag -a v1.1.0 -m two
printf '[package]\nname = "util"\n' > "$d/util/navi.toml"
printf 'pub fn twice(x: i32) -> i32 { x * 2 }\n' > "$d/util/src/lib.navi"
printf '[package]\nname = "foo"\n\n[dependencies]\nutil = { path = "../util" }\nbar = { git = "%s/bar", tag = "v1.1.0" }\n' "$gu" > "$d/foo/navi.toml"
printf 'pub fn value() -> i32 { util::twice(21) }\npub fn bar_n() -> i32 { bar::version().n }\npub fn get() -> bar::Version { bar::version() }\n' > "$d/foo/src/lib.navi"
printf '[package]\nname = "app"\n\n[dependencies]\nf = { path = "../foo" }\nbar = { git = "%s/bar", tag = "v1.0.0" }\n' "$gu" > "$d/app/navi.toml"
printf 'fn main() { println(`${f::value()} ${f::bar_n()} ${bar::version().n}`); }\n' > "$d/app/src/main.navi"
deps() { (cd "$d/app" && NAVI_PKG="$dd/pkg" "$navi" "$@" 2>&1); }
r1=$(deps build -o app . > /dev/null && "$d/app/app") || true
locked=$(grep -c '^commit = ' "$d/app/navi.lock" 2> /dev/null) || true
printf 'pub struct Version { pub n: i32 }\npub fn version() -> Version { Version { n: %s } }\n' 101 > "$d/bar/src/lib.navi"
g commit -qam moved && g tag -f v1.0.0
r2=$(deps build -o app . > /dev/null && "$d/app/app") || true
r3=$(deps update > /dev/null && deps build -o app . > /dev/null && "$d/app/app") || true
printf 'fn take(v: bar::Version) -> i32 { v.n }\nfn main() { println(`${take(f::get())}`); }\n' > "$d/app/src/main.navi"
r4=$(deps build -o app . | sed -E 's#^[^ ]*:[0-9]+:[0-9]+: ##')
if [ "$r1" = "42 110 100" ] && [ "$locked" = 2 ] && [ "$r2" = "42 110 100" ] && [ "$r3" = "42 110 101" ] \
	&& [ "$r4" = "mismatched types: expected bar@v1.0.0::Version, found bar::Version" ]; then
	echo "ok   dependencies"
else
	echo "FAIL dependencies"; printf '%s\n' "$r1" "$locked" "$r2" "$r3" "$r4"; fail=1
fi
# Generic code instantiated by two crates for element types of different
# sizes: the group of either has one instance, so its calls are static and
# differ; each crate's copy must keep its own symbol (it shared one, and
# the linker kept one copy: lib's numbers came out as "1,49,49").
d=$tmp/split
mkdir -p "$d/lib/src" "$d/app/src"
printf '[package]\nname = "lib"\n' > "$d/lib/navi.toml"
printf 'pub fn texts(v: &Vec<u16>) -> String {\n    let s: Vec<String> = v.iter().map(|a: &u16| -> String { a.to_string() }).collect();\n    s.join(",")\n}\n' > "$d/lib/src/lib.navi"
printf '[package]\nname = "app"\n\n[dependencies]\nlib = { path = "../lib" }\n' > "$d/app/navi.toml"
printf 'fn main() {\n    let mut big: Vec<[u64; 4]> = Vec::new();\n    big.push([7, 0, 0, 0]);\n    big.push([8, 0, 0, 0]);\n    let s: Vec<String> = big.iter().map(|b: &[u64; 4]| -> String { b[0].to_string() }).collect();\n    let mut v: Vec<u16> = Vec::new();\n    v.push(1);\n    v.push(2);\n    v.push(3);\n    println(`${s.join(",")} ${lib::texts(&v)}`);\n}\n' > "$d/app/src/main.navi"
r=$(cd "$d/app" && "$navi" run 2>&1 | tail -1) || true
if [ "$r" = "7,8 1,2,3" ]; then echo "ok   generic code in two crates"; else echo "FAIL generic code in two crates: $r"; fail=1; fi
# std::build: each crate's package (navi.toml) and --var, as constants;
# a new version or --var value is a new build, a dependency keeps its own
# package; clap's derive takes the version from it.
d=$tmp/buildinfo
mkdir -p "$d/lib/src" "$d/app/src"
printf '[package]\nname = "lib"\nversion = "0.3.0"\n' > "$d/lib/navi.toml"
printf 'pub fn v() -> &str { std::build::package_version() }\n' > "$d/lib/src/lib.navi"
printf '[package]\nname = "app"\nversion = "1.2.0"\ndescription = "Demo"\nauthors = ["A", "B"]\nlicense = "MIT"\n\n[dependencies]\nlib = { path = "../lib" }\n' > "$d/app/navi.toml"
printf 'use std::cli::Parser;\n#[derive(Parser)]\n#[command(version, about)]\nstruct Cli {}\nfn main() {\n    if std::env::args().count() > 1 { Cli::parse(); }\n    println(`${std::build::package_name()} ${std::build::package_version()} ${std::build::package_description()} ${lib::v()} ${std::build::var("commit").unwrap_or("-")} ${std::build::package_authors()} ${std::build::package_license()}`);\n}\n' > "$d/app/src/main.navi"
b1=$(cd "$d/app" && "$navi" run 2>&1 | tail -1) || true
b2=$(cd "$d/app" && "$navi" run --var commit=abc 2>&1 | tail -1) || true
printf '[package]\nname = "lib"\nversion = "0.4.0"\n' > "$d/lib/navi.toml"
b3=$(cd "$d/app" && "$navi" run 2>&1 | tail -1) || true
b4=$(cd "$d/app" && "$navi" run -- --version 2>&1 | tail -1) || true
if [ "$b1" = "app 1.2.0 Demo 0.3.0 - A, B MIT" ] && [ "$b2" = "app 1.2.0 Demo 0.3.0 abc A, B MIT" ] && [ "$b3" = "app 1.2.0 Demo 0.4.0 - A, B MIT" ] && [ "$b4" = "app 1.2.0" ]; then
	echo "ok   std::build"
else
	echo "FAIL std::build"; printf '%s\n' "$b1" "$b2" "$b3" "$b4"; fail=1
fi
# The same program with std compiled here and with std read from its
# export data (the cache): derived code's positions are the user's file's
# either way (from export data they were the generated file's).
d=$tmp/warmcold
mkdir -p "$d/a" "$d/b" "$d/c"
printf '#[derive(Clone, Debug, PartialEq)]\nstruct P { a: Vec<i32>, b: Option<String> }\nfn main() {\n    let p = P { a: Vec::from([1]), b: Some("x".to_string()) };\n    println(`${p.clone() == p} ${p:?} ${Some(3).clone():?}`);\n}\n' > "$d/m.navi"
NAVI_CACHE=off "$navi" build -o "$d/a/m" "$d/m.navi" > /dev/null 2>&1
NAVI_CACHE="$d/cache" "$navi" build -o "$d/b/m" "$d/m.navi" > /dev/null 2>&1
NAVI_CACHE="$d/cache" "$navi" build -a -o "$d/c/m" "$d/m.navi" > /dev/null 2>&1
if cmp -s "$d/a/m" "$d/b/m" && cmp -s "$d/a/m" "$d/c/m"; then echo "ok   the same program with std from source and from the cache"; else echo "FAIL the same program with std from source and from the cache"; fail=1; fi
for f in tests/types/*/*.navi tests/check/*/*.navi; do front types-dump "$f"; done
# The compiler's own unit tests (#[cfg(test)] modules).
set +e
got=$("$navi" test compiler 2>&1)
code=$?
set -e
if [ "$code" = 0 ]; then echo "ok   navi test compiler ($(echo "$got" | tail -1))"; else echo "FAIL navi test compiler"; echo "$got" | grep -v '^ok ' | head -30; fail=1; fi
# Projects (as Cargo's): navi new and init, navi.toml found from a
# directory below, target/, navi run; a workspace with members by pattern,
# an excluded crate, workspace dependencies, building and testing every
# member at the root (unit and integration tests, NAVI_BIN_EXE_name), -p.
pj=$tmp/projects
mkdir -p "$pj"
pr() { (cd "$pj/$1" && shift && "$navi" "$@" 2>&1); }
p_ok=1
p_fail() { echo "FAIL projects: $*"; p_ok=0; fail=1; }
pr . new --vcs none one > /dev/null || p_fail "navi new"
printf 'fn main() {\n    let a: Vec<String> = std::env::args().collect();\n    println(`args ${a[1..].join(" ")}`);\n    std::process::exit(3);\n}\n' > "$pj/one/src/main.navi"
pr one/src build > /dev/null || p_fail "build below the crate"
[ -x "$pj/one/target/one" ] || [ -f "$pj/one/target/one.exe" ] || p_fail "no target/one"
set +e
r=$(pr one run -- x y); c=$?
set -e
[ "$r" = "args x y" ] && [ "$c" = 3 ] || p_fail "navi run: $r ($c)"
mkdir -p "$pj/none/src" && printf 'fn main() {}\n' > "$pj/none/src/main.navi"
case "$(pr none build)" in *"could not find navi.toml"*) ;; *) p_fail "a directory without navi.toml" ;; esac
mkdir -p "$pj/ws"
printf '[workspace]\nmembers = [\n    "app",\n]\nexclude = ["crates/old"]\n\n[workspace.dependencies]\nutil = { path = "crates/util" }\n' > "$pj/ws/navi.toml"
pr ws new --vcs none app > /dev/null && pr ws new --vcs none --lib crates/util > /dev/null || p_fail "navi new in a workspace"
grep -q '"crates/util",' "$pj/ws/navi.toml" || p_fail "new did not add the member: $(cat "$pj/ws/navi.toml")"
printf '\nutil = { workspace = true }\n' >> "$pj/ws/app/navi.toml"
printf 'fn main() { println(`twice ${util::twice(21)}`); }\n#[test]\nfn unit() { assert_eq(util::twice(2), 4); }\n' > "$pj/ws/app/src/main.navi"
mkdir -p "$pj/ws/app/tests" "$pj/ws/crates/util/tests"
printf '#[test]\nfn runs_the_program() {\n    let out = std::process::Command::new(std::env::var("NAVI_BIN_EXE_app").unwrap()).output().unwrap();\n    assert_eq(String::from_utf8(out.stdout).unwrap(), "twice 42\\n");\n}\n' > "$pj/ws/app/tests/cli.navi"
printf 'pub fn twice(x: i32) -> i32 { x * 2 }\n' > "$pj/ws/crates/util/src/lib.navi"
printf 'use util::twice;\n#[test]\nfn from_outside() { assert_eq(twice(5), 10); }\n' > "$pj/ws/crates/util/tests/api.navi"
mkdir -p "$pj/ws/crates/old/src" && printf '[package]\nname = "old"\n' > "$pj/ws/crates/old/navi.toml" && printf 'fn main() {}\n' > "$pj/ws/crates/old/src/main.navi"
pr ws build > /dev/null || p_fail "build the workspace"
[ "$(pr ws run)" = "twice 42" ] || p_fail "run in the workspace"
r=$(pr ws test)
[ "$(echo "$r" | grep -c '1 passed, 0 failed')" = 3 ] || p_fail "test the workspace: $r"
[ "$(echo "$r" | grep -c 'Running')" = 4 ] || p_fail "test the workspace: $r"
case "$(pr ws build -p nope)" in *"no crate called nope"*) ;; *) p_fail "-p nope" ;; esac
pr ws/crates/old build > /dev/null || p_fail "an excluded crate builds alone"
[ -f "$pj/ws/crates/old/target/old" ] || [ -f "$pj/ws/crates/old/target/old.exe" ] || p_fail "no target of the excluded crate"
mkdir -p "$pj/ws/stray/src" && printf '[package]\nname = "stray"\n' > "$pj/ws/stray/navi.toml" && printf 'fn main() {}\n' > "$pj/ws/stray/src/main.navi"
case "$(pr ws/stray build)" in *"not one of its members"*) ;; *) p_fail "a crate below a workspace, not a member" ;; esac
[ "$p_ok" = 1 ] && echo "ok   projects"
# The linker keeps a vtable's method only if something calls it through
# dyn (crate::merge, as Go's): the string only the uncalled method uses is
# left out (and kept with -d nostrip); the called one works.
dy=$tmp/dyn
mkdir -p "$dy"
printf 'trait T { fn used(&self) -> i32; fn unused(&self) -> String; }\nstruct S;\nimpl T for S {\n    fn used(&self) -> i32 { 42 }\n    fn unused(&self) -> String { "navi-unused-dyn-method".to_string() }\n}\nfn main() {\n    let t: &dyn T = &S;\n    println(`${t.used()}`);\n}\n' > "$dy/d.navi"
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) dx=.exe ;; *) dx= ;; esac
(cd "$dy" && "$navi" build -o d$dx d.navi 2>/dev/null && "$navi" build -d nostrip -o dk$dx d.navi 2>/dev/null)
if [ "$("$dy/d$dx" | tr -d '\r')" = 42 ] && ! grep -q navi-unused-dyn-method "$dy/d$dx" && grep -q navi-unused-dyn-method "$dy/dk$dx"; then
	echo "ok   dyn methods nothing calls left out"
else
	echo "FAIL dyn methods nothing calls left out"; fail=1
fi
# std's doctests: the examples in its documentation, each a test.
set +e
r=$("$navi" test --doc lib/std 2>&1); c=$?
set -e
if [ "$c" = 0 ]; then echo "ok   std doctests ($(echo "$r" | grep -E '^[0-9]+ passed' | tail -1))"; else echo "FAIL std doctests"; echo "$r" | grep -v '^ok ' | grep -v '^warning' | tail -30; fail=1; fi
# navi doc (docs/doc.md): a library's pages and std's, links in them, a
# warning for a link naming nothing; its doctests (run, should_panic,
# no_run, compile_fail, fn main).
dd=$tmp/doc
rm -rf "$dd" && cp -R "$root/tests/doc/geo" "$dd"
d_ok=1
d_fail() { echo "FAIL navi doc: $*"; d_ok=0; fail=1; }
set +e
r=$(cd "$dd" && "$navi" doc 2>&1); c=$?
set -e
[ "$c" = 0 ] || d_fail "exit $c: $r"
case "$r" in *'unresolved link to `missing`'*) ;; *) d_fail "no warning for [missing]: $r" ;; esac
case "$r" in *'unresolved link to `u8`'*) d_fail "warned about std: $r" ;; esac
for f in index.html search-index.js static/main.js geo/index.html geo/struct.Point.html geo/enum.Shape.html geo/trait.Area.html geo/fn.origin.html geo/util/index.html geo/util/fn.helper.html std/index.html std/vec/struct.Vec.html std/primitive.str.html; do
	[ -f "$dd/target/doc/$f" ] || d_fail "no $f"
done
grep -q 'href="../geo/struct.Point.html#method.new"' "$dd/target/doc/geo/struct.Point.html" || d_fail "intra-doc link to Point::new"
grep -q 'href="../geo/fn.origin.html"' "$dd/target/doc/geo/struct.Point.html" || d_fail "intra-doc link to origin"
grep -q 'href="../std/vec/struct.Vec.html"' "$dd/target/doc/geo/type.Points.html" || d_fail "a std type in a declaration"
grep -q 'impl.*Area.*for.*Shape' "$dd/target/doc/geo/trait.Area.html" || d_fail "implementors"
grep -q '/\* private fields \*/' "$dd/target/doc/geo/struct.Point.html" || d_fail "private fields"
grep -q '"dist","method","geo::Point"' "$dd/target/doc/search-index.js" || d_fail "search index"
grep -q 'href="../../std/primitive.f64.html"' "$dd/target/doc/geo/util/fn.helper.html" || d_fail "link to a primitive"
grep -q 'href="../../std/primitive.str.html#method.len"' "$dd/target/doc/geo/util/fn.helper.html" || d_fail "link to a primitive's method"
grep -q 'href="../../geo/index.html#examples"' "$dd/target/doc/geo/util/fn.helper.html" || d_fail "path#fragment link"
grep -q 'Its own docs' "$dd/target/doc/geo/util/inner/index.html" && ! grep -q 'Helpers' "$dd/target/doc/geo/util/inner/index.html" || d_fail "an inline module's docs"
[ -f "$dd/target/doc/std/cli/derive/derive.Parser.html" ] || d_fail "a derive macro's page"
set +e
r=$(cd "$dd" && "$navi" test 2>&1); c=$?
set -e
[ "$c" = 0 ] || d_fail "doctests: exit $c: $r"
case "$r" in *"Doc-tests geo"*"4 passed, 0 failed"*"Point::new (line 41) - compile fail"*) ;; *) d_fail "doctests: $r" ;; esac
[ "$d_ok" = 1 ] && echo "ok   navi doc"
exit $fail
