#!/bin/sh
# Install the nightly fern compiler.
#
#   curl -fsSL https://fern-lang.netlify.app/install.sh | sh
#
# The script downloads the archive for this computer from the rolling
# "nightly" release, checks its SHA-256, and copies `fern` into the install
# directory. When the GitHub CLI is installed and logged in, it also checks
# the build provenance of the archive with `gh attestation verify`. It does
# not change your shell configuration.
#
# Environment:
#   FERN_INSTALL_DIR        where to put fern (default: $HOME/.local/bin)
#   FERN_RELEASE_URL        the release to download from
#   FERN_SKIP_ATTESTATION   set to 1 to skip the provenance check
set -eu

repo=JakeChampion/lang
release=${FERN_RELEASE_URL:-https://github.com/$repo/releases/download/nightly}
dir=${FERN_INSTALL_DIR:-$HOME/.local/bin}

fail() {
  echo "fern install: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "this script needs $1"
}

need curl
need tar
need uname

os=$(uname -s)
arch=$(uname -m)
# A shell under Rosetta reports x86_64 on Apple silicon.
if [ "$os" = Darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64
fi

case "$os/$arch" in
  Linux/x86_64 | Linux/amd64) asset=fern-linux-x86_64 ;;
  Linux/aarch64 | Linux/arm64) asset=fern-linux-arm64 ;;
  Darwin/arm64) asset=fern-darwin-arm64 ;;
  Darwin/*) fail "there is no build for Intel Macs. Fern runs on Apple silicon." ;;
  *) fail "there is no build for $os $arch. Fern runs on Linux arm64, Linux x86-64 and macOS arm64. To build from source, see https://github.com/$repo#readme" ;;
esac

# The x86-64 build needs x86-64-v3 with AES-NI, and has no fallback for an older CPU.
if [ "$asset" = fern-linux-x86_64 ] && [ -r /proc/cpuinfo ]; then
  for flag in avx2 bmi2 aes; do
    grep -qw "$flag" /proc/cpuinfo || fail "this CPU has no $flag. The x86-64 build needs an x86-64-v3 CPU with AES-NI (Intel Haswell or AMD Zen, or newer)."
  done
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "this script needs sha256sum or shasum"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "fern install: downloading $asset.tar.gz"
curl -fsSL -o "$tmp/$asset.tar.gz" "$release/$asset.tar.gz" || fail "cannot download $release/$asset.tar.gz"
curl -fsSL -o "$tmp/$asset.tar.gz.sha256" "$release/$asset.tar.gz.sha256" || fail "cannot download $release/$asset.tar.gz.sha256"

want=$(cut -d' ' -f1 <"$tmp/$asset.tar.gz.sha256")
got=$(sha256 "$tmp/$asset.tar.gz")
[ "$want" = "$got" ] || fail "the SHA-256 of $asset.tar.gz is $got, not $want. Nothing was installed."
echo "fern install: SHA-256 matches"

if [ "${FERN_SKIP_ATTESTATION:-}" = 1 ]; then
  echo "fern install: provenance check skipped (FERN_SKIP_ATTESTATION=1)"
elif command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  gh attestation verify "$tmp/$asset.tar.gz" --repo "$repo" >/dev/null 2>&1 \
    || fail "gh attestation verify found no valid build provenance for $asset.tar.gz. Nothing was installed."
  echo "fern install: build provenance verified"
else
  echo "fern install: provenance check skipped (no logged-in gh)"
fi

mkdir -p "$tmp/x" "$dir"
tar -xzf "$tmp/$asset.tar.gz" -C "$tmp/x"
[ -f "$tmp/x/fern" ] || fail "$asset.tar.gz has no fern binary"
cp "$tmp/x/fern" "$dir/fern.new"
chmod 755 "$dir/fern.new"
# A rename replaces a running fern safely, and macOS does not kill the new binary.
mv -f "$dir/fern.new" "$dir/fern"

echo "fern install: installed $("$dir/fern" -version | head -n 1) at $dir/fern"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "fern install: $dir is not on your PATH. Add it, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
