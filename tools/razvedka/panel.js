/* Панель razvedka.

   Главный экран — один: что на этой странице блокируется и кто трогает страну.
   Кнопки «начать запись» нет: панель открыта — значит уже слушает
   `chrome.devtools.network.onRequestFinished`. Перезагрузка страницы очищает список
   и начинает заново ([ADR 0022](../../docs/decisions/0022-single-run-entry.md)).

   Ограничение платформы: `devtools.network` отдаёт только те запросы, что случились
   при открытом DevTools. Открыли панель после загрузки страницы — список пуст, пока
   не перезагрузите.

   Сравнение двух прогонов сохранено целиком, но убрано под свёртку внизу: для сайта,
   который режет по стране молча, другого способа нет.

   Тела ответов здесь не хранятся. Из тела в момент записи вынимаются гео-поля, и
   дальше живёт только компактная запись — иначе страница на полсотни запросов не
   влезет ни в память, ни в chrome.storage.local. */

import { hostOf, pathKey } from './lib/pathkey.js';
import { geoHeaders, geoFields, parseCdnTrace, isGeoEndpoint } from './lib/geo.js';
import { foldHost, exportDomains } from './lib/domains.js';
import { diffSessions } from './lib/diff.js';
import { scanRun, runIsSilent, defaultExportHosts } from './lib/single.js';
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

/** Перерисовка не чаще раза в 400 мс: на живом сайте запросов сотни в минуту. */
const RENDER_DELAY_MS = 400;

const state = {
  current: null, // то, что показано на главном экране
  sessions: [],
  pickA: null,
  pickB: null,
  diff: null,
  chosen: new Set(), // хосты под экспорт в сравнении
  chosenMain: new Set(), // то же на главном экране
  defaulted: new Set(), // хостам дефолт отметки уже выставлен
  showNoise: false,
};

/* ==========================================================================
   Запись — идёт сама, пока панель открыта
   ========================================================================== */

function newRun(host = '') {
  const startedAt = Date.now();
  // Метки `direct`/`tunnel` у прогона с главного экрана нет: подписывать нечего,
  // трафик идёт как есть. Метка появляется только в сравнении, там она осмысленна.
  return {
    id: `s${startedAt}-${Math.random().toString(36).slice(2, 8)}`,
    name: defaultName(host, startedAt),
    label: '',
    host,
    renamed: false,
    startedAt,
    entries: [],
  };
}

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
  const run = state.current;
  if (!run) return;

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

  run.entries.push(rec);
  // Записанное должно доехать до storage независимо от того, нашлось ли что-то в
  // теле: страница без единого гео-поля — обычное дело, и терять её нельзя.
  persistRun();
  if (!run.host) {
    run.host = host;
    if (!run.renamed) run.name = defaultName(host, run.startedAt);
  }
  scheduleRender();

  const worthBody = textualMime(rec.mime)
    && rec.bodyBytes >= 0
    && rec.bodyBytes < MAX_BODY_BYTES;
  if (!worthBody) return;

  entry.getContent((content) => {
    if (!content || content.length > MAX_BODY_BYTES) return;
    const fields = fieldsFromBody(host, path, rec.mime, content);
    if (Object.keys(fields).length) {
      rec.geoFields = fields;
      persistRun();
      scheduleRender();
    }
  });
}

const persistQueue = makePersistQueue(async () => {
  const run = state.current;
  if (!run || run.entries.length === 0) return;
  await putSession(run);
});

/** Прогон пишется в storage не на каждый запрос: раз в две секунды достаточно,
    чтобы он пережил закрытие DevTools и доехал до сравнения. */
function persistRun() {
  persistQueue.schedule();
}

const renderQueue = makePersistQueue(() => renderMain(), { delay: RENDER_DELAY_MS });

function scheduleRender() {
  renderQueue.schedule();
}

/** Перезагрузка или переход — новая страница, новый список. Прежний остаётся в
    истории: сравнению нужны именно записанные проходы. */
async function startNewRun(url) {
  const prev = state.current;
  persistQueue.cancel();
  renderQueue.cancel();
  // Новый прогон встаёт до первого `await`. Обращение к `chrome.storage.local` длится
  // миллисекунды, а запись главного документа приходит вплотную к переходу: подожди
  // мы здесь — она уедет в прогон, который строкой ниже выбрасывается, и страница на
  // один запрос покажет пустой список.
  state.current = newRun(hostOf(url || '') || '');
  state.chosenMain = new Set();
  state.defaulted = new Set();
  renderMain();
  if (prev && prev.entries.length) await putSession(prev);
  await refreshSessions();
}

