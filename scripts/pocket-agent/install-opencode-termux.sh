#!/usr/bin/env bash
# Explicit opt-in to the community native bionic package, pinned by digest.
set -euo pipefail
[[ ${PREFIX:-} == /data/*/com.termux*/files/usr && $(uname -m) == aarch64 ]] || { echo 'OPENCODE FAIL: native ARM64 Termux required' >&2; exit 1; }
pkg install -y curl coreutils
package=opencode_2.0.12_aarch64.deb
expected=7a11f1b6891e0fd3047101aaaa4a1ac9ec289af707884c6fdc1964fdc1a50060
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
curl --fail --location --proto '=https' --tlsv1.2 "https://github.com/Hope2333/opencode-termux/releases/download/Push260922/$package" --output "$stage/$package"
actual=$(sha256sum "$stage/$package")
[[ ${actual%% *} == "$expected" ]] || { echo 'OPENCODE FAIL: package digest changed; inspect release before updating the pin' >&2; exit 1; }
apt install -y "$stage/$package"
opencode --version
opencode acp --help >/dev/null
echo 'OPENCODE PASS: native executable and ACP help respond. Authenticate your provider with opencode auth login, then run validate-thor.py --chat provider/model.'
