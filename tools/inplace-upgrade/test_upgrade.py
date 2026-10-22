"""Filesystem/process injection tests: never touch host services or networking."""

import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import sqlite3
import tarfile
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('inplace_upgrade', Path(__file__).with_name('upgrade.py'))
upgrade = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(upgrade)


def database(path):
    connection = sqlite3.connect(path)
    connection.executescript('''
        PRAGMA user_version=12;
        CREATE TABLE peers(id TEXT PRIMARY KEY, egress_tunnel_id TEXT, private_key TEXT, enabled INTEGER);
        INSERT INTO peers VALUES('peer','exit','synthetic-key',1);
        CREATE TABLE rules(id TEXT PRIMARY KEY, action TEXT, tunnel_id TEXT);
        INSERT INTO rules VALUES('rule','tunnel','exit');
        CREATE TABLE tunnels(id TEXT PRIMARY KEY,source TEXT,pool TEXT,pool_updated_at TEXT,enabled INTEGER);
        INSERT INTO tunnels VALUES('exit','pool','old-catalog','old-time',1);
        CREATE TABLE warp_registrations(tunnel_id TEXT PRIMARY KEY,private_key TEXT);
        CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT);
        INSERT INTO settings VALUES('installed_version','1.0.0');
        INSERT INTO settings VALUES('server_private_key','synthetic-server-key');
    ''')
    connection.close()


def committed_generation():
    return {
        'kind': 'committed', 'version': 4,
        'policy': {
            'settings': {'server_private_key': 'synthetic-server-key', 'client_mtu': 1280},
            'tunnels': [
                {'id': 'exit', 'source': 'pool', 'name': 'Synthetic pool', 'enabled': True,
                 'raw': 'synthetic-catalog', 'parsed': None,
                 'pool': [{'url': 'synthetic-old-key'}], 'pool_updated_at': 'old-time'},
                {'id': 'fixed', 'source': 'wgconf', 'enabled': True,
                 'raw': 'synthetic-fixed-key', 'parsed': {'private_key': 'synthetic-fixed-key'}},
            ],
            'peers': [{'id': 'peer', 'private_key': 'synthetic-key', 'enabled': True,
                       'egress_tunnel_id': 'exit'}],
            'rules': [{'id': 'rule', 'action': 'tunnel', 'tunnel_id': 'exit', 'enabled': True}],
        },
        'lists': {'synthetic-source': {'Domains': ['example.test'], 'Subnets': ['198.51.100.0/24'],
                    'native': {'projection_version': 2, 'format': 'json',
                               'original_hash': 'a' * 64, 'original_path': '/synthetic/native/original.json',
                               'dns_hash': 'b' * 64, 'dns_path': '/synthetic/native/dns.json'}}},
    }


def synthetic_generated_config(generation):
    return json.dumps({'policy': generation['policy'], 'lists': generation['lists']}, sort_keys=True)


def authority_evidence(generation):
    raw = json.dumps(generation, sort_keys=True)
    digest = hashlib.sha256(raw.encode()).hexdigest()
    return {'version': 4, 'sha256': digest, 'native_sources': 1,
            'raw_sha256': digest, 'generation_raw': raw,
            'generated_config_sha256': hashlib.sha256(synthetic_generated_config(generation).encode()).hexdigest()}


