#!/bin/sh
# Runs the golden tests in tests/run, and navic test / bench on
# tests/harness, with a self-hosted compiler.
#
#   tools/navictest.sh path/to/navic
set -e
cd "$(dirname "$0")/.."
navic=${1:?usage: tools/navictest.sh path/to/navic}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
for f in tests/run/*.navi; do
	n=$(basename "$f" .navi)
	# // error: line:col msg: the build fails with these errors.
	want_err=$(grep -E '^// error:' "$f" | sed -E 's#^// error: ?##')
	if [ -n "$want_err" ]; then
		"$navic" build -o "$tmp/$n" "$f" > "$tmp/$n.build" 2>&1 && { echo "FAIL $f (built)"; fail=1; continue; }
		got_err=$(sed -E 's#^[^:]*:([0-9]+:[0-9]+): #\1 #' "$tmp/$n.build")
		if [ "$got_err" = "$want_err" ]; then echo "ok   $f"; else echo "FAIL $f (errors)"; cat "$tmp/$n.build"; fail=1; fi
		continue
	fi
	if ! "$navic" build -o "$tmp/$n" "$f" > "$tmp/$n.build" 2>&1; then
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
	if [ "$got_out" = "$want_out" ] && [ "$code" = "$want_code" ]; then
		echo "ok   $f"
	else
		echo "FAIL $f (exit $code, want $want_code)"; fail=1
	fi
done
# navi test and navi bench on a crate with passing and failing tests.
set +e
got_out=$("$navic" test tests/harness 2>/dev/null)
code=$?
set -e
if [ "$got_out" = "$(cat tests/harness/expected.out)" ] && [ "$code" = 1 ]; then
	echo "ok   navic test tests/harness"
else
	echo "FAIL navic test tests/harness (exit $code)"; echo "$got_out"; fail=1
fi
if [ "$("$navic" bench tests/harness 2>&1 | grep -c ' ns/iter ')" = 2 ]; then
	echo "ok   navic bench tests/harness"
else
	echo "FAIL navic bench tests/harness"; fail=1
fi
# navic fmt: tests/fmt/x.navi formats to x.out, which stays as it is;
# --check reports a file that would change and passes one that would not.
for f in tests/fmt/*.navi; do
	want="${f%.navi}.out"
	if "$navic" fmt - < "$f" > "$tmp/fmt.out" 2>&1 && cmp -s "$tmp/fmt.out" "$want" \
		&& "$navic" fmt - < "$want" > "$tmp/fmt2.out" 2>&1 && cmp -s "$tmp/fmt2.out" "$want"; then
		echo "ok   navic fmt $f"
	else
		echo "FAIL navic fmt $f"; diff "$want" "$tmp/fmt.out" | head -20; fail=1
	fi
done
cp tests/fmt/exprs.navi "$tmp/check.navi"
set +e
"$navic" fmt --check "$tmp/check.navi" > /dev/null 2>&1; c1=$?
"$navic" fmt "$tmp/check.navi" > /dev/null 2>&1; c2=$?
"$navic" fmt --check "$tmp/check.navi" > /dev/null 2>&1; c3=$?
set -e
if [ "$c1" = 1 ] && [ "$c2" = 0 ] && [ "$c3" = 0 ] && cmp -s "$tmp/check.navi" tests/fmt/exprs.out; then
	echo "ok   navic fmt --check"
else
	echo "FAIL navic fmt --check ($c1 $c2 $c3)"; fail=1
fi
# comment_width from navi.toml's [fmt] table, and --comment-width over it.
mkdir -p "$tmp/cw/src"
printf '[package]\nname = "cw"\n\n[fmt]\ncomment_width = 40\n' > "$tmp/cw/navi.toml"
long='// one two three four five six seven eight nine ten'
printf '%s\nfn main() {}\n' "$long" > "$tmp/cw/src/main.navi"
"$navic" fmt --comment-width 100 "$tmp/cw" > /dev/null 2>&1
w100=$(cat "$tmp/cw/src/main.navi")
"$navic" fmt "$tmp/cw" > /dev/null 2>&1
w40=$(cat "$tmp/cw/src/main.navi")
if [ "$w100" = "$(printf '%s\nfn main() {}' "$long")" ] \
	&& [ "$w40" = "$(printf '// one two three four five six seven\n// eight nine ten\nfn main() {}')" ]; then
	echo "ok   navic fmt comment_width"
else
	echo "FAIL navic fmt comment_width"; echo "$w100"; echo "$w40"; fail=1
fi
# navic lsp: the recorded session tests/lsp/session.jsonl (one message a
# line, the repository's path as @ROOT@) answers as tests/lsp/session.out.
root=$(pwd)
sed "s#@ROOT@#$root#g" tests/lsp/session.jsonl | LC_ALL=C awk '{ printf "Content-Length: %d\r\n\r\n%s", length($0), $0 }' \
	| NAVI_STD="$root/lib/std" "$navic" lsp 2>/dev/null | perl -0777 -pe 's/Content-Length: \d+\r\n\r\n/\n/g' \
	| sed "s#$root#@ROOT@#g" | sed '/^$/d' > "$tmp/lsp.out"
if cmp -s "$tmp/lsp.out" tests/lsp/session.out; then
	echo "ok   navic lsp tests/lsp/session.jsonl"
else
	echo "FAIL navic lsp tests/lsp/session.jsonl"; diff tests/lsp/session.out "$tmp/lsp.out" | head -20; fail=1
fi
exit $fail
