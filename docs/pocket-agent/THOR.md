# Thor Runtime + iPhone Remote Control

This is the deployment runbook for `pocket-main`. Native Android ARM64 compilation
and Linux runtime checks have passed. **No physical Thor or iPhone validation has
been performed.** The final acceptance is the device procedure below, not the
cross-build alone.

## What is reused

The current fork already builds with `GOOS=android GOARCH=arm64 CGO_ENABLED=0`.
Go's Android target includes Linux build constraints, so existing Unix process,
PTY, and pure-Go SQLite sources compile without vendoring or changing dependencies.
Durable Chat, OpenCode ACP, result extraction, restore, worktrees, mobile
bearer authentication, password persistence, and secure Serve lifecycle are all
existing AO implementations.

Android selects the existing tmux runtime for TUI sessions. Desktop Linux retains
its detached PTY runtime. Chat uses the existing persistent ACP host, independently
of the TUI controller. No new agent adapter, mobile app, Electron dependency, PWA,
proot, local model, or routing engine is introduced.

## Requirements

- Native ARM64 Termux, using its private home filesystem. Run the scripts with
  `bash`; their portable `/usr/bin/env` shebang is not an Android installation path.
- Termux Go satisfying `backend/go.mod` (currently Go 1.27.1), Git, clang, tmux,
  Python and curl. Bootstrap installs native package dependencies.
- Keep checkout, repositories, worktrees, SQLite and sockets in Termux-owned
  storage. `/sdcard` and Android shared storage are not working filesystems.
- OpenCode provider authentication and a model listed by `opencode models`.
- The existing AO iPhone app and Tailscale iPhone app on the same private tailnet.
- MagicDNS/HTTPS certificates enabled for the tailnet; access policy permits the
  iPhone to reach the dedicated `thor-ao` node on TCP 443.

## Bootstrap and local validation

```bash
pkg install -y git
mkdir -p "$HOME/src"
cd "$HOME/src"
git clone --branch pocket-main https://github.com/ImJJRay/agent-orchestrator.git
cd agent-orchestrator
bash scripts/pocket-agent/bootstrap-termux.sh --tailscale
export PATH="$HOME/.ao/bin:$PATH"
python scripts/pocket-agent/validate-thor.py
```

Bootstrap source-builds the CLI/daemon and official Tailscale `v1.102.5` into
`~/.ao/bin`. Tailscale uses the Android target with
`ts_omit_systray,ts_omit_ssh`; both binaries were cross-built successfully and their
ELF interpreter was verified as `/system/bin/linker64`. Omitting SSH and desktop
systray avoids two unsupported desktop imports; Serve and userspace networking
remain in the build. The native bootstrap enables CGO with Termux clang so Go
uses Android/bionic DNS resolution rather than assuming `/etc/resolv.conf`.
Cross-build checks used CGO disabled and therefore prove source/link-target
compatibility, not the native DNS/runtime behavior. Do not substitute desktop Linux releases: even a pure-Go
Linux PIE build may name `/lib/ld-linux-aarch64.so.1`, which Android does not have.

The validator creates unique state under `~/.ao/thor-validation`, starts its own
headless daemon, and stops only that daemon. It retains `report.json`, `daemon.log`,
SQLite and throwaway worktrees for diagnosis. It never touches the production
project configuration or reports pairing credentials. It tests native Git
worktree creation by default; AO-owned worker worktree creation is tested with
`--chat` below. A default pass with Chat/TUI/Tailscale marked SKIP does not prove
the complete milestone.

For a custom `AO_DATA_DIR`, export it before bootstrap and validation and put its
`bin` directory on PATH. Keep it below the Termux home directory. Do not carry a
foreign `AO_RUN_FILE` or `AO_PORT` into a production launch.

## Native OpenCode installation

AO expects an executable called `opencode` with the `acp` subcommand. The official
ACP interface is documented at https://opencode.ai/docs/acp/.

