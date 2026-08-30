/* Панель razvedka: запись сетевой сессии, разбор одного прогона, сравнение двух,
   экспорт списка доменов.

   Главный экран — одиночный прогон: что отказало и кто упомянул страну, в двух
   разных разделах с разной подписью уверенности. Сравнение двух прогонов никуда не
   делось, но живёт вторым шагом ([ADR 0022](../../docs/decisions/0022-single-run-entry.md)).

   Тела ответов здесь не хранятся. Из тела в момент записи вынимаются гео-поля, и
   дальше живёт только компактная запись — иначе сессия на полсотни страниц не
   влезет ни в память, ни в chrome.storage.local. */

import { hostOf, pathKey } from './lib/pathkey.js';
import { geoHeaders, geoFields, parseCdnTrace, isGeoEndpoint } from './lib/geo.js';
import { foldHost, exportDomains } from './lib/domains.js';
import { diffSessions } from './lib/diff.js';
import { scanRun, runIsSilent } from './lib/single.js';
import {
  loadSessions, putSession, removeSession, clearSessions, defaultName, MAX_SESSIONS,
} from './lib/storage.js';
import { makePersistQueue } from './lib/persist.js';

const $ = (sel) => document.querySelector(sel);

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]));

/** Потолок тела, которое вообще стоит читать. Выше — это уже не JSON с полем
    `country`, а данные, и разбирать их незачем. */
const MAX_BODY_BYTES = 256 * 1024;

/** Перерисовка живого списка не чаще раза в 400 мс: на живом сайте запросов сотни
    в минуту, и перерисовывать таблицу на каждый — работа впустую. */
const RENDER_DELAY_MS = 400;

const state = {
  recording: null, // текущая сессия или null
  viewing: null, // прогон, показанный в одиночном разделе
  sessions: [],
  pickA: null,
  pickB: null,
  diff: null,
  chosen: new Set(), // хосты, отмеченные под экспорт в сравнении
  chosenSingle: new Set(), // то же в одиночном прогоне
  defaulted: new Set(), // хосты, которым дефолт отметки уже выставлен
  showNoise: false,
};

/* ==========================================================================
   Запись
   ========================================================================== */

function textualMime(mime) {
  const m = String(mime || '').toLowerCase();
  return m.includes('json')
    || m.startsWith('text/')
    || m.includes('javascript')
    || m.includes('xml');
}

/** Гео-сигнал из тела. JSON разбирается как JSON, `/cdn-cgi/trace` — построчно как
    `key=value`. Битое тело сигналом не считается и записи не мешает. */
function fieldsFromBody(host, path, mime, body) {
  if (!body) return {};
  if (path.startsWith('/cdn-cgi/trace')) return parseCdnTrace(body);
  const m = String(mime || '').toLowerCase();
  if (!m.includes('json') && !isGeoEndpoint(host, path)) return {};
  try {
    return geoFields(JSON.parse(body));
  } catch {
    // ip-api и родня умеют отдавать текст вместо JSON — там ловить нечего.
    return {};
  }
}

function recordEntry(entry) {
  const session = state.recording;
  if (!session) return;

  const url = entry?.request?.url || '';
  const host = hostOf(url);
  if (!host) return; // data:, blob: и прочее к хостам отношения не имеет

  const path = pathKey(url);
  const res = entry.response || {};
  const rec = {
    host,
    method: String(entry.request?.method || 'GET').toUpperCase(),
    pathKey: path,
    status: Number(res.status) || 0,
    geoHeaders: geoHeaders(res.headers),
    geoFields: {},
    redirectTo: res.redirectURL || '',
    resourceType: entry._resourceType || '',
    mime: res.content?.mimeType || '',
    bodyBytes: Number(res.content?.size) || 0,
  };

  session.entries.push(rec);
  // Записанная строка должна доехать до storage независимо от того, нашлось ли что-то
  // в теле: прогон без единого гео-поля — обычное дело, и терять его нельзя.
  persistRecording();
  if (!session.host) {
    session.host = host;
    if (!session.renamed) session.name = defaultName(host, session.startedAt);
  }
  renderStatus();
  scheduleSingleRender();

  const worthBody = textualMime(rec.mime)
    && rec.bodyBytes >= 0
    && rec.bodyBytes < MAX_BODY_BYTES;
  if (!worthBody) return;

  entry.getContent((content) => {
    if (!content || content.length > MAX_BODY_BYTES) return;
    const fields = fieldsFromBody(host, path, rec.mime, content);
    if (Object.keys(fields).length) {
      rec.geoFields = fields;
      persistRecording();
      scheduleSingleRender();
    }
  });
}

