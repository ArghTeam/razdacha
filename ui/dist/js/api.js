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

  return expect === 'text' ? text : json;
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
      сервера в JSON-сущностях нет, и собрать конфиг на клиенте нечем. */
  config: (id) => get(`/api/peers/${encodeURIComponent(id)}/config`, { expect: 'text' }),
};

/* --- Туннели ------------------------------------------------------------- */

export const tunnels = {
  list: () => get('/api/tunnels'),
  create: (name, raw) => post('/api/tunnels', { name, raw }),
  update: (id, patchBody) => patch(`/api/tunnels/${encodeURIComponent(id)}`, patchBody),
  remove: (id) => del(`/api/tunnels/${encodeURIComponent(id)}`),
  parse: (raw) => post('/api/tunnels/parse', { raw }),
  check: (id) => post(`/api/tunnels/${encodeURIComponent(id)}/check`),
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

/* --- Живой канал ---------------------------------------------------------
   Одно соединение на всё (docs/05-api.md#websocket). Опрос ставится на паузу
   по visibilitychange: панель, забытая в фоновой вкладке, не должна держать
   демон занятым. Если WS не поднимается — он ещё не написан или не пробрасывается
   через nginx, — интерфейс не ломается, а переходит на редкий опрос. */

const LIVE_RETRY_MS = [1000, 2000, 5000, 10000, 30000];

export function openLive({ onMessage, onState }) {
  let ws = null;
  let attempt = 0;
  let timer = null;
  let stopped = false;

  const state = (s) => { if (onState) onState(s); };

  function connect() {
    if (stopped || document.visibilityState === 'hidden') return;
    const url = `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/ws`;
    try {
      ws = new WebSocket(url);
    } catch {
      schedule();
      return;
    }
    ws.onopen = () => { attempt = 0; state('open'); };
    ws.onmessage = (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch { return; }
      if (msg && msg.type) onMessage(msg.type, msg.data);
    };
    ws.onclose = () => { ws = null; state('closed'); schedule(); };
    ws.onerror = () => { if (ws) ws.close(); };
  }

  function schedule() {
    if (stopped || timer) return;
    const delay = LIVE_RETRY_MS[Math.min(attempt, LIVE_RETRY_MS.length - 1)];
    attempt++;
    timer = setTimeout(() => { timer = null; connect(); }, delay);
  }

  function onVisibility() {
    if (document.visibilityState === 'hidden') {
      if (ws) { ws.onclose = null; ws.close(); ws = null; }
      if (timer) { clearTimeout(timer); timer = null; }
    } else {
      attempt = 0;
      connect();
    }
  }

  document.addEventListener('visibilitychange', onVisibility);
  connect();

  return {
    get connected() { return Boolean(ws && ws.readyState === WebSocket.OPEN); },
    close() {
      stopped = true;
      document.removeEventListener('visibilitychange', onVisibility);
      if (timer) clearTimeout(timer);
      if (ws) { ws.onclose = null; ws.close(); }
    },
  };
}
