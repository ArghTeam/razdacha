/* Точка входа панели: вход, маршрутизация между четырьмя экранами и загрузка
   данных.

   Вход — гейт, а не пятый раздел: пока сессия не подтверждена, панели не видно
   вовсе. Любой 401 от любого запроса возвращает сюда же, а адрес экрана остаётся
   в хеше, поэтому после входа пользователь попадает туда, куда шёл. */

import * as api from './api.js';
import {
  state, toast, toastError, clearToasts, closeModal, closeMenu, modalOpen, loading, applyDocTitle,
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
let logoutPending = false;
let navigationRevision = 0;

/* ==========================================================================
   Вход
   ========================================================================== */

function showGate(message) {
  navigationRevision++;
  clearToasts();
  $('#btn-apply').disabled = false;
  $('#btn-apply').textContent = 'Apply';
  stopPolling();
  closeMenu(false);
  closeModal({ restoreFocus: false });
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
  if (logoutPending) return;
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
async function pull(key, loader, fallback, currentRead = () => true) {
  const currentSession = api.session.guard();
  try {
    const value = await loader();
    if (!currentSession() || !currentRead()) return fallback;
    state.missing.delete(key);
    setOffline(false);
    return value;
  } catch (err) {
    if (!currentSession() || !currentRead()) return fallback;
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
  const currentSession = api.session.guard();
  const tunnelRead = tunnelsScreen.beginInventoryRead();
  const ruleRead = rulesScreen.beginInventoryRead();
  const [settings, peers, tunnels, rules, community, version] = await Promise.all([
    pull('settings', api.settings.get, null),
    pull('peers', api.peers.list, []),
    pull('tunnels', api.tunnels.list, [], tunnelRead.current),
    pull('rules', api.rules.list, [], ruleRead.current),
    pull('community', api.lists.community, []),
    pull('version', api.version.get, null),
  ]);
  if (!currentSession()) return;
  state.version = version;
  state.settings = settings;
  applyDocTitle();
  state.peers = peers || [];
  tunnelRead.accept(tunnels || []);
  ruleRead.accept(rules || []);
  state.communityLists = community || [];
  const diag = await pull('diag', api.diag.get, null);
  if (!currentSession()) return;
  state.diag = diag;
  await refreshApplyStatus();
}

async function refreshApplyStatus() {
  const currentSession = api.session.guard();
  const status = await pull('apply', api.apply.status, null);
  if (!currentSession() || !status || state.missing.has('apply')) return; // Retain the local pending indicator when status is unavailable.
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
  closeMenu();
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
  el.textContent = mismatch ? `${v.version} ≠ ${v.installed_version || 'Unknown'}` : v.version;
  el.title = mismatch
    ? `Running ${v.version}; installed ${v.installed_version || 'Unknown'}. See Diagnostics for details.`
    : `Daemon version ${v.version}${v.commit ? `, commit ${v.commit}` : ''}`;
  el.setAttribute('aria-label', el.title);
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
  el.textContent = behind ? `Available ${versionLabel(latest)}` : 'Latest release';
  el.title = behind
    ? `Running ${versionLabel(running)}; latest release ${versionLabel(latest)}. Update using the installer.`
    : 'No newer stable release is available.';
}

/** Свежесть версии — фоном и вне общей загрузки: GitHub может отвечать
 * секундами, а экран из-за метки в шапке ждать не должен. Спрашивается один раз
 * на заход, такт опроса сюда не заглядывает — релизы выходят не так часто, а
 * лимит GitHub общий на всех за одним адресом. */
async function checkFreshness() {
  const currentSession = api.session.guard();
  const tag = await api.release.latest();
  if (!currentSession() || !tag) return;
  state.latestRelease = tag;
  if (!$('#app').hidden) renderFreshness();
}

function fromHash() {
  const h = location.hash.replace('#', '');
  state.screen = ORDER.includes(h) ? h : 'peers';
}

async function navigate({ background = false } = {}) {
  if ($('#app').hidden) return;
  const currentSession = api.session.guard();
  const revision = ++navigationRevision;
  fromHash();
  render();
  // Данные экрана перечитываются при заходе на него, а дальше — по такту
  // опроса: тот зовёт ту же navigate().
  const screen = SCREENS[state.screen];
  const key = state.screen;
  try {
    await pullInto(key, screen.load, () => revision === navigationRevision);
  } catch (err) {
    if (err.status === 401) return;
  }
  if (!currentSession() || revision !== navigationRevision || $('#app').hidden) return;
  // Accepted background data may be fresh while its inventory controls are in use.
  // Keep their DOM/focus until a later poll; deliberate navigation still renders.
  if (background && (state.openMenu || modalOpen() || typing())) return;
  render();
}

async function pullInto(key, loader, currentNavigation) {
  const currentSession = api.session.guard();
  try {
    await loader();
    if (!currentSession() || !currentNavigation()) return;
    state.missing.delete(key);
    setOffline(false);
  } catch (err) {
    if (!currentSession() || !currentNavigation()) return;
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
    bar.textContent = 'Gateway unreachable. Displayed data may be stale.';
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
    if (document.visibilityState === 'hidden' || modalOpen() || state.openMenu || typing()) return;
    navigate({ background: true });
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
  const currentSession = api.session.guard();
  $('#screen').innerHTML = loading('Loading');
  try {
    await loadAll();
  } catch (err) {
    if (!currentSession()) return;
    if (err.status === 401) return;
    toastError(err);
  }
  if (!currentSession()) return;
  await navigate();
  if (!currentSession()) return;
  checkFreshness();
  startPolling();
}

async function boot() {
  // Measure wrapped chrome so native focus scrolling can reserve the actual space.
  const applyBar = $('#apply-bar');
  const topbar = $('.topbar');
  const measureChrome = () => {
    document.documentElement.style.setProperty('--apply-height', `${Math.ceil(applyBar.getBoundingClientRect().height)}px`);
    document.documentElement.style.setProperty('--shell-header-height', `${Math.ceil(topbar.getBoundingClientRect().height)}px`);
  };
  const chromeObserver = new ResizeObserver(measureChrome);
  chromeObserver.observe(applyBar);
  chromeObserver.observe(topbar);
  measureChrome();
  document.addEventListener('focusin', (event) => {
    if (!event.target.matches('.diag-check-detail pre')) return;
    const detail = event.target;
    requestAnimationFrame(() => {
      if (document.activeElement !== detail || !detail.isConnected) return;
      const top = topbar.getBoundingClientRect().bottom + 12;
      const bottom = (applyBar.hidden ? innerHeight : applyBar.getBoundingClientRect().top) - 12;
      const box = detail.getBoundingClientRect();
      if (box.bottom > bottom) window.scrollBy(0, box.bottom - bottom);
      else if (box.top < top) window.scrollBy(0, box.top - top);
    });
  });
  api.onUnauthorized(() => showGate('Session expired. Log in again.'));

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
    if (logoutPending) return;
    logoutPending = true;
    showGate();
    location.hash = '';
    // A late logout Set-Cookie must settle before a new login can issue a cookie.
    $('#login-submit').disabled = true;
    try { await api.session.logout(); } catch { /* Local cleanup already completed. */ }
    finally {
      logoutPending = false;
      $('#login-submit').disabled = false;
    }
  });

  $('#btn-apply').addEventListener('click', async () => {
    const button = $('#btn-apply');
    if (button.disabled) return;
    const currentSession = api.session.guard();
    button.disabled = true;
    button.textContent = 'Applying…';
    try {
      await api.apply.run();
      if (!currentSession()) return;
      state.dirty = false;
      $('#apply-bar').hidden = true;
      await refreshApplyStatus();
      if (!currentSession()) return;
      toast('Configuration applied');
      await navigate();
    } catch (err) {
      if (currentSession()) toastError(err, 'Configuration was not applied');
    } finally {
      if (currentSession()) { button.disabled = false; button.textContent = 'Apply'; }
    }
  });

  $('#modal-backdrop').addEventListener('click', (ev) => {
    if (ev.target.id === 'modal-backdrop') closeModal();
  });

  document.addEventListener('keydown', (ev) => {
    // Enter в поле, помеченном `data-enter`, равнозначен нажатию его кнопки:
    // формы на экранах не отправляются, а искать мышь ради одного домена —
    // лишний шаг.
    if (ev.key !== 'Enter') return;
    const act = ev.target && ev.target.dataset ? ev.target.dataset.enter : '';
    const fn = act ? ACTIONS[act] : null;
    if (!fn) return;
    ev.preventDefault();
    const currentSession = api.session.guard();
    const res = fn('', ev.target);
    if (res && typeof res.catch === 'function') res.catch((err) => { if (currentSession()) toastError(err); });
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
    const currentSession = api.session.guard();
    const res = fn(btn.dataset.id || '', btn);
    if (res && typeof res.catch === 'function') res.catch((err) => { if (currentSession()) toastError(err); });
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