The investigated native bionic distribution is the community-maintained
[Hope2333/opencode-termux](https://github.com/Hope2333/opencode-termux), not a new
AO adapter. Its current mainline package is OpenCode v2. A pinned native ARM64
package is available in the [Push260922 release](https://github.com/Hope2333/opencode-termux/releases/tag/Push260922).
The release is marked prerelease; native execution and ACP compatibility must be
validated on Thor before treating this as a supported production installation.

```bash
bash scripts/pocket-agent/install-opencode-termux.sh
opencode auth login
opencode models
python scripts/pocket-agent/validate-thor.py --chat 'provider/model'
# Optional secondary path:
python scripts/pocket-agent/validate-thor.py --chat 'provider/model' --tui
```

The installer uses `opencode_2.0.12_aarch64.deb` and pins SHA256
`7a11f1b6891e0fd3047101aaaa4a1ac9ec289af707884c6fdc1964fdc1a50060`.
The downloaded package's digest and control metadata were independently checked.
It is the native bionic family, not the glibc wrapper family. A rolling release
asset that changes causes installation to fail until the pin is deliberately
reviewed. The installer checks `opencode --version` and `opencode acp --help`;
those probes alone do not prove a provider turn.

The Chat test creates an AO-managed isolated worktree and asks OpenCode to write
one token file. It verifies the exact persisted result and file contents, changes
the test project's default model to an invalid sentinel, kills the session,
restarts AO, restores the worker, checks its original persisted model, and sends
another real turn. It uses accept-edits permissions only in the throwaway test
project. A pending approval or provider failure is a failed or timed-out
validation, not a pass. Provider calls consume the selected account's quota.

The optional TUI test checks the native tmux launch, real file edit, isolation,
and model-preserving restore. It does not claim a semantic TUI result:
`ao session result` remains a durable **Chat** result interface. Screen scraping
has not been added as a substitute. Neither TUI nor real OpenCode execution has
been device-validated in this implementation environment.

If the native OpenCode binary or ACP command fails, return the corresponding
OPENCODE/OPENCODE_CHAT diagnostic and native executable version. Do not install
proot merely to hide an uninvestigated failure. An alternate native package can
be used if it provides the same `opencode`/ACP interface; the validator is the
acceptance contract.

## Private Tailscale HTTPS

The Android Tailscale VPN application does not expose the desktop CLI/local API
that AO's `tailscale serve` integration requires. Its VPN address is therefore
not a sufficient secure-pairing deployment. See
https://tailscale.com/docs/reference/tailscale-cli.

The supplied path runs official `tailscaled` in **userspace networking mode**,
as a separate authenticated tailnet node inside Termux. It needs no TUN device,
root, Shizuku, proot, or Android VPN service. This follows Tailscale's documented
userspace mode: https://tailscale.com/docs/concepts/userspace-networking.
The iPhone connects to this userspace node's hostname, not the Android VPN app's
hostname. The Android VPN app can remain available for other uses; AO does not
control it or assume it is running.

```bash
bash scripts/pocket-agent/thor.sh tailscale-start
bash scripts/pocket-agent/thor.sh tailscale-login
# Follow the printed Tailscale account login URL, then enable HTTPS in the tailnet.
bash scripts/pocket-agent/thor.sh tailscale-status
bash scripts/pocket-agent/thor.sh start
bash scripts/pocket-agent/thor.sh mobile-enable
bash scripts/pocket-agent/thor.sh pair
```

`mobile-enable` uses the upstream Serve proxy on HTTPS 443, forwarding only to
the authenticated Connect Mobile listener. The primary API remains loopback-only.
No Funnel is configured. `AO_MOBILE_TAILNET_ONLY=1` prevents automatic Cloudflare
connector discovery, including an explicit Cloudflare binary override.

The pair command prints `aomobile://pair#<base64url-v2-offer>` with stable host
identity, name, platform, one verified private HTTPS endpoint, and the bearer
password. Use the existing mobile app's pairing-code entry, or transfer the link
privately and open it on iPhone. The link is a credential; do not include it in
bug reports. Optional QR rendering can encode that exact link without changing
the mobile client. `ao mobile pair --lan` is an explicit trusted-LAN alternative.
The default never substitutes plaintext tailnet pairing when HTTPS is unavailable.

The userspace node has no Tailscale network interface. AO now advertises its
verified secure endpoint in both status and mobile endpoint refresh, so a phone
can retain the HTTPS route even without a detectable 100.x NIC.

AO reads these optional environment settings at daemon execution time:

| Setting | Purpose |
| --- | --- |
| `AO_TAILSCALE_BINARY` | Explicit CLI executable; default remains `tailscale` |
| `AO_TAILSCALE_SOCKET` | Prepends `--socket=<path>` to discovery, Serve apply/query/clear |
| `AO_MOBILE_TAILNET_ONLY=1` | Disables public Cloudflare connector discovery |

`thor.sh` supplies these values consistently for production starts. New CLI
commands are thin wrappers over existing loopback control routes:
`mobile status`, `enable`, `disable`, `regenerate`, `secure-pairing on|off`, `pair`.
JSON status includes credentials. CLI `enable` preserves an already-enabled
password; `regenerate` deliberately invalidates existing pairing. The upstream
API's existing enable/rotation semantics are unchanged.

Serve configuration, certificates, Android socket/process execution, relay/direct
reachability and login remain physical-device acceptance items. Compilation is
not evidence of network reachability. Userspace mode also does not install OS
routes or MagicDNS resolution inside Termux, so an ordinary local HTTPS request
to its own tailnet IP is not a valid reachability test. Test TLS from a **different
tailnet peer** (especially iPhone).

## Final device acceptance

1. Run the local validator with `--chat 'provider/model'` and optionally `--tui`.
2. Start the production node and Connect Mobile using the sequence above.
3. Run the production configuration check without modifying it:
   `python scripts/pocket-agent/validate-thor.py --secure`.
   This checks login, HTTPS capability, exact Serve target and absence of Funnel;
   it does not label peer TLS or iPhone reachability as tested.
4. On iPhone, enable Tailscale, turn Wi-Fi off, then pair in the existing AO app.
   Confirm the host identity/name and select a disposable registered Git project.
5. Create an OpenCode **Chat** worker using an available authenticated model.
   Submit a bounded file change, inspect status, changed files and final result.
   Confirm the original Git checkout was not edited.
6. Stop AO using `thor.sh stop`, restart it using `thor.sh start`, and reconnect
   without repairing. Confirm the password and configured worker model survive;
   restore the worker and send a follow-up.

From another tailnet peer, a TLS-validating request to
`https://<thor-ao-magicdns-name>/api/v1/identity` should return the host identity.
An unauthenticated request to `/api/v1/projects` must return 401; authenticated
requests must succeed; `/api/v1/mobile/status` and `/shutdown` must return 404 on
the mobile listener. Do not use `curl -k` to convert TLS failure into acceptance.

Return `report.json` if a device check fails, plus the named layer and the relevant
redacted log excerpt. Pairing links, mobile passwords, provider tokens and
Tailscale login URLs are not diagnostic output to share.

## Android lifecycle and limitations

- Termux/Android can kill AO, OpenCode, tmux or tailscaled. SQLite persists AO
  facts, but this is not an Android background-service guarantee.
- Disable Android battery optimization for Termux as appropriate. A Termux wake
  lock can keep the CPU awake but does not defeat process killing or network loss.
  AO's macOS-only keep-awake setting has not been repurposed for Android.
- After a process kill/reboot, run `tailscale-start`, then `start`; login normally
  persists in the dedicated node's state. AO restores enabled mobile state and
  reapplies Serve to the actual listener port. `thor.sh stop` cleans AO's Serve
  route while leaving node authentication available for the next start.
- Automatic boot/service supervision is not claimed. `nohup` provides terminal
  detachment only. No indiscriminate process killing is used.
- Android permissions on `/proc`, PTYs and interface enumeration can differ from
  desktop Linux. TUI uses existing tmux and is a secondary acceptance path;
  inability to enumerate a Tailscale NIC does not block userspace HTTPS.
- The built-in Android VPN app alone still cannot satisfy AO's CLI-based secure
  pairing. If a separately built Android tailscaled cannot execute/serve on this
  device, report the TAILSCALE layer; no public tunnel or insecure fallback is
  automatically enabled.

After physical acceptance, the next batch can address deterministic routing and
telemetry before JEV/harness evaluation. Device/runtime failures take precedence
until the control plane is proven.
