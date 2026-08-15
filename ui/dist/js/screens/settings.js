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
    <div class="field"><label>Резервная копия состояния</label>
      <div class="hint" style="margin-bottom:6px">В файле лежит всё: настройки, туннели,
        правила и <b>приватные ключи всех пиров</b> — то есть доступ ко всему VPN.
        Храните его как пароль.</div>
      <a class="btn" href="${esc(api.backup.DOWNLOAD_PATH)}" download>Скачать состояние</a>
      <div class="hint" style="margin-top:10px">Копия может уезжать в тот же чат телеграма
        по расписанию. Наружу она уходит только зашифрованной — задайте фразу.</div>
      <input type="password" id="s-bak-phrase" placeholder="Парольная фраза" style="margin-top:6px">
      <div class="hint"><b>Забудете фразу — копия бесполезна.</b> Расшифровать её нечем:
        ключ считается из фразы и больше нигде не хранится. Запишите её отдельно от чата,
        куда приходят копии.</div>
      <select id="s-bak-int" style="margin-top:8px">${backupOpts(0)}</select>
      <label style="display:flex;gap:8px;align-items:center;margin-top:8px">
        <input type="checkbox" id="s-bak-on"> Присылать копию в телеграм</label>
      <button class="btn" data-act="send-backup" style="margin-top:8px">Отправить сейчас</button>
      <div class="hint" id="s-bak-last"></div></div>
    <div class="parse-result idle">Остальное живёт в config.yaml: пул адресов, тип DNS,
      WAN-интерфейс, уровень логов.</div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-settings">Сохранить</button>`,
  ), fillSecrets);
}

/* Копия состояния — рядом с оповещениями: канал у них один, и настраивают их
   в один заход. Интервалы редкие: каждая отправка кладёт в чат файл со всеми
   ключами пиров, и «каждый час» здесь не то, что стоит предлагать первым. */
const BACKUP_INTERVALS = [
  [6, 'каждые 6 часов'],
  [24, 'раз в сутки'],
  [168, 'раз в неделю'],
];

function backupOpts(hours) {
  const cur = Number(hours) || 24;
  return BACKUP_INTERVALS.map(([v, label]) =>
    `<option value="${v}" ${cur === v ? 'selected' : ''}>${label}</option>`).join('');
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

/* Обе секретные секции заполняются одним заходом после открытия модалки: у них
   свои ручки, и обе отдают только признаки «сохранено», а не значения. */
async function fillSecrets() {
  await fillNotify();
  await fillBackup();
}

let backupCfg = null;

async function fillBackup() {
  try {
    backupCfg = await api.backup.get();
  } catch (err) {
    if (!err.missing) toastError(err);
    return;
  }
  const phrase = $('#s-bak-phrase');
  const interval = $('#s-bak-int');
  const on = $('#s-bak-on');
  const last = $('#s-bak-last');
  if (!phrase || !interval || !on) return;
  on.checked = !!backupCfg.enabled;
  interval.innerHTML = backupOpts(backupCfg.interval_hours);
  // Пустое поле с подсказкой «сохранена» честнее звёздочек: значения у нас нет.
  phrase.placeholder = backupCfg.passphrase_set
    ? 'Фраза сохранена — оставьте пустым' : 'Парольная фраза';
  if (last) last.innerHTML = backupStatus(backupCfg);
}

/* Пустота не заполняется выдумкой: копию ещё не отправляли — так и говорим. */
function backupStatus(cfg) {
  if (!cfg) return '';
  const parts = [];
  parts.push(cfg.last_sent_at
    ? `Последняя копия ушла ${esc(new Date(cfg.last_sent_at).toLocaleString())}.`
    : 'Копию ещё не отправляли.');
  if (cfg.last_error) parts.push(`Последняя ошибка: ${esc(cfg.last_error)}`);
  if (!cfg.telegram_ready) parts.push('Бот телеграма не настроен — отправлять некуда.');
  return parts.join(' ');
}

async function saveBackup() {
  const phrase = $('#s-bak-phrase');
  const interval = $('#s-bak-int');
  const on = $('#s-bak-on');
  if (!phrase || !interval || !on) return;
  const body = { enabled: on.checked, interval_hours: Number(interval.value) };
  // Пустую фразу не шлём вовсе: на сервере «не прислали» означает «оставить».
  if (phrase.value.trim()) body.passphrase = phrase.value.trim();
  backupCfg = await api.backup.save(body);
}

async function sendBackup() {
  const btn = $('#modal [data-act="send-backup"]');
  btn.disabled = true;
  try {
    // Сначала сохраняем: отправлять то, чего сервер ещё не видел, бессмысленно.
    await saveBackup();
    await api.backup.send();
    toast('Копия отправлена в телеграм');
    await fillBackup();
  } catch (err) {
    toastError(err);
  } finally {
    btn.disabled = false;
  }
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
    // Оповещения и копия лежат за своими ручками, но кнопка «Сохранить» одна:
    // не сохранить их отсюда значило бы соврать пользователю.
    await saveNotify();
    await saveBackup();
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
  'test-notify': testNotify,
  'send-backup': sendBackup,
};
