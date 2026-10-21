const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const { appFixture, defaultState } = require('./app.fixture.cjs');
const screenshotDirectory = process.env.SHELL_SCREENSHOT_DIR;
async function capture(page, name) { if (screenshotDirectory) { fs.mkdirSync(screenshotDirectory, { recursive: true }); await page.screenshot({ path: path.join(screenshotDirectory, name + '.png') }); } }
async function geometry(page, width) {
  const data = await page.evaluate(() => {
    const rectangle = element => { const box = element.getBoundingClientRect(); return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width, height: box.height }; };
    return { pageWidth: document.documentElement.scrollWidth, tabs: [...document.querySelectorAll('#tabs .tab')].map(element => ({ ...rectangle(element), clipped: element.scrollWidth > element.clientWidth + 1 })), version: { ...rectangle(document.querySelector('#brand-version')), clipped: document.querySelector('#brand-version').scrollWidth > document.querySelector('#brand-version').clientWidth + 1 }, controls: [...document.querySelectorAll('.topbar .icon-btn')].map(rectangle), apply: rectangle(document.querySelector('#apply-bar')) };
  });
  assert.equal(data.pageWidth, width);
  for (const control of [...data.tabs, ...data.controls]) { assert(control.width >= 43.5 && control.height >= 43.5); assert(control.left >= -1 && control.right <= width + 1); assert.equal(control.clipped || false, false); }
  const controls = [...data.tabs, ...data.controls];
  for (let i = 0; i < controls.length; i++) for (let j = i + 1; j < controls.length; j++) assert(!(Math.min(controls[i].right, controls[j].right) > Math.max(controls[i].left, controls[j].left) + 1 && Math.min(controls[i].bottom, controls[j].bottom) > Math.max(controls[i].top, controls[j].top) + 1), 'Header controls overlap');
  assert.equal(data.version.clipped, false); assert(data.version.left >= -1 && data.version.right <= width + 1);
}
async function enlargeText(page) {
  await page.evaluate(() => {
    const elements = [...document.querySelectorAll('body, body *')];
    const styles = elements.map(element => { const style = getComputedStyle(element); return [element, parseFloat(style.fontSize), parseFloat(style.lineHeight)]; });
    for (const [element, font, line] of styles) { element.style.fontSize = `${font * 2}px`; if (Number.isFinite(line)) element.style.lineHeight = `${line * 2}px`; }
  });
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}
(async () => {
  const browser = await chromium.launch({ headless: true }); let cases = 0;
  try {
    for (const theme of ['light', 'dark']) for (const width of [1440, 1024, 375, 320]) {
      const diagnostic = structuredClone(defaultState.diag);
      diagnostic.checks.push({ id: 'extended', title: 'Extended synthetic result', status: 'unknown', detail: Array.from({ length: 220 }, (_, index) => `Synthetic diagnostic line ${index + 1}: ${'long-unbroken-result'.repeat(3)}`).join('\n') });
      const t = await appFixture(browser, { theme, viewport: { width, height: width < 500 ? 667 : 1000 }, latestRelease: 'v0.11.0', state: { dirty: true, diag: diagnostic } });
      try {
        for (const screen of ['peers', 'tunnels', 'rules', 'diag']) {
          await t.navigate(screen); await t.page.evaluate(() => window.scrollTo(0, 0));
          await geometry(t.page, width); await capture(t.page, `${theme}-${width}-${screen}`);
        }
        await t.page.locator('.diag-check-detail summary').click(); const pre = t.page.locator('.diag-check-detail pre'); await t.page.keyboard.press('Tab');
        assert.equal(await pre.evaluate(element => element === document.activeElement), true);
        await t.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        await pre.evaluate(element => { element.scrollTop = element.scrollHeight; });
        const clearance = await pre.evaluate(element => ({ top: element.getBoundingClientRect().top, bottom: element.getBoundingClientRect().bottom, header: document.querySelector('.topbar').getBoundingClientRect().bottom, apply: document.querySelector('#apply-bar').getBoundingClientRect().top, remaining: element.scrollHeight - element.scrollTop - element.clientHeight }));
        assert(clearance.top >= clearance.header + 10, JSON.stringify(clearance)); assert(clearance.bottom <= clearance.apply - 10, JSON.stringify(clearance)); assert(clearance.remaining <= 1);
        const detailBox = await pre.boundingBox(); assert(detailBox.width <= width - 28); await capture(t.page, `${theme}-${width}-diagnostic-bottom`);
        const pageScrollBeforeToast = await t.page.evaluate(() => scrollY);
        await t.page.evaluate(async () => { const shell = await import('/js/shell.js'); for (let index = 0; index < 30; index++) shell.toast(`Recent synthetic notification ${index}: ${'Long synthetic detail '.repeat(150)}`, index === 29 ? 'err' : undefined); });
        assert.equal(await t.page.locator('.toast').count(), 3);
        assert.equal(await t.page.evaluate(() => scrollY), pageScrollBeforeToast);
        const reveal = await t.page.locator('.toast').last().evaluate(element => { const stack = element.parentElement.getBoundingClientRect(), box = element.getBoundingClientRect(); return { top: box.top, bottom: box.bottom, stackTop: stack.top, stackBottom: stack.bottom }; });
        assert(reveal.top >= reveal.stackTop - 2 && reveal.bottom <= reveal.stackBottom + 2, JSON.stringify(reveal));
        const box = await t.page.locator('#toasts').boundingBox(), apply = await t.page.locator('#apply-bar').boundingBox(); assert(box.y + box.height <= apply.y - 5); assert(box.y >= 0);
        await t.page.locator('.toast-dismiss').nth(1).focus();
        await t.page.evaluate(async () => (await import('/js/shell.js')).toast(`Newest retained-focus error: ${'Long detail '.repeat(200)}`, 'err'));
        const retainedFocus = await t.page.evaluate(() => { const control = document.activeElement, box = control.getBoundingClientRect(), stack = document.querySelector('#toasts').getBoundingClientRect(); return { dismiss: control.matches('.toast-dismiss'), top: box.top, bottom: box.bottom, stackTop: stack.top, stackBottom: stack.bottom }; });
        assert(retainedFocus.dismiss && retainedFocus.top >= retainedFocus.stackTop - 1 && retainedFocus.bottom <= retainedFocus.stackBottom + 1, JSON.stringify(retainedFocus));
        await capture(t.page, `${theme}-${width}-notifications`); await t.page.locator('.toast-dismiss').last().click(); assert.equal(await t.page.locator('.toast').count(), 2);
        await t.page.evaluate(async () => { (await import('/js/shell.js')).clearToasts(); });
        await t.navigate('peers'); await t.page.locator('[data-act="peer-details"]').click(); await t.page.locator('#client-name').waitFor();
        assert.equal(await t.page.locator('#app').evaluate(element => element.inert), true); await t.page.keyboard.press('Tab'); assert.equal(await t.page.evaluate(() => document.querySelector('#modal').contains(document.activeElement)), true); await t.page.keyboard.press('Escape');
        await t.navigate('rules'); await t.page.locator('[data-act="add-rule"]').first().click(); await t.page.locator('.rule-form').waitFor(); await t.page.keyboard.press('Escape');
        await t.page.locator('#btn-settings').click(); await t.page.locator('.settings-hub').waitFor(); await t.settle(); await t.page.keyboard.press('Escape');
        if (width === 320 || width === 1440) {
          await t.navigate('diag'); await t.page.evaluate(() => window.scrollTo(0, 0)); await enlargeText(t.page); await geometry(t.page, width); await capture(t.page, `${theme}-${width}-text200`);
          const summary = t.page.locator('.diag-check-detail summary');
          await summary.evaluate(element => { element.parentElement.open = true; });
          await summary.focus(); await t.page.keyboard.press('Tab');
          const enlargedPre = t.page.locator('.diag-check-detail pre');
          assert.equal(await enlargedPre.evaluate(element => element === document.activeElement), true);
          await t.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
          const enlargedBounds = await enlargedPre.evaluate(element => ({ top: element.getBoundingClientRect().top, bottom: element.getBoundingClientRect().bottom, header: document.querySelector('.topbar').getBoundingClientRect().bottom, apply: document.querySelector('#apply-bar').getBoundingClientRect().top }));
          assert(enlargedBounds.top >= enlargedBounds.header + 10 && enlargedBounds.bottom <= enlargedBounds.apply - 10, JSON.stringify(enlargedBounds));
        }
        t.assertClean(); cases++;
      } finally { await t.page.close(); }
    }
    for (const theme of ['light', 'dark']) for (const dialog of ['client', 'rule', 'settings', 'tunnel']) {
      const t = await appFixture(browser, { theme, viewport: { width: 320, height: 667 } });
      try {
        if (dialog === 'client') await t.page.locator('[data-act="peer-details"]').click();
        else if (dialog === 'rule') { await t.navigate('rules'); await t.page.locator('[data-act="add-rule"]').first().click(); }
        else if (dialog === 'tunnel') { await t.navigate('tunnels'); await t.page.locator('[data-act="add-tunnel"]').click(); }
        else { await t.page.locator('#btn-settings').click(); await t.page.locator('[data-settings-section="backup"]').click(); }
        await t.settle(); await enlargeText(t.page);
        if (dialog === 'tunnel') {
          await t.page.locator('#t-name').focus(); await t.page.keyboard.press('Tab');
          assert.equal(await t.page.locator('#t-raw').evaluate(element => element === document.activeElement), true);
          await t.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
          const bounds = await t.page.locator('#t-raw').evaluate(element => ({ top: element.getBoundingClientRect().top, bottom: element.getBoundingClientRect().bottom, header: document.querySelector('#modal .modal-head').getBoundingClientRect().bottom, footer: document.querySelector('#modal .modal-foot').getBoundingClientRect().top }));
          assert(bounds.top >= bounds.header + 1 && bounds.bottom <= bounds.footer - 1, JSON.stringify(bounds));
          let repeatedTextareaFocus = 0;
          for (let step = 0; step < 20; step++) {
            await t.page.keyboard.press('Tab');
            await t.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
            const focused = await t.page.evaluate(() => { const field = document.activeElement; if (field.id !== 't-raw') return null; const box = field.getBoundingClientRect(); return { top: box.top, bottom: box.bottom, header: document.querySelector('#modal .modal-head').getBoundingClientRect().bottom, footer: document.querySelector('#modal .modal-foot').getBoundingClientRect().top }; });
            if (focused) { repeatedTextareaFocus++; assert(focused.top >= focused.header + 1 && focused.bottom <= focused.footer - 1, JSON.stringify(focused)); }
          }
          assert(repeatedTextareaFocus >= 3);
        }
        const actions = await t.page.locator('#modal .btn:visible').evaluateAll(elements => elements.map(element => { const box = element.getBoundingClientRect(), modal = document.querySelector('#modal').getBoundingClientRect(); return { text: element.textContent, left: box.left, right: box.right, modalLeft: modal.left, modalRight: modal.right, clipped: element.scrollWidth > element.clientWidth + 1 }; }));
        assert(actions.length > 0);
        for (const action of actions) { assert(!action.clipped, JSON.stringify(action)); assert(action.left >= action.modalLeft && action.right <= action.modalRight, JSON.stringify(action)); }
        await capture(t.page, `${theme}-320-${dialog}-actions-text200`);
        t.assertClean(); cases++;
      } finally { await t.page.close(); }
    }
    console.log(`Passed ${cases} shared shell/diagnostic layout matrices across all four screens`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
