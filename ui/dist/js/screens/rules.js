/* Экран «Правила». Порядок проверки — единственная концепция, которую
   пользователь обязан понять, поэтому подпись про него стоит в шапке, а
   перекрытие правил показывается прямо в строке. */

import * as api from '../api.js';
import {
  state, tunnelById, peerById, listTitle, toast, toastError,
  openModal, closeModal, modalShell, openMenu, notImplemented, refresh, markDirty,
} from '../shell.js';
import { $, $$, esc, lines, badLines, validCidr, plural } from '../util.js';

export const title = 'Правила';

export async function load() {
  state.rules = await api.rules.list();
}

const head = () => `
  <div class="screen-head">
    <h1>Правила</h1>
    <div class="spacer"></div>
    <button class="btn btn-primary" data-act="add-rule">+ Добавить</button>
    <div class="screen-sub">Проверяются сверху вниз, первое совпадение выигрывает.</div>
  </div>`;

const conditions = (r) => ({
  lists: r.community_lists || [],
  domains: r.domains || [],
  subnets: r.subnets || [],
});

/** Домен правила выше забирает мой, если он тот же или мой — его поддомен:
    sing-box сравнивает домены по суффиксу (docs/04-dns-fakeip.md). */
function domainCovers(up, mine) {
  const a = up.replace(/^\*\./, '').toLowerCase();
  const b = mine.replace(/^\*\./, '').toLowerCase();
  return a === b || b.endsWith(`.${a}`);
}

const ipToInt = (s) => s.split('.').reduce((acc, o) => (acc * 256) + Number(o), 0);

/** Подсеть правила выше забирает мою, если моя лежит внутри неё. Голый адрес
    считается /32 — валидатор его допускает. */
function cidrCovers(up, mine) {
  if (!validCidr(up) || !validCidr(mine)) return false;
  const [ua, ub = '32'] = up.split('/');
  const [ma, mb = '32'] = mine.split('/');
  const bits = Number(ub);
  if (bits > Number(mb)) return false;
  if (bits === 0) return true;
  const mask = (0xffffffff << (32 - bits)) >>> 0;
  return ((ipToInt(ua) & mask) >>> 0) === ((ipToInt(ma) & mask) >>> 0);
}

/** Мои условия, которые уже забирает правило выше по порядку. Без этого смысл
    порядка не виден, и пользователь чинит «неработающее» правило. */
function overlap(mine, upper) {
  const hits = [];
  for (const up of upper) {
    if (!up.enabled) continue;
    const u = conditions(up);
    const shared = [
      ...mine.lists.filter((k) => u.lists.includes(k)),
      ...mine.domains.filter((d) => u.domains.some((x) => domainCovers(x, d))),
      ...mine.subnets.filter((n) => u.subnets.some((x) => cidrCovers(x, n))),
    ];
    if (shared.length) hits.push({ rule: up, shared });
  }
  return hits;
}

const shadowedBy = (index) => overlap(conditions(state.rules[index]), state.rules.slice(0, index));

