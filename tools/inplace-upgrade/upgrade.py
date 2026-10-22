#!/usr/bin/env python3
"""Bounded, same-schema/runtime upgrade of a trusted committed installation."""

import argparse
import contextlib
import fcntl
import getpass
import hashlib
import http.cookies
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import sqlite3
import stat
import ssl
import subprocess
import tarfile
import tempfile
import time
import traceback
import urllib.request


class Refused(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise Refused(message)


def sha(path):
    with Path(path).open('rb') as stream:
        result = hashlib.sha256()
        for block in iter(lambda: stream.read(1 << 20), b''):
            result.update(block)
        return result.hexdigest()


def exists(path):
    return os.path.lexists(path)


def identity(path):
    info = Path(path).lstat()
    return [info.st_dev, info.st_ino]


def sync_dir(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def private_json(path, value):
    descriptor, temporary = tempfile.mkstemp(prefix='.ledger-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w') as stream:
            json.dump(value, stream, sort_keys=True, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        sync_dir(path.parent)
    finally:
        if exists(temporary):
            os.unlink(temporary)


def read_private(path):
    info = Path(path).lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid()
            and stat.S_IMODE(info.st_mode) == 0o600, 'Expected private owned input')
    return json.loads(Path(path).read_text())


def no_link_parents(path):
    path = Path(path)
    require(path.is_absolute(), 'Absolute path required')
    for parent in [path, *path.parents]:
        if exists(parent):
            require(not parent.is_symlink(), 'Symlinked path boundary refused')


def fingerprint(path):
    """Do not follow symlinks in copied/protected trees."""
    path = Path(path)
    if not exists(path):
        return None
    result = {}

    def visit(current, relative):
        info = current.lstat()
        data = {'mode': stat.S_IMODE(info.st_mode), 'uid': info.st_uid,
                'gid': info.st_gid}
        if stat.S_ISLNK(info.st_mode):
            data.update(kind='link', target=os.readlink(current))
        elif stat.S_ISDIR(info.st_mode):
            data['kind'] = 'dir'
        elif stat.S_ISREG(info.st_mode):
            data.update(kind='file', sha256=sha(current), size=info.st_size)
        else:
            raise Refused('Unsupported filesystem object in snapshot')
        result[relative] = data
        if data['kind'] == 'dir':
            for child in sorted(current.iterdir()):
                visit(child, str(Path(relative) / child.name))
    visit(path, '.')
    return result


def copy_object(source, destination):
    require(not exists(destination), 'Snapshot/prepared destination exists')
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if source.is_symlink():
        os.symlink(os.readlink(source), destination)
    elif source.is_dir():
        shutil.copytree(source, destination, symlinks=True)
    else:
        shutil.copy2(source, destination, follow_symlinks=False)
    require(fingerprint(source) == fingerprint(destination), 'Snapshot copy differs')


def durable_object(path):
    """Sync snapshot bytes and directories before publication or replacement."""
    if path.is_symlink():
        sync_dir(path.parent)
    elif path.is_dir():
        for child in path.iterdir():
            durable_object(child)
        sync_dir(path)
    else:
        with path.open('rb') as stream:
            os.fsync(stream.fileno())


def atomic_binary(source, target):
    descriptor, temporary = tempfile.mkstemp(prefix='.inplace-', dir=target.parent)
    os.close(descriptor)
    try:
        shutil.copy2(source, temporary)
        durable_object(Path(temporary))
        os.replace(temporary, target)
        sync_dir(target.parent)
    finally:
        if exists(temporary):
            os.unlink(temporary)


def db_state(path):
    connection = sqlite3.connect(Path(path).as_uri() + '?mode=ro', uri=True, timeout=5)
    try:
        require(connection.execute('PRAGMA integrity_check').fetchone() == ('ok',),
                'Database integrity failed')
        result = {'schema': connection.execute('PRAGMA user_version').fetchone()[0],
                  'tables': {}}
        for table in ('peers', 'rules', 'tunnels', 'warp_registrations', 'settings'):
            columns = [row[1] for row in connection.execute('PRAGMA table_info("' + table + '")')]
            key = {'settings': 'key', 'warp_registrations': 'tunnel_id'}.get(table, 'id')
            require(key in columns, 'Stable row identity is missing')
            rows = [dict(zip(columns, row)) for row in connection.execute('SELECT * FROM "' + table + '"')]
            require(len({row[key] for row in rows}) == len(rows), 'Duplicate row identity')
            result['tables'][table] = {'columns': sorted(columns), 'key': key,
                                       'rows': sorted(rows, key=lambda row: str(row[key]))}
        settings = result['tables']['settings']['rows']
        result['installed'] = next((row['value'] for row in settings
                                    if row['key'] == 'installed_version'), None)
        return result
    finally:
        connection.close()


def equal_policy(before, after):
    """All user fields are strict, including peer egress and tunnel source."""
    require(before['schema'] == after['schema'], 'Schema transition is unsupported')
    require(before['tables'].keys() == after['tables'].keys(), 'Policy table set changed')
    for table, left in before['tables'].items():
        right = after['tables'][table]
        require(left['columns'] == right['columns'] and left['key'] == right['key'],
                'Policy schema changed')
        key = left['key']
        old = {row[key]: row for row in left['rows']}
        new = {row[key]: row for row in right['rows']}
        if table == 'settings':
            old.pop('installed_version', None)
            new.pop('installed_version', None)
        require(old.keys() == new.keys(), 'Policy row set changed')
        for row_id in old:
            a, b = dict(old[row_id]), dict(new[row_id])
            if table == 'tunnels' and a.get('source') == b.get('source') == 'pool':
                for name in ('pool', 'pool_updated_at'):
                    a.pop(name, None)
                    b.pop(name, None)
            require(a == b, 'User policy changed')


def authority_generation(proof):
    raw = proof.get('generation_raw')
    require(isinstance(raw, str) and hashlib.sha256(raw.encode()).hexdigest()
            == proof.get('raw_sha256'), 'Missing or inconsistent private authority evidence')
    def unique_object(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, 'Duplicate authority JSON key')
            result[key] = value
        return result
    def invalid_constant(_value):
        raise Refused('Invalid authority JSON number')
    generation = json.loads(raw, object_pairs_hook=unique_object, parse_constant=invalid_constant)
    require(isinstance(generation, dict) and generation.get('kind') == 'committed'
            and type(generation.get('version')) is int and generation['version'] == 4,
            'Unsupported committed authority evidence')
    return generation


def equal_authority(before, after):
    """Only paired source-pool catalog/time fields may change during recovery."""
    left, right = authority_generation(before), authority_generation(after)
    evidence = {'sha256', 'raw_sha256', 'generation_raw', 'generated_config_sha256'}
    def strict_json(value):
        return json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False)
    require(strict_json({key: value for key, value in before.items() if key not in evidence})
            == strict_json({key: value for key, value in after.items() if key not in evidence}),
            'Committed authority proof contract changed')
    def tunnels(generation):
        policy = generation.get('policy')
        require(isinstance(policy, dict), 'Missing committed policy')
        rows = policy.get('tunnels')
        require(rows is None or isinstance(rows, list), 'Invalid committed tunnels')
        result = {}
        for row in rows or []:
            require(isinstance(row, dict) and isinstance(row.get('id'), str)
                    and row['id'] and row['id'] not in result,
                    'Missing or duplicate committed tunnel identity')
            result[row['id']] = row
        return result
    old, new = tunnels(left), tunnels(right)
    require(old.keys() == new.keys(), 'Committed tunnel identity set changed')
    for row_id, previous in old.items():
        current = new[row_id]
        require(previous.get('source') == current.get('source'), 'Committed tunnel source changed')
        if previous.get('source') == current.get('source') == 'pool':
            for field in ('pool', 'pool_updated_at'):
                previous.pop(field, None)
                current.pop(field, None)
    require(strict_json(left) == strict_json(right),
            'Committed authority changed outside paired pool catalog fields')


def generated_config_sha256(proof):
    value = proof.get('generated_config_sha256')
    require(isinstance(value, str) and re.fullmatch('[a-f0-9]{64}', value),
            'Missing exact generated runtime config proof')
    return value


def stopped_state_is_safe(properties, cgroup_pids, queued):
    return (properties.get('ActiveState') in ('inactive', 'failed')
            and properties.get('MainPID') == '0' and properties.get('ControlPID') == '0'
            and properties.get('Job') in ('', '0') and not cgroup_pids and not queued)


def setup_environment():
    # Whitelist instead of forwarding installer/public/Telegram/proxy overrides.
    return {'PATH': '/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
            'LANG': 'C', 'LC_ALL': 'C', 'HOME': '/root'}


class Layout:
    def __init__(self, root=Path('/')):
        self.root = Path(root)
        self.state = self.path('/var/lib/razdacha')
        self.database = self.state / 'state.db'
        self.runtime = self.path('/usr/local/bin/sing-box')
        self.binaries = [self.path('/usr/local/bin/razdacha'),
                         self.path('/usr/local/bin/razdachad')]
        self.trees = [self.state, self.path('/etc/sing-box'), self.path('/var/lib/sing-box')]
        self.protected = [self.path(name) for name in (
            '/etc/nginx', '/etc/razdacha', '/etc/systemd/system/razdachad.service',
            '/etc/systemd/system/sing-box.service', '/etc/sysctl.d/99-razdacha.conf')]
        self.backup_parent = self.path('/var/backups/razdacha')

    def path(self, absolute):
        return self.root / absolute.lstrip('/')


class Host:
    def __init__(self, layout, operation, helper, password):
        self.layout, self.operation, self.helper = layout, operation, helper
        self.password = password
        self.counter = 0
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def run(self, arguments, timeout=45, environment=None):
        self.counter += 1
        completed = subprocess.run([str(arg) for arg in arguments], capture_output=True,
                                   timeout=timeout, env=environment or setup_environment())
        # Commands can print keys or configuration; only private operator files receive them.
        for suffix, payload in (('stdout', completed.stdout), ('stderr', completed.stderr)):
            descriptor, _ = tempfile.mkstemp(
                prefix='command-%04d-%s-' % (self.counter, suffix), dir=self.operation)
            with os.fdopen(descriptor, 'wb') as stream:
                stream.write(payload)
        require(completed.returncode == 0, 'Bounded host command failed; inspect private output')
        return completed.stdout.decode().strip()

    def proof(self, command, *arguments):
        return json.loads(self.run([self.helper, command, *arguments]))

    def service(self, action, service):
        self.run(['systemctl', action, service], timeout=60)

    def properties(self, service):
        names = ['ActiveState', 'MainPID', 'ControlPID', 'ControlGroup', 'Job']
        body = self.run(['systemctl', 'show', service, *['--property=' + name for name in names]])
        result = dict(line.split('=', 1) for line in body.splitlines() if '=' in line)
        require(set(names) <= result.keys(), 'Incomplete service properties')
        return result

    def assert_stopped(self, service):
        properties = self.properties(service)
        pids = []
        cgroup = properties['ControlGroup']
        if cgroup:
            require(cgroup.startswith('/') and '..' not in Path(cgroup).parts,
                    'Unsafe service cgroup path')
            group = Path('/sys/fs/cgroup') / cgroup.lstrip('/')
            if group.exists():
                for path in group.rglob('cgroup.procs'):
                    pids.extend(path.read_text().split())
        unit = service + '.service'
        jobs = self.run(['systemctl', 'list-jobs', '--no-legend', '--no-pager'])
        queued = any(unit in row.split() for row in jobs.splitlines())
        require(stopped_state_is_safe(properties, pids, queued), 'Service still has writers/jobs')

    def stop(self, service):
        # A shutdown watchdog can leave ActiveState=failed after the process exits.
        try:
            self.service('stop', service)
        except Refused:
            pass
        self.assert_stopped(service)

    def guard(self, present):
        if present:
            self.proof('protect')
        guard = self.proof('inspect')
        require(guard['exact_shape'] and guard['present'] == present,
                'Ingress guard state or shape differs')

    def api_version(self):
        token = None
        try:
            body = json.dumps({'password': self.password}).encode()
            request = urllib.request.Request('http://127.0.0.1:8080/api/login', body,
                                             {'Content-Type': 'application/json'})
            with self.http.open(request, timeout=5) as response:
                cookies = http.cookies.SimpleCookie()
                for item in response.headers.get_all('Set-Cookie', []):
                    cookies.load(item)
            require(len(cookies) == 1, 'Unexpected session cookie contract')
            token = '; '.join(name + '=' + item.value for name, item in cookies.items())
            request = urllib.request.Request('http://127.0.0.1:8080/api/version',
                                             headers={'Cookie': token})
            with self.http.open(request, timeout=5) as response:
                version = json.load(response)
            pending = urllib.request.Request('http://127.0.0.1:8080/api/apply/status',
                                             headers={'Cookie': token})
            with self.http.open(pending, timeout=5) as response:
                require(json.load(response) == {'dirty': False}, 'Pending policy must be explicitly applied before upgrade')
            return version
        finally:
            if token:
                request = urllib.request.Request('http://127.0.0.1:8080/api/logout', b'',
                                                 {'Cookie': token})
                with contextlib.suppress(Exception):
                    self.http.open(request, timeout=5).close()

    def process(self, service, expected):
        properties = self.properties(service)
        require(properties['ActiveState'] == 'active', 'Service is not active')
        pid = properties['MainPID']
        require(pid.isdecimal() and int(pid) > 0, 'Service lacks a live main process')
        require(sha(Path('/proc') / pid / 'exe') == expected, 'Running executable differs')
        return int(pid)

    def readiness(self, version, commit, runtime, binary_hashes, schema):
        deadline = time.monotonic() + 30
        while True:
            try:
                result = self.api_version()
                require(result['version'] == version and result['installed_version'] == version
                        and not result['version_mismatch'] and result['schema_version'] == schema,
                        'Daemon version/schema mismatch')
                require(result['commit'] == commit, 'Daemon commit mismatch')
                require(result['singbox']['library'] == runtime
                        and result['singbox']['runtime'] == runtime, 'Runtime/API mismatch')
                with self.http.open('http://127.0.0.1:9090/version', timeout=2) as response:
                    clash = json.load(response)
                require(re.search(r'(?<![0-9.])' + re.escape(runtime) + r'(?![0-9.])',
                                  clash.get('version', '')), 'Actual Clash version mismatch')
                break
            except Exception:
                if time.monotonic() >= deadline:
                    raise Refused('Real daemon/Clash readiness timed out') from None
                time.sleep(0.5)
        self.process('razdachad', binary_hashes['razdachad'])
        pid = self.process('sing-box', binary_hashes['sing-box'])
        arguments = (Path('/proc') / str(pid) / 'cmdline').read_bytes().split(b'\0')
        require(b'-c' in arguments and arguments[arguments.index(b'-c') + 1]
                == b'/etc/sing-box/config.json', 'Runtime uses a different configuration')
        self.run([self.layout.runtime, 'check', '-c', self.layout.path('/etc/sing-box/config.json')])
        self.dns()
        self.guard(False)
        self.process('nginx', binary_hashes['nginx'])
        self.run(['nginx', '-t'])
        tls = ssl.create_default_context(cafile=str(self.layout.path('/etc/razdacha/tls/cert.pem')))
        https = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                            urllib.request.HTTPSHandler(context=tls))
        with https.open('https://10.8.0.1/', timeout=5) as response:
            require(response.status == 200, 'TLS panel readiness failed')
        return result

    @staticmethod
    def dns():
        identifier = os.urandom(2)
        question = b'\x07example\x03com\x00\x00\x01\x00\x01'
        packet = identifier + b'\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00' + question
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as stream:
            stream.settimeout(5)
            stream.connect(('10.8.0.1', 53))
            stream.send(packet)
            response = stream.recv(4096)
        require(len(response) >= 12 and response[:2] == identifier
                and int.from_bytes(response[2:4], 'big') & 0x8000
                and int.from_bytes(response[2:4], 'big') & 15 == 0
                and int.from_bytes(response[6:8], 'big') > 0, 'Real DNS probe failed')


