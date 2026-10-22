const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { suite, act, menu, capture } = require('./ui-sweep.helper.cjs');
const rule = (id, extra = {}) => ({ id, name: `Example ${id}`, enabled: true, priority: 0, action: 'tunnel', tunnel_id: 'exit', via_tunnel_id: '', peer_scope: 'all', peer_ids: [], community_lists: [], domains: ['example.com'], subnets: [], remote_lists: [], resolve_real_ip: false, ...extra });
(async () => {
  const browser = await chromium.launch({ headless: true }); const tests = suite(browser, 'rule inventory/probe');
  try {
    await tests.check('URL-only duplication preserves managed sources and real-IP policy', async t => {
      await menu(t, 'rule', 'r1', 'duplicate-rule');
      const request = t.journal.find(entry => entry.key === 'POST /api/rules');
      assert.deepEqual(request.body.remote_lists, ['https://lists.example.invalid/source.json']); assert.equal(request.body.resolve_real_ip, true);
      assert(!('id' in request.body)); assert(!('priority' in request.body));
      assert.equal(t.state.rules.length, 2); assert((await t.snapshot()).dirty);
      await menu(t, 'rule', 'r1', 'edit-rule'); assert.match(await t.page.locator('#modal').innerText(), /lists.example.invalid/);
    }, { screen: 'rules', state: { rules: [rule('r1', { domains: [], remote_lists: ['https://lists.example.invalid/source.json'], resolve_real_ip: true })] } });
    for (const action of ['tunnel', 'direct', 'block']) await tests.check(`combined resources and selected clients retain the ${action} payload`, async t => {
      await act(t, 'edit-rule', 'r1');
      await t.page.fill('#r-domains', 'example.com\nwork.example.com'); await t.page.fill('#r-subnets', '198.51.100.0/24');
      await t.page.locator('input[name="scope"][value="selected"]').check();
      await t.page.locator('input[name="peer"][value="example"]').check();
      await t.page.locator(`input[name="action"][value="${action}"]`).check();
      if (action === 'tunnel') {
        await t.page.locator('#r-via-field > summary').click(); await t.page.selectOption('#r-via', 'warp');
        await t.page.fill('#r-subnets', '999.1.1.1/24'); await act(t, 'save-rule');
        assert(!t.journal.some(entry => entry.key === 'PATCH /api/rules/r1')); assert.match(await t.page.locator('#r-error').innerText(), /Invalid subnet lines/);
        await t.page.fill('#r-subnets', '198.51.100.0/24');
      }
      await act(t, 'save-rule');
      const body = t.journal.find(entry => entry.key === 'PATCH /api/rules/r1').body;
      assert.deepEqual(body.domains, ['example.com', 'work.example.com']); assert.deepEqual(body.subnets, ['198.51.100.0/24']); assert.deepEqual(body.community_lists, ['work']);
      assert.equal(body.peer_scope, 'selected'); assert.deepEqual(body.peer_ids, ['example', 'disabled']);
      assert.equal(body.action, action); assert.equal(body.tunnel_id, action === 'tunnel' ? 'exit' : ''); assert.equal(body.via_tunnel_id, action === 'tunnel' ? 'warp' : '');
      assert(!('remote_lists' in body)); assert.deepEqual(t.state.rules[0].remote_lists, ['https://lists.example.invalid/managed.json']);
    }, { screen: 'rules', state: {
      rules: [rule('r1', { community_lists: ['work'], remote_lists: ['https://lists.example.invalid/managed.json'], peer_ids: ['disabled'] })],
      community: [{ key: 'work', title: 'Work resources' }],
      peers: [{ id: 'example', name: 'Example laptop', enabled: true }, { id: 'disabled', name: 'Disabled laptop', enabled: false }],
      tunnels: [{ id: 'exit', name: 'Demo exit', enabled: true }, { id: 'warp', name: 'Demo WARP', source: 'warp', enabled: true }],
    } });
    for (const operation of ['duplicate-rule', 'toggle-rule', 'delete-rule']) await tests.check(`${operation} failure preserves inventory; retry succeeds`, async t => {
      const method = operation === 'duplicate-rule' ? 'POST' : operation === 'delete-rule' ? 'DELETE' : 'PATCH';
      const endpoint = operation === 'duplicate-rule' ? '/api/rules' : '/api/rules/r1';
      t.fail(method, endpoint, { error: 'Synthetic inventory action rejected' }, 422);
      const invoke = async () => { await menu(t, 'rule', 'r1', operation); if (operation === 'delete-rule') { await t.page.locator('[data-confirm-accept]').click(); await t.settle(); } };
      await invoke(); assert.equal(t.state.rules.length, 1); assert.equal(t.state.rules[0].enabled, true); assert.deepEqual((await t.snapshot()).rules.map(rule => ({ id: rule.id, enabled: rule.enabled })), [{ id: 'r1', enabled: true }]); assert.equal(await t.page.locator('[data-act="menu-rule"]').count(), 1); assert.match(await t.page.locator('.row').innerText(), /Enabled/); assert.match((await t.snapshot()).toasts, /Synthetic inventory action rejected/);
      t.recover(method, endpoint); await invoke();
      if (operation === 'delete-rule') assert.equal(t.state.rules.length, 0);
      else if (operation === 'toggle-rule') assert.equal(t.state.rules[0].enabled, false);
      else assert.equal(t.state.rules.length, 2);
      const current = await t.snapshot(); assert(current.dirty); assert.deepEqual(current.rules.map(rule => ({ id: rule.id, enabled: rule.enabled })), t.state.rules.map(rule => ({ id: rule.id, enabled: rule.enabled }))); assert.equal(await t.page.locator('[data-act="menu-rule"]').count(), t.state.rules.length);
    }, { screen: 'rules', state: { rules: [rule('r1')] } });
    await tests.check('delete cancellation never sends DELETE', async t => {
      await menu(t, 'rule', 'r1', 'delete-rule'); assert.equal(await t.page.locator('[data-act="close-modal"][autofocus]').evaluate(el => el === document.activeElement), true); await t.page.locator('.modal-foot [data-act="close-modal"]').click();
      assert(!t.journal.some(entry => entry.method === 'DELETE')); assert.equal(t.state.rules.length, 1);
    }, { screen: 'rules', state: { rules: [rule('r1')] } });
    await tests.check('order boundaries, complete payload and failed-order rollback', async t => {
      await act(t, 'menu-rule', 'r1'); assert(await t.page.locator('#row-menu [data-act="rule-up"]').isDisabled()); await t.page.keyboard.press('Escape'); await act(t, 'menu-rule', 'r2'); assert(await t.page.locator('#row-menu [data-act="rule-down"]').isDisabled()); await t.page.keyboard.press('Escape');
      t.fail('PUT', '/api/rules/order', { error: 'Synthetic order rejected' }, 503); await act(t, 'rule-down', 'r1');
      assert.deepEqual(await t.page.locator('[data-act="menu-rule"]').evaluateAll(els => els.map(el => el.dataset.id)), ['r1', 'r2']);
      t.recover('PUT', '/api/rules/order'); await act(t, 'rule-down', 'r1'); assert.deepEqual(t.state.rules.map(r => r.id), ['r2', 'r1']);
      assert.deepEqual(t.journal.filter(entry => entry.key === 'PUT /api/rules/order').at(-1).body, { ids: ['r2', 'r1'] });
    }, { screen: 'rules', state: { rules: [rule('r1'), rule('r2', { priority: 1 })] } });
    await tests.check('preset opens a draft without persistence; Save alone creates it', async t => {
      await act(t, 'preset-rule'); assert(!t.journal.some(entry => entry.key === 'POST /api/rules'));
      assert.equal(await t.page.locator('input[name="list"]:checked').count(), 1);
      await act(t, 'save-rule'); const body = t.journal.find(entry => entry.key === 'POST /api/rules').body;
      assert.deepEqual(body.community_lists, ['preset']); assert.deepEqual(body.remote_lists, []); assert.equal(body.enabled, true);
    }, { screen: 'rules', state: { rules: [], community: [{ key: 'preset', title: 'Example starter source', in_preset: true }] } });
    await tests.check('empty no-tunnel guidance navigates through the actual router', async t => {
      await act(t, 'go-tunnels'); await t.settle(); assert.equal((await t.snapshot()).screen, 'tunnels');
    }, { screen: 'rules', state: { rules: [], tunnels: [], community: [{ key: 'preset', title: 'Example starter source', in_preset: true }] } });
    await tests.check('both probe buttons validate, serialize busy UI and retain server outcomes', async t => {
      const initial = t.journal.length; await act(t, 'probe-run'); assert(!t.journal.slice(initial).some(entry => entry.method === 'POST'));
      await t.page.locator('#probe-domain').fill('example.com'); t.hold('POST', '/api/route/test'); await act(t, 'probe-run'); await t.waitHeld('POST', '/api/route/test');
      assert(await t.page.locator('[data-act="probe-run"]').isDisabled()); assert(await t.page.locator('[data-act="reach-run"]').isDisabled());
      await t.release('POST', '/api/route/test'); await t.settle(); assert.match(await t.page.locator('.probe-out').innerText(), /Synthetic shared-rule match/);
      assert.deepEqual(t.journal.find(entry => entry.key === 'POST /api/route/test').body, { domain: 'example.com' });
      await act(t, 'reach-run'); assert.match(await t.page.locator('.probe-out').last().innerText(), /Synthetic direct response/);
      for (const [action, endpoint] of [['probe-run', '/api/route/test'], ['reach-run', '/api/domain/reachability']]) {
        t.fail('POST', endpoint, { error: 'Synthetic probe unavailable' }, 503); await act(t, action); assert.match(await t.page.locator('.probe').innerText(), /Synthetic probe unavailable/); t.recover('POST', endpoint);
      }
    }, { screen: 'rules', state: { rules: [rule('r1')] } });
    for (const variant of ['refused', 'unknown', 'geoblock', 'unreachable']) await tests.check(`probe ${variant} result is visible without inferred connectivity`, async t => {
      await t.page.locator('#probe-domain').fill('example.com');
      if (variant === 'refused' || variant === 'unknown') {
        t.respond('POST', '/api/route/test', () => t.json({ domain: 'example.com', refused: variant === 'refused', resolve_error: 'Synthetic resolver refusal', verdict: 'Synthetic uncertain route', addresses: [] })); await act(t, 'probe-run');
        assert.match(await t.page.locator('.probe-out').innerText(), /Synthetic resolver refusal/);
      } else {
        t.respond('POST', '/api/domain/reachability', () => t.json({ domain: 'example.com', class: variant === 'geoblock' ? 'geoblock_suspect' : 'unreachable', status: variant === 'geoblock' ? 451 : 0, verdict: 'Synthetic reachability outcome' })); await act(t, 'reach-run');
        assert.match(await t.page.locator('.probe-out').innerText(), /Synthetic reachability outcome/);
      }
    }, { screen: 'rules' });
    tests.done();
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
