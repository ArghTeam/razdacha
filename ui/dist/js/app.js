/* Точка входа панели: вход, маршрутизация между четырьмя экранами и загрузка
   данных.

   Вход — гейт, а не пятый раздел: пока сессия не подтверждена, панели не видно
   вовсе. Любой 401 от любого запроса возвращает сюда же, а адрес экрана остаётся
   в хеше, поэтому после входа пользователь попадает туда, куда шёл. */

import * as api from './api.js';
import {
  state, toast, toastError, closeModal, closeMenu, modalOpen, loading, applyDocTitle,
} from './shell.js';
import { $, $$, compareVersions, versionLabel } from './util.js';

import * as peersScreen from './screens/peers.js';
import * as tunnelsScreen from './screens/tunnels.js';
import * as rulesScreen from './screens/rules.js';
import * as diagScreen from './screens/diag.js';
import * as settingsScreen from './screens/settings.js';

const SCREENS = {
  peers: peersScreen,
  tunnels: tunnelsScreen,
  rules: rulesScreen,
  diag: diagScreen,
};

const ORDER = ['peers', 'tunnels', 'rules', 'diag'];

/* Реестр действий. Каждый экран объявляет свои — делегирование одно на документ. */
const ACTIONS = Object.assign(
  {},
  peersScreen.actions,
  tunnelsScreen.actions,
  rulesScreen.actions,
  diagScreen.actions,
  settingsScreen.actions,
  { 'close-modal': closeModal },
);

let pollTimer = null;

/* ==========================================================================
   Вход
   ========================================================================== */

function showGate(message) {
  stopPolling();
  closeModal();
  $('#app').hidden = true;
  $('#gate').hidden = false;
  const err = $('#login-error');
  if (message) {
    err.textContent = message;
    err.hidden = false;
  } else {
    err.hidden = true;
  }
  $('#password').focus();
}

function showApp() {
  $('#gate').hidden = true;
  $('#app').hidden = false;
  $('#password').value = '';
}

/** Блокировка после серии неудач длится минуты, а не секунды: голое число
    секунд на кнопке читается как ошибка, поэтому минуты — «мм:сс». */
function countdown(sec) {
  if (sec < 60) return `${sec} с`;
  const m = Math.floor(sec / 60);
  const s = String(sec % 60).padStart(2, '0');
  return `${m}:${s}`;
}

/** Обратный отсчёт блокировки: пока он идёт, кнопка входа выключена. */
let lockTimer = null;

function lockLogin(seconds, baseMessage) {
  const btn = $('#login-submit');
  const err = $('#login-error');
  clearInterval(lockTimer);
  let left = Math.max(1, Math.round(seconds));
  const tick = () => {
    if (left <= 0) {
      clearInterval(lockTimer);
      lockTimer = null;
      btn.disabled = false;
      btn.textContent = 'Войти';
      err.textContent = 'Можно попробовать снова.';
      return;
    }
    btn.disabled = true;
    btn.textContent = `Подождите ${countdown(left)}`;
    err.hidden = false;
    err.textContent = baseMessage;
    left--;
  };
  tick();
  lockTimer = setInterval(tick, 1000);
}

async function handleLogin(ev) {
  ev.preventDefault();
  const input = $('#password');
  const btn = $('#login-submit');
  const err = $('#login-error');
  const password = input.value;
  if (!password) {
    err.hidden = false;
    err.textContent = 'Введите пароль.';
    return;
  }

  btn.disabled = true;
  btn.textContent = 'Проверяю…';
  err.hidden = true;
  try {
    await api.session.login(password);
    // Пароль не задерживается ни в DOM, ни в истории: форма никуда не
    // отправляется сама, отправка идёт fetch-ом телом запроса.
    input.value = '';
    btn.disabled = false;
    btn.textContent = 'Войти';
    showApp();
    await start();
  } catch (e) {
    input.value = '';
    input.focus();
    if (e.status === 429) {
      // Retry-After — единственный источник срока блокировки; текст приходит
      // с сервера на русском и показывается как есть.
      lockLogin(e.retryAfter || 60, e.message);
      return;
    }
    btn.disabled = false;
    btn.textContent = 'Войти';
    err.hidden = false;
    err.textContent = e.status === 401 ? 'Неверный пароль.' : e.message;
  }
}

/* ==========================================================================
   Загрузка данных
   ========================================================================== */

/** Тянет кусок состояния, помечая ненаписанный эндпоинт вместо выдумывания нулей. */
async function pull(key, loader, fallback) {
  try {
    const value = await loader();
    state.missing.delete(key);
    setOffline(false);
    return value;
  } catch (err) {
    if (err.status === 401) throw err;
    if (err.missing) {
      state.missing.add(key);
      return fallback;
    }
    if (err.offline) {
      setOffline(true);
      return fallback;
    }
    toastError(err);
    return fallback;
  }
}

