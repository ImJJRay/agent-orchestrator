#!/usr/bin/env bash
# Invoke with bash. Never downloads or launches Electron.
set -euo pipefail
export AO_DATA_DIR=${AO_DATA_DIR:-$HOME/.ao}
export TMPDIR=${TMPDIR:-${PREFIX:+$PREFIX/tmp}}
export TMPDIR=${TMPDIR:-/tmp}
[[ -n ${PREFIX:-} ]] && export SHELL=$PREFIX/bin/bash
export PATH="$AO_DATA_DIR/bin:$PATH"
export AO_MOBILE_TAILNET_ONLY=1
export TS_LOGS_DIR="$AO_DATA_DIR/logs"
export AO_TAILSCALE_BINARY="$AO_DATA_DIR/bin/tailscale"
export AO_TAILSCALE_SOCKET="$AO_DATA_DIR/mobile/tailscaled.sock"
mkdir -p "$AO_DATA_DIR/logs" "$AO_DATA_DIR/mobile"
chmod 700 "$AO_DATA_DIR/logs" "$AO_DATA_DIR/mobile"
ready() { ao status --json 2>/dev/null | python -c 'import json,sys; sys.exit(0 if json.load(sys.stdin)["state"] == "ready" else 1)' 2>/dev/null; }
case ${1:-start} in
  start)
    if ready; then echo 'DAEMON PASS: already ready'; exit 0; fi
    # The daemon itself arbitrates ownership. Do not kill a PID from an old file.
    nohup ao daemon >>"$AO_DATA_DIR/logs/thor-daemon.log" 2>&1 </dev/null &
    for ((i=0; i<60; i++)); do
      if ready; then echo 'DAEMON PASS: loopback API ready'; exit 0; fi
      sleep 0.5
    done
    echo "DAEMON FAIL: inspect $AO_DATA_DIR/logs/thor-daemon.log" >&2; exit 1 ;;
  stop) ao stop ;;
  status) ao status --json ;;
  tailscale-start)
    [[ -x $AO_DATA_DIR/bin/tailscaled ]] || { echo 'TAILSCALE FAIL: bootstrap with --tailscale first' >&2; exit 1; }
    if "$AO_TAILSCALE_BINARY" --socket="$AO_TAILSCALE_SOCKET" status --json >/dev/null 2>&1; then
      echo 'TAILSCALE PASS: userspace daemon already responds'; exit 0
    fi
    nohup "$AO_DATA_DIR/bin/tailscaled" --tun=userspace-networking --socket="$AO_TAILSCALE_SOCKET" --statedir="$AO_DATA_DIR/mobile/tailscale" >>"$AO_DATA_DIR/logs/thor-tailscaled.log" 2>&1 </dev/null &
    for ((i=0; i<30; i++)); do
      if "$AO_TAILSCALE_BINARY" --socket="$AO_TAILSCALE_SOCKET" status --json >/dev/null 2>&1; then
        echo 'TAILSCALE PASS: socket responds. Now run thor.sh tailscale-login'; exit 0
      fi
      sleep 0.5
    done
    echo "TAILSCALE FAIL: inspect $AO_DATA_DIR/logs/thor-tailscaled.log" >&2; exit 1 ;;
  tailscale-login) "$AO_TAILSCALE_BINARY" --socket="$AO_TAILSCALE_SOCKET" up --hostname=thor-ao ;;
  tailscale-status) "$AO_TAILSCALE_BINARY" --socket="$AO_TAILSCALE_SOCKET" status --json ;;
  mobile-enable) ao mobile enable >/dev/null; ao mobile secure-pairing on >/dev/null; echo 'CONNECT_MOBILE PASS: enabled with private HTTPS; run thor.sh pair' ;;
  mobile-disable) ao mobile disable >/dev/null ;;
  pair) ao mobile pair --name 'AYN Thor Max' ;;
  *) echo 'Usage: bash thor.sh start|stop|status|tailscale-start|tailscale-login|tailscale-status|mobile-enable|mobile-disable|pair' >&2; exit 2 ;;
esac