const persistQueue = makePersistQueue(async () => {
  if (!state.recording) return;
  await putSession(state.recording);
});

/** Сессия пишется в storage не на каждый запрос: на живом сайте их сотни в минуту.
    Раз в две секунды достаточно, чтобы прогон пережил закрытие DevTools. */
function persistRecording() {
  if (!state.recording) return;
  persistQueue.schedule();
}

const renderQueue = makePersistQueue(() => renderSingle(), { delay: RENDER_DELAY_MS });

function scheduleSingleRender() {
  renderQueue.schedule();
}

function startRecording() {
  const startedAt = Date.now();
  // Метка прогона на входе не спрашивается: для одного прогона она бессмысленна.
  // Проставляют её в списке прошлых прогонов, и только ради сравнения.
  state.recording = {
    id: `s${startedAt}-${Math.random().toString(36).slice(2, 8)}`,
    name: defaultName('', startedAt),
    label: '',
    host: '',
    renamed: false,
    startedAt,
    entries: [],
  };
  state.viewing = state.recording;
  state.chosenSingle = new Set();
  state.defaulted = new Set();
  renderStatus();
  renderSingle();
  persistRecording();
}

async function stopRecording() {
  const session = state.recording;
  state.recording = null;
  persistQueue.cancel(); // висящий таймер не должен продублировать финальную запись
  renderQueue.cancel();
  if (session) await putSession(session);
  renderStatus();
  renderSingle();
  await refreshSessions();
}

function renderStatus() {
  const rec = state.recording;
  $('#rec-start').hidden = !!rec;
  $('#rec-stop').hidden = !rec;
  const el = $('#rec-status');
  if (!rec) {
    el.className = 'muted';
    el.textContent = 'не пишем';
    return;
  }
  el.className = 'rec';
  el.textContent = `пишем: ${rec.entries.length} запросов`;
}

chrome.devtools.network.onRequestFinished.addListener(recordEntry);

/* ==========================================================================
   Одиночный прогон: что отказало и кто упомянул страну
   ========================================================================== */

/** Ячейка «В правило»: свёрнутый домен и, если свёртки не случилось, причина. */
function ruleCell(host) {
  const fold = foldHost(host);
  const note = fold.reason ? ` <span class="muted small">(${esc(fold.reason)})</span>` : '';
  return `<td class="domain">${esc(fold.domain)}${note}</td>`;
}

/** Отметка по умолчанию: блокировки отмечены, гео-подсказки — нет. Разница не
    косметическая: отказ сервера наблюдаем, гео-заголовок — догадка, и молча класть
    догадку в правило панель не вправе. */
function defaultPick(host, on) {
  if (state.defaulted.has(host)) return;
  state.defaulted.add(host);
  if (on) state.chosenSingle.add(host);
}

function renderSingle() {
  const session = state.viewing;
  const entries = session?.entries || [];
  const scan = scanRun(entries);

  $('#single-name').textContent = session
    ? `— ${session.name} (${entries.length} запросов)`
    : '— ещё не записан';

  $('#single-hint').hidden = !!session && entries.length > 0;

  for (const b of scan.blocked) defaultPick(b.host, true);
  for (const g of scan.geo) defaultPick(g.host, false);

  $('#blocked-empty').hidden = scan.blocked.length > 0;
  $('#blocked').hidden = scan.blocked.length === 0;
  $('#blocked-body').innerHTML = scan.blocked.map((b) => `
    <tr data-host="${esc(b.host)}">
      <td><input type="checkbox" class="pick"${state.chosenSingle.has(b.host) ? ' checked' : ''}></td>
      <td class="host">${esc(b.host)}</td>
      ${ruleCell(b.host)}
      <td>${b.statuses.map((s) => `<span class="badge">${esc(s.status)}</span>&nbsp;×${s.count}`).join(' ')}</td>
      <td>${b.total}</td>
    </tr>`).join('');

  $('#geo-empty').hidden = scan.geo.length > 0;
  $('#geo').hidden = scan.geo.length === 0;
  $('#geo-body').innerHTML = scan.geo.map((g) => {
    const found = [...g.headers, ...g.fields].map((s) => {
      const more = s.more ? ` <span class="muted">и ещё ${s.more}</span>` : '';
      return `<li><code>${esc(s.name)}</code>: ${esc(s.values.join(', '))}${more}</li>`;
    }).join('');
    return `
      <tr data-host="${esc(g.host)}">
        <td><input type="checkbox" class="pick"${state.chosenSingle.has(g.host) ? ' checked' : ''}></td>
        <td class="host">${esc(g.host)}</td>
        ${ruleCell(g.host)}
        <td><ul class="signals">${found}</ul></td>
        <td>${g.requests}</td>
      </tr>`;
  }).join('');

  const picked = state.chosenSingle.size;
  $('#single-export').disabled = picked === 0;
  $('#single-count').textContent = picked ? `отмечено хостов: ${picked}` : '';

  // Пустой прогон — не «ничего нет», а повод сравнивать: сайт умеет резать молча.
  $('#silent').hidden = !!state.recording || !session || entries.length === 0
    || !runIsSilent(scan);
}

