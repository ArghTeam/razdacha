/* Экран «Правила». Порядок проверки — единственная концепция, которую
   пользователь обязан понять, поэтому подпись про него стоит в шапке, а
   перекрытие правил показывается прямо в строке. */

import * as api from '../api.js';
import {
  state, tunnelById, peerById, listTitle, toast, toastError,
  openModal, closeModal, modalShell, openMenu, notImplemented, refresh, markDirty,
  currentModalRevision, isCurrentModal,
} from '../shell.js';
import {
  $, $$, esc, lines, badLines, validCidr, plural, since, stamp,
} from '../util.js';

export const title = 'Правила';

let rulesRevision = 0;
export async function load() {
  const current = api.session.guard(), revision = ++rulesRevision;
  try {
    const rules = await api.rules.list();
    if (current() && revision === rulesRevision) state.rules = rules;
  } catch (err) {
    if (current() && revision === rulesRevision) throw err;
  }
}

const head = () => `
  <div class="screen-head">
    <h1>Правила</h1>
    <div class="spacer"></div>
    <button class="btn btn-primary" data-act="add-rule">+ Добавить</button>
    <div class="screen-sub">Проверяются сверху вниз, первое совпадение выигрывает.</div>
  </div>`;

/* --- мёртвая цель ---------------------------------------------------------
   После ADR 0013 правило с недоступным туннелем не исчезает и не течёт — оно
   отказывает. Отказ обязан быть виден в строке: снаружи «туннель выключен» и
   «всё работает» выглядят одинаково — ресурс просто не открывается.

   Состояний строки три, и схлопывать их нельзя. Кроме отказа есть случай
   слабее: туннель включён и применён, а последняя проверка вернула `down`.
   Правило при этом работает — оно остаётся в конфиге обычным маршрутом и
   отправляет трафик, тот просто не доходит. `reject` не наступает, и фраза
   «трафик отбрасывается» здесь была бы ложью (issue #152). */

/** Почему туннель не довезёт трафик. Пустая строка — довезёт.
    Ложных срабатываний быть не должно: `servers_alive` у пула бывает null,
    когда Clash API недоступен, и это «не знаем», а не «нет живых». */
function tunnelTrouble(t) {
  if (!t) return 'туннель удалён';
  if (!t.enabled) return 'туннель выключен';
  const pool = t.pool;
  if (pool && (pool.servers_total === 0 || pool.servers_alive === 0)) return 'Pool has no usable servers';
  return '';
}

/** Туннель включён, но последняя проверка до цели не дошла. Возвращает время
    последнего удачного ответа (`last_ok`, может быть пустым) либо null, если
    молчания нет. `status: null` — «не проверяли», а не «не отвечает»: признаком
    он не является, ровно как `servers_alive: null` у пула. */
function tunnelSilent(t) {
  if (!t || !t.enabled || t.status !== 'down') return null;
  return { okAt: t.last_ok || '' };
}

/** Что не так с целью правила целиком, включая второе звено цепи: выключенное
    второе звено роняет правило так же, как выключенный первый туннель.
    Возвращает null либо `{ level, text, okAt }`, где level — `dead` (трафик
    отбрасывается) или `silent` (канал не отвечает).

    Отказ ищется по всей цепи раньше молчания: если первое звено недоступно, до
    второго трафик не дойдёт вовсе, и говорить про его канал нечего. */
function ruleTrouble(r) {
  if (r.action !== 'tunnel') return null;
  const links = [{
    t: tunnelById(r.tunnel_id),
    dead: (why) => why,
    silent: 'Канал не отвечает',
  }];
  if (r.via_tunnel_id) {
    links.push({
      t: tunnelById(r.via_tunnel_id),
      dead: (why) => `второе звено цепи: ${why}`,
      silent: 'Канал второго звена цепи не отвечает',
    });
  }
  for (const link of links) {
    const why = tunnelTrouble(link.t);
    if (why) return { level: 'dead', text: link.dead(why), okAt: '' };
  }
  for (const link of links) {
    const silent = tunnelSilent(link.t);
    if (silent) return { level: 'silent', text: link.silent, okAt: silent.okAt };
  }
  return null;
}

/** Подпись под строкой правила: почему оно не работает или работает вхолостую.
    Тексты разные не для красоты — «отбрасывается» и «уходит и теряется» это
    разные вещи, и путать их значит врать про утечку. */
function troubleNote(trouble) {
  if (!trouble) return '';
  if (trouble.level === 'dead') {
    return `
      <div class="rule-dead-note">
        <span>⚠</span>
        <span>Правило не работает: ${esc(trouble.text)}. Трафик по нему отбрасывается,
        а не уходит напрямую — так утечка не подменяет туннель (ADR 0013).</span>
      </div>`;
  }
  /* Времени нет — так и пишем: выдуманная дата хуже её отсутствия. Пустым
     `last_ok` выглядит и туннель, который не отвечал ни разу. */
  const when = trouble.okAt
    ? `Последний раз он отвечал ${esc(stamp(trouble.okAt))} — ${esc(since(trouble.okAt))}.`
    : 'Когда он отвечал в последний раз, сказать нечем: удачных проверок по нему не записано.';
  return `
    <div class="rule-quiet-note">
      <span>◌</span>
      <span>${esc(trouble.text)}: последняя проверка туннеля до цели не дошла. ${when}
      Правило при этом работает — трафик уходит в туннель и теряется уже там,
      а не идёт напрямую и не отбрасывается.</span>
    </div>`;
}

/* --- состояние списков ----------------------------------------------------
   Список, который не обновился, тихо перестаёт ловить домены, и в панели это
   было неотличимо от рабочего состояния. Состояний четыре, и схлопывать их
   нельзя: «обновился», «не обновился с ошибкой», «ни разу не обновлялся» и
   «этот список ведёт сам sing-box, демон его не качает». */
