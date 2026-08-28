/* Детекторы гео-сигнала: заголовки ответа, поля в JSON-теле, известные
   гео-эндпоинты.

   Ни один из детекторов сам по себе не выносит вердикт — они лишь достают
   значения, которые потом сравниваются между двумя прогонами (lib/diff.js).
   Эвристика врёт в обе стороны, доказательство даёт только расхождение. */

/** Заголовки ответа, в которых сервер сообщает, откуда, по его мнению, пришёл
    клиент. Сравнение имён регистронезависимое. */
export const GEO_HEADERS = [
  'cf-ipcountry',
  'cloudfront-viewer-country',
  'cloudfront-viewer-country-region',
  'x-vercel-ip-country',
  'x-country',
  'x-country-code',
  'x-geo-country',
  'x-client-geo-location',
  'x-amz-cf-pop',
  'x-served-by',
  'fly-region',
  'x-appengine-country',
];

/** Ключи JSON, значение которых стоит запомнить: по ним сайт чаще всего и решает,
    что показывать. */
export const GEO_FIELDS = [
  'country',
  'countryCode',
  'country_code',
  'geo',
  'region',
  'market',
  'locale',
  'ip',
  'timezone',
  'detectedCountry',
];

const GEO_FIELDS_LOWER = new Set(GEO_FIELDS.map((k) => k.toLowerCase()));
const GEO_HEADERS_SET = new Set(GEO_HEADERS);

/** Известные гео-эндпоинты: их ответ целиком про страну клиента, и попадание сюда
    само по себе повод показать хост в дифе. */
export const GEO_ENDPOINTS = [
  { host: 'ipapi.co' },
  { host: 'ip-api.com' },
  { host: 'ipinfo.io' },
  { host: 'ipwho.is' },
  { host: 'api.myip.com' },
  { host: 'geolocation-db.com' },
  { path: '/cdn-cgi/trace' },
];

/** Хост+путь → это гео-эндпоинт? Хост сверяется по суффиксу, чтобы поймать
    поддомены вроде `pro.ip-api.com`. */
export function isGeoEndpoint(host, path) {
  const h = String(host || '').toLowerCase();
  const p = String(path || '');
  return GEO_ENDPOINTS.some((e) => {
    if (e.path) return p.startsWith(e.path);
    return h === e.host || h.endsWith(`.${e.host}`);
  });
}

/** Заголовки HAR-entry (`[{name, value}]`) → `{имя: значение}` только по гео-списку.
    Имена приводятся к нижнему регистру: HTTP/2 отдаёт их в нижнем, HTTP/1 — как
    придётся. */
export function geoHeaders(headers) {
  const out = {};
  for (const h of headers || []) {
    const name = String(h?.name || '').toLowerCase();
    if (!GEO_HEADERS_SET.has(name)) continue;
    const value = String(h?.value ?? '').trim();
    if (!value) continue;
    out[name] = value.slice(0, 64);
  }
  return out;
}

/** Значение поля годится, если это строка или число длиной ≤64. Объекты и массивы
    в значение не идут — обход спускается в них дальше. */
function scalarValue(v) {
  if (typeof v === 'number' && Number.isFinite(v)) return String(v);
  if (typeof v !== 'string') return null;
  const s = v.trim();
  if (!s || s.length > 64) return null;
  return s;
}

/** Рекурсивный обход JSON до глубины 6 с записью пути до поля (`data.user.country`).
    Глубже не ходим: полезный сигнал лежит близко к корню, а глубокий обход на
    большом ответе стоит дороже, чем даёт. */
export function geoFields(value, maxDepth = 6) {
  const out = {};
  const walk = (node, path, depth) => {
    if (depth > maxDepth || node === null || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      // Индекс в пути сохраняем: без него два элемента массива слипаются в один ключ.
      node.forEach((item, i) => walk(item, path ? `${path}[${i}]` : `[${i}]`, depth + 1));
      return;
    }
    for (const [key, val] of Object.entries(node)) {
      const here = path ? `${path}.${key}` : key;
      if (GEO_FIELDS_LOWER.has(key.toLowerCase())) {
        const s = scalarValue(val);
        if (s !== null) out[here] = s;
      }
      walk(val, here, depth + 1);
    }
  };
  walk(value, '', 1);
  return out;
}

/** `/cdn-cgi/trace` — не JSON, а `key=value` построчно. Интересно `loc=XX`, но
    забираем всё, что подходит под гео-список, — там же встречается `colo`. */
