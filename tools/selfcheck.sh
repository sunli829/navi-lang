#!/bin/sh
# Compares the debug dumps of navic (the self-hosted compiler) with stage0
# on every test crate and on the compiler itself (navic in NAVI_STAGE0
# mode: no escape analysis, no "navic" feature in std).
#
#   tools/selfcheck.sh [resolve-dump|types-dump ...]
set -e
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/navi0" ./cmd/navi
"$tmp/navi0" build -o "$tmp/navic" compiler
cmds=${*:-"resolve-dump types-dump mir-dump"}
crates=$( (find tests/syntax tests/resolve tests/types tests/check tests/run -name '*.navi'; echo compiler) | sort)
fail=0
for cmd in $cmds; do
	for c in $crates; do
		# Programs using navic-only features (stage0 is frozen).
		[ -f "$c" ] && grep -q '// stage0: skip' "$c" && continue
		n=$(echo "$c" | tr '/' '_')
		"$tmp/navi0" "$cmd" "$c" > "$tmp/$n.0" 2>&1 || true
		NAVI_STAGE0=1 "$tmp/navic" "$cmd" "$c" > "$tmp/$n.1" 2>&1 || true
		if ! cmp -s "$tmp/$n.0" "$tmp/$n.1"; then
			echo "DIFF $cmd $c"
			diff "$tmp/$n.0" "$tmp/$n.1" | head -5
			fail=1
		fi
	done
done
[ $fail = 0 ] && echo "selfcheck: navic matches stage0 ($cmds)"
exit $fail
