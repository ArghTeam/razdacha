/* Экран «Диагностика». Отчёт собирается на клиенте и маскирует ключи, внешние
   адреса и UUID: такие отчёты вставляют в публичные чаты. */

import * as api from '../api.js';
import {
  state, tunnelById, toast, toastError, openModal, modalShell,
  notImplemented, refresh,
} from '../shell.js';
import { esc, since, isOnline, maskIP, maskKey, download, tunnelEndpoint } from '../util.js';

export const title = 'Диагностика';

export async function load() {
  // Пока идёт перезапуск, снимок с сервера не подменяет строки: он относится к
  // прошлой проверке, и часть строк сейчас честно говорит «проверяется».
  if (run) return;
  state.diag = await api.diag.get();
}

const CHECK_ICON = { ok: '✓', warn: '⚠', error: '✕', unknown: '?' };
const OVERALL_TEXT = {
  ok: 'Всё в порядке', warn: 'Есть замечания',
  error: 'Есть ошибки', unknown: 'Состояние неизвестно',
};

/** Ранг статусов из docs/05-api.md: «неизвестно» хуже ok, но лучше warn.
    Считать общий статус приходится здесь: при прогоне по одной проверке сервер
    видит только её и собрать сводку из семи не может. */
const RANK = { ok: 0, unknown: 1, warn: 2, error: 3 };

const worst = (checks) => (checks || []).reduce(
  (acc, c) => ((RANK[c.status] ?? 1) > (RANK[acc] ?? 0) ? c.status : acc), 'ok');

/* --- ход перезапуска ------------------------------------------------------
   Экран перерисовывается целиком, поэтому ход живёт в модуле, а не в DOM:
   после каждой ответившей проверки вызывается refresh(), и разметка собирается
   заново из этого объекта. */

/** Идёт перезапуск: { ids, titles, done: Map(id → проверка), current }. */
let run = null;

export function view() {
  if (state.missing.has('diag') || !state.diag) {
    return head() + `<div class="card">${notImplemented('Диагностика')}</div>`;
  }
  return head() + overallBar() + `
    <div class="card">
      ${rows() || '<div class="empty">Проверок нет.</div>'}
      <div class="toolbar">
        <button class="btn" data-act="download-report">Скачать отчёт</button>
        <button class="btn" data-act="logs">Логи</button>
        <button class="btn" data-act="singbox-config">Конфиг sing-box</button>
      </div>
    </div>
    <p class="screen-sub" style="margin-top:10px">
      В отчёте маскируются ключи, внешние адреса эндпоинтов и UUID: такие отчёты
      вставляют в публичные чаты.
    </p>`;
}

function head() {
  if (!run) {
    return `
      <div class="screen-head">
        <h1>Диагностика</h1>
        <div class="spacer"></div>
        <button class="btn" data-act="rerun-diag">Проверить заново</button>
      </div>`;
  }
  const total = run.ids.length;
  const done = run.done.size;
  return `
    <div class="screen-head">
      <h1>Диагностика</h1>
      <div class="spacer"></div>
      <button class="btn" data-act="rerun-diag" disabled aria-busy="true">
        Проверяю… ${done}&nbsp;/&nbsp;${total}</button>
    </div>
    <div class="diag-progress" role="progressbar" aria-label="Ход проверки"
         aria-valuemin="0" aria-valuemax="${total}" aria-valuenow="${done}">
      <div class="diag-progress-fill" style="width:${Math.round((done / total) * 100)}%"></div>
    </div>`;
}

/** Строки сводки. Во время перезапуска ответившие показывают новый результат,
    остальные — «проверяется»: показывать прошлый результат как новый нельзя. */
