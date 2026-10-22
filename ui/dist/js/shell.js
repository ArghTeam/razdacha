/* Примитивы оболочки: тосты, модальные окна, меню «⋮» и общее состояние экранов.
   Отдельный модуль, чтобы экраны занимались данными, а не разметкой окон. */

import { $, esc } from './util.js';
import { ApiError } from './api.js';

/* --- состояние ------------------------------------------------------------ */

export const state = {
  screen: 'peers',
  peers: [],
  tunnels: [],
  rules: [],
  communityLists: [],
  settings: null,
  diag: null,
  /** Ответ `GET /api/version`: что развёрнуто на сервере. null — ещё не пришло. */
  version: null,
  /** Тег последнего релиза с GitHub, спрошенный браузером. null — не спрашивали,
      не ответили или ответ не разобрали; во всех трёх случаях бейджа нет. */
  latestRelease: null,
  dirty: false,
  /** Эндпоинты, которых ещё нет: экран говорит об этом вместо пустого списка. */
  missing: new Set(),
  openMenu: null,
  closeMenuExtra: null,
};

export const tunnelById = (id) => state.tunnels.find((t) => t.id === id);
export const peerById = (id) => state.peers.find((p) => p.id === id);
export const listTitle = (key) =>
  (state.communityLists.find((l) => l.key === key) || {}).title || key;

/* Заголовок вкладки — с адресом сервера: у оператора открыто несколько панелей
   (прод, стенд), и по одному «razdacha» вкладки не различить. Нет адреса —
   просто «razdacha», без хвоста ` · `. */
export function applyDocTitle() {
  const host = state.settings && state.settings.endpoint_host;
  document.title = host ? `${host} · razdacha` : 'razdacha';
}

/* --- тосты ---------------------------------------------------------------- */

const notifications = new Map();
const maxNotifications = 3;
function revealToastControl(control) {
  const stack = $('#toasts');
  const bounds = stack.getBoundingClientRect();
  const box = control.getBoundingClientRect();
  if (box.top < bounds.top) stack.scrollTop -= bounds.top - box.top;
  else if (box.bottom > bounds.bottom) stack.scrollTop += box.bottom - bounds.bottom;
}
function continueToastFocus() {
  const target = $('#toasts').querySelector('.toast:last-child .toast-dismiss');
  if (target) {
    target.focus({ preventScroll: true });
    revealToastControl(target);
  } else if (modalOpen()) focusModal();
  else {
    const fallback = $('#app').hidden ? $('#password') : $('#tabs [aria-selected="true"]');
    fallback?.focus({ preventScroll: true });
  }
}
function removeToast(element, restoreFocus = true) {
  const focused = element.contains(document.activeElement);
  clearTimeout(notifications.get(element)?.timer);
  notifications.delete(element);
  element.remove();
  if (focused && restoreFocus) continueToastFocus();
  return focused;
}

export function clearToasts() {
  // Authentication/modal cleanup owns the next focus target.
  for (const element of notifications.keys()) removeToast(element, false);
  $('#toasts').replaceChildren();
}

export function toast(text, kind) {
  const value = String(text);
  let restoreFocus = $('#toasts').contains(document.activeElement);
  for (const [element, entry] of notifications) {
    if (!element.isConnected || entry.text === value && entry.kind === kind) restoreFocus = removeToast(element, false) || restoreFocus;
  }
  while (notifications.size >= maxNotifications) restoreFocus = removeToast(notifications.keys().next().value, false) || restoreFocus;
  const element = document.createElement('div');
  element.className = 'toast' + (kind === 'err' ? ' err' : '');
  const content = document.createElement('span');
  content.className = 'toast-message';
  content.textContent = value;
  if (value.length > 300 || value.split('\n').length > 4) content.tabIndex = 0;
  const dismiss = document.createElement('button');
  dismiss.type = 'button';
  dismiss.className = 'toast-dismiss';
  dismiss.setAttribute('aria-label', 'Dismiss notification');
  dismiss.textContent = '×';
  dismiss.addEventListener('click', () => removeToast(element));
  element.append(content, dismiss);
  const stack = $('#toasts');
  stack.appendChild(element);
  stack.scrollTop = stack.scrollHeight;
  if (restoreFocus) continueToastFocus();
  notifications.set(element, { text: value, kind, timer: setTimeout(() => removeToast(element), 4000) });
}

