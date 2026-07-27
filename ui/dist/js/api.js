/* Единственный слой доступа к API. Ни один экран не зовёт fetch сам: контракт
   описан в docs/05-api.md, реализация приезжает отдельными задачами, и
   расхождение должно чиниться здесь, а не по всему интерфейсу.

   Два правила, ради которых слой и выделен:
     - любой 401 означает «сессии нет» и возвращает пользователя на вход;
     - 404 на списочном эндпоинте означает «его ещё не написали», и экран
       обязан сказать это словами, а не показать пустой список. */

/** Ошибка запроса. `code` — слаг из docs/05-api.md, по нему различаются ситуации. */
export class ApiError extends Error {
  constructor(status, code, message, extra = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    Object.assign(this, extra);
  }

  /** Эндпоинта ещё нет: демон отвечает 404 «Такого пути нет» на всё нереализованное. */
  get missing() { return this.status === 404 || this.status === 405 || this.status === 501; }

  /** До сервера не дозвонились — это не ошибка контракта. */
  get offline() { return this.status === 0; }
}

/** Слушатели «сессия кончилась». Подписывается роутер, а не экраны. */
const unauthorizedHandlers = new Set();

export function onUnauthorized(fn) {
  unauthorizedHandlers.add(fn);
  return () => unauthorizedHandlers.delete(fn);
}

async function readBody(res) {
  const text = await res.text();
  const type = res.headers.get('Content-Type') || '';
  if (!type.includes('json')) return { text, json: null };
  try {
    return { text, json: JSON.parse(text) };
  } catch {
    return { text, json: null };
  }
}

/**
 * Один запрос к API.
 * @param {string} method
 * @param {string} path путь начиная с /api/
 * @param {{body?: any, expect?: 'json'|'text', quiet401?: boolean}} opts
 */
async function request(method, path, opts = {}) {
  const { body, expect = 'json', quiet401 = false } = opts;

  const init = {
    method,
    // Cookie сессии — HttpOnly и SameSite=Lax; same-origin достаточно и не
    // открывает панель чужому origin.
    credentials: 'same-origin',
    headers: { Accept: expect === 'json' ? 'application/json' : 'text/plain, */*' },
    cache: 'no-store',
  };
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }

  let res;
  try {
    res = await fetch(path, init);
  } catch (err) {
    throw new ApiError(0, 'network', 'Сервер не отвечает. Проверьте соединение.', { cause: err });
  }

  if (res.status === 401 && !quiet401) {
    for (const fn of unauthorizedHandlers) fn();
  }

  if (res.status === 204) return null;

  const { text, json } = await readBody(res);

  if (!res.ok) {
    const retryAfter = Number(res.headers.get('Retry-After')) || 0;
    throw new ApiError(
      res.status,
      json?.code || `http_${res.status}`,
      json?.error || defaultMessage(res.status),
      { retryAfter },
    );
  }

  if (expect === 'file') return { text, filename: filenameFrom(res) };
  return expect === 'text' ? text : json;
}

/** Имя файла из `Content-Disposition`. Демон — единственный, кто его строит:
    имя туннеля в WireGuard берётся из имени файла и обязано укладываться в его
    ограничения, и второй слагификатор на клиенте давал бы другое имя. */
function filenameFrom(res) {
  const m = /filename="([^"]*)"/.exec(res.headers.get('Content-Disposition') || '');
  return m && m[1] ? m[1] : '';
}

function defaultMessage(status) {
  switch (status) {
    case 401: return 'Требуется вход';
    case 403: return 'Доступ запрещён';
    case 404: return 'Такого пути нет';
    case 409: return 'Конфликт: объект используется';
    case 422: return 'Конфигурация не принята';
    case 429: return 'Слишком много попыток';
    default: return `Ошибка сервера (${status})`;
  }
}

const get = (p, o) => request('GET', p, o);
const post = (p, body, o) => request('POST', p, { body, ...o });
const patch = (p, body) => request('PATCH', p, { body });
const put = (p, body) => request('PUT', p, { body });
const del = (p) => request('DELETE', p);

/* --- Сессия ---------------------------------------------------------------
   Вход и выход единственные, кто гасит глобальную реакцию на 401: 401 на
   `POST /api/login` — это «неверный пароль», а не «сессия истекла», и уводить
   с экрана входа на экран входа бессмысленно. */

export const session = {
  get: () => get('/api/session', { quiet401: true }),
  login: (password) => post('/api/login', { password }, { quiet401: true }),
  logout: () => post('/api/logout', undefined, { quiet401: true }),
};

/* --- Пиры ---------------------------------------------------------------- */

