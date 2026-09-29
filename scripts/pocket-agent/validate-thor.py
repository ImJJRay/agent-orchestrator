#!/usr/bin/env python3
"""Isolated, bounded native runtime validation. Never exports pairing secrets.

Default: local SQLite/Git/worktree/daemon/API/mobile restore checks.
--chat provider/model: real bounded OpenCode task + model/daemon/session restore.
--tui: also exercise native tmux OpenCode; requires --chat.
--secure: verify the production daemon's private Tailscale Serve configuration.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--ao', default=shutil.which('ao') or str(Path.home()/'.ao/bin/ao'))
parser.add_argument('--chat', metavar='PROVIDER/MODEL')
parser.add_argument('--tui', action='store_true')
parser.add_argument('--secure', action='store_true')
parser.add_argument('--timeout', type=int, default=240)
args = parser.parse_args()
if args.tui and not args.chat:
    parser.error('--tui requires --chat provider/model')
if not 10 <= args.timeout <= 1800:
    parser.error('--timeout must be 10..1800 seconds')
checks = []
base_env = os.environ.copy()
base_data = Path(base_env.get('AO_DATA_DIR', Path.home()/'.ao')).resolve()
data = base_data/'thor-validation'/uuid.uuid4().hex[:12]
data.mkdir(parents=True, mode=0o700)
env = base_env.copy()
env.update(AO_DATA_DIR=str(data), AO_RUN_FILE=str(data/'running.json'),
           AO_MOBILE_TAILNET_ONLY='1', AO_TELEMETRY_REMOTE='off', AO_TELEMETRY_EVENTS='off')
# Reserve an available loopback port; the daemon reports its actual port.
with socket.socket() as s:
    s.bind(('127.0.0.1', 0))
    env['AO_PORT'] = str(s.getsockname()[1])
repo = data/'repo'
log = open(data/'daemon.log', 'a')
process = None
sessions = []


def record(layer, status, detail):
    checks.append(dict(layer=layer, status=status, detail=detail))
    print(f'{layer} {status}: {detail}', flush=True)


def run(argv, *, check=True, cwd=None, current_env=None, timeout=60):
    cp = subprocess.run(argv, cwd=cwd, env=current_env or env, capture_output=True,
                        text=True, timeout=timeout)
    if check and cp.returncode:
        raise RuntimeError(f'{argv[0]} {argv[1] if len(argv)>1 else ""} exited {cp.returncode}: {cp.stderr.strip()[:1500]}')
    return cp


def cli(*items, **kwargs):
    return run([args.ao, *items], **kwargs)


def api(path, body=None, *, port=None, headers=None, secure_host=None):
    if port is None:
        port = json.loads((data/'running.json').read_text())['port']
    base = f'https://{secure_host}:{port}' if secure_host else f'http://127.0.0.1:{port}'
    payload = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(base+path, payload, headers or {})
    if payload is not None:
        request.add_header('Content-Type', 'application/json')
    # Never proxy loopback requests or credentials through an ambient proxy.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request, timeout=15) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        return e.code, None


def must_api(path, body=None):
    code, value = api(path, body)
    if not 200 <= code < 300:
        raise RuntimeError(f'{path}: HTTP {code}; inspect daemon.log')
    return value


def start():
    global process
    process = subprocess.Popen([args.ao, 'daemon'], env=env, stdout=log, stderr=log,
                               stdin=subprocess.DEVNULL, start_new_session=True)
    deadline = time.monotonic()+30
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f'daemon exited {process.returncode}; inspect {data}/daemon.log')
        cp = cli('status', '--json', check=False, timeout=5)
        try:
            if json.loads(cp.stdout).get('state') == 'ready':
                return
        except (ValueError, KeyError):
            pass
        time.sleep(0.5)
    raise RuntimeError('daemon readiness timeout')


def stop():
    cli('stop', timeout=30)
    if process:
        process.wait(timeout=15)


def result(sid, previous_turn=None):
    deadline = time.monotonic()+args.timeout
    while time.monotonic() < deadline:
        cp = cli('session', 'result', sid, '--json', check=False)
        try:
            value = json.loads(cp.stdout)
        except ValueError:
            raise RuntimeError('result CLI did not produce JSON: '+cp.stderr[:800])
        if value.get('turnId') != previous_turn:
            if value.get('status') == 'completed' and cp.returncode == 0:
                return value
            if value.get('status') in ('failed', 'malformed'):
                raise RuntimeError('Chat result: '+value['status']+'; '+value.get('errorMessage', '')[:800])
        time.sleep(1)
    raise RuntimeError('Chat timeout; inspect approvals/conversation on the isolated validation daemon')


def worktree(branch):
    raw = run(['git', 'worktree', 'list', '--porcelain'], cwd=repo).stdout
    for block in raw.strip().split('\n\n'):
        lines = block.splitlines()
        if f'branch refs/heads/{branch}' in lines:
            return Path(next(x.removeprefix('worktree ') for x in lines if x.startswith('worktree ')))
    raise RuntimeError('AO did not create the requested isolated worktree')


def worker(mode):
    branch = f'validation/{mode}'
    filename = f'POCKET_{mode.upper()}_OK.txt'
    token = f'POCKET_{mode.upper()}_OK'
    prompt = f'In this worktree only, create {filename} containing exactly {token} followed by a newline. Do not commit, push, or touch any other files. Reply with exactly {token}.'
    response = must_api('/api/v1/sessions', dict(projectId='thor-validation', harness='opencode',
                       mode=mode, kind='worker', model=args.chat, branch=branch,
                       displayName=f'Thor {mode} validation', prompt=prompt))
    sid = response['session']['id']
    sessions.append(sid)
    path = worktree(branch)
    if path == repo or not path.is_relative_to(data):
        raise RuntimeError('worktree escaped isolated validation state')
    if mode == 'chat':
        completed = result(sid)
        if completed['result'].strip() != token:
            raise RuntimeError('exact durable result did not match requested token')
    else:
        deadline = time.monotonic()+args.timeout
        while not (path/filename).exists() and time.monotonic() < deadline:
            time.sleep(1)
    if (path/filename).read_text() != token+'\n' or (repo/filename).exists():
        raise RuntimeError('worker file content/isolation failed')
    cli('session', 'kill', sid)
    # Change the project default: restore must retain the worker's model.
    cli('project', 'set-config', 'thor-validation', '--model', 'invalid/restore-must-not-select-this')
    stop()
    start()
    cli('session', 'restore', sid, timeout=120)
    detail = json.loads(cli('session', 'get', sid, '--json').stdout)['session']
    if detail.get('model') != args.chat or detail.get('mode') != mode:
        raise RuntimeError('session model/mode was not preserved across restore')
    if mode == 'chat':
        cli('send', '--session', sid, '--message', f'Read {filename}. Reply with exactly POCKET_RESTORE_OK. Do not edit anything.')
        resumed = result(sid, completed['turnId'])
        if resumed['result'].strip() != 'POCKET_RESTORE_OK':
            raise RuntimeError('restored OpenCode Chat did not execute correctly')
    cli('session', 'kill', sid)
    return sid


layer = 'GO_BUILD'
try:
    cli('version')
    record(layer, 'PASS', 'built AO executable runs (bootstrap owns compilation)')
    layer = 'DAEMON'
    start()
    record(layer, 'PASS', 'isolated headless daemon ready')
    layer = 'SQLITE'
    if not (data/'ao.db').is_file() or (data/'ao.db').stat().st_size == 0:
        raise RuntimeError('SQLite database missing')
    record(layer, 'PASS', 'daemon migrated durable SQLite')
    layer = 'API'
    for route in ('/healthz', '/readyz', '/api/v1/identity'):
        must_api(route)
    record(layer, 'PASS', 'loopback health/readiness/identity')
    layer = 'GIT'
    repo.mkdir()
    run(['git', 'init', '-b', 'main'], cwd=repo)
    (repo/'README.md').write_text('Thor validation fixture\n')
    run(['git', 'add', 'README.md'], cwd=repo)
    run(['git', '-c', 'user.name=Thor Validation', '-c', 'user.email=thor@example.invalid', 'commit', '-m', 'fixture'], cwd=repo)
    run(['git', 'remote', 'add', 'origin', 'https://github.com/ImJJRay/thor-validation-fixture.git'], cwd=repo)
    cli('project', 'add', '--path', str(repo), '--id', 'thor-validation', '--worker-agent', 'opencode')
    record(layer, 'PASS', 'private Git repository registered through AO API')
    layer = 'WORKTREE'
    run(['git', 'worktree', 'add', '-b', 'validation/git', str(data/'git-worktree')], cwd=repo)
    (data/'git-worktree'/'probe.txt').write_text('isolated\n')
    if (repo/'probe.txt').exists():
        raise RuntimeError('worktree did not isolate changes')
    record(layer, 'PASS', 'native Git worktree add/write; AO-owned creation also checked when --chat runs')
    layer = 'CONNECT_MOBILE'
    mobile = json.loads(cli('mobile', 'enable').stdout)
    password = mobile['password']
    port = mobile['port']
    identity = mobile['hostId']
    if not password or not identity or mobile.get('tunnel', {}).get('supported'):
        raise RuntimeError('missing mobile identity/password or public tunnel unexpectedly enabled')
    auth = {'Authorization': 'Bearer '+password}
    if api('/api/v1/projects', port=port)[0] != 401 or api('/api/v1/projects', port=port, headers=auth)[0] != 200:
        raise RuntimeError('mobile bearer authentication boundary failed')
    for route in ('/api/v1/mobile/status', '/shutdown'):
        if api(route, {} if route == '/shutdown' else None, port=port, headers=auth)[0] != 404:
            raise RuntimeError('loopback control route leaked on mobile listener')
    stop()
    start()
    restored = json.loads(cli('mobile', 'status').stdout)
    if not restored['enabled'] or restored['password'] != password or restored['hostId'] != identity:
        raise RuntimeError('mobile state/password/identity did not survive restart')
    cli('mobile', 'disable')
    record(layer, 'PASS', 'authenticated listener, control-route isolation, and credential-preserving restart')
    layer = 'OPENCODE'
    if shutil.which('opencode', path=env.get('PATH')):
        run(['opencode', '--version'])
        cp = run(['opencode', 'acp', '--help'])
        record(layer, 'PASS', 'native executable and ACP command respond')
    else:
        record(layer, 'FAIL' if args.chat else 'SKIP', 'opencode absent; install native build before --chat validation')
    if args.chat:
        cli('project', 'set-config', 'thor-validation', '--permission', 'accept-edits')
        layer = 'OPENCODE_CHAT'
        worker('chat')
        record(layer, 'PASS', 'AO-owned worktree edit, exact durable result, project-default change, daemon/session restore, follow-up turn')
    else:
        record('OPENCODE_CHAT', 'SKIP', 'rerun with --chat provider/model; consumes configured provider quota')
    if args.tui:
        layer = 'OPENCODE_TUI'
        worker('tui')
        record(layer, 'PASS', 'tmux worker edited isolated file and restored with the persisted model; final semantic TUI result is not available')
    else:
        record('OPENCODE_TUI', 'SKIP', 'rerun with --chat provider/model --tui')
    layer = 'SECURE_PAIRING'
    if args.secure:
        # Read production state through its local API. Never change its settings.
        live = json.loads(cli('mobile', 'status', current_env=base_env).stdout)
        sp = live['securePairing']
        if not live['enabled'] or not sp['active']:
            raise RuntimeError('production secure pairing is not active; run thor.sh mobile-enable first')
        binary = base_env.get('AO_TAILSCALE_BINARY', str(base_data/'bin/tailscale'))
        ts_socket = base_env.get('AO_TAILSCALE_SOCKET', str(base_data/'mobile/tailscaled.sock'))
        ts = json.loads(run([binary, '--socket='+ts_socket, 'status', '--json'], current_env=base_env).stdout)
        if ts.get('BackendState') != 'Running' or not ts.get('CertDomains'):
            raise RuntimeError('userspace node is not logged in with HTTPS certificates enabled')
        serve = json.loads(run([binary, '--socket='+ts_socket, 'serve', 'status', '--json'], current_env=base_env).stdout)
        site = serve.get('Web', {}).get(sp['host']+':443', {})
        target = site.get('Handlers', {}).get('/', {}).get('Proxy')
        if target != f'http://127.0.0.1:{live["port"]}':
            raise RuntimeError('Serve proxy target does not match live authenticated mobile port')
        if any(serve.get('AllowFunnel', {}).values()):
            raise RuntimeError('Funnel is enabled on the dedicated userspace node')
        record('TAILSCALE', 'PASS', 'dedicated userspace node logged in; HTTPS certificate capability present')
        record(layer, 'PASS', 'private Serve target matches authenticated listener; no Funnel. Peer TLS and iPhone reachability still require the off-LAN client check')
    else:
        record('TAILSCALE', 'SKIP', 'production tailnet test requires --secure and authenticated userspace node')
        record(layer, 'SKIP', 'iPhone off-LAN acceptance remains a physical-client check')
except Exception as exc:
    record(layer, 'FAIL', str(exc))
finally:
    # Only stop the daemon this script launched. No pkill or stale-PID kill.
    if process and process.poll() is None:
        try:
            for sid in sessions:
                cli('session', 'kill', sid, check=False)
            stop()
        except Exception:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
    log.close()
    (data/'report.json').write_text(json.dumps(checks, indent=2)+'\n')
    print(f'REPORT: {data}/report.json (contains no pairing credential; daemon.log is local diagnostic data)')
sys.exit(1 if any(c['status'] == 'FAIL' for c in checks) else 0)
