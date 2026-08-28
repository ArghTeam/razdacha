/* Сессии в `chrome.storage.local`.

   Прогон должен переживать закрытие DevTools: пользователь пишет «напрямую»,
   закрывает панель, переключает туннель, открывает снова и пишет второй прогон.
   Держим последние 10 сессий, старые вытесняются — тела мы не храним, но и
   компактные записи на длинной сессии занимают заметно. */

export const STORAGE_KEY = 'razvedka.sessions';
export const MAX_SESSIONS = 10;

/** Оставить последние `max` сессий по времени старта. Чистая функция —
    проверяется в test.js без chrome. */
export function pruneSessions(sessions, max = MAX_SESSIONS) {
  return [...(sessions || [])]
    .sort((a, b) => (b.startedAt || 0) - (a.startedAt || 0))
    .slice(0, max);
}

/** Имя сессии по умолчанию: хост первого документа плюс время старта. */
export function defaultName(host, startedAt) {
  const d = new Date(startedAt || Date.now());
  const p = (n) => String(n).padStart(2, '0');
  const time = `${p(d.getHours())}:${p(d.getMinutes())}`;
  return `${host || 'сессия'} ${time}`;
}

function area() {
  if (typeof chrome === 'undefined' || !chrome.storage?.local) {
    throw new Error('chrome.storage недоступен — расширение запущено вне Chrome');
  }
  return chrome.storage.local;
}

export async function loadSessions() {
  const bag = await area().get(STORAGE_KEY);
  const list = bag?.[STORAGE_KEY];
  return Array.isArray(list) ? list : [];
}

export async function saveSessions(sessions) {
  await area().set({ [STORAGE_KEY]: pruneSessions(sessions) });
}

/** Записать сессию: существующая с тем же id заменяется, новая добавляется. */
export async function putSession(session) {
  const list = await loadSessions();
  const rest = list.filter((s) => s.id !== session.id);
  rest.push(session);
  await saveSessions(rest);
}

export async function removeSession(id) {
  const list = await loadSessions();
  await saveSessions(list.filter((s) => s.id !== id));
}

export async function clearSessions() {
  await area().set({ [STORAGE_KEY]: [] });
}
