#!/bin/sh
# Runs the golden tests in tests/run, and navi test / bench on
# tests/harness, with a self-hosted compiler.
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
for f in tests/run/*.navi; do
	n=$(basename "$f" .navi)
	# // skip: windows (or unix): the test needs what that OS lacks.
	if grep -qxE "// skip: $os" "$f"; then echo "skip $f ($os)"; continue; fi
	# // error: line:col msg: the build fails with these errors.
	want_err=$(grep -E '^// error:' "$f" | sed -E 's#^// error: ?##')
	if [ -n "$want_err" ]; then
		"$navi" build -o "$tmp/$n" "$f" > "$tmp/$n.build" 2>&1 && { echo "FAIL $f (built)"; fail=1; continue; }
		got_err=$(sed -E 's#^[^:]*:([0-9]+:[0-9]+): #\1 #' "$tmp/$n.build")
		if [ "$got_err" = "$want_err" ]; then echo "ok   $f"; else echo "FAIL $f (errors)"; cat "$tmp/$n.build"; fail=1; fi
		continue
	fi
	# A file of #[test] functions (no // out:, // exit: or // stderr:
	# lines): navi test runs them, all must pass.
	if ! grep -qE '^// (out|exit|stderr|warn):' "$f" && grep -q '#\[test\]' "$f"; then
		set +e
		got=$(perl -e 'alarm(300); exec(@ARGV)' "$navi" test "$f" 2>&1)
		code=$?
		set -e
		if [ "$code" = 0 ]; then echo "ok   $f ($(echo "$got" | tail -1))"; else echo "FAIL $f (navi test, exit $code)"; echo "$got" | grep -v '^ok ' | head -30; fail=1; fi
		continue
	fi
	if ! "$navi" build -o "$tmp/$n" "$f" > "$tmp/$n.build" 2>&1; then
		echo "FAIL $f (build)"; cat "$tmp/$n.build"; fail=1; continue
	fi
	want_out=$(grep -E '^// out:' "$f" | sed -E 's#^// out: ?##')
	want_code=$(grep -E '^// exit:' "$f" | sed -E 's#^// exit: *##')
	[ -z "$want_code" ] && want_code=0
	set +e
	# A hung program fails after 5 minutes (perl's alarm: macOS has no
	# timeout command).
	got_out=$(cd "$tmp" && perl -e 'alarm(300); exec(@ARGV)' "./$n" 2>"$tmp/$n.err")
	code=$?
	if [ "$code" = 142 ]; then echo "FAIL $f (timed out)"; fail=1; continue; fi
	set -e
	# // warn: line:col msg lists the build's warnings.
	want_warn=$(grep -E '^// warn:' "$f" | sed -E 's#^// warn: ?##')
	got_warn=$(grep ': warning: ' "$tmp/$n.build" | sed -E 's#^[^:]*:([0-9]+:[0-9]+): warning: #\1 #')
	if [ "$got_warn" != "$want_warn" ]; then
		echo "FAIL $f (warnings)"; cat "$tmp/$n.build"; fail=1; continue
	fi
	# // stderr: text must appear in what the program writes to stderr.
	err_ok=1
	grep -E '^// stderr:' "$f" | sed -E 's#^// stderr: ?##' > "$tmp/$n.want_err"
	while IFS= read -r line; do
		[ -z "$line" ] && continue
		grep -qF -- "$line" "$tmp/$n.err" || { echo "FAIL $f (stderr lacks: $line)"; cat "$tmp/$n.err"; err_ok=0; }
	done < "$tmp/$n.want_err"
	if [ "$err_ok" = 0 ]; then fail=1; continue; fi
	if [ "$got_out" = "$want_out" ] && [ "$code" = "$want_code" ]; then
		echo "ok   $f"
	else
		echo "FAIL $f (exit $code, want $want_code)"; fail=1
		if [ "$got_out" != "$want_out" ]; then
			printf '%s\n' "$want_out" > "$tmp/$n.want"
			printf '%s\n' "$got_out" > "$tmp/$n.got"
			diff "$tmp/$n.want" "$tmp/$n.got" | head -20
			head -20 "$tmp/$n.err"
		fi
	fi
done
# navi test and navi bench on a crate with passing and failing tests.
set +e
got_out=$("$navi" test tests/harness 2>/dev/null)
code=$?
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
# The front end: tests/syntax parses; in tests/resolve (name resolution),
# tests/types and tests/check (type checking) the files of ok/ and sigs/
# have no errors and those of err/ exactly the errors their comments
# "// error: text" expect (an error on that line whose message has text).
front() {
	"$navi" "$1" "$2" > "$tmp/front.out" 2>&1
	grep '^error ' "$tmp/front.out" | sed -E 's#^error [^:]*:([0-9]+):[0-9]+: #\1	#' > "$tmp/front.got"
	grep -n '// error: ' "$2" | sed -E 's#^([0-9]+):.*// error: #\1	#' > "$tmp/front.want"
	if awk -F'\t' 'NR == FNR { want[NR] = $0; line[NR] = $1; text[NR] = $2; n = NR; next }
		{ for (i = 1; i <= n; i++) if (!used[i] && line[i] == $1 && index($2, text[i]) > 0) { used[i] = 1; next }
		  print "unexpected: " $1 ": " $2; bad = 1 }
		END { for (i = 1; i <= n; i++) if (!used[i]) { print "missing: " line[i] ": " text[i]; bad = 1 } exit bad }' \
		"$tmp/front.want" "$tmp/front.got" > "$tmp/front.diff"; then
		echo "ok   $2"
	else
		echo "FAIL $2 ($1)"; head -20 "$tmp/front.diff"; fail=1
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
if [ "$os" = windows ]; then dd=$(cygpath -m "$d"); fi
g() { git -C "$d/bar" -c user.email=t@t -c user.name=t "$@" > /dev/null; }
printf '[package]\nname = "bar"\n' > "$d/bar/navi.toml"
printf 'pub struct Version { pub n: i32 }\npub fn version() -> Version { Version { n: %s } }\n' 100 > "$d/bar/src/lib.navi"
g init -q && g add -A && g commit -qm one && g tag v1.0.0
printf 'pub struct Version { pub n: i32 }\npub fn version() -> Version { Version { n: %s } }\n' 110 > "$d/bar/src/lib.navi"
g commit -qam two && g tag -a v1.1.0 -m two
printf '[package]\nname = "util"\n' > "$d/util/navi.toml"
printf 'pub fn twice(x: i32) -> i32 { x * 2 }\n' > "$d/util/src/lib.navi"
printf '[package]\nname = "foo"\n\n[dependencies]\nutil = { path = "../util" }\nbar = { git = "file://%s/bar", tag = "v1.1.0" }\n' "$dd" > "$d/foo/navi.toml"
printf 'pub fn value() -> i32 { util::twice(21) }\npub fn bar_n() -> i32 { bar::version().n }\npub fn get() -> bar::Version { bar::version() }\n' > "$d/foo/src/lib.navi"
printf '[package]\nname = "app"\n\n[dependencies]\nf = { path = "../foo" }\nbar = { git = "file://%s/bar", tag = "v1.0.0" }\n' "$dd" > "$d/app/navi.toml"
printf 'fn main() { println(`${f::value()} ${f::bar_n()} ${bar::version().n}`); }\n' > "$d/app/src/main.navi"
deps() { (cd "$d/app" && NAVI_PKG="$dd/pkg" "$navi" "$@" 2>&1); }
r1=$(deps build -o app . > /dev/null && "$d/app/app")
locked=$(grep -c '^commit = ' "$d/app/navi.lock" 2> /dev/null)
printf 'pub struct Version { pub n: i32 }\npub fn version() -> Version { Version { n: %s } }\n' 101 > "$d/bar/src/lib.navi"
g commit -qam moved && g tag -f v1.0.0
r2=$(deps build -o app . > /dev/null && "$d/app/app")
r3=$(deps update > /dev/null && deps build -o app . > /dev/null && "$d/app/app")
printf 'fn take(v: bar::Version) -> i32 { v.n }\nfn main() { println(`${take(f::get())}`); }\n' > "$d/app/src/main.navi"
r4=$(deps build -o app . | sed -E 's#^[^ ]*:[0-9]+:[0-9]+: ##')
if [ "$r1" = "42 110 100" ] && [ "$locked" = 2 ] && [ "$r2" = "42 110 100" ] && [ "$r3" = "42 110 101" ] \
	&& [ "$r4" = "mismatched types: expected bar@v1.0.0::Version, found bar::Version" ]; then
	echo "ok   dependencies"
else
	echo "FAIL dependencies"; printf '%s\n' "$r1" "$locked" "$r2" "$r3" "$r4"; fail=1
fi
for f in tests/types/*/*.navi tests/check/*/*.navi; do front types-dump "$f"; done
exit $fail
