// Optional screenshots: CLIENT_SCREENSHOT_DIR=/path node ui/tests/client-layout.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');
(async () => {
  const browser = await chromium.launch({ headless: true });
  let count = 0;
  const destination = process.env.CLIENT_SCREENSHOT_DIR;
  if (destination) fs.mkdirSync(destination, { recursive: true });
  async function shot(page, name) { if (destination) await page.screenshot({ path: path.join(destination, name + '.png') }); }
  try {
    for (const theme of ['light', 'dark']) for (const width of [1440, 1024, 375, 320]) {
      const t = await fixture(browser, { theme, viewport: { width, height: width < 500 ? 844 : 1000 }, peers: [{ ...examplePeer, name: 'ExampleLongUnbrokenDeviceName'.repeat(7), egress_tunnel_id: 'disabled' }] });
      try {
        assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width); await shot(t.page, `${theme}-${width}-inventory`);
        await t.open(); await t.page.waitForFunction(() => !document.querySelector('#client-tunnels-status').textContent.includes('Loading'));
        await shot(t.page, `${theme}-${width}-details`); await t.readyConfig();
        await t.page.locator('.client-config-text > summary').click(); await t.page.locator('[data-act="download-conf"]').scrollIntoViewIfNeeded(); await shot(t.page, `${theme}-${width}-configuration`);
        await t.page.locator('[data-act="inspect-client-delete"]').click(); await t.page.locator('#client-delete-confirmation').waitFor({ state: 'visible' }); await t.page.locator('[data-act="confirm-client-delete"]').scrollIntoViewIfNeeded(); await shot(t.page, `${theme}-${width}-delete`);
        const bounds = await t.page.locator('#modal').boundingBox(); assert(bounds.x >= 0 && bounds.x + bounds.width <= width + 1);
        for (const selector of ['.client-details .btn:visible', '.client-details summary:visible', '.client-routing-choice', '.client-enabled']) {
          const boxes = await t.page.locator(selector).evaluateAll(elements => elements.map(element => { const r = element.getBoundingClientRect(); return { width: r.width, height: r.height }; }));
          for (const box of boxes) { assert(box.height >= (width < 761 ? 43.5 : 39.5), `${selector} hit area ${JSON.stringify(box)}`); assert(box.width >= (width < 761 ? 43.5 : 39.5)); }
        }
        await t.page.locator('[data-act="cancel-client-delete"]').click();
        await t.page.locator('#modal h2').focus();
        for (let i = 0; i < 24; i++) {
          await t.page.keyboard.press('Tab');
          const focus = await t.page.evaluate(() => {
            const element = document.activeElement, modal = document.querySelector('#modal'), r = element.getBoundingClientRect();
            const container = element.closest('.modal-body') || modal, c = container.getBoundingClientRect();
            return { tag: element.tagName, inside: modal.contains(element), left: r.left, right: r.right, top: r.top, bottom: r.bottom, c: { left: c.left, right: c.right, top: c.top, bottom: c.bottom } };
          });
          assert(focus.inside, 'Keyboard focus escaped the dialog');
          assert(focus.left >= focus.c.left - 1 && focus.right <= focus.c.right + 1, `Horizontal focus clipping: ${JSON.stringify(focus)}`);
          assert(focus.top >= focus.c.top - 1 && focus.bottom <= focus.c.bottom + 1, `Vertical focus clipping: ${JSON.stringify(focus)}`);
        }
        if (width === 320 || width === 1440) {
          await t.page.evaluate(() => {
            const elements = [...document.querySelectorAll('#modal, #modal *')];
            const sizes = elements.map(element => { const style = getComputedStyle(element); return [element, parseFloat(style.fontSize), parseFloat(style.lineHeight)]; });
            for (const [element, font, line] of sizes) { element.style.fontSize = `${font * 2}px`; if (Number.isFinite(line)) element.style.lineHeight = `${line * 2}px`; }
          });
          await t.page.locator('[data-act="download-conf"]').scrollIntoViewIfNeeded(); await shot(t.page, `${theme}-${width}-text200-configuration`);
          assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width);
          const footer = await t.page.locator('.modal-foot').boundingBox(), modal = await t.page.locator('#modal').boundingBox();
          assert(footer.y >= modal.y && footer.y + footer.height <= modal.y + modal.height + 1);
          const save = await t.page.locator('[data-act="save-client-details"]').boundingBox(); assert(save.y + save.height <= modal.y + modal.height + 1);
        }
        count++;
      } finally { await t.page.close(); }
    }
    const created = await fixture(browser, { viewport: { width: 375, height: 667 } });
    try {
      await created.invoke('add-peer'); await created.page.fill('#peer-name', 'Created example'); await created.invoke('create-peer'); await created.done();
      await created.page.locator('#client-config-result').waitFor({ state: 'visible' });
      const cue = await created.page.locator('#client-configuration > summary').boundingBox(), body = await created.page.locator('#modal .modal-body').boundingBox();
      assert(cue.y >= body.y - 1 && cue.y + cue.height <= body.y + body.height + 1, 'Creation did not reveal configuration');
      assert.equal(await created.page.evaluate(() => document.activeElement === document.querySelector('#client-configuration > summary')), true);
      await shot(created.page, 'light-375-created-configuration');
      await created.page.fill('#client-name', 'Name draft'); await created.page.locator('[data-act="download-conf"]').scrollIntoViewIfNeeded(); await shot(created.page, 'light-375-name-draft-delivery');
    } finally { await created.page.close(); }
    console.log(`Passed ${count} Chromium client layout/theme matrices with focus and hit-area geometry`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
