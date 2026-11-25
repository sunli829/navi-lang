#!/bin/sh
# Installs navi, or upgrades it to the latest release (macOS and Linux):
#
#   curl -fsSL https://raw.githubusercontent.com/sunli829/navi-lang/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- v0.0.5        (a given release)
#
# The release's package (bin/navi and lib/std) goes to $NAVI_HOME
# (~/.navi by default), replacing what is there; its SHA-256 is checked
# against the release's SHA256SUMS. Add $NAVI_HOME/bin to PATH once.
set -eu

repo=https://github.com/sunli829/navi-lang
home=${NAVI_HOME:-$HOME/.navi}
version=${1:-}

fail() { echo "navi install: $*" >&2; exit 1; }
command -v curl > /dev/null || fail "needs curl"
command -v tar > /dev/null || fail "needs tar"

case "$(uname -s)" in
	Darwin) os=macos ;;
	Linux) os=linux ;;
	*) fail "no release for $(uname -s) (Windows: install.ps1)" ;;
esac
case "$(uname -m)" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=x64 ;;
	*) fail "no release for $(uname -m)" ;;
esac

if [ -z "$version" ]; then
	# The latest release: where /releases/latest redirects (.../tag/vX).
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") || fail "cannot reach $repo"
	version=${url##*/}
	case "$version" in v*) ;; *) fail "no release found at $repo" ;; esac
fi

pkg=navi-$version-$os-$arch
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $pkg"
curl -fsSL -o "$tmp/$pkg.tar.gz" "$repo/releases/download/$version/$pkg.tar.gz" || fail "no package $pkg in release $version"
curl -fsSL -o "$tmp/SHA256SUMS" "$repo/releases/download/$version/SHA256SUMS" || fail "no SHA256SUMS in release $version"
want=$(grep " $pkg.tar.gz\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
if command -v sha256sum > /dev/null; then
	got=$(sha256sum "$tmp/$pkg.tar.gz" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$tmp/$pkg.tar.gz" | cut -d' ' -f1)
fi
[ -n "$want" ] && [ "$want" = "$got" ] || fail "checksum mismatch for $pkg.tar.gz"
tar xzf "$tmp/$pkg.tar.gz" -C "$tmp"

# The new bin and lib replace the old ones (an upgrade leaves no old std).
mkdir -p "$home"
rm -rf "$home/bin" "$home/lib"
mv "$tmp/$pkg/bin" "$tmp/$pkg/lib" "$home/"
echo "navi $version installed in $home"

case ":$PATH:" in
	*":$home/bin:"*) ;;
	*)
		echo "Add it to PATH (in ~/.zshrc, ~/.bashrc or ~/.profile):"
		echo "    export PATH=\"$home/bin:\$PATH\""
		;;
esac
