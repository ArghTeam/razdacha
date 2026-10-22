const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { appFixture, root } = require('./app.fixture.cjs');
const destination = process.env.UI_FINAL_SCREENSHOTS;
const sourceHashes = Object.fromEntries(['index.html', 'app.css', ...['app', 'shell', 'api'].map(name => `js/${name}.js`), ...['peers', 'rules', 'tunnels', 'settings', 'diag'].map(name => `js/screens/${name}.js`)].map(file => [file, crypto.createHash('sha256').update(fs.readFileSync(path.join(root, file))).digest('hex')]));
async function capture(page, name, kind = 'normal') {
  if (!destination) return;
  const directory = path.join(destination, kind); fs.mkdirSync(directory, { recursive: true });
  const scene = await page.evaluate(async () => {
    const screen = document.querySelector('#tabs [aria-selected="true"]')?.dataset.screen;
    const heading = document.querySelector('#screen h1')?.textContent.trim();
    const expected = screen ? (await import(`/js/screens/${screen}.js`)).title : null;
    return { screen, heading, matches: heading === expected };
  });
  assert(scene.matches, `Capture heading does not match selected screen: ${scene.screen}`);
  const headingHash = crypto.createHash('sha256').update(scene.heading).digest('hex');
  const filename = name + '.png'; await page.screenshot({ path: path.join(directory, filename), fullPage: false });
  fs.appendFileSync(path.join(destination, 'evidence.jsonl'), JSON.stringify({ file: `${kind}/${filename}`, viewport: page.viewportSize(), screen: scene.screen, headingHash, headingMatchesModuleTitle: scene.matches, sourceHashes }) + '\n');
}
function suite(browser, title) {
  let count = 0;
  return {
    async check(name, run, options = {}) {
      const t = await appFixture(browser, options);
      try { await run(t); t.assertClean(); count++; console.log(`PASS ${title}: ${name}`); }
      finally { await t.page.close(); }
    },
    done() { console.log(`Passed ${count} ${title} cases`); },
  };
}
async function act(t, action, id) {
  const selector = `[data-act="${action}"]${id ? `[data-id="${id}"]` : ''}`;
  if (!await t.page.locator(selector).count()) {
    const domains = { 'peer-details': 'peer', 'peer-configuration': 'peer', 'edit-rule': 'rule', 'toggle-rule': 'rule', 'rule-up': 'rule', 'rule-down': 'rule', 'check-tunnel': 'tunnel', 'refresh-pool': 'tunnel' };
    const domain = domains[action];
    assert(domain, `Missing action ${action}`);
    await t.page.locator(`[data-act="menu-${domain}"]${id ? `[data-id="${id}"]` : ''}`).first().click();
  }
  await t.page.locator(selector).first().click();
  await t.settle();
}
async function menu(t, domain, id, action) {
  await act(t, `menu-${domain}`, id);
  await t.page.locator(`#row-menu [data-act="${action}"]`).click(); await t.settle();
}
async function visibleFocus(page, selector) {
  assert(selector, 'An intended focused control is required');
  await page.waitForFunction(selector => {
    const el = document.activeElement, modal = document.querySelector('#modal');
    if (!modal?.contains(el) || el !== document.querySelector(selector)) return false;
    const rect = el.getBoundingClientRect(), bounds = modal.getBoundingClientRect();
    let top = bounds.top, bottom = bounds.bottom;
    if (el.closest('.modal-body')) {
      top = Math.max(top, modal.querySelector('.modal-head').getBoundingClientRect().bottom);
      bottom = Math.min(bottom, modal.querySelector('.modal-foot').getBoundingClientRect().top);
    }
    for (let parent = el.parentElement; parent && parent !== modal; parent = parent.parentElement) {
      const style = getComputedStyle(parent), box = parent.getBoundingClientRect();
      if (/(auto|scroll|hidden)/.test(style.overflowY)) { top = Math.max(top, box.top); bottom = Math.min(bottom, box.bottom); }
    }
    return rect.width > 0 && rect.height > 0 && rect.top >= top - 1 && rect.bottom <= bottom + 1 && rect.left >= bounds.left - 1 && rect.right <= bounds.right + 1;
  }, selector);
}
async function scrollEnds(page, selector, { requireOverflow = false } = {}) {
  const scrolls = await page.locator(selector).evaluateAll(elements => elements.map(el => {
    el.scrollTop = el.scrollHeight;
    const box = el.getBoundingClientRect(), last = el.lastElementChild?.getBoundingClientRect();
    return { max: el.scrollHeight - el.clientHeight, actual: el.scrollTop, width: box.width, height: box.height, bottom: box.bottom, lastBottom: last?.bottom, overflow: getComputedStyle(el).overflowY };
  }));
  assert(scrolls.length > 0, `No scroll containers matched ${selector}`);
  for (const scroll of scrolls) {
    assert(scroll.width > 0 && scroll.height > 0, `Hidden scroll container: ${selector}`);
    assert(/auto|scroll/.test(scroll.overflow), `Container is not user-scrollable: ${selector}`);
    if (requireOverflow) assert(scroll.max > 0, `Stress container did not overflow: ${selector}`);
    assert(Math.abs(scroll.max - scroll.actual) <= 2, `Scroll end unreachable: ${JSON.stringify(scroll)}`);
    if (scroll.lastBottom !== undefined) assert(scroll.lastBottom <= scroll.bottom + 1, `Last content remains below the scroll end: ${selector}`);
  }
}
module.exports = { suite, act, menu, capture, visibleFocus, scrollEnds, sourceHashes };