def validate_manifest(manifest):
    required = {'archive', 'archive_sha256', 'helper', 'helper_sha256', 'version',
                'commit', 'schema', 'runtime', 'razdacha_sha256', 'razdachad_sha256'}
    require(set(manifest) == required, 'Expected-input manifest fields differ')
    for key in required:
        if key.endswith('sha256'):
            require(isinstance(manifest[key], str) and re.fullmatch('[a-f0-9]{64}', manifest[key]),
                    'Expected SHA256 is invalid')
    require(re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?', manifest['version']),
            'Expected release version is invalid')
    require(re.fullmatch('[a-f0-9]{12,40}', manifest['commit']), 'Expected commit is invalid')
    require(isinstance(manifest['schema'], int) and manifest['schema'] > 0,
            'Expected schema is invalid')
    require(re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', manifest['runtime']), 'Pinned runtime is invalid')


def stage(manifest, operation):
    validate_manifest(manifest)
    for key in ('archive', 'helper'):
        path = Path(manifest[key])
        no_link_parents(path)
        require(path.is_file() and path.stat().st_uid == os.geteuid()
                and not path.stat().st_mode & 0o022, 'Untrusted release/helper input')
        require(sha(path) == manifest[key + '_sha256'], 'Immutable input checksum mismatch')
    staged = operation / 'candidate'
    staged.mkdir(mode=0o700)
    shutil.copy2(manifest['helper'], staged / 'proof')
    os.chmod(staged / 'proof', 0o700)
    require(sha(staged / 'proof') == manifest['helper_sha256'], 'Staged helper checksum mismatch')
    with tarfile.open(manifest['archive'], 'r:gz') as archive:
        members = {}
        for member in archive.getmembers():
            name = member.name
            while name.startswith('./'):
                name = name[2:]
            if name in ('', '.') and member.isdir():
                continue
            require(name in ('razdacha', 'razdachad', 'LICENSE', 'README.md')
                    and member.isfile() and name not in members and member.size <= 256 << 20,
                    'Unsafe or unexpected archive member')
            members[name] = member
        require(set(members) == {'razdacha', 'razdachad', 'LICENSE', 'README.md'},
                'Release archive membership differs')
        for name in ('razdacha', 'razdachad'):
            target = staged / name
            with archive.extractfile(members[name]) as source, target.open('xb') as output:
                shutil.copyfileobj(source, output)
            os.chmod(target, 0o700)
            require(sha(target) == manifest[name + '_sha256'], 'Candidate executable checksum mismatch')
    return staged


def restore_plan_entry(entry, rename=os.rename):
    """Recover both halves of an atomic swap using persisted inode identities."""
    live, prepared, quarantine = map(Path, (entry['live'], entry['prepared'], entry['quarantine']))
    old, replacement = entry['old'], entry['replacement']
    if exists(live):
        current = identity(live)
        if current == replacement:
            require(not exists(prepared), 'Duplicate restored identity')
        else:
            require(current == old and not exists(quarantine), 'Unknown live/quarantine identity')
            rename(live, quarantine)
            sync_dir(live.parent)
    if exists(prepared):
        require(identity(prepared) == replacement and not exists(live), 'Unknown prepared identity')
        rename(prepared, live)
        sync_dir(live.parent)
    require(exists(live) and identity(live) == replacement, 'Restored identity not established')
    if old is not None:
        require(exists(quarantine) and identity(quarantine) == old, 'Original live object was lost')
    else:
        require(not exists(quarantine), 'Unexpected quarantine for absent live object')


class Transaction:
    def __init__(self, layout, operation, host, ledger=None):
        self.layout, self.operation, self.host = layout, Path(operation), host
        self.ledger = ledger or {'phase': 'created', 'mutated': False}

    def phase(self, name):
        self.ledger['phase'] = name
        private_json(self.operation / 'status.json', self.ledger)
        print(name, flush=True)

    def protected(self):
        return {str(path): fingerprint(path) for path in self.layout.protected}

    def preflight(self, manifest):
        staged = stage(manifest, self.operation)
        self.host.helper = staged / 'proof'
        capabilities = self.host.proof('capabilities')
        require(capabilities == {'schema': manifest['schema'], 'runtime': manifest['runtime']},
                'Candidate helper schema/runtime differs')
        for name, arguments in (('razdacha', ['version']), ('razdachad', ['-version'])):
            require(self.host.run([staged / name, *arguments]) == manifest['version'],
                    'Candidate executable version differs')
            require(self.host.proof('candidate', staged / name) == {'runtime': manifest['runtime']},
                    'Candidate linked runtime differs')
        paths = self.layout.trees + self.layout.protected + self.layout.binaries + [self.layout.runtime]
        for path in paths:
            no_link_parents(path)
            require(exists(path), 'Required installation path is absent')
        require(all(path.is_dir() for path in self.layout.trees), 'Expected state trees')
        for setting in ('ip_forward', 'ip_nonlocal_bind'):
            require(self.layout.path('/proc/sys/net/ipv4/' + setting).read_text().strip() == '1',
                    'Setup-managed kernel setting is not healthy')
        baseline = db_state(self.layout.database)
        require(baseline['schema'] == manifest['schema'], 'Schema transition is unsupported')
        binary_hashes = {path.name: sha(path) for path in self.layout.binaries + [self.layout.runtime, self.layout.path('/usr/sbin/nginx')]}
        api = self.host.api_version()
        require(api['version'] == baseline['installed'], 'Installed baseline differs')
        self.host.readiness(api['version'], api['commit'], manifest['runtime'], binary_hashes,
                            manifest['schema'])
        authority = self.host.proof('authority')
        equal_authority(authority, authority)
        require(sha(self.layout.path('/etc/sing-box/config.json')) == generated_config_sha256(authority),
                'Helper generator does not reproduce baseline runtime config; transition unsupported')
        wireguard = self.host.proof('wireguard')
        size = sum(sum(item.get('size', 0) for item in (fingerprint(path) or {}).values())
                   for path in paths)
        required = size * 4 + Path(manifest['archive']).stat().st_size * 3 + (64 << 20)
        for parent in {self.operation.parent, *[path.parent for path in paths]}:
            require(shutil.disk_usage(parent).free >= required, 'Insufficient snapshot/recovery space')
        self.ledger.update(manifest=manifest, baseline=baseline, binary_hashes=binary_hashes,
                           baseline_api=api, authority=authority, wireguard=wireguard,
                           protected=self.protected(),
                           root_ids={str(path): identity(path) for path in self.layout.trees},
                           helper_sha256=manifest['helper_sha256'],
                           config_sha256=sha(self.layout.path('/etc/sing-box/config.json')))
        self.phase('preflight-verified')

    def snapshot(self):
        self.phase('stopping-daemon')
        # The ledger is durable before stop: interruption cannot be mistaken for a
        # pre-mutation failure. It never authorizes a rollback without a snapshot.
        self.ledger['mutated'] = True
        self.phase('stop-requested')
        self.host.stop('razdachad')
        self.host.guard(True)
        self.phase('ingress-protected')
        self.host.stop('sing-box')
        self.host.assert_stopped('razdachad')
        self.host.assert_stopped('sing-box')
        self.host.guard(True)
        equal_policy(self.ledger['baseline'], db_state(self.layout.database))
        require(self.protected() == self.ledger['protected'], 'Protected files changed before snapshot')
        equal_authority(self.ledger['authority'], self.host.proof('authority'))
        snapshot = self.operation / 'snapshot'
        snapshot.mkdir(mode=0o700)
        roots = self.layout.trees + self.layout.protected + self.layout.binaries + [self.layout.runtime]
        for index, source in enumerate(roots):
            copy_object(source, snapshot / str(index))
        # Replace only the stopped COPY's database with a verified standalone WAL
        # snapshot. The source is never checkpointed or switched out of WAL.
        destination = snapshot / 'standalone.db'
        self.host.proof('backup', destination)
        state_copy = snapshot / '0'
        for suffix in ('', '-wal', '-shm'):
            with contextlib.suppress(FileNotFoundError):
                (state_copy / ('state.db' + suffix)).unlink()
        os.rename(destination, state_copy / 'state.db')
        equal_policy(db_state(state_copy / 'state.db'), db_state(self.layout.database))
        self.ledger['baseline'] = db_state(state_copy / 'state.db')
        self.ledger['snapshot'] = {str(path): str(snapshot / str(index)) for index, path in enumerate(roots)}
        self.ledger['snapshot_fingerprints'] = {str(path): fingerprint(snapshot / str(index))
                                              for index, path in enumerate(roots)}
        durable_object(snapshot)
        self.phase('snapshot-verified')

    def verify_preservation(self, after_start=False):
        equal_policy(self.ledger['baseline'], db_state(self.layout.database))
        current_authority = self.host.proof('authority')
        equal_authority(self.ledger['authority'], current_authority)
        require(self.host.proof('wireguard') == self.ledger['wireguard'], 'WireGuard identity changed')
        require(self.protected() == self.ledger['protected'], 'Protected TLS/nginx/unit files changed')
        require(sha(self.layout.runtime) == self.ledger['binary_hashes']['sing-box'], 'Pinned runtime changed')
        expected_config = (generated_config_sha256(current_authority) if after_start
                           else self.ledger['config_sha256'])
        require(sha(self.layout.path('/etc/sing-box/config.json')) == expected_config,
                'Runtime configuration differs from its exact accepted generation')
        require(sha(self.layout.path('/usr/sbin/nginx')) == self.ledger['binary_hashes']['nginx'],
                'Installed nginx executable changed')
        for setting in ('ip_forward', 'ip_nonlocal_bind'):
            require(self.layout.path('/proc/sys/net/ipv4/' + setting).read_text().strip() == '1',
                    'Setup-managed kernel setting changed')

    def forward(self):
        self.phase('replacing-binaries')
        for target in self.layout.binaries:
            atomic_binary(self.operation / 'candidate' / target.name, target)
        self.phase('running-setup')
        self.host.run([self.layout.binaries[0], 'setup', '--db', self.layout.database,
                       '--daemon', self.layout.binaries[1], '--start=false'], timeout=120,
                      environment=setup_environment())
        self.verify_preservation()
        for target in self.layout.binaries:
            require(sha(target) == self.ledger['manifest'][target.name + '_sha256'],
                    'Installed candidate executable changed before start')
        self.host.run(['systemctl', 'daemon-reload'])
        self.phase('starting-candidate')
        self.host.service('start', 'sing-box')
        self.host.service('start', 'razdachad')
        manifest = self.ledger['manifest']
        hashes = {name: manifest[name + '_sha256'] for name in ('razdacha', 'razdachad')}
        hashes['sing-box'] = self.ledger['binary_hashes']['sing-box']
        hashes['nginx'] = self.ledger['binary_hashes']['nginx']
        self.host.readiness(manifest['version'], manifest['commit'], manifest['runtime'],
                            hashes, manifest['schema'])
        self.verify_preservation(after_start=True)
        self.phase('accepted')

    def prepare_recovery(self):
        snapshot = self.ledger.get('snapshot')
        require(snapshot, 'No complete fresh snapshot; administrator recovery is required')
        for name, saved in snapshot.items():
            require(fingerprint(Path(saved)) == self.ledger['snapshot_fingerprints'][name],
                    'Rollback snapshot identity/content changed')
        suffix = self.operation.name
        plan = []
        for name, saved in snapshot.items():
            live = Path(name)
            if live in self.layout.trees:
                require(identity(live) == self.ledger['root_ids'][name], 'Unknown live tree identity')
            no_link_parents(live)
            prepared = live.with_name('.' + live.name + '.recovery-' + suffix)
            quarantine = live.with_name('.' + live.name + '.failed-' + suffix)
            require(not exists(prepared) and not exists(quarantine), 'Existing recovery sibling refused')
            copy_object(Path(saved), prepared)
            durable_object(prepared)
            plan.append({'live': str(live), 'prepared': str(prepared), 'quarantine': str(quarantine),
                         'old': identity(live) if exists(live) else None,
                         'replacement': identity(prepared)})
        self.ledger['recovery_plan'] = plan
        self.phase('rollback-prepared')

    def recover(self):
        self.phase('rollback-protecting')
        self.host.stop('razdachad')
        self.host.guard(True)
        self.host.stop('sing-box')
        self.host.assert_stopped('razdachad')
        self.host.assert_stopped('sing-box')
        self.host.guard(True)
        if not self.ledger.get('recovery_plan'):
            self.prepare_recovery()
        self.phase('rollback-restoring')
        for entry in self.ledger['recovery_plan']:
            restore_plan_entry(entry)
        # Recovery never starts a daemon over unverified restored state. This also
        # catches modified prepared content whose directory inode stayed stable.
        self.verify_preservation()
        require(db_state(self.layout.database)['installed'] == self.ledger['baseline']['installed'],
                'Rollback installed version differs')
        for target in self.layout.binaries + [self.layout.runtime]:
            require(sha(target) == self.ledger['binary_hashes'][target.name],
                    'Restored executable differs before start')
        self.host.run(['systemctl', 'daemon-reload'])
        self.phase('rollback-starting')
        self.host.service('start', 'sing-box')
        self.host.service('start', 'razdachad')
        baseline = self.ledger['baseline_api']
        self.host.readiness(baseline['version'], baseline['commit'], self.ledger['manifest']['runtime'],
                            self.ledger['binary_hashes'], self.ledger['baseline']['schema'])
        self.verify_preservation(after_start=True)
        self.phase('rolled-back')

    def execute(self, manifest):
        try:
            self.preflight(manifest)
            self.snapshot()
            self.forward()
        except BaseException:
            self.ledger['failure'] = traceback.format_exc()
            self.phase('failed')
            if self.ledger.get('mutated'):
                self.recover_safely()
            raise

    def recover_safely(self):
        try:
            self.recover()
        except BaseException:
            self.ledger['recovery_failure'] = traceback.format_exc()
            # Stop the daemon again before protecting; a failed readiness check
            # may occur after successful committed recovery removed the guard.
            try:
                self.host.stop('razdachad')
                self.host.guard(True)
            except BaseException:
                self.ledger['protection_failure'] = traceback.format_exc()
            self.phase('recovery-required')
            raise


def completed(ledger):
    return ledger.get('phase') in ('accepted', 'rolled-back', 'checked') or not ledger.get('mutated', False)


def require_current(parent, operation, ledger):
    marker = read_private(parent / 'inplace-current.json')
    require(marker == {'operation': str(operation), 'identity': identity(operation),
                       'token': ledger['token']}, 'Stale or non-current recovery refused')


def finish_current(parent, operation, ledger):
    require_current(parent, operation, ledger)
    if completed(ledger):
        (parent / 'inplace-current.json').unlink()
        sync_dir(parent)


def claim_operation(parent, operation):
    marker_path = parent / 'inplace-current.json'
    if exists(marker_path):
        marker = read_private(marker_path)
        previous = Path(marker['operation'])
        require(previous.parent == parent, 'Current operation points outside backup directory')
        no_link_parents(previous)
        old = read_private(previous / 'status.json')
        require_current(parent, previous, old)
        require(completed(old), 'Previous upgrade needs explicit recovery')
        finish_current(parent, previous, old)
    # Refuse orphan operations if a marker was lost/removed outside this tool.
    for previous in parent.glob('inplace-*'):
        if previous.name == 'inplace-current.json':
            continue
        require(previous.is_dir() and not previous.is_symlink(), 'Unknown prior operation object')
        old = read_private(previous / 'status.json')
        require(completed(old), 'Unfinished prior operation exists without current marker')
    operation.mkdir(mode=0o700)
    ledger = {'phase': 'created', 'mutated': False, 'token': os.urandom(16).hex()}
    private_json(operation / 'status.json', ledger)
    private_json(marker_path, {'operation': str(operation), 'identity': identity(operation),
                              'token': ledger['token']})
    return ledger


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('run', 'check', 'status', 'recover'))
    parser.add_argument('--operation', required=True, type=Path)
    parser.add_argument('--manifest', type=Path)
    parser.add_argument('--password-fd', type=int)
    args = parser.parse_args()
    require(os.geteuid() == 0, 'Root is required')
    os.umask(0o077)
    layout = Layout()
    operation = args.operation
    require(operation.parent == layout.backup_parent and re.fullmatch(r'inplace-[a-zA-Z0-9-]{8,64}', operation.name),
            'Operation must be a named direct child of the private backup directory')
    no_link_parents(layout.backup_parent)
    layout.backup_parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    require(layout.backup_parent.stat().st_uid == 0 and not layout.backup_parent.stat().st_mode & 0o077,
            'Private root-owned backup parent is required')
    lock_path = layout.path('/run/razdacha-inplace-upgrade.lock')
    no_link_parents(lock_path)
    descriptor = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.action in ('run', 'check'):
            require(args.manifest is not None, 'Run requires an expected-input manifest')
            ledger = claim_operation(layout.backup_parent, operation)
        else:
            no_link_parents(operation)
            require(operation.is_dir() and operation.stat().st_uid == 0
                    and stat.S_IMODE(operation.stat().st_mode) == 0o700, 'Invalid operation directory')
            ledger = read_private(operation / 'status.json')
            if args.action == 'status':
                print(json.dumps({'phase': ledger['phase'], 'recovery_required': ledger['phase'] == 'recovery-required'}))
                return
            require(not completed(ledger), 'Completed operations cannot be replayed')
            require_current(layout.backup_parent, operation, ledger)
        password = (os.fdopen(os.dup(args.password_fd)).read().rstrip('\r\n')
                    if args.password_fd is not None else getpass.getpass('Panel password: '))
        helper = operation / 'candidate' / 'proof'
        host = Host(layout, operation, helper, password)
        transaction = Transaction(layout, operation, host, ledger)
        def interrupted(_number, _frame):
            raise InterruptedError('Operator signal interrupted upgrade')
        for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(number, interrupted)
        try:
            if args.action == 'run':
                transaction.execute(read_private(args.manifest))
            elif args.action == 'check':
                transaction.preflight(read_private(args.manifest))
                transaction.phase('checked')
            else:
                require(sha(helper) == ledger['helper_sha256'], 'Recovery helper checksum changed')
                transaction.recover_safely()
        except BaseException:
            if not transaction.ledger.get('mutated'):
                transaction.ledger['failure'] = traceback.format_exc()
                transaction.phase('failed')
            raise
        finally:
            finish_current(layout.backup_parent, operation, transaction.ledger)


if __name__ == '__main__':
    try:
        main()
    except BaseException:
        # Detailed exceptions and command output belong in the private operation.
        print('Operation did not complete; inspect private status and use explicit recovery.', flush=True)
        raise SystemExit(1) from None