chrome.devtools.network.onRequestFinished.addListener(recordEntry);
chrome.devtools.network.onNavigated.addListener((url) => { startNewRun(url); });

/* ==========================================================================
   Главный экран
   ========================================================================== */

/** Отметка по умолчанию: блокировки отмечены, гео-подсказки — нет. Разница не
    косметическая: отказ сервера наблюдаем, гео-заголовок — догадка, и молча класть
    догадку в правило панель не вправе (ADR 0022). */
function applyDefaults(scan) {
  const blocked = new Set(defaultExportHosts(scan));
  for (const host of [...blocked, ...scan.geo.map((g) => g.host)]) {
    if (state.defaulted.has(host)) continue;
    state.defaulted.add(host);
    if (blocked.has(host)) state.chosenMain.add(host);
  }
}

function pickBox(host) {
  return `<td class="pick-cell"><input type="checkbox" class="pick"${state.chosenMain.has(host) ? ' checked' : ''}></td>`;
}

/** Домен, который уедет в правило. Свёрнутый до eTLD+1 либо полный хост, если это
    общий CDN — свёрнутый `cloudfront.net` завернул бы в туннель чужие сайты. */
function ruleNote(host) {
  const fold = foldHost(host);
  if (fold.domain === host) return '';
  return ` <span class="muted">→ ${esc(fold.domain)}</span>`;
}

function renderMain() {
  const run = state.current;
  const entries = run?.entries || [];
  const scan = scanRun(entries);
  applyDefaults(scan);

  $('#site').textContent = run?.host || '—';
  $('#count').textContent = entries.length ? `· ${entries.length} запросов` : '';
  $('#hint').hidden = entries.length > 0;

  $('#blocked-empty').hidden = scan.blocked.length > 0;
  $('#blocked').hidden = scan.blocked.length === 0;
  $('#blocked-body').innerHTML = scan.blocked.map((b) => `
    <tr data-host="${esc(b.host)}">
      ${pickBox(b.host)}
      <td class="host">${esc(b.host)}${ruleNote(b.host)}</td>
      <td class="what">${b.statuses.map((s) => `<span class="badge">${esc(s.status)}</span> ×${s.count}`).join(' ')}</td>
    </tr>`).join('');

  $('#geo-empty').hidden = scan.geo.length > 0;
  $('#geo').hidden = scan.geo.length === 0;
  $('#geo-note').hidden = scan.geo.length === 0;
  $('#geo-body').innerHTML = scan.geo.map((g) => {
    const found = [...g.headers, ...g.fields].map((s) => {
      const more = s.more ? ` <span class="muted">и ещё ${s.more}</span>` : '';
      return `<div><code>${esc(s.name)}</code>: ${esc(s.values.join(', '))}${more}</div>`;
    }).join('');
    return `
      <tr data-host="${esc(g.host)}">
        ${pickBox(g.host)}
        <td class="host">${esc(g.host)}${ruleNote(g.host)}</td>
        <td class="what">${found}</td>
      </tr>`;
  }).join('');

  // Ни отказов, ни упоминаний страны — это не «всё в порядке»: сайт умеет резать
  // молча. Одной строкой, без крупного блока и без зова в сравнение.
  $('#silent').hidden = entries.length === 0 || !runIsSilent(scan);

  renderCopyButton();
}

function renderCopyButton() {
  const domains = exportDomains([...state.chosenMain]);
  const btn = $('#copy-domains');
  btn.disabled = domains.length === 0;
  btn.textContent = domains.length
    ? `Скопировать ${domains.length} ${plural(domains.length, 'домен', 'домена', 'доменов')}`
    : 'Скопировать домены';
}

function plural(n, one, few, many) {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return one;
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few;
  return many;
}

document.addEventListener('change', (e) => {
  if (!e.target.classList?.contains('pick')) return;
  const host = e.target.closest('tr')?.dataset.host;
  if (!host) return;
  if (e.target.checked) state.chosenMain.add(host);
  else state.chosenMain.delete(host);
  renderCopyButton();
  $('#copy-note').textContent = '';
});