export function view() {
  if (state.missing.has('rules')) {
    return head() + `<div class="card">${notImplemented('Правила')}</div>`;
  }

  const rows = state.rules.map((r, i) => {
    const shadow = shadowedBy(i);
    const shadowed = new Set(shadow.flatMap((h) => h.shared));
    /* Цепь показывается целиком: из имени одного туннеля не видно, сколько за
       правилом прыжков, а второе звено меняет адрес, который увидит ресурс. */
    const via = r.via_tunnel_id
      ? ` → ${esc((tunnelById(r.via_tunnel_id) || {}).name || 'туннель удалён')}`
      : '';
    const dest = r.action === 'direct'
      ? '<span class="badge">напрямую</span>'
      : r.action === 'block'
        ? '<span class="badge err">блокировать</span>'
        : `<span class="badge accent">→ ${esc((tunnelById(r.tunnel_id) || {}).name || 'туннель удалён')}${via}</span>`;

    const chip = (text, key) =>
      `<span class="chip${shadowed.has(key) ? ' shadowed' : ''}">${esc(text)}</span>`;
    const chips = [
      ...(r.community_lists || []).map((k) => chip(listTitle(k), k)),
      ...(r.domains || []).map((d) => chip(d, d)),
      ...(r.subnets || []).map((n) => chip(n, n)),
      ...(r.remote_lists || []).map(() => '<span class="chip">внешний список</span>'),
    ].join('');

    const scope = r.peer_scope === 'selected'
      ? (r.peer_ids || []).map((id) => (peerById(id) || {}).name || '?').join(', ')
      : 'все клиенты';

    const note = shadow.length ? `
      <div class="overlap-note">
        <span>⚠</span>
        <span>перекрыто правилом ${shadow.map((h) => `«${esc(h.rule.name)}»`).join(', ')}
        по ${shadow.flatMap((h) => h.shared).map((k) => esc(listTitle(k))).join(', ')} —
        для этих ресурсов сработает то правило, а не это</span>
      </div>` : '';

    return `
      <div class="row${r.enabled ? '' : ' dim'}">
        <div class="rule-order">
          <button class="ord-btn" data-act="rule-up" data-id="${esc(r.id)}" ${i === 0 ? 'disabled' : ''} aria-label="Выше">▲</button>
          <button class="ord-btn" data-act="rule-down" data-id="${esc(r.id)}" ${i === state.rules.length - 1 ? 'disabled' : ''} aria-label="Ниже">▼</button>
        </div>
        <span class="rule-num">${i + 1}</span>
        <div class="row-main">
          <div class="row-title">${esc(r.name)} ${dest}</div>
          <div class="row-meta">${esc(scope)}${r.resolve_real_ip ? ' · реальный IP' : ''}</div>
          <div class="chips">${chips || '<span class="chip">условий нет — правило не попадёт в конфиг</span>'}</div>
          ${note}
        </div>
        <div class="row-actions">
          <button class="toggle" role="switch" aria-checked="${Boolean(r.enabled)}" data-act="toggle-rule" data-id="${esc(r.id)}" aria-label="Включено"></button>
          <button class="btn btn-sm" data-act="edit-rule" data-id="${esc(r.id)}">Изменить</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-rule" data-id="${esc(r.id)}" aria-label="Ещё">⋮</button>
          </div>
        </div>
      </div>`;
  }).join('');

  const listsNote = state.missing.has('community')
    ? '<p class="screen-sub" style="margin-top:10px">Каталог готовых списков пока недоступен: GET /api/lists/community не реализован. Свои домены и подсети работают.</p>'
    : '';

  return head() + `
    <div class="card">${rows || '<div class="empty">Правил пока нет — весь трафик идёт напрямую.</div>'}</div>
    ${listsNote}`;
}

/* --- форма правила -------------------------------------------------------- */

/** Порог, после которого каталог сервисов без поиска не читается. */
const SEARCH_FROM = 8;

/** Действия объясняются в форме: «напрямую» и «блокировать» путают постоянно. */
const ACTION_HINT = {
  direct: 'Мимо туннелей — трафик уходит напрямую, с IP этого сервера. Так выводят банки и госуслуги из-под более общего правила ниже.',
  tunnel: 'Через выбранный туннель — ресурс видит его адрес, а не адрес сервера.',
  block: 'Соединение отбрасывается: ресурс не открывается вовсе. Это не «напрямую», а «никуда».',
};

const PROBLEM = {
  domain: { word: 'домен', tail: 'Ожидается example.com или *.example.com — без http:// и путей.' },
  cidr: { word: 'подсеть', tail: 'Ожидается 203.0.113.0/24 или один адрес 203.0.113.7.' },
};

/** Правила, которые проверяются раньше этого. Новое встаёт последним. */
function rulesAbove(id) {
  const i = state.rules.findIndex((x) => x.id === id);
  return i < 0 ? state.rules.slice() : state.rules.slice(0, i);
}

