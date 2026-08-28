/* Сравнение двух прогонов: что изменилось между «напрямую» и «через туннель».

   Ядро инструмента. Эвристика по заголовкам даёт кандидатов и врёт в обе стороны;
   расхождение между двумя прогонами — измерение. Записи сопоставляются по
   `(host, method, pathKey)`, вердикт выносится по хосту агрегатом. */

import { hostOf, pathKey } from './pathkey.js';
import { runVerdict } from './geo.js';

/** Веса сигналов. Порядок соответствует убыванию доказательности: смена
    гео-заголовка означает, что сервер увидел другую страну; присутствие хоста
    только в одном прогоне чаще всего означает рекламу, а не гео. */
export const WEIGHTS = {
  geoHeader: 100,
  geoField: 80,
  statusUnblock: 70,
  statusOther: 30,
  redirect: 50,
  onlyOneRun: 15,
};

export const TIER_HIGH = 100;
export const TIER_MEDIUM = 50;

/** Аналитика и реклама: их присутствие меняется от прогона к прогону само по себе,
    а в туннель они не нужны. Список нарочно короткий и очевидный — раздутый
    чёрный список начинает прятать полезное. */
export const ANALYTICS = [
  'google-analytics.com',
  'googletagmanager.com',
  'doubleclick.net',
  'facebook.net',
  'hotjar.com',
  'sentry.io',
  'amplitude.com',
  'mixpanel.com',
  'segment.com',
  'segment.io',
  'mc.yandex.ru',
  'mc.yandex.com',
  'top-mail.ru',
  'adservice.google.com',
];

/** Коды, которыми сайт отказывает по географии или лимиту. Переход между этой
    группой и 2xx — самый прямой признак, что туннель что-то изменил. */
const BLOCKED = new Set([403, 451, 429]);

export function isAnalytics(host) {
  const h = String(host || '').toLowerCase();
  return ANALYTICS.some((a) => h === a || h.endsWith(`.${a}`));
}

/** Статика: картинки, шрифты, стили. Такой хост в правило не нужен — он отдаёт
    байты, а не решает про страну. */
export function isStaticEntry(entry) {
  const mime = String(entry?.mime || '').toLowerCase();
  const type = String(entry?.resourceType || '').toLowerCase();
  const staticMime = mime.startsWith('image/')
    || mime.startsWith('font/')
    || mime.startsWith('audio/')
    || mime.startsWith('video/')
    || mime.startsWith('text/css');
  const staticType = ['image', 'font', 'stylesheet', 'media'].includes(type);
  if (mime && type) return staticMime && staticType;
  return staticMime || staticType;
}

/** Языковой или региональный префикс пути: `/ru/`, `/en-us/`, `/de-DE/`.
    Возвращает префикс в нижнем регистре либо пустую строку. */
export function localePrefix(url) {
  const p = pathKey(url);
  const seg = p.split('/')[1] || '';
  return /^[a-z]{2}(?:[-_][a-z]{2})?$/i.test(seg) ? seg.toLowerCase() : '';
}

/** Редирект «ушёл в другое место»: другой хост либо другой языковой префикс. */
export function redirectDiffers(a, b) {
  const ra = String(a || '');
  const rb = String(b || '');
  if (!ra && !rb) return false;
  if (!ra || !rb) return true;
  if (ra === rb) return false;
  const ha = hostOf(ra);
  const hb = hostOf(rb);
  if (ha && hb && ha !== hb) return true;
  return localePrefix(ra) !== localePrefix(rb);
}

function statusSignal(a, b) {
  if (!a || !b || a === b) return null;
  const aBlocked = BLOCKED.has(a);
  const bBlocked = BLOCKED.has(b);
  const aOk = a >= 200 && a < 300;
  const bOk = b >= 200 && b < 300;
  if ((aBlocked && bOk) || (bBlocked && aOk)) return 'statusUnblock';
  return 'statusOther';
}

/** Сгруппировать записи прогона по хосту, внутри — по `(method, pathKey)`.
    Из одинаковых ключей берём последнюю запись: повтор того же запроса обычно
    отличается только временем. */