$('#clear').addEventListener('click', async () => {
  const run = state.current;
  persistQueue.cancel();
  renderQueue.cancel();
  // Та же причина, что в `startNewRun`: подмена прогона идёт до похода в storage.
  state.current = newRun();
  state.chosenMain = new Set();
  state.defaulted = new Set();
  $('#export-text').hidden = true;
  $('#copy-note').textContent = '';
  renderMain();
  if (run) await removeSession(run.id);
  await refreshSessions();
});

$('#copy-domains').addEventListener('click', async () => {
  const text = exportDomains([...state.chosenMain]).join('\n');
  await copyOut(text);
});

/** Список доменов в буфер. Буфер в панели DevTools доступен не всегда — тогда
    показываем текст, и человек копирует сам. */
async function copyOut(text) {
  const area = $('#export-text');
  try {
    await navigator.clipboard.writeText(text);
    area.hidden = true;
    $('#copy-note').textContent = 'скопировано';
  } catch {
    area.hidden = false;
    area.value = text;
    area.select();
    $('#copy-note').textContent = 'выделено — скопируйте вручную';
  }
}

/* ==========================================================================
   Сравнение двух прогонов — под свёрткой внизу
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
  const list = state.sessions;
  $('#sessions-empty').hidden = list.length > 0;
  $('#sessions').hidden = list.length === 0;
  $('#sessions-body').innerHTML = list.map((s) => `
    <tr data-id="${esc(s.id)}">
      <td><input type="radio" name="pickA" value="${esc(s.id)}"${state.pickA === s.id ? ' checked' : ''}></td>
      <td><input type="radio" name="pickB" value="${esc(s.id)}"${state.pickB === s.id ? ' checked' : ''}></td>
      <td><input type="text" class="name" value="${esc(s.name)}" size="20"></td>
      <td>
        <select class="label">
          ${Object.entries(LABELS).map(([k, v]) => `
            <option value="${k}"${(s.label || '') === k ? ' selected' : ''}>${v}</option>`).join('')}
        </select>
      </td>
      <td>${s.entries?.length || 0}</td>
      <td class="muted">${stamp(s.startedAt)}</td>
      <td><button class="drop">удалить</button></td>
    </tr>`).join('');
  $('#compare').disabled = !(state.pickA && state.pickB && state.pickA !== state.pickB);
}

$('#sessions-body').addEventListener('change', async (e) => {
  const id = e.target.closest('tr')?.dataset.id;
  if (!id) return;
  if (e.target.name === 'pickA') state.pickA = id;
  if (e.target.name === 'pickB') state.pickB = id;
  const session = state.sessions.find((s) => s.id === id);
  if (session && e.target.classList.contains('name')) {
    session.name = e.target.value;
    session.renamed = true;
    await putSession(session);
  }
  if (session && e.target.classList.contains('label')) {
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

$('#sessions-clear').addEventListener('click', async () => {
  if (!confirm(`Удалить все прогоны (${state.sessions.length})?`)) return;
  state.pickA = null;
  state.pickB = null;
  state.diff = null;
  $('#result').hidden = true;
  await clearSessions();
  await refreshSessions();
});

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
    const fold = foldHost(h.host);
    const note = fold.reason ? ` <span class="muted small">(${esc(fold.reason)})</span>` : '';
    const tags = [];
    if (h.analytics) tags.push('аналитика');
    if (h.staticOnly) tags.push('статика');
    return `
      <tr class="${h.noise ? 'noise' : ''}" data-host="${esc(h.host)}">
        <td><input type="checkbox" class="pick-diff"${state.chosen.has(h.host) ? ' checked' : ''}></td>
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
  if (!e.target.classList.contains('pick-diff')) return;
  const host = e.target.closest('tr')?.dataset.host;
  if (!host) return;
  if (e.target.checked) state.chosen.add(host);
  else state.chosen.delete(host);
});

$('#export').addEventListener('click', async () => {
  await copyOut(exportDomains([...state.chosen]).join('\n'));
});

/* ==========================================================================
   Старт
   ========================================================================== */

state.current = newRun();
renderMain();
refreshSessions().then(() => {
  if (state.sessions.length >= MAX_SESSIONS) {
    console.info(`razvedka: прогонов ${state.sessions.length}, старые вытесняются`);
  }
});