async function loadAll() {
  const [settings, peers, tunnels, rules, community, version] = await Promise.all([
    pull('settings', api.settings.get, null),
    pull('peers', api.peers.list, []),
    pull('tunnels', api.tunnels.list, []),
    pull('rules', api.rules.list, []),
    pull('community', api.lists.community, []),
    pull('version', api.version.get, null),
  ]);
  state.version = version;
  state.settings = settings;
  applyDocTitle();
  state.peers = peers || [];
  state.tunnels = tunnels || [];
  state.rules = rules || [];
  state.communityLists = community || [];
  state.diag = await pull('diag', api.diag.get, null);
  await refreshApplyStatus();
}

async function refreshApplyStatus() {
  const status = await pull('apply', api.apply.status, null);
  if (state.missing.has('apply')) return; // плашка остаётся на локальной отметке
  state.dirty = api.isDirty(status);
  $('#apply-bar').hidden = !state.dirty;
}

/* ==========================================================================
   Отрисовка
   ========================================================================== */

function render() {
  $$('.tab').forEach((t) => t.setAttribute('aria-selected', String(t.dataset.screen === state.screen)));
  renderVersion();
  renderFreshness();
  const el = $('#screen');
  el.innerHTML = SCREENS[state.screen].view();
  el.scrollTop = 0;
}

/** Версия демона в шапке.
 *
 * Три состояния, и ни одно не должно выглядеть сломанным. Версии нет (старый
 * демон без ручки, сервер не ответил) — метка скрыта целиком, пустое место в
 * шапке читается как «панель отвалилась». Версия есть — показывается как есть,
 * включая `dev`: сборка из рабочего дерева так и называется, и подставлять
 * вместо неё прочерк значило бы прятать правду. Версия разошлась с записанной
 * установщиком — метка становится предупреждением: это ровно случай «на диске
 * новый бинарник, а systemd крутит старый процесс». */
function renderVersion() {
  const el = $('#brand-version');
  const v = state.version;
  if (!v || !v.version) {
    el.hidden = true;
    return;
  }
  el.hidden = false;
  const mismatch = Boolean(v.version_mismatch);
  el.classList.toggle('warn', mismatch);
  el.textContent = mismatch ? `${v.version} ≠ ${v.installed_version}` : v.version;
  el.title = mismatch
    ? `Работает ${v.version}, установщик записал ${v.installed_version}.`
      + ' Похоже, после обновления демон не перезапустился. Подробности — на экране «Диагностика».'
    : `Версия демона${v.commit ? `, коммит ${v.commit}` : ''}`;
}

/** Бейдж свежести рядом с версией.
 *
 * Молчание — штатный исход, а не сбой: GitHub недоступен, лимит выбран, версия
 * `dev` или разобрать её не вышло — метки просто нет. «Неизвестно» в шапке было
 * бы шумом, за которым не стоит ни одного действия.
 *
 * Отставание названо версией, а не числом коммитов и не словом «устарела»:
 * обновляются до версии, и её имя — единственное, что здесь можно применить.
 * Заодно это ноль дополнительных запросов к GitHub: тег последнего релиза уже
 * есть, а счёт коммитов пришлось бы спрашивать отдельно. */
function renderFreshness() {
  const el = $('#brand-fresh');
  const running = state.version && state.version.version;
  const latest = state.latestRelease;
  const cmp = running && latest ? compareVersions(latest, running) : null;
  if (cmp === null) {
    el.hidden = true;
    el.textContent = '';
    el.classList.remove('behind');
    return;
  }
  el.hidden = false;
  const behind = cmp > 0;
  el.classList.toggle('behind', behind);
  // `latest` стоит и тогда, когда развёрнуто новее релиза (сборка из ветки):
  // «отстали на -1» — неправда, а свежее на GitHub и правда ничего нет.
  el.textContent = behind ? `есть ${versionLabel(latest)}` : 'latest';
  el.title = behind
    ? `Работает ${versionLabel(running)}, на GitHub ${versionLabel(latest)}.`
      + ' Обновление — тем же установщиком, что и первая установка.'
    : 'Свежее на GitHub нет: развёрнута последняя версия.';
}

/** Свежесть версии — фоном и вне общей загрузки: GitHub может отвечать
 * секундами, а экран из-за метки в шапке ждать не должен. Спрашивается один раз
 * на заход, такт опроса сюда не заглядывает — релизы выходят не так часто, а
 * лимит GitHub общий на всех за одним адресом. */
async function checkFreshness() {
  const tag = await api.release.latest();
  if (!tag) return;
  state.latestRelease = tag;
  if (!$('#app').hidden) renderFreshness();
}

function fromHash() {
  const h = location.hash.replace('#', '');
  state.screen = ORDER.includes(h) ? h : 'peers';
}

async function navigate() {
  fromHash();
  render();
  // Данные экрана перечитываются при заходе на него, а дальше — по такту
  // опроса: тот зовёт ту же navigate().
  const screen = SCREENS[state.screen];
  const key = state.screen;
  try {
    await pullInto(key, screen.load);
  } catch (err) {
    if (err.status === 401) return;
  }
  render();
}

