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
  state.diag = await api.diag.get();
}

const CHECK_ICON = { ok: '✓', warn: '⚠', error: '✕', unknown: '?' };
const OVERALL_TEXT = {
  ok: 'Всё в порядке', warn: 'Есть замечания',
  error: 'Есть ошибки', unknown: 'Состояние неизвестно',
};

const head = () => `
  <div class="screen-head">
    <h1>Диагностика</h1>
    <div class="spacer"></div>
    <button class="btn" data-act="rerun-diag">Проверить заново</button>
  </div>`;

export function view() {
  if (state.missing.has('diag') || !state.diag) {
    return head() + `<div class="card">${notImplemented('Диагностика')}</div>`;
  }

  const d = state.diag;
  const checks = d.checks || [];
  const rows = checks.map((c) => `
    <div class="check">
      <span class="check-icon ${esc(c.status)}">${CHECK_ICON[c.status] || '?'}</span>
      <div>
        <div class="check-title">${esc(c.title)}</div>
        <div class="check-detail">${esc(c.detail || '')}</div>
      </div>
    </div>`).join('');

  return head() + `
    <div class="overall">
      <span class="check-icon ${esc(d.overall)}">${CHECK_ICON[d.overall] || '?'}</span>
      <strong>${esc(OVERALL_TEXT[d.overall] || 'Состояние неизвестно')}</strong>
      <span style="color:var(--fg-dim);font-size:13px;margin-left:auto">проверено ${esc(since(d.checked_at || new Date().toISOString()))}</span>
    </div>
    <div class="card">
      ${rows || '<div class="empty">Проверок нет.</div>'}
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

export const actions = {
  'logs': modalLogs,
  'singbox-config': modalSingboxConfig,
  'download-report': report,

  'rerun-diag': async (_id, btn) => {
    btn.disabled = true;
    btn.textContent = 'Проверяю…';
    try {
      const res = await api.diag.run();
      state.diag = res && res.checks ? res : await api.diag.get();
      refresh();
      toast('Проверки перезапущены');
    } catch (err) {
      btn.disabled = false;
      btn.textContent = 'Проверить заново';
      toastError(err, 'Проверки не перезапущены');
    }
  },
};