function rows() {
  const list = run
    ? run.ids.map((id) => run.done.get(id) || { id, title: run.titles.get(id), pending: true })
    : (state.diag.checks || []);

  return list.map((c) => `
    <div class="check${c.pending ? ' pending' : ''}">
      ${c.pending
    ? '<span class="check-icon check-spin" aria-hidden="true"></span>'
    : `<span class="check-icon ${esc(c.status)}">${CHECK_ICON[c.status] || '?'}</span>`}
      <div>
        <div class="check-title">${esc(c.title)}</div>
        <div class="check-detail">${c.pending
    ? (c.id === run.current ? 'проверяется…' : 'в очереди')
    : esc(c.detail || '')}</div>
      </div>
    </div>`).join('');
}

/** Когда проверяли. Секунды сразу после прогона читаются как сбой отсчёта,
    поэтому первые полминуты — «только что». */
function checkedAgo(iso) {
  const age = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(age)) return 'время проверки неизвестно';
  return age < 30_000 ? 'проверено только что' : `проверено ${since(iso)}`;
}

/** Общий статус и время последней проверки. Время берётся из ответа сервера:
    подставлять часы браузера — значит выдавать старый снимок за свежий. */
function overallBar() {
  if (run) {
    return `
      <div class="overall">
        <span class="check-icon check-spin" aria-hidden="true"></span>
        <strong>Идёт проверка</strong>
        <span class="overall-time">${esc(run.titles.get(run.current) || '')}</span>
      </div>`;
  }
  const d = state.diag;
  const status = d.overall || worst(d.checks);
  return `
    <div class="overall">
      <span class="check-icon ${esc(status)}">${CHECK_ICON[status] || '?'}</span>
      <strong>${esc(OVERALL_TEXT[status] || 'Состояние неизвестно')}</strong>
      <span class="overall-time">${d.checked_at
    ? esc(checkedAgo(d.checked_at))
    : 'время проверки неизвестно'}</span>
    </div>`;
}

/* --- логи и конфиг -------------------------------------------------------- */

function modalLogs() {
  openModal(modalShell('Логи', `
    <div class="radios">
      <label class="radio-pill"><input type="radio" name="logsrc" value="razdachad" checked> razdachad</label>
      <label class="radio-pill"><input type="radio" name="logsrc" value="sing-box"> sing-box</label>
    </div>
    <pre class="pane" id="log-pane">Загружаю…</pre>`,
  '<button class="btn" data-act="close-modal">Закрыть</button>'),
  (m) => {
    const pane = m.querySelector('#log-pane');
    const upd = async () => {
      const src = m.querySelector('input[name="logsrc"]:checked').value;
      pane.textContent = 'Загружаю…';
      try {
        pane.textContent = api.logLines(await api.diag.logs(src)).join('\n');
      } catch (err) {
        pane.textContent = err.missing
          ? 'Логи появятся позже: GET /api/logs ещё не реализован.'
          : err.message;
      }
    };
    m.querySelectorAll('input[name="logsrc"]').forEach((e) => e.addEventListener('change', upd));
    upd();
  });
}

function modalSingboxConfig() {
  openModal(modalShell('Конфиг sing-box', `
    <div class="parse-result idle">Только просмотр: конфиг генерируется целиком из состояния
      и никогда не патчится вручную.</div>
    <pre class="pane" id="cfg-pane">Загружаю…</pre>`,
  '<button class="btn" data-act="close-modal">Закрыть</button>'),
  (m) => {
    const pane = m.querySelector('#cfg-pane');
    api.diag.singboxConfig()
      .then((text) => { pane.textContent = text; })
      .catch((err) => {
        pane.textContent = err.missing
          ? 'Конфиг появится позже: GET /api/diag/singbox-config ещё не реализован.'
          : err.message;
      });
  });
}

/* --- отчёт ---------------------------------------------------------------- */