export const peers = {
  list: () => get('/api/peers'),
  create: (name) => post('/api/peers', { name }),
  update: (id, patchBody) => patch(`/api/peers/${encodeURIComponent(id)}`, patchBody),
  remove: (id) => del(`/api/peers/${encodeURIComponent(id)}`),
  /** Клиентский .conf — text/plain. Собирает его демон: публичного ключа
      сервера в JSON-сущностях нет, и собрать конфиг на клиенте нечем. Имя
      файла приходит оттуда же, в `Content-Disposition`. Возвращает
      `{ text, filename }`. */
  config: (id) => get(`/api/peers/${encodeURIComponent(id)}/config`, { expect: 'file' }),
};

/* --- Оповещения -----------------------------------------------------------
   Токен бота наружу не отдаётся: в ответе только `token_set`. Пустой `token`
   в запросе означает «оставить прежний» — иначе сохранение галочки стирало бы
   секрет, которого в форме и не было. */

export const notify = {
  get: () => get('/api/notify'),
  save: (body) => put('/api/notify', body),
  test: () => post('/api/notify/test'),
};

/* --- Туннели ------------------------------------------------------------- */

export const tunnels = {
  list: () => get('/api/tunnels'),
  create: (name, raw) => post('/api/tunnels', { name, raw }),
  update: (id, patchBody) => patch(`/api/tunnels/${encodeURIComponent(id)}`, patchBody),
  remove: (id) => del(`/api/tunnels/${encodeURIComponent(id)}`),
  parse: (raw) => post('/api/tunnels/parse', { raw }),
  check: (id) => post(`/api/tunnels/${encodeURIComponent(id)}/check`),
  /** Пул: перечитать каталог и пересобрать список серверов. Пока ручки нет,
      404 приходит как `err.missing` — экран говорит это словами. */
  refresh: (id) => post(`/api/tunnels/${encodeURIComponent(id)}/refresh`),
  /** Пул: состав каталога по одному серверу. Отдельным запросом, а не полем
      списка: сотня с лишним записей в каждом ответе экрана — лишний трафик,
      и нужны они только открытой модалке «Детали». */
  poolServers: (id) => get(`/api/tunnels/${encodeURIComponent(id)}/pool`),
};

/* --- Правила ------------------------------------------------------------- */

export const rules = {
  list: () => get('/api/rules'),
  create: (rule) => post('/api/rules', rule),
  update: (id, patchBody) => patch(`/api/rules/${encodeURIComponent(id)}`, patchBody),
  remove: (id) => del(`/api/rules/${encodeURIComponent(id)}`),
  /** Порядок меняется целиком: промежуточных состояний с дублями быть не должно. */
  reorder: (ids) => put('/api/rules/order', { ids }),
};

export const lists = {
  community: () => get('/api/lists/community'),
};

/* --- Настройки ----------------------------------------------------------- */

export const settings = {
  get: () => get('/api/settings'),
  update: (patchBody) => patch('/api/settings', patchBody),
};

/* --- Диагностика --------------------------------------------------------- */

export const diag = {
  get: () => get('/api/diag'),
  /** Без `id` — все проверки одним ответом; с `id` — одна, чтобы экран
      показывал ход построчно (docs/05-api.md#diagnostics). */
  run: (id) => post(id ? `/api/diag/run?check=${encodeURIComponent(id)}` : '/api/diag/run'),
  singboxConfig: () => get('/api/diag/singbox-config', { expect: 'text' }),
  logs: (source, count = 200) =>
    get(`/api/logs?source=${encodeURIComponent(source)}&lines=${count}`, { expect: 'text' }),
};

/* --- Применение ---------------------------------------------------------- */

export const apply = {
  run: () => post('/api/apply'),
  status: () => get('/api/apply/status'),
};

/** Есть ли непримененные изменения. Имя поля в docs/05-api.md не закреплено,
    поэтому принимаются все правдоподобные — расхождение чинится здесь одной строкой. */
export function isDirty(status) {
  if (!status || typeof status !== 'object') return false;
  return Boolean(status.dirty ?? status.pending ?? status.has_changes ?? status.changed);
}

/** Строки лога: сервер может отдать текст, массив строк или объект с массивом. */
export function logLines(payload) {
  if (typeof payload === 'string') {
    try {
      const parsed = JSON.parse(payload);
      return logLines(parsed);
    } catch {
      return payload.split('\n');
    }
  }
  if (Array.isArray(payload)) return payload.map(String);
  if (payload && Array.isArray(payload.lines)) return payload.lines.map(String);
  return [];
}
