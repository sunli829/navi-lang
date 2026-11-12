#!/bin/sh
# Bootstraps the self-hosted compiler and checks the fixed point:
#   stage0 (Go) builds stage1 from compiler/, stage1 builds stage2, stage2
#   builds stage3; stage2 and stage3 must be byte-identical. Also checks
#   that stage1's object file for the compiler equals stage0's.
#
#   tools/bootstrap.sh [outdir]
set -e
cd "$(dirname "$0")/.."
out=${1:-$(mktemp -d)}
mkdir -p "$out/stage1" "$out/stage2" "$out/stage3"
go build -o "$out/navi0" ./cmd/navi
"$out/navi0" build -o "$out/stage1/navic" compiler
"$out/navi0" build -c -o "$out/stage0.o" compiler
"$out/stage1/navic" build -c -o "$out/stage1.o" compiler
cmp "$out/stage0.o" "$out/stage1.o"
echo "stage1 object == stage0 object"
"$out/stage1/navic" build -o "$out/stage2/navic" compiler
"$out/stage2/navic" build -o "$out/stage3/navic" compiler
cmp "$out/stage2/navic" "$out/stage3/navic"
echo "stage2 == stage3 ($out/stage2/navic)"
