// Optional artifacts: RULE_SCREENSHOT_DIR=<temporary directory> node ui/tests/rule-layout.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { chromium } = require('playwright');
const { startServer, fixture } = require('./rule.fixture.cjs');
const destination = process.env.RULE_SCREENSHOT_DIR;
async function shot(page, name) {
  if (!destination) return;
  await fs.mkdir(destination, { recursive: true });
  await page.screenshot({ path: path.join(destination, `${name}.png`) });
}
async function focusGeometry(page) {
  const focus = await page.evaluate(() => {
    const element = document.activeElement, modal = document.querySelector('#modal'), rectangle = element.getBoundingClientRect();
    const bounds = { left: 0, top: 0, right: innerWidth, bottom: innerHeight };
    for (let parent = element.parentElement; parent; parent = parent.parentElement) {
      const style = getComputedStyle(parent), r = parent.getBoundingClientRect();
      if (/(auto|scroll|hidden)/.test(style.overflowY)) { bounds.top = Math.max(bounds.top, r.top); bounds.bottom = Math.min(bounds.bottom, r.bottom); }
      if (/(auto|scroll|hidden)/.test(style.overflowX)) { bounds.left = Math.max(bounds.left, r.left); bounds.right = Math.min(bounds.right, r.right); }
    }
    return { inside: modal.contains(element), id: element.id, tag: element.tagName, left: rectangle.left, right: rectangle.right, top: rectangle.top, bottom: rectangle.bottom, bounds };
  });
  assert(focus.inside, `Focus escaped: ${JSON.stringify(focus)}`);
  assert(focus.left >= focus.bounds.left - 1 && focus.right <= focus.bounds.right + 1, `Horizontal focus clipping: ${JSON.stringify(focus)}`);
  assert(focus.top >= focus.bounds.top - 1 && focus.bottom <= focus.bounds.bottom + 1, `Vertical focus clipping: ${JSON.stringify(focus)}`);
}
async function checkFrame(page, width) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth), width, 'Document overflow');
  const modal = await page.locator('#modal').boundingBox(), footer = await page.locator('.modal-foot').boundingBox();
  assert(modal.x >= 0 && modal.x + modal.width <= width + 1);
  assert(footer.y >= modal.y && footer.y + footer.height <= modal.y + modal.height + 1, 'Footer escaped modal');
  const save = await page.locator('[data-act="save-rule"]').boundingBox();
  assert(save.y + save.height <= modal.y + modal.height + 1, 'Save is clipped');
  const body = await page.locator('.modal-body').boundingBox();
  assert(body.height >= 100, 'Primary scroller is unusably short');
}
async function doubleText(page) {
  await page.evaluate(() => {
    const sizes = [...document.querySelectorAll('#modal, #modal *')].map(element => { const style = getComputedStyle(element); return [element, parseFloat(style.fontSize), parseFloat(style.lineHeight)]; });
    for (const [element, font, line] of sizes) { element.style.fontSize = `${font * 2}px`; if (Number.isFinite(line)) element.style.lineHeight = `${line * 2}px`; }
  });
}
(async () => {
  const { server, url } = await startServer();
  const browser = await chromium.launch({ headless: true });
  let cases = 0;
  try {
    for (const theme of ['light', 'dark']) for (const width of [1440, 1024, 375, 320]) {
      const t = await fixture(browser, url, { theme, width });
      try {
        const { page } = t;
        assert.deepEqual(await page.locator('.rule-section > h3').allTextContents(), ['Name', 'Resources', 'Action', 'Clients']);
        assert.equal(await page.locator('#r-picked').getAttribute('open'), null, 'Selected sources start compact');
        assert.equal(await page.locator('#r-catalog').getAttribute('open'), null, 'Catalog starts compact');
        assert.match(await page.locator('#r-peer-count').innerText(), /24 selected.*1 overridden.*1 disabled/);
        assert.match(await page.locator('#r-peers .rule-item').first().innerText(), /LongUnbrokenClientIdentity/);
        const name = page.locator('#r-peers .rule-item-name').first();
        assert.equal(await name.evaluate(element => element.scrollWidth <= element.clientWidth + 1), true, 'Client name is clipped');
        await checkFrame(page, width); await shot(page, `${theme}-${width}-top`);
        await page.locator('#r-peer-q').fill('Example device 24');
        assert.equal(await page.locator('#r-peers .rule-item:visible').count(), 1);
        await page.locator('#r-peer-q').fill('unmatched'); assert(await page.locator('#r-peers-empty').isVisible());
        await page.locator('#r-peer-q').fill('');
        await page.locator('#r-peer-q').fill('Example device 24'); await page.locator('#r-peers input').last().focus(); await focusGeometry(page);
        await page.locator('#r-picked > summary').click();
        assert.equal(await page.locator('#r-picked-chips button').count(), 36);
        while (await page.getByRole('button', { name: 'Next selected sources page', exact: true }).isEnabled()) await page.getByRole('button', { name: 'Next selected sources page', exact: true }).click(); await page.locator('#r-picked-chips button').last().focus(); await focusGeometry(page); await shot(page, `${theme}-${width}-selection-end`);
        await page.locator('#r-picked > summary').click();
        await page.locator('#r-catalog > summary').click(); await page.locator('#r-list-q').fill('source 36');
        assert.equal(await page.locator('#r-lists .rule-item:visible').count(), 1);
        await page.locator('#r-list-q').fill('source 36'); await page.locator('#r-lists input').last().focus(); await focusGeometry(page);
        await page.locator('#r-catalog > summary').click();
        const manual = page.locator('#r-domains');
        assert.equal((await manual.inputValue()).split('\n').length, 39);
        await manual.evaluate(element => { element.scrollTop = element.scrollHeight; });
        assert(await manual.evaluate(element => element.scrollTop + element.clientHeight >= element.scrollHeight - 1));
        await page.locator('#r-tunnel').focus(); await focusGeometry(page); await shot(page, `${theme}-${width}-action`);
        await page.locator('#r-via-field > summary').click(); assert(await page.locator('#r-via').isVisible());
        await page.locator('#r-via-field > summary').click();
        await page.locator('#modal h2').focus();
        for (let i = 0; i < 43; i++) { await page.keyboard.press('Tab'); await focusGeometry(page); }
        for (const selector of ['.rule-choices label', '.rule-disclosure > summary', '.rule-item:visible', '.modal-foot .btn']) {
          const sizes = await page.locator(selector).evaluateAll(elements => elements.map(element => { const r = element.getBoundingClientRect(); return [r.width, r.height]; }));
          for (const [w, h] of sizes) { assert(w >= (width < 761 ? 43.5 : 39.5), `${selector} narrow hit area`); assert(h >= (width < 761 ? 43.5 : 39.5), `${selector} short hit area`); }
        }
        await doubleText(page); await checkFrame(page, width);
        await page.locator('#r-tunnel').focus(); await focusGeometry(page); await shot(page, `${theme}-${width}-text200-action`);
        await page.locator('#r-name').focus(); await focusGeometry(page); await shot(page, `${theme}-${width}-text200-top`);
        assert.deepEqual(t.errors, []); cases++;
      } finally { await t.context.close(); }
    }
    for (const scenario of ['empty', 'unavailable', 'url-only', 'orphan', 'chain', 'missing-chain']) {
      const t = await fixture(browser, url, { width: 375, height: 667, scenario });
      try {
        const { page } = t; await checkFrame(page, 375);
        if (scenario === 'empty') {
          await page.locator('input[name="scope"][value="selected"]').check(); assert.match(await page.locator('#r-peers-empty').innerText(), /No clients available/);
          await page.locator('#r-catalog > summary').click(); assert.match(await page.locator('#r-catalog').innerText(), /catalog unavailable/i);
        }
        if (scenario === 'unavailable') {
          assert.match(await page.locator('#r-unavailable-note').innerText(), /Retained until explicitly removed/);
          await page.locator('#r-picked > summary').click(); assert.match(await page.locator('#r-picked').innerText(), /retained-source.*catalog unavailable/s);
          await page.locator('#r-picked-chips button').click(); assert.equal(await page.locator('input[name="list"]:checked').count(), 0); await focusGeometry(page);
        }
        if (scenario === 'url-only') {
          assert.equal(await page.locator('.rule-remote li').count(), 2); assert.equal(await page.locator('.rule-remote a, .rule-remote img').count(), 0);
          await page.locator('.rule-remote').scrollIntoViewIfNeeded(); assert.match(await page.locator('.rule-remote').innerText(), /<escaped-source>/);
        }
        if (scenario === 'orphan') assert.match(await page.locator('.rule-status:visible').innerText(), /Assignment keeps this rule disabled/);
        if (scenario.includes('chain')) { assert.equal(await page.locator('#r-via-field').getAttribute('open'), ''); await page.locator('#r-via').focus(); await focusGeometry(page); }
        await shot(page, `state-${scenario}`); assert.deepEqual(t.errors, []); cases++;
      } finally { await t.context.close(); }
    }
    const failure = await fixture(browser, url, { width: 320, height: 667, scenario: 'url-only' });
    try {
      await failure.page.route('**/api/rules/r1', route => route.fulfill({ status: 422, json: { error: 'Synthetic save failure. Correct the source and retry.' } }));
      await failure.page.locator('[data-act="save-rule"]').click();
      await failure.page.locator('#r-error').waitFor({ state: 'visible' });
      assert.match(await failure.page.locator('#r-error').innerText(), /Your draft is preserved/);
      await shot(failure.page, 'state-save-error'); await checkFrame(failure.page, 320); cases++;
    } finally { await failure.context.close(); }
    console.log(`Passed ${cases} Chromium rule layout/state matrices, including eight theme/viewport/text-size matrices`);
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(error => { console.error(error); process.exitCode = 1; });