/** Показать ошибку запроса. Текст ошибки приходит с сервера на русском и
    показывается как есть — таково соглашение проекта. */
export function toastError(err, fallback = 'Не получилось') {
  if (err instanceof ApiError && err.status === 401) return; // уже уводит на вход
  const msg = err instanceof ApiError ? err.message : fallback;
  toast(msg, 'err');
  if (!(err instanceof ApiError)) console.error(err);
}

/* --- модальные окна ------------------------------------------------------- */

let modalRevision = 0;
const modalCleanups = new Set();
let modalOpener = null;
let backgroundInert = [];
const usable = (el) => el instanceof HTMLElement && el.isConnected &&
  !el.matches(':disabled') && !el.closest('[hidden], [inert]') && el.getClientRects().length > 0;
const focusable = (root) => [...root.querySelectorAll('button, input, select, textarea, a[href], summary, [tabindex]')]
  .filter((el) => usable(el) && el.tabIndex >= 0);

// The row may rerender during a request. Keep its stable identity as a fallback.
function focusOrigin(el) {
  if (!(el instanceof HTMLElement)) return null;
  return { element: el, id: el.id, act: el.dataset.act, item: el.dataset.id };
}
function restoreOrigin(origin) {
  let target = origin?.element;
  if (!usable(target) && origin?.id) target = document.getElementById(origin.id);
  if (!usable(target) && origin?.act) {
    target = [...document.querySelectorAll('[data-act]')].find((el) =>
      el.dataset.act === origin.act && el.dataset.id === origin.item && usable(el));
  }
  if (!usable(target)) target = $('#tabs [aria-selected="true"]');
  if (usable(target)) target.focus({ preventScroll: true });
}
function focusModal() {
  const modal = $('#modal');
  const target = modal.querySelector('[autofocus]') || modal.querySelector('h2') || focusable(modal)[0] || modal;
  target.focus({ preventScroll: true });
}

// All persistent modal copies have the same lifetime, including download blobs.
function resetModal() {
  modalRevision++;
  for (const cleanup of modalCleanups) cleanup();
  modalCleanups.clear();
  const modal = $('#modal');
  for (const canvas of modal.querySelectorAll('canvas')) {
    canvas.width = 0;
    canvas.height = 0;
  }
  for (const field of modal.querySelectorAll('input, textarea')) field.value = '';
  for (const key of Object.keys(modal.dataset)) delete modal.dataset[key];
  modal.replaceChildren();
}

export const currentModalRevision = () => modalRevision;
export const isCurrentModal = (revision) => revision === modalRevision && modalOpen();
export const onModalCleanup = (cleanup) => modalCleanups.add(cleanup);

export function openModal(html, onMount) {
  if (!modalOpen()) {
    modalOpener = focusOrigin(document.activeElement);
    backgroundInert = ['#app', '#gate'].map((selector) => {
      const element = $(selector);
      const previous = element.inert;
      element.inert = true;
      return { element, previous };
    });
  }
  closeMenu(false);
  resetModal();
  const modal = $('#modal');
  modal.innerHTML = html;
  modal.tabIndex = -1;
  const heading = modal.querySelector('h2');
  if (heading) {
    heading.id = 'modal-title';
    heading.tabIndex = -1;
    modal.setAttribute('aria-labelledby', heading.id);
  }
  $('#modal-backdrop').hidden = false;
  if (onMount) onMount(modal);
  if (modalOpen() && (!modal.contains(document.activeElement) || !usable(document.activeElement))) focusModal();
  return modalRevision;
}