document.addEventListener('change', (e) => {
  if (!e.target.classList?.contains('pick')) return;
  const row = e.target.closest('tr');
  if (!row || !row.closest('#blocked-body, #geo-body')) return;
  const host = row.dataset.host;
  if (!host) return;
  if (e.target.checked) state.chosenSingle.add(host);
  else state.chosenSingle.delete(host);
  const picked = state.chosenSingle.size;
  $('#single-export').disabled = picked === 0;
  $('#single-count').textContent = picked ? `отмечено хостов: ${picked}` : '';
});

$('#single-export').addEventListener('click', () => {
  showExport([...state.chosenSingle]);
});

$('#silent-open').addEventListener('click', () => {
  $('#sessions-box').open = true;
  const ab = $('#ab');
  ab.open = true;
  ab.scrollIntoView({ block: 'start' });
});

/* ==========================================================================
   Список прошлых прогонов
   ========================================================================== */

const LABELS = { '': 'без метки', direct: 'напрямую', tunnel: 'через туннель' };

function stamp(ms) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getDate())}.${p(d.getMonth() + 1)} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

async function refreshSessions() {
  state.sessions = (await loadSessions()).sort((a, b) => b.startedAt - a.startedAt);
  renderSessions();
}

function renderSessions() {
  const body = $('#sessions-body');
  const list = state.sessions;
  $('#sessions-count').textContent = list.length ? `(${list.length})` : '';
  $('#sessions-empty').hidden = list.length > 0;
  $('#sessions').hidden = list.length === 0;
  body.innerHTML = list.map((s) => `
    <tr data-id="${esc(s.id)}">
      <td><input type="radio" name="pickA" value="${esc(s.id)}"${state.pickA === s.id ? ' checked' : ''}></td>
      <td><input type="radio" name="pickB" value="${esc(s.id)}"${state.pickB === s.id ? ' checked' : ''}></td>
      <td><input type="text" class="name" value="${esc(s.name)}" size="22"></td>
      <td>
        <select class="label">
          ${Object.entries(LABELS).map(([k, v]) => `
            <option value="${k}"${(s.label || '') === k ? ' selected' : ''}>${v}</option>`).join('')}
        </select>
      </td>
      <td>${s.entries?.length || 0}</td>
      <td class="muted">${stamp(s.startedAt)}</td>
      <td>
        <button class="show">показать</button>
        <button class="drop">удалить</button>
      </td>
    </tr>`).join('');
  $('#compare').disabled = !(state.pickA && state.pickB && state.pickA !== state.pickB);
}

$('#sessions-body').addEventListener('change', async (e) => {
  const row = e.target.closest('tr');
  const id = row?.dataset.id;
  if (!id) return;
  if (e.target.name === 'pickA') state.pickA = id;
  if (e.target.name === 'pickB') state.pickB = id;
  const session = state.sessions.find((s) => s.id === id);
  if (!session) return;
  if (e.target.classList.contains('name')) {
    session.name = e.target.value;
    session.renamed = true;
    await putSession(session);
  }
  if (e.target.classList.contains('label')) {
    session.label = e.target.value;
    await putSession(session);
  }
  $('#compare').disabled = !(state.pickA && state.pickB && state.pickA !== state.pickB);
});

$('#sessions-body').addEventListener('click', async (e) => {
  const id = e.target.closest('tr')?.dataset.id;
  if (!id) return;
  if (e.target.classList.contains('show')) {
    if (state.recording) return; // идёт запись — сверху показан именно он
    state.viewing = state.sessions.find((s) => s.id === id) || null;
    state.chosenSingle = new Set();
    state.defaulted = new Set();
    renderSingle();
    $('#single').scrollIntoView({ block: 'start' });
    return;
  }
  if (!e.target.classList.contains('drop')) return;
  if (state.pickA === id) state.pickA = null;
  if (state.pickB === id) state.pickB = null;
  if (state.viewing?.id === id) state.viewing = null;
  await removeSession(id);
  await refreshSessions();
  renderSingle();
});

$('#rec-start').addEventListener('click', startRecording);
$('#rec-stop').addEventListener('click', stopRecording);