const LIST_STATE = {
  updated: { word: '', cls: '' },
  failed: { word: 'не обновился', cls: 'list-bad' },
  never: { word: 'ни разу не обновлялся', cls: 'list-cold' },
  unknown: { word: 'состояние неизвестно', cls: 'list-cold' },
  core: { word: '', cls: '' },
};

/** Состояния списков правила по ключу и по адресу — их отдаёт `GET /api/rules`
    полем `lists_status`. Старый демон поля не отдаёт вовсе: тогда состояний нет
    и чипы выглядят как раньше, без выдуманной зелёной галочки. */
function listStates(r) {
  const out = new Map();
  for (const st of r.lists_status || []) out.set(st.key || st.url, st);
  return out;
}

/** Подпись состояния для чипа: коротко и без наведения. */
function listMark(st) {
  if (!st) return '';
  if (st.state === 'updated') return `обновлён ${since(st.updated_at)}`;
  return LIST_STATE[st.state] ? LIST_STATE[st.state].word : '';
}

/** Всплывающая подсказка: подробности, включая текст ошибки источника. */
function listHint(st, name) {
  if (!st) return '';
  switch (st.state) {
    case 'updated':
      return `${name}: подсети обновлены ${stamp(st.updated_at)}`;
    case 'failed':
      return `${name}: последняя попытка обновления не удалась — ${st.error}`
        + (st.updated_at ? `. В деле версия от ${stamp(st.updated_at)}.` : '. Содержимого нет вовсе.');
    case 'never':
      return `${name}: демон ещё ни разу не обновлял этот список`;
    case 'unknown':
      return `${name}: планировщик списков не работает, состояние неизвестно`;
    case 'core':
      return `${name}: домены качает сам sing-box, у демона состояния по нему нет`;
    default:
      return '';
  }
}

const conditions = (r) => ({
  lists: r.community_lists || [],
  domains: r.domains || [],
  subnets: r.subnets || [],
  peer_scope: r.peer_scope,
  peer_ids: r.peer_ids || [],
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
    // Whole-Internet exits precede shared rules; only intersecting clients overlap.
    const applies = (rule, p) => rule.peer_scope !== 'selected' || (rule.peer_ids || []).includes(p.id);
    if (!state.peers.some((p) => p.enabled && !p.egress_tunnel_id && applies(mine, p) && applies(up, p))) continue;
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
    /* Недоступная цель красится прямо в строке: правило с ней не течёт мимо
       туннеля, а отбрасывает трафик (ADR 0013), и снаружи это выглядит как
       «ничего не открывается» без единой подсказки почему. Молчащий канал
       красится слабее и другим знаком: это не отказ. */
    const trouble = r.enabled ? ruleTrouble(r) : null;
    const dead = trouble && trouble.level === 'dead';
    const troubleMark = dead ? '⚠ ' : (trouble ? '◌ ' : '');
    const troubleTone = dead ? 'err' : (trouble ? 'warn' : 'accent');
    const dest = r.action === 'direct'
      ? '<span class="badge">напрямую</span>'
      : r.action === 'block'
        ? '<span class="badge err">блокировать</span>'
        : `<span class="badge ${troubleTone}">${troubleMark}→ ${
          esc((tunnelById(r.tunnel_id) || {}).name || 'туннель удалён')}${via}</span>`;

    const states = listStates(r);
    const chip = (text, key, extra = '') =>
      `<span class="chip${shadowed.has(key) ? ' shadowed' : ''}${extra}">${esc(text)}</span>`;
    /* Чип списка носит своё состояние: не обновившийся источник перестаёт
       ловить домены молча, и по виду чипа это было не отличить от рабочего. */
    const listChip = (name, key) => {
      const st = states.get(key);
      const mark = listMark(st);
      const cls = st && LIST_STATE[st.state] ? LIST_STATE[st.state].cls : '';
      const hint = listHint(st, name);
      return `<span class="chip${shadowed.has(key) ? ' shadowed' : ''}${cls ? ` ${cls}` : ''}"${
        hint ? ` title="${esc(hint)}"` : ''}>${esc(name)}${
        mark ? `<span class="chip-state">${esc(mark)}</span>` : ''}</span>`;
    };
    const chips = [
      ...(r.community_lists || []).map((k) => listChip(listTitle(k), k)),
      ...(r.domains || []).map((d) => chip(d, d)),
      ...(r.subnets || []).map((n) => chip(n, n)),
      ...(r.remote_lists || []).map((url) => listChip('внешний список', url)),
    ].join('');

    /* Текст ошибки источника — строкой под чипами, а не только в подсказке:
       «почему правило перестало ловить» читается без наведения. */
    const badLists = (r.lists_status || []).filter((st) => st.state === 'failed');
    const listNote = badLists.length ? `
      <div class="overlap-note">
        <span>⚠</span>
        <span>${badLists.map((st) =>
    `${esc(st.key ? listTitle(st.key) : st.url)} — ${esc(st.error)}`).join('; ')}${
  badLists.some((st) => st.updated_at)
    ? '. Правило работает на прошлой версии списка.'
    : '. Содержимого нет вовсе — эти условия правило сейчас не ловит.'}</span>
      </div>` : '';

    const troubleLine = troubleNote(trouble);

    const scope = r.peer_scope === 'selected'
      ? ((r.peer_ids || []).map((id) => (peerById(id) || {}).name || '?').join(', ') || 'No selected clients')
      : 'все клиенты';

    const note = shadow.length ? `
      <div class="overlap-note">
        <span>⚠</span>
        <span>Earlier rules ${shadow.map((h) => `“${esc(h.rule.name)}”`).join(', ')}
        take precedence for intersecting shared-rule clients and resources:
        ${shadow.flatMap((h) => h.shared).map((k) => esc(listTitle(k))).join(', ')}.
        Whole-Internet exits precede shared rules.</span>
      </div>` : '';

    return `
      <div class="row${r.enabled ? '' : ' dim'}${dead ? ' rule-dead' : ''}${
  trouble && !dead ? ' rule-quiet' : ''}">
        <div class="rule-order">
          <button class="ord-btn" data-act="rule-up" data-id="${esc(r.id)}" ${i === 0 ? 'disabled' : ''} aria-label="Move rule ${esc(r.name)} up">▲</button>
          <button class="ord-btn" data-act="rule-down" data-id="${esc(r.id)}" ${i === state.rules.length - 1 ? 'disabled' : ''} aria-label="Move rule ${esc(r.name)} down">▼</button>
        </div>
        <span class="rule-num">${i + 1}</span>
        <div class="row-main">
          <div class="row-title">${esc(r.name)} ${dest}</div>
          <div class="row-meta">${esc(scope)}${r.resolve_real_ip ? ' · реальный IP' : ''}</div>
          <div class="chips">${chips || '<span class="chip">условий нет — правило не попадёт в конфиг</span>'}</div>
          ${r.inactive_reason === 'no_selected_peers' ? '<div class="hint">Inactive: assign clients, then explicitly enable and Apply.</div>' : ''}${troubleLine}${listNote}${note}
        </div>
        <div class="row-actions">
          <button class="toggle" role="switch" aria-checked="${Boolean(r.enabled)}" data-act="toggle-rule" data-id="${esc(r.id)}" aria-label="Enable rule ${esc(r.name)}"></button>
          <button class="btn btn-sm" data-act="edit-rule" data-id="${esc(r.id)}" aria-label="Edit rule ${esc(r.name)}">Изменить</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-rule" data-id="${esc(r.id)}" aria-label="Actions for rule ${esc(r.name)}" aria-haspopup="menu" aria-expanded="false">⋮</button>
          </div>
        </div>
      </div>`;
  }).join('');

  const listsNote = state.missing.has('community')
    ? '<p class="screen-sub" style="margin-top:10px">Каталог готовых списков пока недоступен: GET /api/lists/community не реализован. Свои домены и подсети работают.</p>'
    : '';

  return head() + probeView() + `
    <div class="card">${rows || emptyView()}</div>
    ${listsNote}`;
}

