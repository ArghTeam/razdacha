/* Экран «Правила». Порядок проверки — единственная концепция, которую
   пользователь обязан понять, поэтому подпись про него стоит в шапке, а
   перекрытие правил показывается прямо в строке. */

import * as api from '../api.js';
import {
  state, tunnelById, peerById, listTitle, toast, toastError,
  openModal, closeModal, modalShell, openMenu, notImplemented, refresh, markDirty,
} from '../shell.js';
import { $, $$, esc, lines, badLines, plural } from '../util.js';

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

const conditions = (r) => [...(r.community_lists || []), ...(r.domains || []), ...(r.subnets || [])];

/** Правило ниже перекрыто, если правило выше делит с ним хотя бы одно условие.
    Без этого смысл порядка не виден, и пользователь чинит «неработающее» правило. */
function shadowedBy(index) {
  const mine = new Set(conditions(state.rules[index]));
  const hits = [];
  for (let i = 0; i < index; i++) {
    const up = state.rules[i];
    if (!up.enabled) continue;
    const shared = conditions(up).filter((k) => mine.has(k));
    if (shared.length) hits.push({ rule: up, shared });
  }
  return hits;
}

export function view() {
  if (state.missing.has('rules')) {
    return head() + `<div class="card">${notImplemented('Правила')}</div>`;
  }

  const rows = state.rules.map((r, i) => {
    const shadow = shadowedBy(i);
    const shadowed = new Set(shadow.flatMap((h) => h.shared));
    const dest = r.action === 'direct'
      ? '<span class="badge">напрямую</span>'
      : r.action === 'block'
        ? '<span class="badge err">блокировать</span>'
        : `<span class="badge accent">→ ${esc((tunnelById(r.tunnel_id) || {}).name || 'туннель удалён')}</span>`;

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

function modalRule(id) {
  const r = id ? state.rules.find((x) => x.id === id) : {
    id: null, name: '', action: 'tunnel', tunnel_id: (state.tunnels[0] || {}).id || null,
    enabled: true, community_lists: [], domains: [], subnets: [], remote_lists: [],
    peer_scope: 'all', peer_ids: [], resolve_real_ip: false,
  };

  const listsHtml = state.communityLists.length
    ? state.communityLists.map((l) => `
      <label><input type="checkbox" name="list" value="${esc(l.key)}" ${(r.community_lists || []).includes(l.key) ? 'checked' : ''}>
        ${esc(l.title)}</label>`).join('')
    : '<span style="color:var(--fg-faint);font-size:13px">Каталог списков недоступен — задайте домены и подсети вручную.</span>';

  const tunnelOpts = state.tunnels.map((t) =>
    `<option value="${esc(t.id)}" ${r.tunnel_id === t.id ? 'selected' : ''}>${esc(t.name)}</option>`).join('');

  const peersHtml = state.peers.map((p) => `
    <label><input type="checkbox" name="peer" value="${esc(p.id)}" ${(r.peer_ids || []).includes(p.id) ? 'checked' : ''}>
      ${esc(p.name)}</label>`).join('');

  openModal(modalShell(id ? r.name : 'Новое правило', `
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
        <select id="r-tunnel" style="width:auto;min-width:160px" ${r.action === 'tunnel' ? '' : 'disabled'}>${tunnelOpts}</select>
      </div>
    </div>

    <div class="field">
      <label>Готовые списки</label>
      <div class="list-grid">${listsHtml}</div>
    </div>

    <div class="two-col">
      <div class="field">
        <label for="r-domains">Свои домены</label>
        <textarea id="r-domains" spellcheck="false" placeholder="example.com">${esc((r.domains || []).join('\n'))}</textarea>
        <div class="line-errors" id="r-domains-err"></div>
      </div>
      <div class="field">
        <label for="r-subnets">Свои подсети</label>
        <textarea id="r-subnets" spellcheck="false" placeholder="203.0.113.0/24">${esc((r.subnets || []).join('\n'))}</textarea>
        <div class="line-errors" id="r-subnets-err"></div>
      </div>
    </div>

    <div class="field">
      <label>Для кого</label>
      <div class="radios">
        <label class="radio-pill"><input type="radio" name="scope" value="all" ${r.peer_scope === 'selected' ? '' : 'checked'}> все клиенты</label>
        <label class="radio-pill"><input type="radio" name="scope" value="selected" ${r.peer_scope === 'selected' ? 'checked' : ''}> выбранные</label>
      </div>
      <div class="list-grid" id="r-peers" style="margin-top:6px;${r.peer_scope === 'selected' ? '' : 'display:none'}">${peersHtml}</div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-rule" data-id="${esc(id || '')}">Сохранить</button>`),
  (m) => {
    $$('input[name="action"]', m).forEach((el) => el.addEventListener('change', () => {
      m.querySelector('#r-tunnel').disabled =
        m.querySelector('input[name="action"]:checked').value !== 'tunnel';
    }));
    $$('input[name="scope"]', m).forEach((el) => el.addEventListener('change', () => {
      m.querySelector('#r-peers').style.display =
        m.querySelector('input[name="scope"]:checked').value === 'selected' ? '' : 'none';
    }));
    const live = (sel, out, kind, word) => {
      const el = m.querySelector(sel), o = m.querySelector(out);
      const upd = () => {
        const bad = badLines(el.value, kind);
        o.textContent = bad.length
          ? `${plural(bad.length, 'Строка', 'Строки', 'Строки')} ${bad.join(', ')} — не ${word}`
          : '';
      };
      el.addEventListener('input', upd);
      upd();
    };
    live('#r-domains', '#r-domains-err', 'domain', 'домен');
    live('#r-subnets', '#r-subnets-err', 'cidr', 'подсеть');
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

  if (!name) { toast('Введите название правила', 'err'); return; }
  if (badLines(m.querySelector('#r-domains').value, 'domain').length
    || badLines(m.querySelector('#r-subnets').value, 'cidr').length) {
    toast('Исправьте подсвеченные строки', 'err'); return;
  }
  if (action === 'tunnel' && !m.querySelector('#r-tunnel').value) {
    toast('Сначала добавьте туннель', 'err'); return;
  }
  if (scope === 'selected' && !peerIds.length) {
    toast('Выберите хотя бы одного клиента', 'err'); return;
  }
  if (!listsSel.length && !domains.length && !subnets.length) {
    toast('Правило без условий поймало бы весь трафик — добавьте списки, домены или подсети', 'err');
    return;
  }

  const body = {
    name,
    action,
    tunnel_id: action === 'tunnel' ? m.querySelector('#r-tunnel').value : '',
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