function report() {
  const s = state.settings || {};
  const d = state.diag || { overall: 'unknown', checks: [] };
  const out = [
    'razdacha — отчёт диагностики',
    `дата: ${new Date().toISOString()}`,
    `общий статус: ${d.overall}`,
    '',
    '## Проверки',
    ...(d.checks || []).map((c) => `${String(c.status).padEnd(5)} ${c.title}: ${c.detail || ''}`),
    '',
    '## Настройки',
    `порт wg: ${s.wg_listen_port ?? '—'}`,
    'внешний адрес: <замаскирован>',
    `MTU клиентов: ${s.client_mtu ?? '—'}`,
    `DNS: ${s.dns_upstream ?? '—'} (${s.dns_type ?? '—'})`,
    '',
    '## Туннели',
    ...state.tunnels.map((t) =>
      `${t.name}: ${t.type}, ${maskIP(tunnelEndpoint(t))}, ${t.status ?? 'unknown'}${t.latency_ms ? `, ${t.latency_ms} мс` : ''}`),
    '',
    '## Клиенты',
    ...state.peers.map((p) =>
      `${p.name}: ${p.address}, ключ ${maskKey(p.public_key)}, endpoint ${maskIP(p.endpoint) || '—'}, ${isOnline(p) ? 'онлайн' : 'офлайн'}`),
    '',
    '## Правила',
    ...state.rules.map((r, i) =>
      `${i + 1}. ${r.name} → ${r.action}${r.tunnel_id ? ' (' + ((tunnelById(r.tunnel_id) || {}).name || '?') + ')' : ''}${r.enabled ? '' : ' [выкл]'}`),
    '',
  ].join('\n');
  download('razdacha-diag.txt', out);
  toast('Отчёт скачан, ключи и адреса замаскированы');
}

/* --- перезапуск ----------------------------------------------------------- */

/**
 * Прогнать проверки по одной, показывая ход.
 *
 * Порядок берётся из последней сводки: список проверок задаёт демон, а не UI,
 * и зашитый здесь перечень разъехался бы с ним при первой же новой проверке.
 * Проверки идут последовательно — так видно, какая выполняется сейчас; они
 * локальные и быстрые, параллелить нечего.
 */
async function rerun() {
  if (run) return; // повторное нажатие не плодит запросов
  const base = (state.diag && state.diag.checks) || [];
  if (!base.length) return rerunAll();

  run = {
    ids: base.map((c) => c.id),
    titles: new Map(base.map((c) => [c.id, c.title])),
    done: new Map(),
    current: base[0].id,
  };
  refresh();

  let checkedAt = null;
  let failed = 0;
  for (const id of run.ids) {
    run.current = id;
    refresh();
    try {
      const res = await api.diag.run(id);
      const list = (res && res.checks) || [];
      // Демон без поддержки `?check=` вернёт всю сводку — берём свою строку.
      const c = list.find((x) => x.id === id) || (list.length === 1 ? list[0] : null);
      run.done.set(id, c || unanswered(id, 'демон не вернул эту проверку'));
      if (res && res.checked_at) checkedAt = res.checked_at;
    } catch (err) {
      failed++;
      run.done.set(id, unanswered(id, err.message));
    }
  }

  const checks = run.ids.map((id) => run.done.get(id));
  run = null;
  state.diag = { checks, overall: worst(checks), checked_at: checkedAt };
  refresh();
  if (failed) toast(`Проверок не ответило: ${failed}`, 'err');
  else toast('Проверки перезапущены');
}

/** Проверка не ответила. Статус «неизвестно» с причиной, а не прошлый
    результат: невыполненная проверка — это не «всё хорошо». */
const unanswered = (id, why) => ({
  id,
  title: (run && run.titles.get(id)) || id,
  status: 'unknown',
  detail: `проверка не ответила: ${why}`,
});

/** Запасной путь: сводки ещё нет, перечня проверок тоже — просим всё разом. */
async function rerunAll() {
  try {
    const res = await api.diag.run();
    state.diag = res && res.checks ? res : await api.diag.get();
    refresh();
    toast('Проверки перезапущены');
  } catch (err) {
    toastError(err, 'Проверки не перезапущены');
  }
}

export const actions = {
  'logs': modalLogs,
  'singbox-config': modalSingboxConfig,
  'download-report': report,
  'rerun-diag': rerun,
};
