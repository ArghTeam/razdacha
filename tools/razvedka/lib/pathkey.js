/* Нормализация пути запроса до шаблона, по которому два прогона сопоставимы.

   Прогоны не повторяют друг друга дословно: session id, счётчики, таймстемпы,
   идентификаторы карточек. Сравнивать URL целиком бессмысленно — совпадений почти
   не будет. Поэтому ключ сравнения — `(host, method, pathKey)`, где query
   отброшен, а сегменты, похожие на идентификатор, схлопнуты в `*`. */

/** Сегмент считается идентификатором, если он длиной ≥8 и состоит только из
    hex-символов либо только из цифр. Восемь — граница, ниже которой начинают
    попадаться осмысленные слова («abcdefa» не бывает, а «feedbac» уже бывает). */
const ID_LIKE = /^(?:[0-9a-fA-F]{8,}|[0-9]{8,})$/;

/** UUID в каноничном виде, с дефисами. Длина сегмента тут ни при чём — форма
    узнаётся целиком. */
const UUID_LIKE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/** Один сегмент пути → он сам либо `*`. */
export function normalizeSegment(seg) {
  if (!seg) return seg;
  if (UUID_LIKE.test(seg)) return '*';
  if (ID_LIKE.test(seg)) return '*';
  return seg;
}

/** URL или путь → шаблон пути. Query и фрагмент отбрасываются.
    Некорректный URL не роняет запись: возвращается то, что удалось разобрать. */
export function pathKey(url) {
  let path = String(url ?? '');
  try {
    path = new URL(path).pathname;
  } catch {
    // Относительный путь либо мусор: отрезаем query и фрагмент руками.
    const cut = path.search(/[?#]/);
    if (cut >= 0) path = path.slice(0, cut);
  }
  if (!path) return '/';
  const trailing = path.length > 1 && path.endsWith('/');
  const parts = path.split('/').map(normalizeSegment);
  let out = parts.join('/');
  if (!out.startsWith('/')) out = `/${out}`;
  if (trailing && !out.endsWith('/')) out += '/';
  return out;
}

/** Хост из URL. Пустая строка, если URL не разбирается. */
export function hostOf(url) {
  try {
    return new URL(String(url)).hostname.toLowerCase();
  } catch {
    return '';
  }
}

/** Ключ сопоставления записей внутри хоста. */
export function entryKey(method, url) {
  return `${String(method || 'GET').toUpperCase()} ${pathKey(url)}`;
}
