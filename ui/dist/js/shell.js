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

/* --- тосты ---------------------------------------------------------------- */

export function toast(text, kind) {
  const el = document.createElement('div');
  el.className = 'toast' + (kind === 'err' ? ' err' : '');
  el.textContent = text;
  $('#toasts').appendChild(el);
  setTimeout(() => el.remove(), 4000);
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

export function openModal(html, onMount) {
  $('#modal').innerHTML = html;
  $('#modal-backdrop').hidden = false;
  if (onMount) onMount($('#modal'));
}

export function closeModal() {
  $('#modal-backdrop').hidden = true;
  $('#modal').innerHTML = '';
}

export const modalOpen = () => !$('#modal-backdrop').hidden;

export function modalShell(title, body, foot) {
  return `
    <div class="modal-head"><h2>${esc(title)}</h2>
      <button class="icon-btn" data-act="close-modal" aria-label="Закрыть">✕</button></div>
    <div class="modal-body">${body}</div>
    <div class="modal-foot">${foot}</div>`;
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
  closeMenu();
  const menu = document.createElement('div');
  menu.className = 'menu';
  menu.innerHTML = items.map((it) =>
    `<button data-act="${it.act}" data-id="${esc(it.id)}" class="${it.danger ? 'danger' : ''}">${esc(it.label)}</button>`).join('');
  document.body.appendChild(menu);

  const place = () => placeMenu(menu, anchor);
  place();
  window.addEventListener('scroll', place, true);
  window.addEventListener('resize', place);

  state.openMenu = menu;
  state.closeMenuExtra = () => {
    window.removeEventListener('scroll', place, true);
    window.removeEventListener('resize', place);
  };
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

export function closeMenu() {
  if (state.closeMenuExtra) { state.closeMenuExtra(); state.closeMenuExtra = null; }
  if (state.openMenu) { state.openMenu.remove(); state.openMenu = null; }
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