/* --- пустой экран ---------------------------------------------------------
   Пустой список правил — законное состояние, а не ошибка: так выглядит и
   свежая установка, и сервер, где правила удалили осознанно. Поэтому первая
   строка говорит, что происходит без правил, и только потом предлагает
   стартовый набор — предложением, от которого ничего не меняется, пока его не
   приняли (ADR 0014).

   Состав пресета приходит из каталога полем `in_preset`: своего списка ключей
   у панели нет, иначе он разъезжался бы с каталогом демона. */

/** Списки стартового пресета в порядке каталога. Пусто — каталог не пришёл. */
const presetLists = () => state.communityLists.filter((l) => l.in_preset);

/** Туннели, в которые правило довезёт трафик прямо сейчас. Выключенный не
    подходит: правило с ним не работает, а отбрасывает трафик (ADR 0013), и
    предлагать его как цель стартового правила значит предлагать блокировку. */
const liveTunnels = () => state.tunnels.filter((t) => t.enabled);

function emptyView() {
  const lead = `<div class="empty-lead">No shared rules. Clients without a selected exit use direct routing.</div>`;

  const preset = presetLists();
  if (!preset.length) {
    /* Каталог не пришёл — состав предлагать нечем, и выдумывать его панель не
       станет. Про сам каталог экран уже говорит отдельной строкой. */
    return `<div class="empty empty-start">${lead}</div>`;
  }

  const names = preset.map((l) => l.title).join(', ');

  if (!liveTunnels().length) {
    return `<div class="empty empty-start">${lead}
      <div class="empty-offer">
        <div class="empty-offer-text">Направлять пока некуда: ни одного включённого туннеля.
          Стартовый набор (${esc(names)}) можно собрать одной кнопкой, но сперва нужен туннель —
          правило с выключенным туннелем не пускает трафик напрямую, а отбрасывает его.</div>
        <button class="btn" data-act="go-tunnels">Перейти к туннелям</button>
      </div></div>`;
  }

  return `<div class="empty empty-start">${lead}
    <div class="empty-offer">
      <div class="empty-offer-text">Если начинать не с нуля: стартовый набор отправит в туннель
        ${esc(names)}. Форма откроется заполненной — туннель выбираете вы, лишнее снимаете,
        название меняете. Правило получится обычным: его можно выключить, поправить и удалить,
        и в силу оно войдёт после «Применить».</div>
      <button class="btn btn-primary" data-act="preset-rule">Собрать стартовое правило</button>
    </div></div>`;
}

/* --- пробник маршрута -----------------------------------------------------
   «Куда уйдёт этот домен и по какому правилу» — вопрос, на который до сих пор
   отвечал только конфиг, прочитанный руками. Спрашивается работающий sing-box,
   а не база: правки копятся и применяются кнопкой, и объяснять порядок правил
   бессмысленно, если применён другой их набор.

   Живёт на экране правил, а не на пятом экране: экранов четыре
   (`docs/00-vision.md`), и ответ нужен ровно там, где стоит список правил. */

/** Состояние пробника переживает перерисовку: экран перечитывается по такту
    опроса, и ответ, стёртый через пятнадцать секунд, читать некогда. */
