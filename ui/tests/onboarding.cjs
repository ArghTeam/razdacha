const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const { appFixture, clientConfig } = require('./app.fixture.cjs');
const screenshots = process.env.ONBOARDING_SCREENSHOTS;
const pending = { pending: true, peer_id: 'example', read_only: false, country: 'AUTO', prepared: false, applied: false };
const writes = t => t.journal.filter(entry => entry.method === 'POST' && entry.pathname.startsWith('/api/onboarding/'));
async function capture(page, name) { if (screenshots) { fs.mkdirSync(screenshots, { recursive: true }); await page.screenshot({ path: path.join(screenshots, name + '.png') }); } }

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const width of [1440, 390]) {
      const t = await appFixture(browser, { viewport: { width, height: 900 }, state: { authenticated: false, onboarding: pending } });
      try {
        assert.equal(writes(t).length, 0);
        await t.login();
        await t.page.getByText('Choose how to start', { exact: true }).waitFor();
        assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width);
        await capture(t.page, `setup-${width}-choose`);
        await t.page.locator('input[value="free"]').check();
        await t.page.locator('#setup-country').fill('DE');
        assert.equal(writes(t).length, 0, 'selection mutated server');
        await t.page.locator('[data-act="setup-review"]').click();
        await capture(t.page, `setup-${width}-review`);
        await t.page.locator('[data-act="setup-apply"]').click();
        await t.page.getByText('Your client is ready to import', { exact: true }).waitFor();
        assert.deepEqual(writes(t).map(entry => entry.pathname), ['/api/onboarding/configure', '/api/onboarding/apply']);
        assert.deepEqual(writes(t)[0].body, { mode: 'free', country: 'DE' });
        assert.equal(t.journal.filter(entry => entry.pathname.endsWith('/config')).length, 0, 'profile loaded without explicit delivery');
        await t.page.locator('[data-act="setup-profile"]').click();
        await t.page.locator('#setup-profile-result').waitFor({ state: 'visible' });
        const downloaded = t.page.waitForEvent('download');
        await t.page.locator('[data-act="setup-download"]').click();
        const file = await downloaded;
        assert.equal(fs.readFileSync(await file.path(), 'utf8'), clientConfig);
        assert(await t.page.locator('#setup-qr').evaluate(canvas => canvas.width > 0));
        await capture(t.page, `setup-${width}-synthetic-profile`);
        assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width);
        await t.page.locator('[data-act="setup-finish"]').click();
        await t.page.locator('#modal-backdrop').waitFor({ state: 'hidden' });
        assert.equal(t.state.onboarding.pending, false);
        assert.equal(await t.page.locator('#modal canvas').count(), 0, 'private QR survived close');
        await t.page.locator('[data-act="open-setup"]').click();
        await t.page.getByText('Choose how to start', { exact: true }).waitFor();
        await t.page.keyboard.press('Escape');
        assert.equal(t.state.onboarding.pending, true, 'Escape completed setup');
        t.assertClean();
      } finally { await t.page.close(); }
    }

    const retry = await appFixture(browser, { state: { onboarding: pending } });
    try {
      await retry.page.getByText('Choose how to start', { exact: true }).waitFor();
      await retry.page.locator('input[value="free"]').check();
      await retry.page.locator('#setup-country').fill('RU');
      await retry.page.locator('[data-act="setup-review"]').click();
      assert(await retry.page.getByText('RU and BY are excluded.', { exact: false }).isVisible());
      assert.equal(writes(retry).length, 0);
      await retry.page.locator('#setup-country').fill('DE');
      await retry.page.locator('[data-act="setup-review"]').click();
      retry.fail('POST', '/api/onboarding/apply', { error: 'Synthetic source loading; retry Apply' }, 503);
      await retry.page.locator('[data-act="setup-apply"]').click();
      await retry.page.getByText('Synthetic source loading; retry Apply', { exact: true }).waitFor();
      assert.equal(retry.state.onboarding.pending, true);
      retry.recover('POST', '/api/onboarding/apply');
      await retry.page.locator('[data-act="setup-apply"]').click();
      await retry.page.getByText('Your client is ready to import', { exact: true }).waitFor();
      assert.equal(writes(retry).filter(entry => entry.pathname.endsWith('/configure')).length, 1, 'retry duplicated configuration intent');
      retry.assertClean();
    } finally { await retry.page.close(); }

    const keep = await appFixture(browser, { state: { onboarding: { ...pending, read_only: true } } });
    try {
      await keep.page.getByText('Choose how to start', { exact: true }).waitFor();
      assert(await keep.page.locator('input[value="free"]').isDisabled());
      await keep.page.locator('[data-act="setup-review"]').click();
      await keep.page.locator('[data-act="setup-apply"]').click();
      await keep.page.getByText('Your client is ready to import', { exact: true }).waitFor();
      assert.equal(writes(keep)[0].body.mode, 'skip');
      assert.equal(keep.state.peers.length, 1);
      keep.assertClean();
    } finally { await keep.page.close(); }

    const empty = await appFixture(browser, { state: { onboarding: { ...pending, prepared: true, applied: true, mode: 'free', pool_enabled: true, candidate_count: 0, catalog_outcome: 'no_candidates' } } });
    try {
      await empty.page.getByText('Your client is ready to import', { exact: true }).waitFor();
      assert(await empty.page.getByText('No eligible free exits are available.', { exact: true }).isVisible());
      assert(await empty.page.getByText('they do not fall back to direct routing.', { exact: false }).isVisible());
      empty.assertClean();
    } finally { await empty.page.close(); }

    const delayed = await appFixture(browser);
    try {
      delayed.hold('POST', '/api/onboarding/reopen');
      await delayed.page.locator('[data-act="open-setup"]').click();
      await delayed.waitHeld('POST', '/api/onboarding/reopen');
      await delayed.page.evaluate(async () => { const shell = await import('/js/shell.js'); shell.openModal(shell.modalShell('Replacement draft', '<input id="replacement-draft" value="Keep this edit">', '')); });
      await delayed.release('POST', '/api/onboarding/reopen');
      await delayed.settle();
      assert.equal(await delayed.page.locator('#replacement-draft').inputValue(), 'Keep this edit');
      assert(await delayed.page.getByRole('heading', { name: 'Replacement draft' }).isVisible());
      delayed.assertClean();
    } finally { await delayed.page.close(); }
  } finally { await browser.close(); }
  console.log('First-login web setup browser regressions passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