export function parseCdnTrace(text) {
  const out = {};
  for (const line of String(text || '').split('\n')) {
    const eq = line.indexOf('=');
    if (eq <= 0) continue;
    const key = line.slice(0, eq).trim();
    const val = line.slice(eq + 1).trim();
    if (!key || !val || val.length > 64) continue;
    out[key] = val;
  }
  const geo = {};
  if (out.loc) geo.loc = out.loc;
  if (out.colo) geo.colo = out.colo;
  if (out.ip) geo.ip = out.ip;
  if (out.tls) geo.tls = out.tls;
  return geo;
}

/* ==========================================================================
   Вердикт прогона: какая страна определилась
   ========================================================================== */

/** Названия стран, встречающиеся в ответах открытым текстом. Список нарочно
    короткий — сюда попадает то, что реально видно в гео-ответах, а не весь ISO. */
const COUNTRY_NAMES = {
  russia: 'RU',
  'russian federation': 'RU',
  россия: 'RU',
  belarus: 'BY',
  kazakhstan: 'KZ',
  netherlands: 'NL',
  germany: 'DE',
  france: 'FR',
  'united kingdom': 'GB',
  'united states': 'US',
  usa: 'US',
  finland: 'FI',
  sweden: 'SE',
  poland: 'PL',
  turkey: 'TR',
  japan: 'JP',
  singapore: 'SG',
  ukraine: 'UA',
  armenia: 'AM',
  georgia: 'GE',
  latvia: 'LV',
  lithuania: 'LT',
  estonia: 'EE',
  switzerland: 'CH',
  austria: 'AT',
  spain: 'ES',
  italy: 'IT',
  canada: 'CA',
};

/** Значения, которые выглядят как код страны, но ею не являются. `XX` Cloudflare
    отдаёт, когда страну не определил; `T1` — выход из Tor. */
const NOT_A_COUNTRY = new Set(['XX', 'T1', 'ZZ']);

/** Строка → ISO-код страны либо null. Берём только две заглавные буквы или
    известное название; всё остальное — не страна, а регион, город или POP. */
export function countryCode(value) {
  const raw = String(value ?? '').trim();
  if (!raw) return null;
  if (/^[A-Za-z]{2}$/.test(raw)) {
    const up = raw.toUpperCase();
    // Строчные две буквы — чаще язык (`en`), чем страна; берём только верхний регистр.
    if (raw !== up) return null;
    return NOT_A_COUNTRY.has(up) ? null : up;
  }
  const named = COUNTRY_NAMES[raw.toLowerCase()];
  return named || null;
}

/** Заголовки, чьё значение осмысленно проверять на код страны. `x-amz-cf-pop`,
    `x-served-by` и `fly-region` — это POP, не страна; они полезны для дифа, но в
    вердикт не идут. */
const VERDICT_HEADERS = new Set([
  'cf-ipcountry',
  'cloudfront-viewer-country',
  'x-vercel-ip-country',
  'x-country',
  'x-country-code',
  'x-geo-country',
  'x-appengine-country',
]);

/** Поля тела, чьё значение осмысленно проверять на код страны. `locale`, `ip`,
    `timezone` и `market` в вердикт не идут: `ru-RU` — язык, а не выход. */
const VERDICT_FIELDS = new Set(['country', 'countrycode', 'country_code', 'detectedcountry', 'loc']);

function lastSegment(path) {
  const parts = String(path).split('.');
  const tail = parts[parts.length - 1] || '';
  return tail.replace(/\[\d+\]$/, '').toLowerCase();
}

/** Вердикт прогона: какая страна определилась и по скольким хостам.
    Записи — `[{host, geoHeaders, geoFields}]`. Один хост голосует за код один раз:
    иначе сайт, дёргающий свой гео-эндпоинт двадцать раз, перевесит все остальные. */
export function runVerdict(entries) {
  const byCode = new Map(); // код → Set хостов
  const add = (code, host) => {
    if (!code) return;
    if (!byCode.has(code)) byCode.set(code, new Set());
    byCode.get(code).add(host || '?');
  };
  for (const e of entries || []) {
    for (const [name, value] of Object.entries(e.geoHeaders || {})) {
      if (VERDICT_HEADERS.has(name)) add(countryCode(value), e.host);
    }
    for (const [path, value] of Object.entries(e.geoFields || {})) {
      if (VERDICT_FIELDS.has(lastSegment(path))) add(countryCode(value), e.host);
    }
  }
  const ranked = [...byCode.entries()]
    .map(([code, hosts]) => ({ code, hosts: hosts.size }))
    .sort((a, b) => b.hosts - a.hosts || a.code.localeCompare(b.code));
  return { top: ranked[0] || null, all: ranked };
}