function indexRun(entries) {
  const byHost = new Map();
  for (const e of entries || []) {
    const host = String(e.host || '').toLowerCase();
    if (!host) continue;
    if (!byHost.has(host)) byHost.set(host, new Map());
    const key = `${String(e.method || 'GET').toUpperCase()} ${e.pathKey || '/'}`;
    byHost.get(host).set(key, e);
  }
  return byHost;
}

/** Один сигнал — один вклад в вес, сколько бы раз он ни сработал по хосту.
    Иначе хост, дёргающий свой гео-эндпоинт на каждой странице, набирает тысячу
    очков там, где другой набрал бы сотню за то же самое. */
function pushSignal(signals, kind, detail) {
  if (!signals.has(kind)) signals.set(kind, detail);
}

export function tierOf(score) {
  if (score >= TIER_HIGH) return 'высокая';
  if (score >= TIER_MEDIUM) return 'средняя';
  return 'низкая';
}

/** Сравнить два прогона.
    `a`, `b` — сессии `{label, name, entries}`.
    Возвращает `{verdictA, verdictB, indistinguishable, hosts}`; `hosts`
    отсортирован по убыванию веса. */
export function diffSessions(a, b) {
  const entriesA = a?.entries || [];
  const entriesB = b?.entries || [];
  const verdictA = runVerdict(entriesA);
  const verdictB = runVerdict(entriesB);
  const codeA = verdictA.top?.code || null;
  const codeB = verdictB.top?.code || null;
  // Прогоны неразличимы, если страна не сменилась. Скорее всего туннель не
  // сработал, и сравнивать нечего: любое расхождение здесь — шум.
  const indistinguishable = !codeA || !codeB || codeA === codeB;

  const idxA = indexRun(entriesA);
  const idxB = indexRun(entriesB);
  const hosts = new Set([...idxA.keys(), ...idxB.keys()]);

  const result = [];
  for (const host of hosts) {
    const mapA = idxA.get(host);
    const mapB = idxB.get(host);
    const signals = new Map();

    if (!mapA || !mapB) {
      pushSignal(signals, 'onlyOneRun', `хост есть только в прогоне ${mapA ? 'A' : 'B'}`);
    } else {
      for (const [key, ea] of mapA) {
        const eb = mapB.get(key);
        if (!eb) continue;

        for (const [name, va] of Object.entries(ea.geoHeaders || {})) {
          const vb = (eb.geoHeaders || {})[name];
          if (vb !== undefined && vb !== va) {
            pushSignal(signals, 'geoHeader', `${name}: ${va} → ${vb}`);
          }
        }
        for (const [path, va] of Object.entries(ea.geoFields || {})) {
          const vb = (eb.geoFields || {})[path];
          if (vb !== undefined && vb !== va) {
            pushSignal(signals, 'geoField', `${path}: ${va} → ${vb}`);
          }
        }
        const st = statusSignal(ea.status, eb.status);
        if (st) pushSignal(signals, st, `${key}: ${ea.status} → ${eb.status}`);

        if (redirectDiffers(ea.redirectTo, eb.redirectTo)) {
          pushSignal(signals, 'redirect', `${key}: ${ea.redirectTo || '—'} → ${eb.redirectTo || '—'}`);
        }
      }
    }

    // Хост без единого сигнала в выдачу не идёт: он вёл себя одинаково.
    if (signals.size === 0) continue;

    let score = 0;
    for (const kind of signals.keys()) score += WEIGHTS[kind] || 0;

    const all = [...(mapA?.values() || []), ...(mapB?.values() || [])];
    const staticOnly = all.length > 0 && all.every(isStaticEntry);
    const noise = staticOnly || isAnalytics(host);

    result.push({
      host,
      score,
      tier: tierOf(score),
      noise,
      staticOnly,
      analytics: isAnalytics(host),
      signals: [...signals.entries()].map(([kind, detail]) => ({
        kind, detail, weight: WEIGHTS[kind] || 0,
      })),
      countA: mapA ? mapA.size : 0,
      countB: mapB ? mapB.size : 0,
    });
  }

  // Шум уходит вниз списка независимо от веса — он там не потому, что слабый,
  // а потому, что в правило не нужен.
  result.sort((x, y) => (x.noise === y.noise
    ? y.score - x.score || x.host.localeCompare(y.host)
    : (x.noise ? 1 : -1)));

  return { verdictA, verdictB, indistinguishable, hosts: result };
}
