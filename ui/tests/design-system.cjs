// Real menu paths and synthetic geometry contract for the embedded dashboard.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const { appFixture } = require('./app.fixture.cjs');
const { normalState } = require('./integrated.fixture.cjs');
const { act, menu, visibleFocus } = require('./ui-sweep.helper.cjs');
const destination = process.env.DESIGN_SCREENSHOT_DIR;
async function shot(page, name) {
  if (!destination) return;
  fs.mkdirSync(destination, { recursive: true });
  await page.screenshot({ path: path.join(destination, name + '.png') });
}
async function frame(page, width) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth), width);
  const result = await page.locator('#modal').evaluate(modal => {
    const rect = el => { const b = el.getBoundingClientRect(); return { top: b.top, bottom: b.bottom, left: b.left, right: b.right, width: b.width, height: b.height }; };
    return { modal: rect(modal), header: rect(modal.querySelector('.modal-head')), body: rect(modal.querySelector('.modal-body')), footer: rect(modal.querySelector('.modal-foot')), scroller: getComputedStyle(modal.querySelector('.modal-body')).overflowY };
  });
  assert(result.modal.left >= 0 && result.modal.right <= width);
  assert(result.header.top >= 0 && result.footer.bottom <= page.viewportSize().height + 1);
  assert(result.body.height >= 100, JSON.stringify(result));
  assert.equal(result.scroller, 'auto');
  assert(result.body.top >= result.header.bottom - 1 && result.body.bottom <= result.footer.top + 1);
  return result;
}
(async () => {
  const browser = await chromium.launch({ headless: true }); let cases = 0;
  try {
    for (const theme of ['light', 'dark']) for (const width of [1440, 1024, 390, 320]) {
      const t = await appFixture(browser, { theme, viewport: { width, height: width < 500 ? 844 : 1000 }, state: normalState });
      const prefix = `${theme}-${width}`;
      try {
        const metrics = [];
        for (const [screen, domain] of [['peers', 'peer'], ['tunnels', 'tunnel'], ['rules', 'rule']]) {
          await t.navigate(screen);
          const rows = await t.page.locator('#screen .row').evaluateAll(rows => rows.map(row => ({ actions: row.querySelectorAll('[data-act]').length, menus: row.querySelectorAll('.more-btn[aria-haspopup="menu"]').length })));
          assert(rows.length); for (const row of rows) assert.deepEqual(row, { actions: 1, menus: 1 });
          metrics.push(await t.page.locator(`.more-btn[data-act="menu-${domain}"]`).first().evaluate(el => ({ width: el.getBoundingClientRect().width, height: el.getBoundingClientRect().height })));
          assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width);
          await shot(t.page, `${prefix}-${screen}`);
        }
        for (const metric of metrics) assert.deepEqual(metric, { width: width < 761 ? 44 : 40, height: width < 761 ? 44 : 40 });
        await act(t, 'menu-rule', 'work');
        assert(await t.page.locator('#row-menu [data-act="rule-up"]').isDisabled());
        assert(await t.page.locator('#row-menu [data-act="rule-down"]').isDisabled());
        await t.page.keyboard.press('End'); assert.equal(await t.page.evaluate(() => document.activeElement.dataset.act), 'delete-rule');
        await t.page.keyboard.press('Escape');
        assert.equal(await t.page.evaluate(() => document.activeElement.dataset.act), 'menu-rule');
        await menu(t, 'rule', 'work', 'edit-rule');
        const f = await frame(t.page, width);
        if (width >= 1024) {
          assert(f.modal.width >= Math.min(1180, width - 32) - 1);
          const geometry = await t.page.evaluate(() => {
            const resources = document.querySelector('.rule-resources').getBoundingClientRect();
            const action = document.querySelector('.rule-action').getBoundingClientRect();
            const title = document.querySelector('.rule-action h3').getBoundingClientRect();
            return { resourcesRight: resources.right, actionLeft: action.left, actionTop: action.top, titleLeft: title.left, bodyTop: document.querySelector('.modal-body').getBoundingClientRect().top };
          });
          assert(geometry.actionLeft > geometry.resourcesRight);
          assert(geometry.titleLeft >= geometry.actionLeft + 15);
          assert(geometry.actionTop - geometry.bodyTop < 220);
        }
        await t.page.locator('#r-catalog > summary').click(); await t.page.fill('#r-list-q', 'Media');
        assert.equal(await t.page.locator('#r-lists .rule-item:visible').count(), 1);
        assert.equal(await t.page.locator('#r-lists').evaluate(el => getComputedStyle(el).overflowY), 'visible');
        await shot(t.page, `${prefix}-rule-expanded`);
        await t.page.keyboard.press('Escape');
        await t.navigate('peers'); await menu(t, 'peer', 'tablet', 'peer-details');
        assert(await t.page.isChecked('[name="client-route-mode"][value="tunnel"]'));
        assert.equal(await t.page.inputValue('#client-target'), 'exit');
        await frame(t.page, width); await shot(t.page, `${prefix}-client-settings`);
        await t.page.keyboard.press('Escape');
        await menu(t, 'peer', 'example', 'peer-configuration'); await t.page.locator('#client-config-result').waitFor();
        await frame(t.page, width); await shot(t.page, `${prefix}-client-configuration`);
        await t.page.keyboard.press('Escape');
        assert.equal(await t.page.locator('#client-config-text').count(), 0);
        await t.page.locator('#btn-settings').click(); await t.page.locator('[data-settings-section="backup"]').click(); await t.settle();
        await t.page.locator('.modal-body').evaluate(el => { el.scrollTop = el.scrollHeight; });
        const nav = await t.page.locator('.settings-nav').boundingBox(), head = await t.page.locator('.modal-head').boundingBox();
        assert(nav.y >= head.y + head.height - 1);
        await t.page.locator('[data-settings-section="network"]').click();
        assert(await t.page.locator('#s-mtu').isVisible());
        await frame(t.page, width); await shot(t.page, `${prefix}-settings`);
        await t.page.keyboard.press('Escape');
        await t.navigate('tunnels'); await menu(t, 'tunnel', 'pool', 'pool-details');
        const search = await t.page.locator('#pool-search').boundingBox();
        assert.equal(Math.round(search.height), width < 761 ? 44 : 40);
        assert(search.width >= 200);
        await frame(t.page, width); await shot(t.page, `${prefix}-pool`);
        await t.page.keyboard.press('Escape');
        await t.navigate('rules'); await menu(t, 'rule', 'work', 'delete-rule');
        assert.equal(await t.page.locator('.modal-foot [autofocus]').evaluate(el => el === document.activeElement), true);
        await t.page.keyboard.press('Escape'); assert(!t.journal.some(entry => entry.method === 'DELETE'));
        t.assertClean(); cases++;
      } finally { await t.page.close(); }
    }
    for (const width of [1440, 390, 320]) {
      const sources = Array.from({ length: 34 }, (_, index) => ({ key: `source-${index}`, title: `Example source ${index + 1}` }));
      const selected = sources.map(source => source.key);
      const t = await appFixture(browser, { screen: 'rules', viewport: { width, height: 900 }, state: { ...normalState, community: sources, rules: [{ ...normalState.rules[0], community_lists: selected }] } });
      try {
        await menu(t, 'rule', 'work', 'edit-rule');
        await t.page.locator('#r-catalog > summary').click();
        assert.equal(await t.page.locator('#r-lists .rule-item:visible').count(), 6);
        assert.equal(await t.page.locator('#r-lists input:checked').count(), 34);
        const pageHeight = await t.page.locator('#r-lists').evaluate(el => el.getBoundingClientRect().height);
        assert(pageHeight < (width > 860 ? 240 : 440), `Unbounded source page: ${pageHeight}`);
        await t.page.getByRole('button', { name: 'Next sources page', exact: true }).click();
        await t.page.locator('#r-lists input[value="source-6"]').uncheck();
        await t.page.fill('#r-list-q', 'Example source 34');
        assert.equal(await t.page.locator('#r-lists .rule-item:visible').count(), 1);
        await t.page.locator('#r-domains').focus(); await visibleFocus(t.page, '#r-domains');
        if (width > 860) {
          const aside = await t.page.locator('.rule-outcome').boundingBox();
          const action = await t.page.locator('.rule-action').boundingBox();
          const clients = await t.page.locator('.rule-clients').boundingBox();
          assert(Math.abs(action.y + action.height - clients.y) <= 1, 'Outcome pane drifted apart');
          assert(aside.y >= (await t.page.locator('.modal-body').boundingBox()).y - 1);
        }
        await shot(t.page, `long-sources-${width}-manual-and-outcome`);
        await act(t, 'save-rule');
        const saved = t.journal.find(entry => entry.key === 'PATCH /api/rules/work').body.community_lists;
        assert.equal(saved.length, 33); assert(saved.includes('source-33')); assert(!saved.includes('source-6'));
        t.assertClean(); cases++;
      } finally { await t.page.close(); }
    }
    for (const domain of ['rule', 'tunnel']) for (const dismissal of ['escape', 'replacement', 'expiry', 'entity']) {
      const t = await appFixture(browser, { screen: domain === 'rule' ? 'rules' : 'tunnels', state: normalState });
      try {
        const id = domain === 'rule' ? 'work' : 'exit';
        await menu(t, domain, id, `delete-${domain}`);
        // Retain the detached control to prove it cannot authorize a later session.
        await t.page.evaluate(() => { window.obsoleteDelete = document.querySelector('[data-confirm-accept]'); });
        if (dismissal === 'escape') await t.page.keyboard.press('Escape');
        else if (dismissal === 'replacement') await t.page.evaluate(async () => {
          const shell = await import('/js/shell.js'); shell.openModal(shell.modalShell('Replacement', '<input value="Retained draft">', ''));
        });
        else if (dismissal === 'expiry') {
          t.fail('GET', '/api/diag', { error: 'Synthetic expiry' }, 401);
          await t.page.evaluate(async () => { try { await (await import('/js/api.js')).diag.get(); } catch {} });
          await t.page.locator('#gate').waitFor({ state: 'visible' }); t.recover('GET', '/api/diag'); await t.login();
        } else await t.page.evaluate(async domain => {
          const { state } = await import('/js/shell.js');
          const key = domain === 'rule' ? 'rules' : 'tunnels'; state[key] = state[key].map(item => ({ ...item }));
        }, domain);
        await t.page.evaluate(() => window.obsoleteDelete.click()); await t.settle();
        assert(!t.journal.some(entry => entry.method === 'DELETE'), `${domain} dispatched after ${dismissal}`);
        if (dismissal === 'replacement') assert.equal(await t.page.locator('#modal input').inputValue(), 'Retained draft');
        t.assertClean(); cases++;
      } finally { await t.page.close(); }
    }
    for (const [operation, id, endpoint, text] of [['check-tunnel', 'exit', '/api/tunnels/exit/check', 'Checking HTTPS'], ['refresh-pool', 'pool', '/api/tunnels/pool/refresh', 'Refreshing catalog']]) {
      const t = await appFixture(browser, { screen: 'tunnels', state: normalState });
      try {
        t.hold('POST', endpoint); await menu(t, 'tunnel', id, operation); await t.waitHeld('POST', endpoint);
        assert.equal(await t.page.locator('#row-menu').count(), 0);
        assert.match(await t.page.locator('.row[aria-busy="true"] [data-operation-status]').innerText(), new RegExp(text));
        await menu(t, 'tunnel', id, operation);
        assert.equal(t.journal.filter(entry => entry.key === `POST ${endpoint}`).length, 1);
        await t.release('POST', endpoint); await t.settle();
        assert.equal(await t.page.locator('[data-operation-status]').count(), 0);
        t.assertClean(); cases++;
      } finally { await t.page.close(); }
    }
    console.log(`Passed ${cases} unified design matrices with actual menu, workspace, settings and confirmation paths`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
