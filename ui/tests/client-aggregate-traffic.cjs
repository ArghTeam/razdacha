const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const [name, peers, received, sent] of [
      ['measured', [{ ...examplePeer, rx_bytes: 1024, tx_bytes: 2048 }, { ...examplePeer, id: 'other', rx_bytes: 1024, tx_bytes: 1024 }], '2.0 KiB', '3.0 KiB'],
      ['mixed transports', [{ ...examplePeer, rx_bytes: 1024, tx_bytes: 2048 }, { ...examplePeer, id: 'reality-only', wireguard_enabled: false, reality_enabled: true, rx_bytes: 1000000, tx_bytes: 2000000 }], '1.0 KiB', '2.0 KiB'],
      ['Reality only', [{ ...examplePeer, wireguard_enabled: false, reality_enabled: true, rx_bytes: 1000000, tx_bytes: 2000000 }], 'Unknown', 'Unknown'],
      ['zero', [{ ...examplePeer, rx_bytes: 0, tx_bytes: 0 }], '0 B', '0 B'],
      ['missing', [{ ...examplePeer, rx_bytes: null, tx_bytes: 1024 }], 'Unknown', '1.0 KiB'],
      ['invalid', [{ ...examplePeer, rx_bytes: '1024', tx_bytes: -1 }], 'Unknown', 'Unknown'],
      ['partial', [{ ...examplePeer, rx_bytes: 1024, tx_bytes: 0 }, { ...examplePeer, id: 'other', rx_bytes: undefined, tx_bytes: 0 }], 'Unknown', '0 B'],
    ]) {
      const t = await fixture(browser, { peers });
      try {
        assert.equal(await t.page.locator('.stat').filter({ has: t.page.getByText('WireGuard received', { exact: true }) }).locator('.stat-value').textContent(), received, name);
        assert.equal(await t.page.locator('.stat').filter({ has: t.page.getByText('WireGuard sent', { exact: true }) }).locator('.stat-value').textContent(), sent, name);
        assert.equal(await t.page.locator('.client-row').count(), peers.length, name);
        const rows = await t.page.locator('.client-traffic').allTextContents();
        const measured = peers.filter(peer => peer.wireguard_enabled !== false);
        assert.equal(rows.length, measured.length, `${name}: only WG rows have traffic observations`);
        for (let i = 0; i < measured.length; i++) {
          if ([measured[i].rx_bytes, measured[i].tx_bytes].some(value => typeof value !== 'number' || !Number.isFinite(value) || value < 0)) assert(rows[i].includes('Unknown'), `${name}: missing per-client observation shown as zero`);
        }
        assert.equal(await t.page.locator('.summary .stat').count(), 4, 'one unified transport summary');
        assert(await t.page.getByText('WG counters and REALITY current-flow snapshots have different scopes', { exact: false }).isVisible());
      } finally { await t.page.close(); }
    }
  } finally { await browser.close(); }
  console.log('Aggregate client traffic regressions passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
