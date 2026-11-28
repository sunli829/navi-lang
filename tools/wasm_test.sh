#!/bin/sh
# The tests of js-wasm32 (docs/wasm.md): tests/run but for what a browser
# has not (files, sockets, processes, C functions, assembly), and
# tests/wasm (std::js, the DOM), each run in a headless browser (navi test
# finds Chrome or Chromium, else Firefox; NAVI_BROWSER names one).
#
#   tools/wasm_test.sh path/to/navi
set -e
navi=${1:-navi}
cd "$(dirname "$0")/.."
skip=" asm c_blocking c_callback c_link c_many_args c_variadic cli cli_more env_process fatal fs_ops gc gc_pacer gc_release hpack http http2 io_adapters log_builder net os panics preempt std_edges tls tls_server unicode_os "
files=""
for f in tests/run/*.navi; do
	n=$(basename "$f" .navi)
	case "$skip" in *" $n "*) continue ;; esac
	files="$files $f"
done
for f in tests/wasm/*.navi; do
	files="$files $f"
done
fail=0
for f in $files; do
	if out=$("$navi" test --target js-wasm32 --timeout 2m "$f" 2>&1); then
		echo "ok   $f ($(echo "$out" | grep -E '^[0-9]+ passed' | tail -1))"
	else
		echo "FAIL $f"
		echo "$out" | grep -v '^ok ' | tail -20
		fail=1
	fi
done
exit $fail
