/* Точка входа панели: вход, маршрутизация между четырьмя экранами и загрузка
   данных.

   Вход — гейт, а не пятый раздел: пока сессия не подтверждена, панели не видно
   вовсе. Любой 401 от любого запроса возвращает сюда же, а адрес экрана остаётся
   в хеше, поэтому после входа пользователь попадает туда, куда шёл. */

import * as api from './api.js';
import {
  state, toast, toastError, closeModal, closeMenu, modalOpen, loading,
} from './shell.js';
import { $, $$ } from './util.js';

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
  const [settings, peers, tunnels, rules, community] = await Promise.all([
    pull('settings', api.settings.get, null),
    pull('peers', api.peers.list, []),
    pull('tunnels', api.tunnels.list, []),
    pull('rules', api.rules.list, []),
    pull('community', api.lists.community, []),
  ]);
  state.settings = settings;
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
  const el = $('#screen');
  el.innerHTML = SCREENS[state.screen].view();
  el.scrollTop = 0;
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
   наполовину заполненную форму. */
function startPolling() {
  stopPolling();
  pollTimer = setInterval(() => {
    if (document.visibilityState === 'hidden' || modalOpen()) return;
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
