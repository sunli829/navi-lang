#!/bin/sh
# Runs the golden tests in tests/run with a self-hosted compiler.
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
	if [ "$got_out" = "$want_out" ] && [ "$code" = "$want_code" ]; then
		echo "ok   $f"
	else
		echo "FAIL $f (exit $code, want $want_code)"; fail=1
	fi
done
exit $fail