function modalRule(id) {
  const r = id ? state.rules.find((x) => x.id === id) : {
    id: null, name: '', action: 'tunnel', tunnel_id: (state.tunnels[0] || {}).id || null,
    via_tunnel_id: '', enabled: true,
    community_lists: [], domains: [], subnets: [], remote_lists: [],
    peer_scope: 'all', peer_ids: [], resolve_real_ip: false,
  };

  const above = rulesAbove(id);
  const pos = above.length + 1;
  const total = id ? state.rules.length : state.rules.length + 1;
  const orderNote = pos === 1
    ? 'Правила проверяются сверху вниз, побеждает первое совпавшее. Это правило первое: до остальных дойдёт только то, что оно не забрало.'
    : `Правила проверяются сверху вниз, побеждает первое совпавшее. Это правило ${pos}-е из ${total}: `
      + `${pos - 1} ${plural(pos - 1, 'правило', 'правила', 'правил')} выше ${plural(pos - 1, 'забирает', 'забирают', 'забирают')} `
      + `свои ресурсы раньше${id ? '' : '; новое встаёт последним, порядок меняется стрелками в списке'}.`;

  const listsHtml = state.communityLists.length
    ? state.communityLists.map((l) => `
      <label class="rule-item" data-title="${esc(String(l.title).toLowerCase())} ${esc(l.key)}">
        <input type="checkbox" name="list" value="${esc(l.key)}" ${(r.community_lists || []).includes(l.key) ? 'checked' : ''}>
        <span class="rule-item-name">${esc(l.title)}</span>
        ${l.has_subnets ? '<span class="rule-mini">подсети</span>' : ''}
      </label>`).join('')
    : '<span class="rule-none">Каталог списков недоступен — задайте домены и подсети вручную.</span>';

  const searchHtml = state.communityLists.length > SEARCH_FROM
    ? `<input type="search" class="rule-search" id="r-list-q" autocomplete="off" spellcheck="false"
         placeholder="Поиск: youtube, telegram…" aria-label="Поиск по спискам">`
    : '';

  const tunnelOpts = state.tunnels.map((t) =>
    `<option value="${esc(t.id)}" ${r.tunnel_id === t.id ? 'selected' : ''}>${esc(t.name)}</option>`).join('');

  /* Второе звено цепи — только WARP (ADR 0012), поэтому в выпадашке лежат
     туннели с source = warp, а не весь инвентарь: выбирать нечего из того,
     что всё равно будет отклонено. Нет WARP — нет и поля. */
  const warps = state.tunnels.filter((t) => t.source === 'warp');
  const viaOpts = ['<option value="">не нужно</option>'].concat(warps.map((t) =>
    `<option value="${esc(t.id)}" ${r.via_tunnel_id === t.id ? 'selected' : ''}>${esc(t.name)}</option>`)).join('');
  const viaHtml = warps.length ? `
          <div class="field rule-via" id="r-via-field">
            <label for="r-via">И дальше через</label>
            <select id="r-via" class="rule-tunnel">${viaOpts}</select>
            <div class="hint">Второе звено: трафик уходит в выбранный туннель, а наружу выходит
              через WARP — ресурс видит адрес Cloudflare, а не адрес туннеля. Дальше цепь не
              продолжается: WARP всегда последний.</div>
          </div>` : '';

  const peersHtml = state.peers.map((p) => `
    <label class="rule-item"><input type="checkbox" name="peer" value="${esc(p.id)}" ${(r.peer_ids || []).includes(p.id) ? 'checked' : ''}>
      <span class="rule-item-name">${esc(p.name)}</span></label>`).join('');

  /* Поток сверху вниз, а не две зоны: каталог из двух десятков сервисов —
     самый широкий блок формы, и в половине окна ему доставались две колонки по
     200 px, в которые не влезает даже среднее название. Во всю ширину их
     четыре. Две колонки остаются только там, где поля правда парные (issue #95). */
  openModal(modalShell(id ? r.name : 'Новое правило', `
    <div class="rule-form">
      <div class="rule-order-note">${esc(orderNote)}</div>

      <div class="rule-scroll">
        <div class="rule-pair">
          <div class="field">
            <label for="r-name">Название</label>
            <input type="text" id="r-name" value="${esc(r.name)}" placeholder="YouTube и Google" autocomplete="off">
          </div>

          <div class="field">
            <label>Куда</label>
            <div class="radios">
              <label class="radio-pill"><input type="radio" name="action" value="direct" ${r.action === 'direct' ? 'checked' : ''}> напрямую</label>
              <label class="radio-pill"><input type="radio" name="action" value="tunnel" ${r.action === 'tunnel' ? 'checked' : ''}> в туннель</label>
              <label class="radio-pill"><input type="radio" name="action" value="block" ${r.action === 'block' ? 'checked' : ''}> блокировать</label>
              <select id="r-tunnel" class="rule-tunnel" ${r.action === 'tunnel' ? '' : 'disabled'}>${tunnelOpts}</select>
            </div>
            <div class="hint" id="r-action-hint"></div>
${viaHtml}
          </div>
        </div>

        <section class="rule-catalog" id="r-catalog">
          <div class="rule-catalog-head">
            <h3 class="rule-zone-head">Готовые списки</h3>
            <span class="rule-catalog-count" id="r-catalog-count"></span>
          </div>
          <div class="rule-picked" id="r-picked" hidden>
            <span class="rule-picked-label">Выбрано:</span>
            <div class="rule-picked-chips" id="r-picked-chips"></div>
          </div>
          <div class="rule-catalog-bar">
            ${searchHtml}
            <span class="hint">Обновляются сами. Пометка «подсети» — есть готовые диапазоны адресов, они ловятся даже без DNS.</span>
          </div>
          <div class="list-grid rule-grid" id="r-lists">${listsHtml}
            <span class="rule-none" id="r-lists-empty" hidden>Ничего не нашлось</span>
          </div>
        </section>

        <div class="rule-pair">
          <div class="field">
            <label for="r-domains">Свои домены</label>
            <textarea id="r-domains" spellcheck="false" placeholder="example.com">${esc((r.domains || []).join('\n'))}</textarea>
            <div class="line-errors" id="r-domains-err"></div>
            <div class="hint">По одному в строке, совпадение по суффиксу: <code>example.com</code> ловит и <code>cdn.example.com</code>.
              Работает через FakeIP — клиент спрашивает домен у DNS сервера и получает подставной адрес, привязанный к этому правилу.</div>
          </div>

          <div class="field">
            <label for="r-subnets">Свои подсети</label>
            <textarea id="r-subnets" spellcheck="false" placeholder="203.0.113.0/24">${esc((r.subnets || []).join('\n'))}</textarea>
            <div class="line-errors" id="r-subnets-err"></div>
            <div class="hint">Тут FakeIP не участвует: клиент идёт сразу на настоящий адрес, спрашивать нечего.
              Такой трафик метится по совпадению с nft-сетом — поэтому подсети ловят и приложения со своим DNS.</div>
          </div>
        </div>

        <div class="field rule-peers-field">
          <label>Для кого</label>
          <div class="radios">
            <label class="radio-pill"><input type="radio" name="scope" value="all" ${r.peer_scope === 'selected' ? '' : 'checked'}> все клиенты</label>
            <label class="radio-pill"><input type="radio" name="scope" value="selected" ${r.peer_scope === 'selected' ? 'checked' : ''}> выбранные</label>
          </div>
          <div class="list-grid rule-grid rule-peers" id="r-peers" ${r.peer_scope === 'selected' ? '' : 'hidden'}>${peersHtml}</div>
        </div>

        <div class="rule-overlap" id="r-overlap" hidden></div>
      </div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-rule" data-id="${esc(id || '')}">Сохранить</button>`),
  (m) => {
    /* Куда: подсказка меняется вместе с выбором — она объясняет именно то
       действие, которое пользователь только что нажал. */
    const viaSel = m.querySelector('#r-via');
    const syncAction = () => {
      const act = m.querySelector('input[name="action"]:checked').value;
      m.querySelector('#r-tunnel').disabled = act !== 'tunnel';
      m.querySelector('#r-action-hint').textContent = ACTION_HINT[act] || '';
      /* «Напрямую» и «блокировать» наружу не выходят — второму звену цепи
         взяться неоткуда, и сервер такое правило отклонит. */
      if (viaSel) {
        m.querySelector('#r-via-field').hidden = act !== 'tunnel';
        viaSel.disabled = act !== 'tunnel';
      }
    };
    $$('input[name="action"]', m).forEach((el) => el.addEventListener('change', syncAction));
    syncAction();

    $$('input[name="scope"]', m).forEach((el) => el.addEventListener('change', () => {
      m.querySelector('#r-peers').hidden =
        m.querySelector('input[name="scope"]:checked').value !== 'selected';
    }));

    const catalogCount = m.querySelector('#r-catalog-count');

    /* Выбранные списки повторяются строкой над каталогом: при поиске
       отмеченное уезжает за фильтр, и без этой строки не видно, что выбрано.
       Строка лежит в потоке каталога и не отжимает сетку: раньше она росла над
       ней и тем сильнее, чем активнее пользовались (issue #95). */
    const picked = m.querySelector('#r-picked');
    const pickedChips = m.querySelector('#r-picked-chips');
    const renderPicked = () => {
      const keys = $$('input[name="list"]:checked', m).map((e) => e.value);
      picked.hidden = !keys.length;
      pickedChips.innerHTML = keys.map((k) =>
        `<button type="button" class="rule-chip" data-key="${esc(k)}" title="Убрать">${esc(listTitle(k))} ✕</button>`).join('');
      catalogCount.textContent = keys.length
        ? `выбрано ${keys.length} из ${state.communityLists.length}`
        : '';
    };
    picked.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-key]');
      if (!btn) return;
      const box = m.querySelector(`input[name="list"][value="${CSS.escape(btn.dataset.key)}"]`);
      if (box) box.checked = false;
      renderPicked();
      renderOverlap();
    });

    const q = m.querySelector('#r-list-q');
    if (q) {
      q.addEventListener('input', () => {
        const needle = q.value.trim().toLowerCase();
        let shown = 0;
        $$('#r-lists .rule-item', m).forEach((el) => {
          const hit = !needle || el.dataset.title.includes(needle);
          el.hidden = !hit;
          if (hit) shown++;
        });
        m.querySelector('#r-lists-empty').hidden = shown > 0;
      });
    }

    /* Перекрытие считается по тому, что в форме сейчас, а не по сохранённому:
       иначе пользователь узнаёт о конфликте только после «Сохранить». */
    const box = m.querySelector('#r-overlap');
    function renderOverlap() {
      const mine = {
        lists: $$('input[name="list"]:checked', m).map((e) => e.value),
        domains: lines(m.querySelector('#r-domains').value),
        subnets: lines(m.querySelector('#r-subnets').value),
      };
      const hits = overlap(mine, above);
      box.hidden = !hits.length;
      if (!hits.length) return;
      box.innerHTML = '<strong>Это уже забирают правила выше</strong>'
        + hits.map((h) => `<div class="rule-overlap-line">«${esc(h.rule.name)}» — ${
          h.shared.map((k) => esc(listTitle(k))).join(', ')}</div>`).join('')
        + '<div class="rule-overlap-tail">Для этих ресурсов сработает правило выше, а не это. '
        + 'Либо уберите пересечение, либо поднимите правило стрелками в списке.</div>';
    }

    $$('input[name="list"]', m).forEach((el) => el.addEventListener('change', () => {
      renderPicked();
      renderOverlap();
    }));

    const live = (sel, out, kind) => {
      const el = m.querySelector(sel), o = m.querySelector(out);
      const upd = () => {
        const all = el.value.split('\n');
        const bad = badLines(el.value, kind);
        el.classList.toggle('bad', bad.length > 0);
        o.innerHTML = bad.length
          ? bad.slice(0, 3).map((n) =>
            `<div>Строка ${n}: «${esc(all[n - 1].trim())}» — не ${PROBLEM[kind].word}</div>`).join('')
            + (bad.length > 3 ? `<div>…и ещё ${bad.length - 3}</div>` : '')
            + `<div class="rule-overlap-tail">${PROBLEM[kind].tail}</div>`
          : '';
        renderOverlap();
      };
      el.addEventListener('input', upd);
      upd();
    };
    live('#r-domains', '#r-domains-err', 'domain');
    live('#r-subnets', '#r-subnets-err', 'cidr');

    renderPicked();
    renderOverlap();
    m.querySelector('#r-name').focus();
  });
}