export function closeModal({ restoreFocus = true } = {}) {
  const wasOpen = modalOpen();
  $('#modal-backdrop').hidden = true;
  resetModal();
  $('#modal').removeAttribute('aria-labelledby');
  for (const { element, previous } of backgroundInert) element.inert = previous;
  backgroundInert = [];
  const origin = modalOpener;
  modalOpener = null;
  if (wasOpen && restoreFocus) restoreOrigin(origin);
}

// These listeners never capture a dialog's DOM or async callbacks.
document.addEventListener('focusin', (event) => {
  if (modalOpen() && !$('#modal').contains(event.target)) focusModal();
  if (!event.target.matches('#t-name, #t-raw')) return;
  const field = event.target;
  requestAnimationFrame(() => {
    if (!modalOpen() || !field.isConnected || document.activeElement !== field) return;
    const body = field.closest('.modal-body');
    const visible = body.getBoundingClientRect(), box = field.getBoundingClientRect();
    if (box.bottom > visible.bottom - 12) body.scrollTop += box.bottom - visible.bottom + 12;
    else if (box.top < visible.top + 12) body.scrollTop -= visible.top + 12 - box.top;
  });
});
document.addEventListener('keydown', (event) => {
  if (!modalOpen()) return;
  if (event.key === 'Escape') {
    event.preventDefault();
    closeModal();
  } else if (event.key === 'Tab') {
    const modal = $('#modal');
    const items = focusable(modal);
    const index = items.indexOf(document.activeElement);
    if (!items.length || (event.shiftKey ? index <= 0 : index < 0 || index === items.length - 1)) {
      event.preventDefault();
      (event.shiftKey ? items.at(-1) : items[0])?.focus();
      if (!items.length) modal.focus();
    }
  }
});

export const modalOpen = () => !$('#modal-backdrop').hidden;

export function modalShell(title, body, foot) {
  return `
    <div class="modal-head"><h2>${esc(title)}</h2>
      <button class="icon-btn" data-act="close-modal" aria-label="Close dialog">✕</button></div>
    <div class="modal-body">${body}</div>
    <div class="modal-foot">${foot}</div>`;
}

/** In-app destructive confirmation; closing or replacement cancels pending intent. */
export function confirmAction(title, identity, consequence) {
  return new Promise(resolve => {
    openModal(modalShell(title,
      `<div class="confirm-content"><p>Delete <strong>${esc(identity)}</strong>?</p><p class="hint">${esc(consequence)}</p></div>`,
      '<button class="btn" data-act="close-modal" autofocus>Cancel</button><button class="btn btn-danger" data-confirm-accept>Delete permanently</button>'), modal => {
      const revision = currentModalRevision();
      onModalCleanup(() => resolve(false));
      modal.querySelector('[data-confirm-accept]').addEventListener('click', () => {
        if (!isCurrentModal(revision)) return;
        resolve(true);
        closeModal();
      }, { once: true });
    });
  });
}

/* --- меню «⋮» ------------------------------------------------------------- */

/** Отступ меню от кнопки и от края экрана. */
const MENU_GAP = 4;

/**
 * Меню живёт в body, а не рядом с кнопкой: у `.card` стоит `overflow: hidden`
 * ради скруглённых углов, и меню внутри строки обрезалось по краю карточки —
 * пунктов было не видно. Позиция считается от кнопки и пересчитывается при
 * прокрутке: `position: fixed` внутри обрезающего предка не спасает.
 */