async function pullInto(key, loader) {
  try {
    await loader();
    state.missing.delete(key);
    setOffline(false);
  } catch (err) {
    if (err.status === 401) throw err;
    if (err.missing) state.missing.add(key);
    else if (err.offline) setOffline(true);
    else toastError(err);
  }
}

/* --- баннер «сервер не отвечает» ------------------------------------------ */

function setOffline(on) {
  const existing = $('#offline-bar');
  if (on && !existing) {
    const bar = document.createElement('div');
    bar.id = 'offline-bar';
    bar.className = 'offline-bar';
    bar.textContent = 'Сервер не отвечает. Данные на экране могли устареть.';
    $('#app').prepend(bar);
  } else if (!on && existing) {
    existing.remove();
  }
}

/* ==========================================================================
   Опрос
   ========================================================================== */

/* Единственный источник свежих чисел: живого канала нет (docs/05-api.md).
   Такт пропускается, пока вкладка скрыта — панель, забытая в фоне, не должна
   держать демон занятым, — и пока открыта модалка: перерисовка снесла бы
   наполовину заполненную форму. По той же причине такт молчит, пока курсор
   стоит в поле ввода прямо на экране: перерисовка экрана заменяет разметку
   целиком, и набранное вместе с фокусом пропадало бы посреди слова. */
function typing() {
  const el = document.activeElement;
  return Boolean(el && el.matches && el.matches('input, textarea'));
}

function startPolling() {
  stopPolling();
  pollTimer = setInterval(() => {
    if (document.visibilityState === 'hidden' || modalOpen() || typing()) return;
    navigate();
  }, 15000);
}

function stopPolling() {
  clearInterval(pollTimer);
  pollTimer = null;
}

/* ==========================================================================
   Запуск
   ========================================================================== */

async function start() {
  $('#screen').innerHTML = loading('Загружаю');
  try {
    await loadAll();
  } catch (err) {
    if (err.status === 401) return;
    toastError(err);
  }
  await navigate();
  checkFreshness();
  startPolling();
}

async function boot() {
  api.onUnauthorized(() => showGate('Сессия истекла — войдите заново.'));

  $('#login-form').addEventListener('submit', handleLogin);

  document.addEventListener('razdacha:render', () => {
    if (!$('#app').hidden) render();
  });

  $('#tabs').addEventListener('click', (ev) => {
    const tab = ev.target.closest('.tab');
    if (tab) location.hash = tab.dataset.screen;
  });
  window.addEventListener('hashchange', navigate);

  $('#btn-settings').addEventListener('click', settingsScreen.modalSettings);

  $('#btn-logout').addEventListener('click', async () => {
    try { await api.session.logout(); } catch { /* сессия и так недействительна */ }
    location.hash = '';
    showGate();
  });

  $('#btn-apply').addEventListener('click', async () => {
    const b = $('#btn-apply');
    b.disabled = true;
    b.textContent = 'Применяю…';
    try {
      await api.apply.run();
      state.dirty = false;
      $('#apply-bar').hidden = true;
      toast('Конфигурация применена');
      await navigate();
    } catch (err) {
      // 422 — sing-box check не прошёл; прежний конфиг остался активным.
      toastError(err, 'Конфигурация не применена');
    } finally {
      b.disabled = false;
      b.textContent = 'Применить';
    }
  });

  $('#modal-backdrop').addEventListener('click', (ev) => {
    if (ev.target.id === 'modal-backdrop') closeModal();
  });

  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') { closeModal(); closeMenu(); }
    // Enter в поле, помеченном `data-enter`, равнозначен нажатию его кнопки:
    // формы на экранах не отправляются, а искать мышь ради одного домена —
    // лишний шаг.
    if (ev.key !== 'Enter') return;
    const act = ev.target && ev.target.dataset ? ev.target.dataset.enter : '';
    const fn = act ? ACTIONS[act] : null;
    if (!fn) return;
    ev.preventDefault();
    const res = fn('', ev.target);
    if (res && typeof res.catch === 'function') res.catch((err) => toastError(err));
  });

  // Одно делегирование на документ: разметка экранов перерисовывается целиком,
  // и слушатели на элементах пришлось бы навешивать заново после каждой правки.
  document.addEventListener('click', (ev) => {
    const btn = ev.target.closest('[data-act]');
    if (!btn) { closeMenu(); return; }
    const act = btn.dataset.act;
    if (!act.startsWith('menu-')) closeMenu();
    const fn = ACTIONS[act];
    if (!fn) return;
    const res = fn(btn.dataset.id || '', btn);
    if (res && typeof res.catch === 'function') res.catch((err) => toastError(err));
  });

  fromHash();

  let authed = false;
  try {
    const s = await api.session.get();
    authed = Boolean(s && s.authenticated);
  } catch {
    authed = false;
  }

  if (!authed) { showGate(); return; }
  showApp();
  await start();
}

boot();