class FakeHost:
    def __init__(self, layout, operation, manifest):
        self.layout, self.operation, self.manifest = layout, operation, manifest
        self.helper = None
        self.protected = False
        self.events = []
        self.services = {'razdachad': True, 'sing-box': True}
        self.fail_readiness = False
        self.fail_old_readiness = False
        self.fail_setup = False
        self.change_config = False
        self.corrupt_started_config = False
        self.catalog_refresh = False
        self.fault_stop = False
        self.refresh_before_stop = False
        self.corrupt_before_stop = False

    def proof(self, command, *arguments):
        self.events.append(('proof', command))
        if command == 'capabilities':
            return {'schema': 12, 'runtime': '1.12.25'}
        if command == 'candidate':
            return {'runtime': '1.12.25'}
        if command == 'authority':
            result = authority_evidence(upgrade.read_private(self.layout.state / 'applied-network.json'))
            result.update(checker_sha256=upgrade.sha(self.layout.runtime), checker_version='1.12.25')
            return result
        if command == 'wireguard':
            return {'sha256': 'b' * 64, 'peers': 1}
        if command == 'backup':
            destination = sqlite3.connect(arguments[0])
            source = sqlite3.connect(self.layout.database)
            try:
                source.backup(destination)
            finally:
                source.close()
                destination.close()
            os.chmod(arguments[0], 0o600)
            return {'complete': True}
        raise AssertionError(command)

    def run(self, arguments, **_kwargs):
        self.events.append(('run', tuple(map(str, arguments))))
        if 'setup' in arguments:
            if self.change_config:
                self.layout.path('/etc/sing-box/config.json').write_text('changed runtime config')
            if self.fail_setup:
                self.layout.protected[2].write_text('partial setup unit')
                raise upgrade.Refused('injected setup failure')
            connection = sqlite3.connect(self.layout.database)
            connection.execute("UPDATE settings SET value='1.1.0' WHERE key='installed_version'")
            connection.commit()
            connection.close()
            return ''
        if any(value in arguments for value in ('version', '-version')):
            return '1.1.0'
        return ''

    def api_version(self):
        state = upgrade.db_state(self.layout.database)
        current = state['installed']
        return {'version': current, 'installed_version': current, 'commit': '1' * 12 if current == '1.0.0' else '2' * 12,
                'schema_version': 12, 'version_mismatch': False,
                'singbox': {'library': '1.12.25', 'runtime': '1.12.25'}}

    def readiness(self, version, _commit, _runtime, hashes, _schema):
        self.events.append(('ready', version))
        if version == '1.1.0' and self.fail_readiness:
            raise upgrade.Refused('candidate readiness failed after daemon guard release')
        if version == '1.0.0' and self.fail_old_readiness and any(event == ('ready', '1.1.0') for event in self.events):
            raise upgrade.Refused('rollback readiness failed')
        for path in self.layout.binaries + [self.layout.runtime]:
            if path.name == 'razdacha':
                continue
            upgrade.require(upgrade.sha(path) == hashes[path.name], 'actual process executable differs')
        self.guard(False)
        return self.api_version()

    def guard(self, present):
        self.events.append(('guard', present))
        if present:
            upgrade.require(not self.services['razdachad'], 'guard set while daemon running')
            self.protected = True
        upgrade.require(self.protected == present, 'guard state differs')

    def stop(self, service):
        self.events.append(('stop', service))
        if self.fault_stop:
            raise upgrade.Refused('stop failed')
        if service == 'razdachad' and self.refresh_before_stop:
            self.refresh_before_stop = False
            self.refresh_catalog()
        if service == 'razdachad' and self.corrupt_before_stop:
            self.corrupt_before_stop = False
            self.layout.path('/etc/sing-box/config.json').write_text('arbitrary concurrent config')
        self.services[service] = False

    def refresh_catalog(self):
        connection = sqlite3.connect(self.layout.database)
        connection.execute("UPDATE tunnels SET pool='new',pool_updated_at='new-time' WHERE source='pool'")
        connection.commit()
        connection.close()
        path = self.layout.state / 'applied-network.json'
        generation = upgrade.read_private(path)
        for tunnel in generation['policy']['tunnels']:
            if tunnel['source'] == 'pool':
                tunnel['pool'] = [{'url': 'synthetic-refreshed-key'}]
                tunnel['pool_updated_at'] = 'refreshed-time'
        upgrade.private_json(path, generation)
        self.layout.path('/etc/sing-box/config.json').write_text(synthetic_generated_config(generation))

    def assert_stopped(self, service):
        upgrade.require(not self.services[service], 'writer still running')

    def service(self, action, service):
        self.events.append((action, service))
        if action == 'start':
            self.services[service] = True
            if service == 'razdachad':
                # Trusted daemon recovery releases its native guard independently
                # of subsequent operator acceptance, just like production.
                self.protected = False
                if self.catalog_refresh:
                    self.refresh_catalog()
                if self.corrupt_started_config and upgrade.db_state(self.layout.database)['installed'] == '1.1.0':
                    self.layout.path('/etc/sing-box/config.json').write_text('arbitrary changed config')


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.layout = upgrade.Layout(self.root)
        for directory in self.layout.trees + self.layout.protected[:2]:
            directory.mkdir(parents=True, mode=0o700)
            (directory / 'kept').write_text('preserve')
        self.layout.path('/etc/sing-box/config.json').write_text('known committed config')
        for path in self.layout.protected[2:] + self.layout.binaries + [self.layout.runtime, self.layout.path('/usr/sbin/nginx')]:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('old-' + path.name)
        for name in ('ip_forward', 'ip_nonlocal_bind'):
            path = self.layout.path('/proc/sys/net/ipv4/' + name)
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('1\n')
        database(self.layout.database)
        upgrade.private_json(self.layout.state / 'applied-network.json', committed_generation())
        self.layout.path('/etc/sing-box/config.json').write_text(synthetic_generated_config(committed_generation()))
        self.layout.backup_parent.mkdir(parents=True)
        self.operation = self.layout.backup_parent / 'inplace-test-operation'
        self.operation.mkdir(mode=0o700)
        helper = self.root / 'proof-input'
        helper.write_text('synthetic helper')
        helper.chmod(0o600)
        self.archive = self.root / 'release.tar.gz'
        with tarfile.open(self.archive, 'w:gz') as archive:
            for name, body in {'razdacha': b'new-cli', 'razdachad': b'new-daemon',
                               'LICENSE': b'license', 'README.md': b'readme'}.items():
                info = tarfile.TarInfo('./' + name)
                info.size = len(body)
                archive.addfile(info, io.BytesIO(body))
        self.manifest = {'archive': str(self.archive), 'archive_sha256': upgrade.sha(self.archive),
                         'helper': str(helper), 'helper_sha256': upgrade.sha(helper),
                         'version': '1.1.0', 'commit': '2' * 12, 'runtime': '1.12.25', 'schema': 12,
                         'razdacha_sha256': hashlib.sha256(b'new-cli').hexdigest(),
                         'razdachad_sha256': hashlib.sha256(b'new-daemon').hexdigest()}
        self.host = FakeHost(self.layout, self.operation, self.manifest)
        self.transaction = upgrade.Transaction(self.layout, self.operation, self.host)

    def test_success_keeps_policy_and_snapshot(self):
        self.host.catalog_refresh = True
        self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'accepted')
        self.assertFalse(self.host.protected)
        self.assertNotEqual(self.transaction.ledger['authority']['sha256'], self.host.proof('authority')['sha256'])
        self.assertNotEqual(self.transaction.ledger['config_sha256'], upgrade.sha(self.layout.path('/etc/sing-box/config.json')))
        self.assertEqual(upgrade.sha(self.layout.path('/etc/sing-box/config.json')), self.host.proof('authority')['generated_config_sha256'])
        saved = Path(self.transaction.ledger['snapshot'][str(self.layout.state)])
        self.assertEqual(upgrade.db_state(saved / 'state.db')['installed'], '1.0.0')
        self.assertTrue((self.operation / 'status.json').stat().st_mode & 0o777 == 0o600)
        self.assertLess(self.host.events.index(('stop', 'razdachad')),
                        self.host.events.index(('guard', True)))
        self.assertLess(self.host.events.index(('guard', True)),
                        self.host.events.index(('stop', 'sing-box')))

    def test_check_only_has_no_service_or_guard_mutations(self):
        self.transaction.preflight(self.manifest)
        self.transaction.phase('checked')
        self.assertEqual(self.transaction.ledger['phase'], 'checked')
        self.assertFalse(self.transaction.ledger['mutated'])
        self.assertFalse(any(event[0] in ('stop', 'start') or event == ('guard', True)
                             for event in self.host.events))
        self.assertFalse((self.operation / 'snapshot').exists())

    def test_unsafe_archive_member_is_rejected_before_host_mutation(self):
        with tarfile.open(self.archive, 'w:gz') as archive:
            member = tarfile.TarInfo('../escape')
            member.size = 1
            archive.addfile(member, io.BytesIO(b'x'))
        self.manifest['archive_sha256'] = upgrade.sha(self.archive)
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertFalse(any(event[0] == 'stop' for event in self.host.events))
        self.assertFalse((self.root / 'escape').exists())

    def test_duplicate_baseline_authority_is_rejected_before_stop_for_check_and_run(self):
        for action in ('check', 'run'):
            with self.subTest(action=action):
                fixture = Fixture('test_success_keeps_policy_and_snapshot')
                fixture.setUp()
                try:
                    original = fixture.host.proof
                    def duplicate(command, *arguments):
                        evidence = original(command, *arguments)
                        if command == 'authority':
                            raw = evidence['generation_raw']
                            raw = raw[:-1] + ', "version": 4}'
                            evidence.update(generation_raw=raw, raw_sha256=hashlib.sha256(raw.encode()).hexdigest())
                        return evidence
                    fixture.host.proof = duplicate
                    operation = (fixture.transaction.preflight if action == 'check'
                                 else fixture.transaction.execute)
                    with self.assertRaises(upgrade.Refused):
                        operation(fixture.manifest)
                    self.assertFalse(any(event[0] == 'stop' for event in fixture.host.events))
                finally:
                    fixture.doCleanups()

    def test_incompatible_baseline_generator_is_refused_before_stop(self):
        self.layout.path('/etc/sing-box/config.json').write_text('different generator or checker-pruned config')
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertFalse(any(event[0] == 'stop' for event in self.host.events))

    def test_arbitrary_post_start_config_is_rejected_despite_allowed_pool_refresh(self):
        self.host.catalog_refresh = True
        self.host.corrupt_started_config = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'rolled-back')
        actual = upgrade.sha(self.layout.path('/etc/sing-box/config.json'))
        self.assertEqual(actual, self.host.proof('authority')['generated_config_sha256'])
        self.assertFalse(self.host.protected)

    def test_pool_refresh_between_preflight_and_snapshot_uses_exact_stopped_baseline(self):
        for rollback in (False, True):
            with self.subTest(rollback=rollback):
                fixture = Fixture('test_success_keeps_policy_and_snapshot')
                fixture.setUp()
                try:
                    initial_config = upgrade.sha(fixture.layout.path('/etc/sing-box/config.json'))
                    initial_authority = fixture.host.proof('authority')
                    fixture.host.refresh_before_stop = True
                    fixture.host.fail_readiness = rollback
                    if rollback:
                        with self.assertRaises(upgrade.Refused):
                            fixture.transaction.execute(fixture.manifest)
                    else:
                        fixture.transaction.execute(fixture.manifest)
                    ledger = fixture.transaction.ledger
                    self.assertEqual(ledger['phase'], 'rolled-back' if rollback else 'accepted')
                    self.assertEqual(ledger['config_sha256'], initial_config)
                    self.assertEqual(ledger['authority'], initial_authority)
                    self.assertNotEqual(ledger['stopped_config_sha256'], initial_config)
                    self.assertEqual(ledger['stopped_config_sha256'], ledger['stopped_authority']['generated_config_sha256'])
                    self.assertEqual(upgrade.sha(fixture.layout.path('/etc/sing-box/config.json')), ledger['stopped_config_sha256'])
                    self.assertFalse(fixture.host.protected)
                finally:
                    fixture.doCleanups()

    def test_arbitrary_config_between_preflight_and_snapshot_is_not_rebaselined(self):
        self.host.corrupt_before_stop = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'recovery-required')
        self.assertTrue(self.host.protected)
        self.assertNotIn('stopped_config_sha256', self.transaction.ledger)
        self.assertNotIn(('ready', '1.1.0'), self.host.events)

    def test_checksum_failure_does_not_stop_services(self):
        self.manifest['archive_sha256'] = '0' * 64
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertFalse(any(event[0] == 'stop' for event in self.host.events))
        self.assertEqual(self.transaction.ledger['phase'], 'failed')

    def test_partial_binary_replacement_restores_both_binaries(self):
        original = upgrade.atomic_binary
        calls = []
        def partial(source, target):
            original(source, target)
            calls.append(target)
            if len(calls) == 1:
                raise InterruptedError('interrupted after first atomic binary replacement')
        with patch.object(upgrade, 'atomic_binary', partial):
            with self.assertRaises(InterruptedError):
                self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'rolled-back')
        self.assertTrue(all(path.read_text() == 'old-' + path.name for path in self.layout.binaries))
        self.assertFalse(self.host.protected)

    def test_partial_setup_files_are_quarantined_and_restored(self):
        original = self.layout.protected[2].read_text()
        self.host.fail_setup = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'rolled-back')
        self.assertEqual(self.layout.protected[2].read_text(), original)
        quarantines = list(self.layout.protected[2].parent.glob('.' + self.layout.protected[2].name + '.failed-*'))
        self.assertEqual(len(quarantines), 1)
        self.assertEqual(quarantines[0].read_text(), 'partial setup unit')

    def test_changed_runtime_config_is_rejected_before_candidate_start(self):
        self.host.change_config = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'rolled-back')
        self.assertNotIn(('ready', '1.1.0'), self.host.events)
        self.assertEqual(self.layout.path('/etc/sing-box/config.json').read_text(), synthetic_generated_config(committed_generation()))

    def test_failed_post_publication_readiness_guards_before_restoring(self):
        self.host.fail_readiness = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'rolled-back')
        ready_index = self.host.events.index(('ready', '1.1.0'))
        following = self.host.events[ready_index + 1:]
        self.assertLess(following.index(('stop', 'razdachad')), following.index(('guard', True)))
        self.assertLess(following.index(('guard', True)), following.index(('stop', 'sing-box')))

    def test_rollback_accepts_startup_pool_republication_and_preserves_raw_baseline(self):
        baseline_raw = (self.layout.state / 'applied-network.json').read_bytes()
        self.host.catalog_refresh = True
        self.host.fail_readiness = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'rolled-back')
        self.assertFalse(self.host.protected)
        before = self.transaction.ledger['authority']
        after = self.host.proof('authority')
        self.assertNotEqual(before['sha256'], after['sha256'])
        self.assertNotEqual(before['generated_config_sha256'], after['generated_config_sha256'])
        self.assertEqual(json.loads(before['generation_raw']), json.loads(baseline_raw))
        saved = Path(self.transaction.ledger['snapshot'][str(self.layout.state)])
        self.assertEqual((saved / 'applied-network.json').read_bytes(), baseline_raw)
        upgrade.equal_authority(before, after)

    def test_failed_rollback_retains_guard_and_recovery_phase(self):
        self.host.fail_readiness = True
        self.host.fail_old_readiness = True
        with self.assertRaises(upgrade.Refused):
            self.transaction.execute(self.manifest)
        self.assertEqual(self.transaction.ledger['phase'], 'recovery-required')
        self.assertTrue(self.host.protected)
        self.assertFalse(self.host.services['razdachad'])

    def test_unknown_tree_identity_is_not_overlaid(self):
        self.transaction.preflight(self.manifest)
        self.transaction.snapshot()
        changed = self.layout.state.with_name('foreign-old')
        os.rename(self.layout.state, changed)
        self.layout.state.mkdir()
        (self.layout.state / 'foreign').write_text('preserve foreign tree')
        with self.assertRaises(upgrade.Refused):
            self.transaction.recover_safely()
        self.assertEqual((self.layout.state / 'foreign').read_text(), 'preserve foreign tree')
        self.assertTrue(self.host.protected)
        self.assertEqual(self.transaction.ledger['phase'], 'recovery-required')

    def test_each_post_snapshot_phase_failure_rolls_back(self):
        # Separate fixtures prevent reusing operation paths/snapshots.
        for phase in ('replacing-binaries', 'running-setup', 'starting-candidate', 'accepted'):
            with self.subTest(phase=phase):
                fixture = Fixture('test_success_keeps_policy_and_snapshot')
                fixture.setUp()
                try:
                    original = fixture.transaction.phase
                    failed = [False]
                    def injected(name):
                        original(name)
                        if name == phase and not failed[0]:
                            failed[0] = True
                            raise InterruptedError('injected phase boundary')
                    fixture.transaction.phase = injected
                    with self.assertRaises(InterruptedError):
                        fixture.transaction.execute(fixture.manifest)
                    self.assertEqual(fixture.transaction.ledger['phase'], 'rolled-back')
                finally:
                    fixture.doCleanups()


