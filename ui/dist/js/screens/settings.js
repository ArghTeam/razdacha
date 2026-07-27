/* Настройки — не раздел навигации, а шестерёнка в шапке: «четыре экрана, не
   больше». Шесть полей, всё прочее живёт в config.yaml. */

import * as api from '../api.js';
import {
  state, toast, toastError, openModal, closeModal, modalShell,
  notice, refresh, markDirty,
} from '../shell.js';
import { $, esc, intervalSeconds } from '../util.js';

const INTERVALS = [
  [3600, 'каждый час'],
  [21600, 'каждые 6 часов'],
  [86400, 'раз в сутки'],
  [604800, 'раз в неделю'],
];

/* Проверка туннелей — не то же самое, что обновление списков: тут единицы —
   минуты, а нижняя граница в 30 секунд стоит в демоне, потому что каждый прогон
   пробивает обычные туннели настоящим запросом (ADR 0011). */
const CHECK_INTERVALS = [
  [60, 'каждую минуту'],
  [120, 'каждые 2 минуты'],
  [300, 'каждые 5 минут'],
  [900, 'каждые 15 минут'],
];

export function modalSettings() {
  const s = state.settings;
  if (!s) {
    openModal(modalShell('Настройки',
      notice('Настройки появятся позже',
        'Демон ещё не отдаёт GET /api/settings — показывать нечего и менять нечего.'),
      '<button class="btn" data-act="close-modal">Закрыть</button>'));
    return;
  }

  const interval = intervalSeconds(s.list_update_interval);
  const opts = INTERVALS.map(([v, label]) =>
    `<option value="${v}" ${interval === v ? 'selected' : ''}>${label}</option>`).join('');

  const checkEvery = Number(s.tunnel_check_interval) || 120;
  const checkOpts = CHECK_INTERVALS.map(([v, label]) =>
    `<option value="${v}" ${checkEvery === v ? 'selected' : ''}>${label}</option>`).join('');

  openModal(modalShell('Настройки', `
    <div class="two-col">
      <div class="field"><label for="s-port">Порт WireGuard</label>
        <input type="number" id="s-port" value="${esc(s.wg_listen_port)}"></div>
      <div class="field"><label for="s-host">Внешний адрес</label>
        <input type="text" id="s-host" value="${esc(s.endpoint_host)}"></div>
      <div class="field"><label for="s-mtu">MTU клиентов</label>
        <input type="number" id="s-mtu" value="${esc(s.client_mtu)}">
        <div class="hint">1280 — значение по умолчанию, менять без причины не стоит.</div></div>
      <div class="field"><label for="s-dns">DNS-апстрим</label>
        <input type="text" id="s-dns" value="${esc(s.dns_upstream)}"></div>
    </div>
    <div class="field"><label for="s-int">Обновление списков</label>
      <select id="s-int">${opts}</select></div>
    <div class="field"><label for="s-check">Проверка туннелей</label>
      <select id="s-check">${checkOpts}</select>
      <div class="hint">Как часто демон сам опрашивает состояние туннелей.</div></div>
    <div class="parse-result idle">Остальное живёт в config.yaml: пул адресов, тип DNS,
      WAN-интерфейс, уровень логов.</div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-settings">Сохранить</button>`));
}

async function saveSettings() {
  const s = state.settings;
  const body = {
    wg_listen_port: Number($('#s-port').value) || s.wg_listen_port,
    endpoint_host: $('#s-host').value.trim() || s.endpoint_host,
    client_mtu: Number($('#s-mtu').value) || s.client_mtu,
    dns_upstream: $('#s-dns').value.trim() || s.dns_upstream,
    list_update_interval: Number($('#s-int').value),
    tunnel_check_interval: Number($('#s-check').value),
  };
  const btn = $('#modal [data-act="save-settings"]');
  btn.disabled = true;
  try {
    const res = await api.settings.update(body);
    state.settings = res && res.wg_listen_port ? res : { ...s, ...body };
    closeModal();
    markDirty();
    refresh();
    // Порт, пул и MTU меняют клиентские конфиги: сервер говорит об этом явно.
    if (res && res.requires_client_reconfig) {
      toast('Клиентам нужно перевыдать конфиги: изменены порт, пул или MTU', 'err');
    } else {
      toast('Настройки сохранены');
    }
  } catch (err) {
    btn.disabled = false;
    toastError(err, 'Настройки не сохранены');
  }
}

export const actions = {
  'save-settings': saveSettings,
};