async function saveRule(id) {
  const m = $('#modal');
  const name = m.querySelector('#r-name').value.trim();
  const action = m.querySelector('input[name="action"]:checked').value;
  const scope = m.querySelector('input[name="scope"]:checked').value;
  const domains = lines(m.querySelector('#r-domains').value);
  const subnets = lines(m.querySelector('#r-subnets').value);
  const listsSel = $$('input[name="list"]:checked', m).map((e) => e.value);
  const peerIds = $$('input[name="peer"]:checked', m).map((e) => e.value);

  if (!name) { toast('Введите название правила', 'err'); m.querySelector('#r-name').focus(); return; }

  /* Молча выбросить непонятую строку нельзя: пользователь решит, что правило её
     учитывает. Поэтому сохранение упирается в поле, где ошибка. */
  const badDomains = badLines(m.querySelector('#r-domains').value, 'domain');
  const badSubnets = badLines(m.querySelector('#r-subnets').value, 'cidr');
  if (badDomains.length || badSubnets.length) {
    const field = badDomains.length ? '#r-domains' : '#r-subnets';
    const nums = badDomains.length ? badDomains : badSubnets;
    const what = badDomains.length ? 'в доменах' : 'в подсетях';
    toast(`Не разобрана ${plural(nums.length, 'строка', 'строки', 'строк')} ${nums.join(', ')} ${what} — исправьте или удалите`, 'err');
    m.querySelector(field).focus();
    return;
  }
  if (action === 'tunnel' && !m.querySelector('#r-tunnel').value) {
    toast('Сначала добавьте туннель', 'err'); return;
  }
  if (scope === 'selected' && !peerIds.length) {
    toast('Выберите хотя бы одного клиента', 'err'); return;
  }
  /* Списков по ссылке в форме нет — их задают через API, — но условием совпадения
     они считаются наравне с остальным (store.Rule.validate, #142). Без этой
     поправки правило, живущее на remote_lists, нельзя было бы отредактировать в
     панели: сохранение упиралось бы в проверку строже серверной. */
  const remote = (id ? (state.rules.find((x) => x.id === id) || {}).remote_lists : null) || [];
  if (!listsSel.length && !domains.length && !subnets.length && !remote.length) {
    toast('Правило без условий поймало бы весь трафик — добавьте списки, домены или подсети', 'err');
    return;
  }

  const via = m.querySelector('#r-via');
  const body = {
    name,
    action,
    tunnel_id: action === 'tunnel' ? m.querySelector('#r-tunnel').value : '',
    // Пустая строка снимает второе звено: правило возвращается к одному туннелю.
    via_tunnel_id: action === 'tunnel' && via ? via.value : '',
    community_lists: listsSel,
    domains,
    subnets,
    peer_scope: scope,
    peer_ids: scope === 'selected' ? peerIds : [],
  };

  const btn = m.querySelector('[data-act="save-rule"]');
  btn.disabled = true;
  try {
    if (id) await api.rules.update(id, body);
    else await api.rules.create({ enabled: true, remote_lists: [], resolve_real_ip: false, ...body });
    closeModal();
    markDirty();
    state.rules = await api.rules.list();
    refresh();
    toast(`Правило «${name}» сохранено`);
  } catch (err) {
    btn.disabled = false;
    toastError(err, 'Правило не сохранено');
  }
}

