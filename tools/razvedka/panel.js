/* Панель razvedka: запись сетевой сессии, сравнение двух прогонов, экспорт списка
   доменов.

   Тела ответов здесь не хранятся. Из тела в момент записи вынимаются гео-поля, и
   дальше живёт только компактная запись — иначе сессия на полсотни страниц не
   влезет ни в память, ни в chrome.storage.local. */

import { hostOf, pathKey } from './lib/pathkey.js';
import { geoHeaders, geoFields, parseCdnTrace, isGeoEndpoint } from './lib/geo.js';
import { foldHost, exportDomains } from './lib/domains.js';
import { diffSessions } from './lib/diff.js';
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

const state = {
  recording: null, // текущая сессия или null
  sessions: [],
  pickA: null,
  pickB: null,
  diff: null,
  chosen: new Set(), // хосты, отмеченные под экспорт
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

function startRecording() {
  const startedAt = Date.now();
  state.recording = {
    id: `s${startedAt}-${Math.random().toString(36).slice(2, 8)}`,
    name: defaultName('', startedAt),
    label: $('#rec-label').value,
    host: '',
    renamed: false,
    startedAt,
    entries: [],
  };
  renderStatus();
  persistRecording();
}

async function stopRecording() {
  const session = state.recording;
  state.recording = null;
  persistQueue.cancel(); // висящий таймер не должен продублировать финальную запись
  if (session) await putSession(session);
  renderStatus();
  await refreshSessions();
}

function renderStatus() {
  const rec = state.recording;
  $('#rec-start').hidden = !!rec;
  $('#rec-stop').hidden = !rec;
  $('#rec-label').disabled = !!rec;
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
   Список сессий
   ========================================================================== */

const LABELS = { direct: 'напрямую', tunnel: 'через туннель' };

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
  $('#sessions-empty').hidden = list.length > 0;
  $('#sessions').hidden = list.length === 0;
  body.innerHTML = list.map((s) => `
    <tr data-id="${esc(s.id)}">
      <td><input type="radio" name="pickA" value="${esc(s.id)}"${state.pickA === s.id ? ' checked' : ''}></td>
      <td><input type="radio" name="pickB" value="${esc(s.id)}"${state.pickB === s.id ? ' checked' : ''}></td>
      <td><input type="text" class="name" value="${esc(s.name)}" size="26"></td>
      <td>
        <select class="label">
          ${Object.entries(LABELS).map(([k, v]) => `
            <option value="${k}"${s.label === k ? ' selected' : ''}>${v}</option>`).join('')}
        </select>
      </td>
      <td>${s.entries?.length || 0}</td>
      <td class="muted">${stamp(s.startedAt)}</td>
      <td><button class="drop">удалить</button></td>
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
  if (!e.target.classList.contains('drop')) return;
  const id = e.target.closest('tr')?.dataset.id;
  if (!id) return;
  if (state.pickA === id) state.pickA = null;
  if (state.pickB === id) state.pickB = null;
  await removeSession(id);
  await refreshSessions();
});

$('#rec-start').addEventListener('click', startRecording);
$('#rec-stop').addEventListener('click', stopRecording);

$('#sessions-clear').addEventListener('click', async () => {
  if (!confirm(`Удалить все сессии (${state.sessions.length})?`)) return;
  state.pickA = null;
  state.pickB = null;
  state.diff = null;
  $('#result').hidden = true;
  $('#export-box').hidden = true;
  await clearSessions();
  await refreshSessions();
});

/* ==========================================================================
   Сравнение
   ========================================================================== */

function verdictLine(tag, session, verdict) {
  const top = verdict.top;
  const label = LABELS[session.label] || session.label;
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
    const fold = foldHost(h.host);
    const note = fold.reason ? ` <span class="muted small">(${esc(fold.reason)})</span>` : '';
    const tags = [];
    if (h.analytics) tags.push('аналитика');
    if (h.staticOnly) tags.push('статика');
    return `
      <tr class="${h.noise ? 'noise' : ''}" data-host="${esc(h.host)}">
        <td><input type="checkbox" class="pick"${state.chosen.has(h.host) ? ' checked' : ''}></td>
        <td class="host">${esc(h.host)}${tags.length ? ` <span class="muted small">${esc(tags.join(', '))}</span>` : ''}</td>
        <td class="domain">${esc(fold.domain)}${note}</td>
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
  if (!e.target.classList.contains('pick')) return;
  const host = e.target.closest('tr')?.dataset.host;
  if (!host) return;
  if (e.target.checked) state.chosen.add(host);
  else state.chosen.delete(host);
});

/* ==========================================================================
   Экспорт
   ========================================================================== */

$('#export').addEventListener('click', () => {
  $('#export-box').hidden = false;
  $('#export-text').value = exportDomains([...state.chosen]).join('\n');
  $('#copy-note').textContent = '';
});

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
    console.info(`razvedka: сессий ${state.sessions.length}, старые вытесняются`);
  }
});
renderStatus();