const probe = { domain: '', busy: false, result: null, error: '' };

/** Проба доступности домена — сосед пробника маршрута, но вопрос другой: не
    «куда уйдёт», а «открывается ли напрямую или отдаёт геоблок». Своё состояние,
    чтобы ответ одной пробы не стирал ответ другой. */
const reach = { busy: false, result: null, error: '' };

function probeView() {
  const busy = probe.busy || reach.busy;
  return `
    <div class="card probe">
      <div class="probe-bar">
        <label for="probe-domain">Shared rules: domain check</label>
        <input type="search" id="probe-domain" class="probe-input" autocomplete="off" spellcheck="false"
          placeholder="youtube.com" value="${esc(probe.domain)}" data-enter="probe-run"
          aria-label="Домен для проверки">
        <button class="btn" data-act="probe-run" ${busy ? 'disabled' : ''}>${
  probe.busy ? 'Спрашиваю…' : 'Куда уйдёт'}</button>
        <button class="btn" data-act="reach-run" ${busy ? 'disabled' : ''}>${
  reach.busy ? 'Проверяю…' : 'Доступность напрямую'}</button>
      </div>
      <div class="probe-hint"><b>Scope: shared rules.</b> Per-client whole-Internet exits are not evaluated here. <b>Куда уйдёт</b> — отвечает работающий sing-box: его правила
        маршрутизации и его же резолвер; непримененные правки в ответ не попадают.
        <b>Доступность напрямую</b> — демон сам идёт к домену с адреса сервера и говорит, открывается
        ли тот в лоб или отдаёт геоблок (403/451, Cloudflare 1020).</div>
      ${probeResult()}
      ${reachResult()}
    </div>`;
}

/* --- проба доступности напрямую -------------------------------------------
   Вердикт приходит с сервера готовой русской фразой и показывается как есть.
   Класс красит рамку: `geoblock_suspect` — тревожно, `unreachable` — приглушённо,
   `reachable` — спокойно. Ничего от себя панель не добавляет: «похоже на геоблок»
   сказал демон, и додумывать «а туннель починит» здесь нельзя (это Фаза 1). */
function reachResult() {
  if (reach.error) return `<div class="probe-out err">${esc(reach.error)}</div>`;
  const p = reach.result;
  if (!p) return '';
  const tone = p.class === 'geoblock_suspect' ? 'err'
    : p.class === 'unreachable' ? 'warn' : 'accent';
  const code = p.status ? ` <span class="mono">HTTP ${esc(String(p.status))}</span>` : '';
  return `<div class="probe-out">
      <div class="probe-domain mono">${esc(p.domain)} — доступность напрямую${code}</div>
      <div class="probe-verdict ${tone}">${esc(p.verdict)}</div>
    </div>`;
}

function probeResult() {
  if (probe.error) return `<div class="probe-out err">${esc(probe.error)}</div>`;
  const p = probe.result;
  if (!p) return '';

  const rule = p.rule
    ? `<div class="probe-rule"><span class="badge ${p.refused ? 'err' : 'accent'}">${esc(p.rule.name)}</span>
        <span class="mono">${esc(p.rule.outbound)}</span></div>`
    : '';
  /* Отказ резолвера — не молчание: правило «блокировать» и правило с
     недоступным туннелем отвечают на DNS отказом (ADR 0013), и это ответ о
     маршрутизации, а не поломка DNS. Причина приходит с сервера текстом и
     показывается как есть. */
  const noAddr = p.resolve_error
    ? `<div class="probe-addr${p.refused ? ' refused' : ''}">адреса нет: ${esc(p.resolve_error)}</div>`
    : '<div class="probe-addr">адреса нет: резолвер ядра ничего не вернул</div>';
  const addrs = p.addresses && p.addresses.length
    ? `<div class="probe-addr mono">${esc(p.addresses.join(', '))}${p.fakeip ? ' — FakeIP' : ''}</div>`
    : noAddr;

  return `<div class="probe-out">
      <div class="probe-domain mono">${esc(p.domain)}</div>
      ${rule}
      <div class="probe-verdict">${esc(p.verdict)}</div>
      ${addrs}
    </div>`;
}

async function runProbe() {
  const field = $('#probe-domain');
  probe.domain = field ? field.value.trim() : probe.domain;
  if (!probe.domain) {
    toast('Введите домен, например youtube.com', 'err');
    if (field) field.focus();
    return;
  }
  probe.busy = true;
  probe.error = '';
  refresh();
  try {
    probe.result = await api.route.test(probe.domain);
  } catch (err) {
    // Недоступное ядро отвечает 503 с объяснением — показываем его как есть:
    // пустой результат читался бы как «ни одно правило не сработает».
    probe.result = null;
    probe.error = err.message;
  } finally {
    probe.busy = false;
    refresh();
  }
}

async function runReach() {
  const field = $('#probe-domain');
  probe.domain = field ? field.value.trim() : probe.domain;
  if (!probe.domain) {
    toast('Введите домен, например youtube.com', 'err');
    if (field) field.focus();
    return;
  }
  reach.busy = true;
  reach.error = '';
  refresh();
  try {
    reach.result = await api.domain.reachability(probe.domain);
  } catch (err) {
    reach.result = null;
    reach.error = err.message;
  } finally {
    reach.busy = false;
    refresh();
  }
}

/* --- форма правила -------------------------------------------------------- */

/** Правила, которые проверяются раньше этого. Новое встаёт последним. */
function rulesAbove(id) {
  const i = state.rules.findIndex((x) => x.id === id);
  return i < 0 ? state.rules.slice() : state.rules.slice(0, i);
}

