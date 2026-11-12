#!/bin/sh
# Compares navic with go build on the synthetic projects of bench/gen
# (plan section 6): cold build, rebuild without changes, rebuild after
# changing one function body, and peak memory.
#
#   tools/bench.sh path/to/navic [lines...]     (default: 10000 100000)
#
# Go gets a private cache holding only its std (as in everyday use), plus
# a fully cold run; navic compiles its std every time unless its build
# cache hits. The user's own Go cache is not touched.
set -e
cd "$(dirname "$0")/.."
navic=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
shift
export NAVI_STD=$PWD/lib/std
sizes=${*:-"10000 100000"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
t() { /usr/bin/time -l "$@" 2>&1 >/dev/null | awk '/real/ {r=$1} /maximum resident/ {m=$1} END {printf "%.2fs %dMB", r, m/1048576}'; }
printf "%-8s %-10s %-16s %-16s %-16s\n" lines compiler cold no-change one-body
for n in $sizes; do
	d=$tmp/p$n
	go run ./bench/gen -lines "$n" -out "$d" >/dev/null
	cache=$tmp/cache$n
	cold=$(cd "$d/navi" && NAVI_CACHE=$cache t "$navic" build -o "$tmp/nb" .)
	noop=$(cd "$d/navi" && NAVI_CACHE=$cache t "$navic" build -o "$tmp/nb" .)
	sed -i '' 's/dx \* dx + dy \* dy/dx * dx + dy * dy + 0/' "$d/navi/src/m0.navi"
	one=$(cd "$d/navi" && NAVI_CACHE=$cache t "$navic" build -o "$tmp/nb" .)
	printf "%-8s %-10s %-16s %-16s %-16s\n" "$n" navic "$cold" "$noop" "$one"
	# Go with an empty cache of its own (std compiled too), then with only
	# std in the cache (the usual situation).
	gfull=$(cd "$d/go" && GOCACHE=$tmp/gofull t go build -o "$tmp/gb" .)
	mkdir -p "$tmp/hello" && printf 'module hello\n\ngo 1.22\n' > "$tmp/hello/go.mod"
	printf 'package main\n\nimport (\n\t"fmt"\n\t"strings"\n)\n\nfunc main() { fmt.Println(strings.Repeat("a", 1)) }\n' > "$tmp/hello/main.go"
	(cd "$tmp/hello" && GOCACHE=$tmp/gocache$n go build -o "$tmp/hb" . >/dev/null 2>&1)
	export GOCACHE=$tmp/gocache$n
	gcold=$(cd "$d/go" && t go build -o "$tmp/gb" .)
	gnoop=$(cd "$d/go" && t go build -o "$tmp/gb" .)
	sed -i '' 's/dx\*dx + dy\*dy/dx*dx + dy*dy + 0/' "$d/go/m0.go"
	gone=$(cd "$d/go" && t go build -o "$tmp/gb" .)
	unset GOCACHE
	printf "%-8s %-10s %-16s %-16s %-16s\n" "$n" "go build" "$gcold" "$gnoop" "$gone"
	printf "%-8s %-10s %-16s\n" "$n" "go (no cache)" "$gfull"
done