class PureTests(unittest.TestCase):
    def test_authority_accepts_only_paired_pool_catalog_fields_without_mutating_evidence(self):
        baseline = authority_evidence(committed_generation())
        changed = committed_generation()
        changed['policy']['tunnels'][0]['pool'] = [{'url': 'synthetic-new-key'}]
        changed['policy']['tunnels'][0]['pool_updated_at'] = 'later'
        after = authority_evidence(changed)
        saved_before, saved_after = copy.deepcopy(baseline), copy.deepcopy(after)
        upgrade.equal_authority(baseline, after)
        self.assertEqual(baseline, saved_before)
        self.assertEqual(after, saved_after)
        # A new catalog may remove an optional pool field; all other fields remain strict.
        del changed['policy']['tunnels'][0]['pool']
        upgrade.equal_authority(baseline, authority_evidence(changed))

    def test_authority_rejects_changed_policy_lists_and_native_authority(self):
        baseline = authority_evidence(committed_generation())
        cases = {
            'server key': lambda g: g['policy']['settings'].update(server_private_key='changed'),
            'tunnel key': lambda g: g['policy']['tunnels'][1].update(raw='changed'),
            'pool raw source': lambda g: g['policy']['tunnels'][0].update(raw='changed'),
            'pool parsed key': lambda g: g['policy']['tunnels'][0].update(parsed={'private_key':'changed'}),
            'source transition': lambda g: g['policy']['tunnels'][0].update(source='json'),
            'activation': lambda g: g['policy']['tunnels'][0].update(enabled=False),
            'tunnel identity': lambda g: g['policy']['tunnels'][0].update(id='different'),
            'duplicate identity': lambda g: g['policy']['tunnels'].append(copy.deepcopy(g['policy']['tunnels'][0])),
            'tunnel order': lambda g: g['policy']['tunnels'].reverse(),
            'peer key': lambda g: g['policy']['peers'][0].update(private_key='changed'),
            'peer egress': lambda g: g['policy']['peers'][0].update(egress_tunnel_id='fixed'),
            'peer activation': lambda g: g['policy']['peers'][0].update(enabled=False),
            'rule': lambda g: g['policy']['rules'][0].update(action='direct'),
            'list body': lambda g: g['lists']['synthetic-source'].update(Domains=['different.test']),
            'list closure': lambda g: g['lists'].clear(),
            'native original': lambda g: g['lists']['synthetic-source']['native'].update(original_hash='c'*64),
            'native path': lambda g: g['lists']['synthetic-source']['native'].update(original_path='/synthetic/other'),
            'native DNS': lambda g: g['lists']['synthetic-source']['native'].update(dns_hash='d'*64),
            'native projection': lambda g: g['lists']['synthetic-source']['native'].update(projection_version=1),
            'kind': lambda g: g.update(kind='bootstrap'),
            'version': lambda g: g.update(version=3),
            'unknown generation field': lambda g: g.update(future_authority='changed'),
        }
        for name, mutate in cases.items():
            with self.subTest(name=name):
                changed = committed_generation()
                mutate(changed)
                with self.assertRaises(upgrade.Refused):
                    upgrade.equal_authority(baseline, authority_evidence(changed))
        fixed = committed_generation()
        changed = copy.deepcopy(fixed)
        changed['policy']['tunnels'][1]['pool'] = [{'url':'must-not-be-ignored'}]
        with self.assertRaises(upgrade.Refused):
            upgrade.equal_authority(authority_evidence(fixed), authority_evidence(changed))

    def test_unknown_authority_field_type_changes_are_rejected(self):
        for old, new in ((True, 1), (1, 1.0), ({'value': True}, {'value': 1})):
            with self.subTest(old=old, new=new):
                before, after = committed_generation(), committed_generation()
                before['future_field'], after['future_field'] = old, new
                with self.assertRaises(upgrade.Refused):
                    upgrade.equal_authority(authority_evidence(before), authority_evidence(after))

    def test_authority_requires_current_complete_private_evidence(self):
        before = authority_evidence(committed_generation())
        for field in ('generation_raw', 'raw_sha256'):
            broken = dict(before)
            broken.pop(field)
            with self.subTest(field=field), self.assertRaises(upgrade.Refused):
                upgrade.equal_authority(before, broken)
        broken = dict(before, generation_raw='{}')
        with self.assertRaises(upgrade.Refused):
            upgrade.equal_authority(before, broken)

    def test_failed_unit_without_processes_is_stopped(self):
        properties = {'ActiveState': 'failed', 'MainPID': '0', 'ControlPID': '0', 'Job': '0'}
        self.assertTrue(upgrade.stopped_state_is_safe(properties, [], False))
        for key, value in [('MainPID', '12'), ('ControlPID', '8'), ('Job', '7'), ('ActiveState', 'activating')]:
            changed = dict(properties, **{key: value})
            self.assertFalse(upgrade.stopped_state_is_safe(changed, [], False))
        self.assertFalse(upgrade.stopped_state_is_safe(properties, ['42'], False))
        self.assertFalse(upgrade.stopped_state_is_safe(properties, [], True))

    def test_only_paired_source_pool_catalog_fields_can_differ(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'state.db'
            database(path)
            baseline = upgrade.db_state(path)
        changed = copy.deepcopy(baseline)
        pool = changed['tables']['tunnels']['rows'][0]
        pool.update(pool='refreshed', pool_updated_at='later')
        upgrade.equal_policy(baseline, changed)
        for table, field, value in [('peers', 'egress_tunnel_id', None),
                                     ('peers', 'private_key', 'different'),
                                     ('tunnels', 'source', 'json'),
                                     ('tunnels', 'enabled', 0),
                                     ('rules', 'action', 'direct')]:
            altered = copy.deepcopy(changed)
            altered['tables'][table]['rows'][0][field] = value
            with self.subTest(field=field), self.assertRaises(upgrade.Refused):
                upgrade.equal_policy(baseline, altered)
        native = copy.deepcopy(baseline)
        native['tables']['tunnels']['rows'][0]['source'] = 'json'
        changed_native = copy.deepcopy(native)
        changed_native['tables']['tunnels']['rows'][0]['pool'] = 'different'
        with self.assertRaises(upgrade.Refused):
            upgrade.equal_policy(native, changed_native)

    def test_restore_recovers_interruption_between_atomic_tree_renames(self):
        for interruption in (0, 1, 2):
            with self.subTest(interruption=interruption), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                live, prepared, quarantine = [root / name for name in ('live', 'prepared', 'quarantine')]
                live.mkdir()
                prepared.mkdir()
                (live / 'data').write_text('failed current state')
                (prepared / 'data').write_text('saved state')
                entry = {'live': str(live), 'prepared': str(prepared), 'quarantine': str(quarantine),
                         'old': upgrade.identity(live), 'replacement': upgrade.identity(prepared)}
                if interruption >= 1:
                    os.rename(live, quarantine)
                if interruption >= 2:
                    os.rename(prepared, live)
                upgrade.restore_plan_entry(entry)
                self.assertEqual((live / 'data').read_text(), 'saved state')
                self.assertEqual((quarantine / 'data').read_text(), 'failed current state')
                # A restarted explicit recovery must be idempotent.
                upgrade.restore_plan_entry(entry)

    def test_unknown_quarantine_and_prepared_identity_are_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            live, prepared, quarantine = [root / name for name in ('live', 'prepared', 'quarantine')]
            for path in (live, prepared, quarantine):
                path.mkdir()
            entry = {'live': str(live), 'prepared': str(prepared), 'quarantine': str(quarantine),
                     'old': upgrade.identity(live), 'replacement': upgrade.identity(prepared)}
            with self.assertRaises(upgrade.Refused):
                upgrade.restore_plan_entry(entry)
            self.assertTrue(live.exists())

    def test_current_marker_blocks_new_upgrade_and_stale_recovery(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory).resolve()
            first = parent / 'inplace-first-operation'
            first_ledger = upgrade.claim_operation(parent, first)
            first_ledger.update(mutated=True, phase='starting-candidate')
            upgrade.private_json(first / 'status.json', first_ledger)
            upgrade.require_current(parent, first, first_ledger)
            second = parent / 'inplace-second-operation'
            with self.assertRaises(upgrade.Refused):
                upgrade.claim_operation(parent, second)
            self.assertFalse(second.exists())
            first_ledger['phase'] = 'accepted'
            upgrade.private_json(first / 'status.json', first_ledger)
            upgrade.finish_current(parent, first, first_ledger)
            second_ledger = upgrade.claim_operation(parent, second)
            with self.assertRaises(upgrade.Refused):
                upgrade.require_current(parent, first, first_ledger)
            upgrade.require_current(parent, second, second_ledger)

    def test_missing_marker_does_not_allow_an_unfinished_upgrade(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory).resolve()
            first = parent / 'inplace-first-operation'
            ledger = upgrade.claim_operation(parent, first)
            ledger.update(mutated=True, phase='starting-candidate')
            upgrade.private_json(first / 'status.json', ledger)
            (parent / 'inplace-current.json').unlink()
            with self.assertRaises(upgrade.Refused):
                upgrade.claim_operation(parent, parent / 'inplace-second-operation')

    def test_checked_operation_does_not_block_fresh_live_operation(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory).resolve()
            checked = parent / 'inplace-check-operation'
            ledger = upgrade.claim_operation(parent, checked)
            ledger['phase'] = 'checked'
            upgrade.private_json(checked / 'status.json', ledger)
            upgrade.finish_current(parent, checked, ledger)
            live = parent / 'inplace-live-operation'
            next_ledger = upgrade.claim_operation(parent, live)
            upgrade.require_current(parent, live, next_ledger)

    def test_setup_environment_drops_all_operator_overrides(self):
        with patch.dict(os.environ, {'RAZDACHA_PUBLIC': '0', 'RAZDACHA_TELEGRAM_TOKEN': 'synthetic',
                                    'HTTPS_PROXY': 'synthetic', 'LD_PRELOAD': 'synthetic'}):
            environment = upgrade.setup_environment()
        self.assertEqual(set(environment), {'PATH', 'LANG', 'LC_ALL', 'HOME'})


if __name__ == '__main__':
    unittest.main()