/** Форма правила. `seed` заполняет поля нового правила заранее — так открывается
    стартовый пресет: правило создаёт тот же «Сохранить», что и у заведённого
    руками, и отличить его потом будет нечем (ADR 0014). */
function modalRule(id, seed = null) {
  const r = id ? state.rules.find((x) => x.id === id) : {
    id: null, name: '', action: 'tunnel', tunnel_id: '', via_tunnel_id: '', enabled: true,
    community_lists: [], domains: [], subnets: [], remote_lists: [],
    peer_scope: 'all', peer_ids: [], resolve_real_ip: false, ...(seed || {}),
  };
  if (!r) return;
  const above = rulesAbove(id);
  const position = above.length + 1;
  const total = state.rules.length + (id ? 0 : 1);
  const catalogKeys = new Set(state.communityLists.map((list) => list.key));
  const unavailableKeys = (r.community_lists || []).filter((key) => !catalogKeys.has(key));
  const catalog = state.communityLists.concat(unavailableKeys.map((key) => ({ key, title: key, unavailable: true })));
  const listsHtml = catalog.map((list) => `
    <label class="rule-item" data-title="${esc(`${list.title} ${list.key}`.toLowerCase())}">
      <input type="checkbox" name="list" value="${esc(list.key)}" ${(r.community_lists || []).includes(list.key) ? 'checked' : ''}>
      <span class="rule-item-content"><span class="rule-item-name">${esc(list.title)}</span>
        ${list.unavailable ? '<span class="rule-item-status">Catalog unavailable · retained</span>' : ''}
        ${list.has_subnets ? '<span class="rule-item-status">Includes subnets</span>' : ''}</span>
    </label>`).join('');
  const tunnelLabel = (t) => `${t.name}${!t.enabled ? ' (disabled; blocked)' : ''}${t.pool && (t.pool.servers_total === 0 || t.pool.servers_alive === 0) ? ' (no usable servers)' : ''}`;
  const tunnelOpts = `<option value="" ${!r.tunnel_id ? 'selected' : ''}>Select a tunnel</option>`
    + (r.tunnel_id && !tunnelById(r.tunnel_id) ? `<option value="${esc(r.tunnel_id)}" selected>Unavailable tunnel (preserved)</option>` : '')
    + state.tunnels.map((t) => `<option value="${esc(t.id)}" ${r.tunnel_id === t.id ? 'selected' : ''}>${esc(tunnelLabel(t))}</option>`).join('');
  // Only WARP can be the final link. Preserve unavailable existing references.
  const warps = state.tunnels.filter((t) => t.source === 'warp');
  const missingVia = r.via_tunnel_id && !warps.some((t) => t.id === r.via_tunnel_id);
  const viaOpts = '<option value="">No second link</option>' + warps.map((t) =>
    `<option value="${esc(t.id)}" ${r.via_tunnel_id === t.id ? 'selected' : ''}>${esc(tunnelLabel(t))}</option>`).join('')
    + (missingVia ? `<option value="${esc(r.via_tunnel_id)}" selected>Unavailable chain tunnel (preserved)</option>` : '');
  const peersHtml = state.peers.map((peer) => `
    <label class="rule-item" data-title="${esc(peer.name.toLowerCase())}">
      <input type="checkbox" name="peer" value="${esc(peer.id)}" ${(r.peer_ids || []).includes(peer.id) ? 'checked' : ''}>
      <span class="rule-item-content"><span class="rule-item-name">${esc(peer.name)}</span>
        ${!peer.enabled ? '<span class="rule-item-status">Disabled client</span>' : ''}
        ${peer.egress_tunnel_id ? '<span class="rule-item-status">Whole-Internet exit overrides shared rules</span>' : ''}</span>
    </label>`).join('');
  const orphan = r.peer_scope === 'selected' && !r.enabled
    && !(r.peer_ids || []).some((peerId) => state.peers.some((peer) => peer.id === peerId));

  openModal(modalShell(id ? r.name : 'New rule', `
    <div class="rule-form">
      <div class="line-errors rule-error" id="r-error" role="alert" hidden></div>
      <section class="rule-section rule-identity" aria-labelledby="r-identity-heading">
        <h3 id="r-identity-heading">Name</h3>
        <label for="r-name">Rule name</label>
        <input type="text" id="r-name" value="${esc(r.name)}" placeholder="Example services" autocomplete="off">
        <div class="rule-priority"><span>Priority ${position} of ${total} · first match wins</span>
          <details class="rule-help"><summary>Ordering help</summary>
            <p>Rules are checked from top to bottom for shared-rule clients. Earlier matches take precedence.
              ${id ? 'Use the arrows in the rule inventory to change priority.' : 'New rules are added last; use the inventory arrows to move them after saving.'}
              Whole-Internet client exits take precedence over shared rules.</p>
          </details>
        </div>
      </section>

      <section class="rule-section" aria-labelledby="r-clients-heading">
        <h3 id="r-clients-heading">Clients</h3>
        <fieldset class="rule-choices"><legend>Apply this shared rule to</legend>
          <label class="radio-pill"><input type="radio" name="scope" value="all" ${r.peer_scope === 'selected' ? '' : 'checked'}> All clients</label>
          <label class="radio-pill"><input type="radio" name="scope" value="selected" ${r.peer_scope === 'selected' ? 'checked' : ''}> Selected clients</label>
        </fieldset>
        <p class="hint">Whole-Internet exits override shared rules. Disabled clients do not gain access from this selection.</p>
        ${orphan ? '<p class="rule-status">Inactive: no selected clients. Assignment keeps this rule disabled. Enable it explicitly in the rule inventory, then Apply.</p>' : ''}
        <div id="r-peer-selection" ${r.peer_scope === 'selected' ? '' : 'hidden'}>
          <div class="rule-list-tools"><label for="r-peer-q">Search clients</label><span id="r-peer-count" class="rule-count" aria-live="polite"></span></div>
          <input type="search" class="rule-search" id="r-peer-q" placeholder="Client name" autocomplete="off">
          <div class="list-grid rule-grid rule-peers" id="r-peers">${peersHtml}</div>
          <p class="rule-none" id="r-peers-empty" ${state.peers.length ? 'hidden' : ''}>${state.peers.length ? 'No clients match this search.' : 'No clients available. Add a client before assigning this rule.'}</p>
        </div>
      </section>

      <section class="rule-section rule-resources" aria-labelledby="r-resources-heading">
        <h3 id="r-resources-heading">Resources</h3>
        <div class="rule-source-heading"><h4>Community sources</h4><span class="rule-count" id="r-catalog-count" aria-live="polite"></span></div>
        <p class="rule-status" id="r-unavailable-note" hidden></p>
        <details class="rule-disclosure" id="r-picked" hidden>
          <summary>Review selected sources</summary>
          <div class="rule-picked-chips" id="r-picked-chips"></div>
        </details>
        <details class="rule-disclosure" id="r-catalog">
          <summary>Browse community catalog</summary>
          ${catalog.length ? `<label for="r-list-q" class="rule-search-label">Search sources</label>
            <input type="search" class="rule-search" id="r-list-q" autocomplete="off" spellcheck="false" placeholder="Source name or ID">`
            : '<p class="rule-none">Community catalog unavailable. Manual resources remain available below.</p>'}
          <div class="list-grid rule-grid" id="r-lists">${listsHtml}</div>
          <p class="rule-none" id="r-lists-empty" hidden>No sources match this search.</p>
        </details>
        ${(r.remote_lists || []).length ? `<div class="rule-remote"><h4>API-managed URL sources · ${r.remote_lists.length}</h4>
          <p class="hint">Read-only here. Save preserves these sources; use the API to change them.</p>
          <ul>${r.remote_lists.map((url) => `<li><code>${esc(url)}</code></li>`).join('')}</ul></div>` : ''}
        <div class="rule-pair rule-manual">
          <div class="field"><label for="r-domains">Domains</label>
            <textarea id="r-domains" rows="4" spellcheck="false" aria-describedby="r-domains-hint r-domains-err" placeholder="example.com">${esc((r.domains || []).join('\n'))}</textarea>
            <div class="line-errors" id="r-domains-err"></div>
            <div class="hint" id="r-domains-hint">One per line. example.com also matches cdn.example.com.</div>
          </div>
          <div class="field"><label for="r-subnets">IPv4 subnets or addresses</label>
            <textarea id="r-subnets" rows="4" spellcheck="false" aria-describedby="r-subnets-hint r-subnets-err" placeholder="203.0.113.0/24">${esc((r.subnets || []).join('\n'))}</textarea>
            <div class="line-errors" id="r-subnets-err"></div>
            <div class="hint" id="r-subnets-hint">One per line. Use 203.0.113.0/24 or 203.0.113.7.</div>
          </div>
        </div>
        <details class="rule-help"><summary>Matching and DNS help</summary>
          <p>Domains match by suffix, including subdomains. Manual domains normally use FakeIP, while subnets match real destination IPv4 addresses, including applications with their own DNS. Existing source and DNS settings remain unchanged by this editor.</p>
        </details>
      </section>

      <section class="rule-section rule-action" aria-labelledby="r-action-heading">
        <h3 id="r-action-heading">Action</h3>
        <fieldset class="rule-choices"><legend>For matching traffic</legend>
          <label class="radio-pill"><input type="radio" name="action" value="tunnel" ${r.action === 'tunnel' ? 'checked' : ''}> Tunnel</label>
          <label class="radio-pill"><input type="radio" name="action" value="direct" ${r.action === 'direct' ? 'checked' : ''}> Direct</label>
          <label class="radio-pill"><input type="radio" name="action" value="block" ${r.action === 'block' ? 'checked' : ''}> Block</label>
        </fieldset>
        <div class="field rule-target" id="r-tunnel-field" ${r.action === 'tunnel' ? '' : 'hidden'}>
          <label for="r-tunnel">Tunnel</label>
          <select id="r-tunnel" class="rule-tunnel" ${r.action === 'tunnel' ? '' : 'disabled'}>${tunnelOpts}</select>
        </div>
        <p class="rule-action-hint" id="r-action-hint" aria-live="polite"></p>
        ${warps.length || missingVia ? `<details class="rule-disclosure rule-via" id="r-via-field" ${r.via_tunnel_id ? 'open' : ''}>
          <summary>Advanced · WARP chain${r.via_tunnel_id ? ' · configured' : ''}</summary>
          <div class="field"><label for="r-via">Final exit through WARP</label><select id="r-via" class="rule-tunnel">${viaOpts}</select></div>
          <p class="hint">Optional second link: traffic first uses the selected tunnel, then exits through WARP. WARP must be last; the two links must differ. No direct fallback.</p>
        </details>` : ''}
        <div class="rule-overlap" id="r-overlap" hidden></div>
      </section>
    </div>`,
  `<span class="rule-save-note">Save records changes. Apply activates routing.</span>
    <button class="btn" data-act="close-modal">Cancel</button>
    <button class="btn btn-primary" data-act="save-rule" data-id="${esc(id || '')}">Save changes</button>`), (m) => {
    // Native focus scrolling can leave the bottom of a field behind the footer.
    m.querySelector('.rule-form').addEventListener('focusin', (event) => {
      if (event.target.matches('input, textarea, select, summary, button')) {
        event.target.scrollIntoView({ block: 'nearest', inline: 'nearest' });
      }
    });
    const viaSel = m.querySelector('#r-via');
    const syncAction = () => {
      const action = m.querySelector('input[name="action"]:checked').value;
      const tunnel = m.querySelector('#r-tunnel');
      tunnel.disabled = action !== 'tunnel';
      m.querySelector('#r-tunnel-field').hidden = action !== 'tunnel';
      tunnel.title = tunnel.selectedOptions[0]?.textContent || '';
      const trouble = ruleTrouble({ action, tunnel_id: tunnel.value, via_tunnel_id: viaSel?.value || '' });
      const hint = m.querySelector('#r-action-hint');
      hint.classList.toggle('blocked', trouble?.level === 'dead');
      hint.textContent = trouble
        ? (trouble.level === 'dead' ? 'Target unavailable or disabled: matching traffic is blocked after Apply. There is no direct fallback.'
          : 'The last health check failed. Traffic still uses the tunnel; there is no direct fallback.')
        : action === 'tunnel' ? 'Matching traffic uses this tunnel after Apply. Health does not prove client connectivity; there is no direct fallback.'
          : action === 'direct' ? 'Matching traffic goes directly from this server, using its public IP.'
            : 'Matching connections are rejected. These resources will not open.';
      if (viaSel) {
        m.querySelector('#r-via-field').hidden = action !== 'tunnel';
        viaSel.disabled = action !== 'tunnel';
        viaSel.title = viaSel.selectedOptions[0]?.textContent || '';
      }
    };
    $$('input[name="action"]', m).forEach((element) => element.addEventListener('change', syncAction));
    m.querySelector('#r-tunnel').addEventListener('change', syncAction);
    viaSel?.addEventListener('change', syncAction);
    syncAction();

    const filterRows = (query, list, empty) => {
      const needle = query.value.trim().toLowerCase();
      let visible = 0;
      $$('.rule-item', list).forEach((row) => {
        row.hidden = !row.dataset.title.includes(needle);
        if (!row.hidden) visible++;
      });
      empty.hidden = visible > 0;
    };
    const peerQuery = m.querySelector('#r-peer-q');
    peerQuery.addEventListener('input', () => filterRows(peerQuery, m.querySelector('#r-peers'), m.querySelector('#r-peers-empty')));
    const syncPeers = () => {
      m.querySelector('#r-peer-selection').hidden = m.querySelector('input[name="scope"]:checked').value !== 'selected';
      const selected = $$('input[name="peer"]:checked', m).map((element) => peerById(element.value)).filter(Boolean);
      const overridden = selected.filter((peer) => peer.egress_tunnel_id).length;
      const disabled = selected.filter((peer) => !peer.enabled).length;
      m.querySelector('#r-peer-count').textContent = `${selected.length} selected${overridden ? ` · ${overridden} overridden` : ''}${disabled ? ` · ${disabled} disabled` : ''}`;
      renderOverlap();
    };
    $$('input[name="scope"], input[name="peer"]', m).forEach((element) => element.addEventListener('change', syncPeers));

    const picked = m.querySelector('#r-picked');
    const renderPicked = () => {
      const selected = $$('input[name="list"]:checked', m).map((element) => element.value);
      const unavailable = selected.filter((key) => !catalogKeys.has(key));
      picked.hidden = !selected.length;
      picked.querySelector('summary').textContent = `Review ${selected.length} selected sources${unavailable.length ? ` · ${unavailable.length} unavailable` : ''}`;
      m.querySelector('#r-picked-chips').innerHTML = selected.map((key) =>
        `<button type="button" class="rule-chip" data-key="${esc(key)}" aria-label="Remove ${esc(listTitle(key))}">
          <span>${esc(listTitle(key))}${!catalogKeys.has(key) ? ' · catalog unavailable' : ''}</span><span aria-hidden="true">×</span></button>`).join('');
      m.querySelector('#r-catalog-count').textContent = `${selected.length} selected${unavailable.length ? ` · ${unavailable.length} unavailable` : ''}`;
      const warning = m.querySelector('#r-unavailable-note');
      warning.hidden = !unavailable.length;
      warning.textContent = `${unavailable.length} selected source${unavailable.length === 1 ? ' is' : 's are'} absent from the catalog. Retained until explicitly removed; review selected sources to inspect them.`;
    };
    picked.addEventListener('click', (event) => {
      const button = event.target.closest('[data-key]');
      if (!button) return;
      const checkbox = m.querySelector(`input[name="list"][value="${CSS.escape(button.dataset.key)}"]`);
      if (checkbox) checkbox.checked = false;
      renderPicked();
      renderOverlap();
      // Removing the focused chip must not strand keyboard focus in the document.
      (picked.hidden ? m.querySelector('#r-catalog > summary') : picked.querySelector('summary')).focus();
    });
    const listQuery = m.querySelector('#r-list-q');
    listQuery?.addEventListener('input', () => filterRows(listQuery, m.querySelector('#r-lists'), m.querySelector('#r-lists-empty')));
    $$('input[name="list"]', m).forEach((element) => element.addEventListener('change', () => { renderPicked(); renderOverlap(); }));

    function renderOverlap() {
      const mine = {
        lists: $$('input[name="list"]:checked', m).map((element) => element.value),
        domains: lines(m.querySelector('#r-domains').value),
        subnets: lines(m.querySelector('#r-subnets').value),
        peer_scope: m.querySelector('input[name="scope"]:checked').value,
        peer_ids: $$('input[name="peer"]:checked', m).map((element) => element.value),
      };
      const hits = overlap(mine, above);
      const box = m.querySelector('#r-overlap');
      box.hidden = !hits.length;
      if (!hits.length) return;
      box.innerHTML = '<strong>Earlier rules overlap for shared-rule clients</strong>'
        + hits.map((hit) => `<div class="rule-overlap-line">“${esc(hit.rule.name)}” — ${hit.shared.map((key) => esc(listTitle(key))).join(', ')}</div>`).join('')
        + '<div class="rule-overlap-tail">Earlier rules take precedence for intersecting clients and these resources. Whole-Internet exits precede shared rules.</div>';
    }
    const live = (selector, output, kind) => {
      const field = m.querySelector(selector), error = m.querySelector(output);
      const update = () => {
        const all = field.value.split('\n');
        const bad = badLines(field.value, kind);
        field.classList.toggle('bad', bad.length > 0);
        field.setAttribute('aria-invalid', String(bad.length > 0));
        error.innerHTML = bad.length ? bad.slice(0, 3).map((line) =>
          `<div>Line ${line}: “${esc(all[line - 1].trim())}” is not a valid ${kind === 'domain' ? 'domain' : 'IPv4 subnet or address'}.</div>`).join('')
          + (bad.length > 3 ? `<div>${bad.length - 3} more invalid lines.</div>` : '') : '';
        renderOverlap();
      };
      field.addEventListener('input', update);
      update();
    };
    live('#r-domains', '#r-domains-err', 'domain');
    live('#r-subnets', '#r-subnets-err', 'cidr');
    renderPicked();
    syncPeers();
    m.querySelector('#r-name').focus();
  });
}