export function openMenu(anchor, items) {
  closeMenu(false);
  const menu = document.createElement('div');
  menu.className = 'menu';
  menu.id = 'row-menu';
  menu.setAttribute('role', 'menu');
  menu.setAttribute('aria-label', anchor.getAttribute('aria-label') || 'Actions');
  anchor.setAttribute('aria-haspopup', 'menu');
  anchor.setAttribute('aria-expanded', 'true');
  anchor.setAttribute('aria-controls', menu.id);
  menu.innerHTML = items.map((it) =>
    `<button role="menuitem" tabindex="-1" data-act="${it.act}" data-id="${esc(it.id)}" class="${it.danger ? 'danger' : ''}"${typeof it.enabled === 'boolean' ? ` data-enabled="${it.enabled}"` : ''}${it.disabled ? ' disabled aria-disabled="true"' : ''}>${esc(it.label)}</button>`).join('');
  document.body.appendChild(menu);
  const origin = focusOrigin(anchor);
  const place = () => {
    if (!usable(anchor)) { closeMenu(false); return; }
    placeMenu(menu, anchor);
  };
  const keydown = (event) => {
    const buttons = [...menu.querySelectorAll('button:not(:disabled)')];
    let index = buttons.indexOf(document.activeElement);
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      closeMenu();
      return;
    }
    if (event.key === 'Tab') { closeMenu(); return; }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = buttons.length - 1;
    else index = (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
    buttons.forEach((button, i) => { button.tabIndex = i === index ? 0 : -1; });
    buttons[index]?.focus();
  };
  const focusin = (event) => {
    if (!menu.contains(event.target) && event.target !== anchor) closeMenu(false);
  };
  state.openMenu = menu;
  state.closeMenuExtra = (restoreFocus) => {
    window.removeEventListener('scroll', place, true);
    window.removeEventListener('resize', place);
    menu.removeEventListener('keydown', keydown);
    document.removeEventListener('focusin', focusin);
    anchor.setAttribute('aria-expanded', 'false');
    anchor.removeAttribute('aria-controls');
    if (restoreFocus) restoreOrigin(origin);
  };
  place();
  window.addEventListener('scroll', place, true);
  window.addEventListener('resize', place);
  menu.addEventListener('keydown', keydown);
  document.addEventListener('focusin', focusin);
  const first = menu.querySelector('button:not(:disabled)');
  if (first) { first.tabIndex = 0; first.focus(); }
}

/** Прижимает меню к правому краю кнопки; если снизу не помещается — вверх. */
function placeMenu(menu, anchor) {
  const a = anchor.getBoundingClientRect();
  const m = menu.getBoundingClientRect();

  let left = a.right - m.width;
  left = Math.min(Math.max(MENU_GAP, left), window.innerWidth - m.width - MENU_GAP);

  const below = a.bottom + MENU_GAP;
  const top = below + m.height > window.innerHeight && a.top - m.height - MENU_GAP > 0
    ? a.top - m.height - MENU_GAP
    : below;

  menu.style.left = `${Math.round(left)}px`;
  menu.style.top = `${Math.round(top)}px`;
}

export function closeMenu(restoreFocus = true) {
  const restore = restoreFocus && Boolean(state.openMenu?.contains(document.activeElement));
  const cleanup = state.closeMenuExtra;
  state.closeMenuExtra = null;
  if (state.openMenu) { state.openMenu.remove(); state.openMenu = null; }
  if (cleanup) cleanup(restore);
}

/* --- перерисовка и плашка применения -------------------------------------- */

/** Перерисовать текущий экран. Слушает роутер в app.js. */
export function refresh() {
  document.dispatchEvent(new CustomEvent('razdacha:render'));
}

/** Правка ушла в БД, но ещё не применена: изменения копятся и применяются
    пакетно по `POST /api/apply` (docs/05-api.md#applying). */
export function markDirty() {
  state.dirty = true;
  $('#apply-bar').hidden = false;
}

/* --- заглушки ------------------------------------------------------------- */

/** Экран без данных объясняет причину. Пустой список и «0» на месте
    невыясненного — это враньё, поэтому их здесь нет. */
export function notice(title, detail, kind = '') {
  return `<div class="notice ${kind}"><strong>${esc(title)}</strong>${esc(detail)}</div>`;
}

export const notImplemented = (what) =>
  notice(`${what}: данные появятся позже`,
    'Демон ещё не отдаёт этот раздел — эндпоинт не реализован. Как только он появится, экран заполнится сам.');

export const loading = (what) => `<div class="loading">${esc(what)}…</div>`;
