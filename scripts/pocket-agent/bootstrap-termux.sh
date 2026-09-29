#!/usr/bin/env bash
# Run explicitly with bash on Termux; no desktop installer or proot.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
[[ ${PREFIX:-} == /data/*/com.termux*/files/usr ]] || { echo 'GO_BUILD FAIL: run in native Termux' >&2; exit 1; }
[[ $(uname -m) == aarch64 ]] || { echo 'GO_BUILD FAIL: ARM64 required' >&2; exit 1; }
with_tailscale=false
case ${1:-} in '' ) ;; --tailscale ) with_tailscale=true ;; * ) echo 'Usage: bash bootstrap-termux.sh [--tailscale]' >&2; exit 2 ;; esac
pkg install -y golang git clang tmux curl python
export AO_DATA_DIR=${AO_DATA_DIR:-$HOME/.ao}
export TMPDIR=${TMPDIR:-$PREFIX/tmp}
export SHELL=$PREFIX/bin/bash
[[ $AO_DATA_DIR == "$HOME"/* ]] || { echo 'FILESYSTEM FAIL: AO_DATA_DIR must be in Termux-owned home storage' >&2; exit 1; }
mkdir -p "$AO_DATA_DIR/bin" "$AO_DATA_DIR/logs"
chmod 700 "$AO_DATA_DIR" "$AO_DATA_DIR/logs"
[[ $(go env GOOS) == android ]] || { echo 'GO_BUILD FAIL: use the native Termux Go toolchain' >&2; exit 1; }
(cd "$root/backend" && CGO_ENABLED=1 go build -trimpath -o "$AO_DATA_DIR/bin/ao.new" ./cmd/ao)
mv "$AO_DATA_DIR/bin/ao.new" "$AO_DATA_DIR/bin/ao"
echo 'GO_BUILD PASS: native AO built'
if $with_tailscale; then
  # Official source, Android ARM64: no TUN, root, glibc, or VPN API.
  # This separate userspace node retains upstream tailscale serve semantics.
  ts_version=v1.102.5
  ts_source=$(GOWORK=off go mod download -json "tailscale.com@$ts_version" | python -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')
  for binary in tailscale tailscaled; do
    (cd "$ts_source" && GOOS=android GOARCH=arm64 CGO_ENABLED=1 go build -mod=readonly -trimpath -tags=ts_omit_systray,ts_omit_ssh -o "$AO_DATA_DIR/bin/$binary.new" "./cmd/$binary")
    mv "$AO_DATA_DIR/bin/$binary.new" "$AO_DATA_DIR/bin/$binary"
  done
  echo 'TAILSCALE PASS: official userspace binaries built; login and device execution remain to validate'
fi
printf 'Next: bash %q start\nThen: python %q\n' "$root/scripts/pocket-agent/thor.sh" "$root/scripts/pocket-agent/validate-thor.py"
echo 'OpenCode is installed separately. See docs/pocket-agent/THOR.md for native builds and ACP verification.'