/** Порядок отправляется целиком: атомарно и без промежуточных дублей приоритета. */
async function move(id, delta) {
  const i = state.rules.findIndex((r) => r.id === id);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= state.rules.length) return;
  const before = state.rules.slice();
  [state.rules[i], state.rules[j]] = [state.rules[j], state.rules[i]];
  refresh();
  try {
    await api.rules.reorder(state.rules.map((r) => r.id));
    markDirty();
  } catch (err) {
    state.rules = before;
    refresh();
    toastError(err, 'Порядок не изменён');
  }
}

export const actions = {
  'add-rule': () => modalRule(null),
  'edit-rule': (id) => modalRule(id),
  'save-rule': (id) => saveRule(id || null),
  'rule-up': (id) => move(id, -1),
  'rule-down': (id) => move(id, 1),

  'toggle-rule': async (id) => {
    const r = state.rules.find((x) => x.id === id);
    const enabled = !r.enabled;
    try {
      await api.rules.update(id, { enabled });
      r.enabled = enabled;
      markDirty();
      refresh();
    } catch (err) { toastError(err, 'Не переключено'); }
  },

  'menu-rule': (id, btn) => openMenu(btn, [
    { act: 'edit-rule', id, label: 'Изменить' },
    { act: 'duplicate-rule', id, label: 'Дублировать' },
    { act: 'delete-rule', id, label: 'Удалить', danger: true },
  ]),

  'duplicate-rule': async (id) => {
    const r = state.rules.find((x) => x.id === id);
    const copy = { ...r, name: `${r.name} (копия)` };
    delete copy.id;
    delete copy.priority;
    try {
      await api.rules.create(copy);
      markDirty();
      state.rules = await api.rules.list();
      refresh();
      toast('Правило продублировано');
    } catch (err) { toastError(err, 'Не продублировано'); }
  },

  'delete-rule': async (id) => {
    const r = state.rules.find((x) => x.id === id);
    if (!confirm(`Удалить правило «${r.name}»?`)) return;
    try {
      await api.rules.remove(id);
      state.rules = state.rules.filter((x) => x.id !== id);
      markDirty();
      refresh();
      toast('Правило удалено');
    } catch (err) { toastError(err, 'Не удалено'); }
  },
};
