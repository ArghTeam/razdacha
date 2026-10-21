// Ordinary readable synthetic data, separate from deliberate stress fixtures.
const normalState = {
  peers: [{ id: 'example', name: 'Example laptop', address: '192.0.2.2', enabled: true, rx_bytes: 24576, tx_bytes: 1024 }, { id: 'tablet', name: 'Office tablet', address: '192.0.2.3', enabled: true, egress_tunnel_id: 'exit' }],
  tunnels: [{ id: 'exit', name: 'Demo exit', type: 'socks', enabled: true, source: 'url', host: 'proxy.example.invalid', port: 1080, status: 'up', latency_ms: 17 }, { id: 'pool', name: 'Demo pool', type: 'vless', source: 'pool', enabled: false, pool: { servers_total: 3, servers_alive: null } }],
  rules: [{ id: 'work', name: 'Work resources', priority: 0, enabled: true, action: 'tunnel', tunnel_id: 'exit', via_tunnel_id: '', peer_scope: 'all', peer_ids: [], community_lists: ['work'], domains: ['example.com'], subnets: [], remote_lists: [] }],
  community: [{ key: 'work', title: 'Work services', has_subnets: true, in_preset: true }, { key: 'media', title: 'Media services' }],
  notify: { enabled: false, token_set: true, chat_id: '-1001234567890' },
  backup: { enabled: false, passphrase_set: true, interval_hours: 24, telegram_ready: true, last_sent_at: null, last_error: '' },
  poolDetails: { servers: [{ title: 'Demo candidate', in_rotation: true, alive: null }, { title: 'Reserve candidate', in_rotation: false, alive: null, ping_ms: 24 }], excluded: [] },
  diag: { overall: 'ok', checked_at: '2026-10-21T12:00:00Z', checks: [{ id: 'wg', title: 'WireGuard', status: 'ok', detail: 'Synthetic interface available' }, { id: 'nft', title: 'Policy', status: 'ok', detail: 'Synthetic policy available' }] },
};
module.exports = { normalState };
if (require.main === module) {
  require('./shell-preview.fixture.cjs').startPreview(Number(process.env.PORT) || 0, normalState)
    .then(({ server }) => console.log(`Synthetic integrated UI preview: http://127.0.0.1:${server.address().port}`));
}
