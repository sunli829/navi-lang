#!/bin/sh
# Runs the golden tests in tests/run as linux-arm64 programs on an Android
# arm64 device or emulator (a Linux kernel with bionic), with adb: navi
# links them itself (for bionic: NAVI_ELF_LIBC=bionic), or with
# ANDROID_LINK=ndk writes objects the NDK's clang links.
#
#   tools/androidtest.sh path/to/navi [VAR=value ...]
#
# $ANDROID_NDK (default: the newest under ~/Library/Android/sdk/ndk) and
# $ADB (default: adb from the SDK's platform-tools) locate the tools.
set -e
cd "$(dirname "$0")/.."
navi=${1:?usage: tools/androidtest.sh path/to/navi [VAR=value ...]}
shift
sdk=$HOME/Library/Android/sdk
ndk=${ANDROID_NDK:-$(ls -d "$sdk"/ndk/* | sort | tail -1)}
adb=${ADB:-$sdk/platform-tools/adb}
cc=$(ls -d "$ndk"/toolchains/llvm/prebuilt/*/bin | head -1)/clang
dir=/data/local/tmp/navi-test-$$
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"; "$adb" shell rm -rf $dir' EXIT
mkdir -p "$tmp/bin"
fail=0
for f in tests/run/*.navi; do
	n=$(basename "$f" .navi)
	grep -q '^// error:' "$f" && continue
	if [ "$ANDROID_LINK" = ndk ]; then
		if ! "$navi" build --target linux-arm64 -c -o "$tmp/$n.o" "$f" > "$tmp/$n.build" 2>&1; then
			echo "FAIL $f (build)"; cat "$tmp/$n.build"; fail=1; continue
		fi
		"$cc" --target=aarch64-linux-android35 -o "$tmp/bin/$n" "$tmp/$n.o" -lm || { echo "FAIL $f (link)"; fail=1; }
	elif ! NAVI_ELF_LIBC=bionic "$navi" build --target linux-arm64 -o "$tmp/bin/$n" "$f" > "$tmp/$n.build" 2>&1; then
		echo "FAIL $f (build)"; cat "$tmp/$n.build"; fail=1
	fi
done
"$adb" shell rm -rf $dir
"$adb" shell mkdir -p $dir
"$adb" push "$tmp/bin/." $dir/ > /dev/null
for b in "$tmp"/bin/*; do
	n=$(basename "$b")
	f=tests/run/$n.navi
	want_out=$(grep -E '^// out:' "$f" | sed -E 's#^// out: ?##')
	want_code=$(grep -E '^// exit:' "$f" | sed -E 's#^// exit: *##')
	[ -z "$want_code" ] && want_code=0
	got=$("$adb" shell "cd $dir && chmod 755 $n && env $* ./$n 2>/dev/null; echo EXIT=\$?" | tr -d '\r')
	# printf, not echo: sh's echo would expand backslashes in the output.
	code=$(printf '%s\n' "$got" | tail -1 | sed 's/EXIT=//')
	got_out=$(printf '%s\n' "$got" | sed '$d')
	if [ "$got_out" = "$want_out" ] && [ "$code" = "$want_code" ]; then
		echo "ok   $f"
	else
		echo "FAIL $f (exit $code, want $want_code)"; fail=1
	fi
done
exit $fail
