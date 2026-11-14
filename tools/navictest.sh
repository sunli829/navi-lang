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
	got_out=$(cd "$tmp" && "./$n" 2>"$tmp/$n.err")
	code=$?
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
exit $fail
