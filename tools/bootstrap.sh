#!/bin/sh
# Bootstraps navi from an earlier release (as Go builds itself with an
# earlier Go): the release's navi (the version in tools/bootstrap-version,
# downloaded and checked, or $NAVI_BOOTSTRAP, a navi to use) builds stage1
# from compiler/ with this checkout's std, stage1 builds stage2, stage2
# builds stage3; stage2 and stage3 must be byte-identical.
#
# So compiler/ and lib/std may only use what that release supports: a new
# language feature or intrinsic is used there once a release has it (then
# tools/bootstrap-version moves to that release).
#
#   tools/bootstrap.sh [outdir]      (the tool: outdir/stage2/navi)
set -e
cd "$(dirname "$0")/.."
root=$PWD
out=${1:-$(mktemp -d)}
mkdir -p "$out/stage1" "$out/stage2" "$out/stage3"
exe=
ext=tar.gz
case "$(uname -s)" in
Darwin) os=macos ;;
Linux) os=linux ;;
MINGW* | MSYS* | CYGWIN*) os=windows exe=.exe ext=zip ;;
*) echo "bootstrap: unsupported host $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=x64 ;;
*) echo "bootstrap: unsupported host $(uname -m)" >&2; exit 1 ;;
esac
if [ $os = windows ]; then
	NAVI_STD=$(cygpath -m "$root/lib/std")
else
	NAVI_STD=$root/lib/std
fi
export NAVI_STD

boot=$NAVI_BOOTSTRAP
if [ -z "$boot" ]; then
	v=$(cat tools/bootstrap-version)
	pkg=navi-$v-$os-$arch
	dir=$out/boot
	# (Releases before v0.0.2 call it navic.)
	boot=$dir/$pkg/bin/navi$exe
	[ -x "$boot" ] || [ ! -x "$dir/$pkg/bin/navic$exe" ] || boot=$dir/$pkg/bin/navic$exe
	if [ ! -x "$boot" ]; then
		mkdir -p "$dir"
		url=https://github.com/sunli829/navi-lang/releases/download/$v
		curl -fsSL -o "$dir/$pkg.$ext" "$url/$pkg.$ext"
		curl -fsSL -o "$dir/SHA256SUMS" "$url/SHA256SUMS"
		if command -v sha256sum > /dev/null; then sum="sha256sum"; else sum="shasum -a 256"; fi
		(cd "$dir" && grep " $pkg.$ext\$" SHA256SUMS | $sum -c -)
		if [ $ext = zip ]; then
			(cd "$dir" && unzip -q "$pkg.$ext")
		else
			tar xzf "$dir/$pkg.$ext" -C "$dir"
		fi
		[ -x "$boot" ] || boot=$dir/$pkg/bin/navic$exe
	fi
fi
echo "bootstrap: $boot"
"$boot" build -o "$out/stage1/navi$exe" compiler
"$out/stage1/navi$exe" build -o "$out/stage2/navi$exe" compiler
"$out/stage2/navi$exe" build -o "$out/stage3/navi$exe" compiler
cmp "$out/stage2/navi$exe" "$out/stage3/navi$exe"
echo "stage2 == stage3 ($out/stage2/navi$exe)"
