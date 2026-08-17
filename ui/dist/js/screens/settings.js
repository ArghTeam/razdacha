/* Настройки — не раздел навигации, а шестерёнка в шапке: «четыре экрана, не
   больше». Шесть полей, всё прочее живёт в config.yaml. */

import * as api from '../api.js';
import {
  state, toast, toastError, openModal, closeModal, modalShell,
  notice, refresh, markDirty, applyDocTitle,
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

/* Оповещения живут в общей модалке настроек, а не отдельным экраном: их
   четыре, и вход — гейт, а не пятый раздел. Токен приходит признаком
   `token_set`, само значение сервер не отдаёт. */
let notifyCfg = null;

export async function modalSettings() {
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
        <input type="number" id="s-mtu" min="1280" max="1420" value="${esc(s.client_mtu)}">
        <div class="hint">1280 — значение по умолчанию, менять без причины не стоит.
          1420 — максимальная скорость в надёжной сети.</div></div>
      <div class="field"><label for="s-dns">DNS-апстрим</label>
        <input type="text" id="s-dns" value="${esc(s.dns_upstream)}"></div>
    </div>
    <div class="field"><label for="s-int">Обновление списков</label>
      <select id="s-int">${opts}</select></div>
    <div class="field"><label for="s-check">Проверка туннелей</label>
      <select id="s-check">${checkOpts}</select>
      <div class="hint">Как часто демон сам опрашивает состояние туннелей.</div></div>
    <div class="field"><label for="s-ntf-chat">Оповещения в телеграм</label>
      <div class="hint" style="margin-bottom:6px">Бота заводите в @BotFather, затем
        добавьте его в чат и укажите идентификатор чата.</div>
      <input type="text" id="s-ntf-chat" placeholder="Идентификатор чата, например -1001234567890">
      <input type="password" id="s-ntf-token" placeholder="Токен бота" style="margin-top:6px">
      <label style="display:flex;gap:8px;align-items:center;margin-top:8px">
        <input type="checkbox" id="s-ntf-on"> Присылать оповещения</label>
      <button class="btn" data-act="test-notify" style="margin-top:8px">Отправить тестовое</button></div>
    <div class="parse-result idle">Остальное живёт в config.yaml: пул адресов, тип DNS,
      WAN-интерфейс, уровень логов.</div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-settings">Сохранить</button>`,
  ), fillNotify);
}

/* Настройки оповещений приезжают отдельным запросом: они лежат вне
   `GET /api/settings`, чтобы токен не уезжал вместе с остальными полями. */
async function fillNotify() {
  try {
    notifyCfg = await api.notify.get();
  } catch (err) {
    if (!err.missing) toastError(err);
    return;
  }
  const chat = $('#s-ntf-chat');
  const token = $('#s-ntf-token');
  const on = $('#s-ntf-on');
  if (!chat || !token || !on) return;
  chat.value = notifyCfg.chat_id || '';
  on.checked = !!notifyCfg.enabled;
  // Пустое поле с подсказкой «сохранён» честнее звёздочек: значения у нас нет.
  token.placeholder = notifyCfg.token_set ? 'Токен сохранён — оставьте пустым' : 'Токен бота';
}

/* Сохранение оповещений отделено от сохранения настроек: у них разные ручки, и
   отказ одной не должен молча съедать другую. */
async function saveNotify() {
  const chat = $('#s-ntf-chat');
  const token = $('#s-ntf-token');
  const on = $('#s-ntf-on');
  if (!chat || !token || !on) return;
  const body = { enabled: on.checked, chat_id: chat.value.trim() };
  // Пустой токен не шлём вовсе: на сервере «не прислали» означает «оставить».
  if (token.value.trim()) body.token = token.value.trim();
  notifyCfg = await api.notify.save(body);
}

async function testNotify() {
  const btn = $('#modal [data-act="test-notify"]');
  btn.disabled = true;
  try {
    // Сначала сохраняем: тестировать то, чего сервер ещё не видел, бессмысленно.
    await saveNotify();
    await api.notify.test();
    toast('Тестовое сообщение отправлено');
  } catch (err) {
    toastError(err);
  } finally {
    btn.disabled = false;
  }
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
    // Оповещения лежат за своей ручкой, но кнопка «Сохранить» одна: не
    // сохранить их отсюда значило бы соврать пользователю.
    await saveNotify();
    const res = await api.settings.update(body);
    state.settings = res && res.wg_listen_port ? res : { ...s, ...body };
    applyDocTitle();
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
  'test-notify': testNotify,
};