async function saveRule(id) {
  const m = $('#modal');
  if (!m.querySelector('#r-name')) return;
  const revision = currentModalRevision(), currentSession = api.session.guard();
  const report = (message) => {
    const error = m.querySelector('#r-error');
    error.hidden = false;
    error.textContent = message;
    error.scrollIntoView({ block: 'nearest' });
  };
  const name = m.querySelector('#r-name').value.trim();
  const action = m.querySelector('input[name="action"]:checked').value;
  const scope = m.querySelector('input[name="scope"]:checked').value;
  const domains = lines(m.querySelector('#r-domains').value);
  const subnets = lines(m.querySelector('#r-subnets').value);
  const listsSel = $$('input[name="list"]:checked', m).map((e) => e.value);
  const peerIds = $$('input[name="peer"]:checked', m).map((e) => e.value);

  if (!name) { report('Enter a rule name.'); m.querySelector('#r-name').focus(); return; }

  /* Молча выбросить непонятую строку нельзя: пользователь решит, что правило её
     учитывает. Поэтому сохранение упирается в поле, где ошибка. */
  const badDomains = badLines(m.querySelector('#r-domains').value, 'domain');
  const badSubnets = badLines(m.querySelector('#r-subnets').value, 'cidr');
  if (badDomains.length || badSubnets.length) {
    const field = badDomains.length ? '#r-domains' : '#r-subnets';
    const nums = badDomains.length ? badDomains : badSubnets;
    report(`Invalid ${badDomains.length ? 'domain' : 'subnet'} lines: ${nums.join(', ')}. Correct or remove them before saving.`);
    m.querySelector(field).focus();
    return;
  }
  if (action === 'tunnel' && !m.querySelector('#r-tunnel').value) {
    report('Select a tunnel before saving. Add a tunnel first if the inventory is empty.'); return;
  }
  if (scope === 'selected' && !peerIds.length && (!id || (state.rules.find((r) => r.id === id) || {}).enabled)) {
    report('Select at least one client before enabling this rule.'); return;
  }
  /* Списков по ссылке в форме нет — их задают через API, — но условием совпадения
     они считаются наравне с остальным (store.Rule.validate, #142). Без этой
     поправки правило, живущее на remote_lists, нельзя было бы отредактировать в
     панели: сохранение упиралось бы в проверку строже серверной. */
  const remote = (id ? (state.rules.find((x) => x.id === id) || {}).remote_lists : null) || [];
  if (!listsSel.length && !domains.length && !subnets.length && !remote.length) {
    report('Add a community source, domain or subnet before saving.');
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
  if (btn.disabled) return;
  btn.disabled = true;
  btn.textContent = 'Saving…';
  m.querySelector('#r-error').hidden = true;
  try {
    if (id) await api.rules.update(id, body);
    else await api.rules.create({ enabled: true, remote_lists: [], resolve_real_ip: false, ...body });
    if (!currentSession()) return;
    markDirty();
    if (isCurrentModal(revision)) closeModal();
    const listRevision = ++rulesRevision;
    const rules = await api.rules.list();
    if (!currentSession() || listRevision !== rulesRevision) return;
    state.rules = rules;
    refresh();
  } catch (err) {
    if (!currentSession() || !isCurrentModal(revision)) return;
    btn.disabled = false;
    btn.textContent = 'Save';
    report(`Rule could not be saved: ${err.message}. Your draft is preserved; correct the problem and retry.`);
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
  'probe-run': () => runProbe(),
  'reach-run': () => runReach(),
  'add-rule': () => modalRule(null),

  /* Стартовый пресет: не создаёт правило, а открывает обычную форму с
     отмеченными списками. Цель выбирает пользователь — по умолчанию
     подставляется первый включённый туннель, а не первый попавшийся. */
  'preset-rule': () => {
    const preset = presetLists();
    const target = liveTunnels()[0];
    if (!preset.length || !target) {
      toast('Стартовый набор сейчас собрать не из чего', 'err');
      return;
    }
    modalRule(null, {
      name: 'Стартовый набор',
      tunnel_id: target.id,
      community_lists: preset.map((l) => l.key),
    });
  },

  'go-tunnels': () => { location.hash = 'tunnels'; },
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