$('#sessions-clear').addEventListener('click', async () => {
  if (!confirm(`Удалить все прогоны (${state.sessions.length})?`)) return;
  state.pickA = null;
  state.pickB = null;
  state.diff = null;
  state.viewing = state.recording;
  $('#result').hidden = true;
  $('#export-box').hidden = true;
  await clearSessions();
  await refreshSessions();
  renderSingle();
});

/* ==========================================================================
   Сравнение двух прогонов
   ========================================================================== */

function verdictLine(tag, session, verdict) {
  const top = verdict.top;
  const label = LABELS[session.label || ''] || session.label;
  if (!top) return `${tag} (${label}): страна не определилась`;
  return `${tag} (${label}): ${top.code} (по ${top.hosts} ${top.hosts === 1 ? 'хосту' : 'хостам'})`;
}

$('#compare').addEventListener('click', () => {
  const a = state.sessions.find((s) => s.id === state.pickA);
  const b = state.sessions.find((s) => s.id === state.pickB);
  if (!a || !b) return;
  const diff = diffSessions(a, b);
  state.diff = { ...diff, a, b };
  // Под экспорт по умолчанию отмечено всё, кроме шума: статика и аналитика в
  // туннеле не нужны, а снять галочку дешевле, чем вычищать список руками.
  state.chosen = new Set(diff.hosts.filter((h) => !h.noise).map((h) => h.host));
  renderDiff();
});

function renderDiff() {
  const d = state.diff;
  if (!d) return;
  $('#result').hidden = false;
  $('#verdict').innerHTML = `${esc(verdictLine('A', d.a, d.verdictA))}<br>${esc(verdictLine('B', d.b, d.verdictB))}`;

  const warn = $('#warning');
  warn.hidden = !d.indistinguishable;
  if (d.indistinguishable) {
    warn.textContent = 'Прогоны неразличимы: страна не сменилась. Скорее всего туннель '
      + 'не сработал — сравнивать нечего, всё расхождение ниже это шум.';
  }

  const rows = d.hosts.filter((h) => state.showNoise || !h.noise);
  $('#hosts-empty').hidden = rows.length > 0;
  $('#hosts-body').innerHTML = rows.map((h) => {
    const tags = [];
    if (h.analytics) tags.push('аналитика');
    if (h.staticOnly) tags.push('статика');
    return `
      <tr class="${h.noise ? 'noise' : ''}" data-host="${esc(h.host)}">
        <td><input type="checkbox" class="pick-diff"${state.chosen.has(h.host) ? ' checked' : ''}></td>
        <td class="host">${esc(h.host)}${tags.length ? ` <span class="muted small">${esc(tags.join(', '))}</span>` : ''}</td>
        ${ruleCell(h.host)}
        <td>${h.score}</td>
        <td class="tier-${esc(h.tier)}">${esc(h.tier)}</td>
        <td><ul class="signals">${h.signals.map((s) => `<li>${esc(s.detail)} <span class="muted">+${s.weight}</span></li>`).join('')}</ul></td>
      </tr>`;
  }).join('');
}

$('#show-noise').addEventListener('change', (e) => {
  state.showNoise = e.target.checked;
  renderDiff();
});

$('#hosts-body').addEventListener('change', (e) => {
  if (!e.target.classList.contains('pick-diff')) return;
  const host = e.target.closest('tr')?.dataset.host;
  if (!host) return;
  if (e.target.checked) state.chosen.add(host);
  else state.chosen.delete(host);
});

/* ==========================================================================
   Экспорт
   ========================================================================== */

/** Один экспорт на обе выдачи: формат и свёртка доменов у одиночного прогона и у
    сравнения одинаковые — plain-список, который читает слой lists. */
function showExport(hosts) {
  $('#export-box').hidden = false;
  $('#export-text').value = exportDomains(hosts).join('\n');
  $('#copy-note').textContent = '';
  $('#export-box').scrollIntoView({ block: 'nearest' });
}

$('#export').addEventListener('click', () => showExport([...state.chosen]));

$('#copy').addEventListener('click', async () => {
  const text = $('#export-text').value;
  try {
    await navigator.clipboard.writeText(text);
    $('#copy-note').textContent = 'скопировано';
  } catch {
    // Буфер в панели DevTools доступен не всегда — тогда выделяем, и человек
    // копирует сам.
    $('#export-text').select();
    $('#copy-note').textContent = 'выделено — скопируйте вручную';
  }
});

refreshSessions().then(() => {
  if (state.sessions.length >= MAX_SESSIONS) {
    console.info(`razvedka: прогонов ${state.sessions.length}, старые вытесняются`);
  }
});
renderStatus();
renderSingle();
