/* Мелкие помощники: форматирование, экранирование, склонения.
   Всё, что рисует текст пользователю, живёт здесь — интерфейс русский, а
   правила склонения числительных нельзя размазывать по экранам. */

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

/** Экранирование для вставки в innerHTML. Все данные приходят от пользователя. */
export const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

export function bytes(n) {
  if (!n) return '0 Б';
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`;
}

export function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

export function since(iso) {
  if (!iso) return 'никогда';
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return 'никогда';
  const sec = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (sec < 60) return `${sec} ${plural(sec, 'секунду', 'секунды', 'секунд')} назад`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} ${plural(min, 'минуту', 'минуты', 'минут')} назад`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} ${plural(h, 'час', 'часа', 'часов')} назад`;
  const d = Math.floor(h / 24);
  return `${d} ${plural(d, 'день', 'дня', 'дней')} назад`;
}

/** Онлайн — хендшейк менее 3 минут назад, то же правило, что в docs/02-data-model.md.
    Поле `online` от сервера считается главным, расчёт по хендшейку — запасным. */
export function isOnline(peer) {
  if (!peer || peer.enabled === false) return false;
  if (typeof peer.online === 'boolean') return peer.online;
  if (!peer.last_handshake) return false;
  return (Date.now() - new Date(peer.last_handshake).getTime()) < 180_000;
}

export const TUNNEL_LABEL = {
  wireguard: 'WireGuard', vless: 'VLESS', shadowsocks: 'Shadowsocks',
  trojan: 'Trojan', hysteria2: 'Hysteria2', socks: 'SOCKS', raw: 'JSON',
};

/** Адрес туннеля. Хранимый `parsed` пользуется server/server_port,
    ответ `POST /api/tunnels/parse` — host/port; принимаем оба. */
export function tunnelEndpoint(t) {
  const p = t.parsed || {};
  const host = p.server ?? p.host ?? t.host ?? '';
  const port = p.server_port ?? p.port ?? t.port ?? '';
  if (!host) return '—';
  return port ? `${host}:${port}` : String(host);
}

/** Интервал обновления списков: Go отдаёт time.Duration наносекундами,
    а форма считает в секундах. Различаем по порядку величины. */
export function intervalSeconds(v) {
  const n = Number(v) || 0;
  return n >= 1_000_000 ? Math.round(n / 1_000_000_000) : n;
}

export const lines = (text) => (text || '').split('\n').map((l) => l.trim()).filter(Boolean);

const RE_DOMAIN = /^(\*\.)?([a-z0-9_-]+\.)+[a-z]{2,}$/i;

export function validCidr(s) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\/(\d{1,2}))?$/.exec(s);
  if (!m) return false;
  for (let i = 1; i <= 4; i++) if (+m[i] > 255) return false;
  return m[5] === undefined || +m[5] <= 32;
}

/** Номера строк, которые не проходят проверку. Валидация построчная — как в
    docs/06-ui-screens.md: пользователь должен видеть, какую строку чинить. */
export function badLines(text, kind) {
  return (text || '').split('\n').map((l, i) => [l.trim(), i + 1])
    .filter(([l]) => l !== '')
    .filter(([l]) => (kind === 'domain' ? !RE_DOMAIN.test(l) : !validCidr(l)))
    .map(([, n]) => n);
}

export const maskIP = (ip) => (ip || '').replace(/^(\d+)\.(\d+)\.\d+\.\d+/, '$1.$2.x.x');
export const maskKey = (k) => (k ? k.slice(0, 4) + '…' + k.slice(-4) : '');

/** Скачивание сгенерированного на клиенте файла. */
export function download(name, text, type = 'text/plain;charset=utf-8') {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Имя файла из имени устройства: кириллица остаётся, остальное схлопывается. */
export const slug = (s) => String(s).replace(/[^\w\dА-Яа-яЁё-]+/g, '-').toLowerCase();
