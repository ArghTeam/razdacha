/* Разбор одного прогона: что отказало и кто упомянул страну.

   Два рода данных, и смешивать их нельзя ([ADR 0022](../../../docs/decisions/0022-single-run-entry.md)):

   - блокировки — наблюдение. 403/451/429 и запрос, который не дошёл, это то, что
     сервер ответил браузеру, второго прогона для них не нужно;
   - упоминания страны — подсказка. Гео-заголовок ничего не доказывает: `cf-ipcountry`
     отдаёт половина интернета, и от него может не зависеть ровно ничего.

   Поэтому здесь нет ни общего веса, ни тира: числовая колонка, в которой рядом стоят
   измерение и догадка, выдаёт догадку за измерение. Веса живут в `diff.js` и считают
   расхождение между двумя прогонами — это другое измерение и другой ответ. */

/** Коды, которыми сайт отказывает по географии или лимиту. Тот же набор, что в
    `diff.js`: там он ловит разблокировку, здесь — сам отказ. */
export const BLOCK_STATUSES = [403, 451, 429];

const BLOCK_SET = new Set(BLOCK_STATUSES);

/** Статус нулевой означает, что ответа не было вовсе: запрос оборвали, сбросили или
    он не дошёл. Для разбора это тот же отказ, только без кода. */
export const ABORTED = 'оборван';

/** Статус записи → как назвать отказ, либо пустая строка, если отказа не было. */
export function blockKind(status) {
  const s = Number(status) || 0;
  if (s === 0) return ABORTED;
  return BLOCK_SET.has(s) ? String(s) : '';
}

/** Сколько разных значений одного признака показывать. Больше трёх — это уже не
    подсказка, а простыня: сайт, отдающий `x-served-by` на каждый запрос, забьёт экран. */
export const MAX_VALUES = 3;

function addValue(map, key, value) {
  if (!map.has(key)) map.set(key, new Set());
  map.get(key).add(value);
}

function listValues(map) {
  return [...map.entries()].map(([name, values]) => ({
    name,
    values: [...values].slice(0, MAX_VALUES),
    more: Math.max(0, values.size - MAX_VALUES),
  }));
}

/** Разобрать один прогон.

    Записи — те же, что пишет панель: `[{host, status, geoHeaders, geoFields}]`.
    Возвращает `{blocked, geo, hosts, entries}`:

    - `blocked` — `[{host, total, statuses: [{status, count}]}]`, по убыванию числа
      отказавших запросов. `status` — код либо `оборван`;
    - `geo` — `[{host, requests, headers, fields}]`, где `headers` и `fields` —
      `[{name, values, more}]`; по убыванию числа найденных признаков;
    - `hosts` — сколько разных хостов встретилось в прогоне, `entries` — сколько
      записей: пустая выдача при сотне хостов и пустая при нуле — разные новости. */
export function scanRun(entries) {
  const blockedBy = new Map(); // хост → Map(вид отказа → счётчик)
  const geoBy = new Map(); // хост → {headers, fields, requests}
  const hosts = new Set();
  let total = 0;

  for (const e of entries || []) {
    const host = String(e?.host || '').toLowerCase();
    if (!host) continue;
    hosts.add(host);
    total += 1;

    const kind = blockKind(e.status);
    if (kind) {
      if (!blockedBy.has(host)) blockedBy.set(host, new Map());
      const byKind = blockedBy.get(host);
      byKind.set(kind, (byKind.get(kind) || 0) + 1);
    }

    const headers = e.geoHeaders || {};
    const fields = e.geoFields || {};
    if (!Object.keys(headers).length && !Object.keys(fields).length) continue;
    if (!geoBy.has(host)) {
      geoBy.set(host, { headers: new Map(), fields: new Map(), requests: 0 });
    }
    const bag = geoBy.get(host);
    bag.requests += 1;
    for (const [name, value] of Object.entries(headers)) addValue(bag.headers, name, String(value));
    for (const [path, value] of Object.entries(fields)) addValue(bag.fields, path, String(value));
  }

  const blocked = [...blockedBy.entries()]
    .map(([host, byKind]) => {
      const statuses = [...byKind.entries()]
        .map(([status, count]) => ({ status, count }))
        .sort((a, b) => b.count - a.count || a.status.localeCompare(b.status));
      return {
        host,
        total: statuses.reduce((n, s) => n + s.count, 0),
        statuses,
      };
    })
    .sort((a, b) => b.total - a.total || a.host.localeCompare(b.host));

  const geo = [...geoBy.entries()]
    .map(([host, bag]) => ({
      host,
      requests: bag.requests,
      headers: listValues(bag.headers),
      fields: listValues(bag.fields),
    }))
    .sort((a, b) => (b.headers.length + b.fields.length) - (a.headers.length + a.fields.length)
      || b.requests - a.requests
      || a.host.localeCompare(b.host));

  return { blocked, geo, hosts: hosts.size, entries: total };
}

/** Прогон, в котором не нашлось ни отказа, ни упоминания страны. Это не «ничего нет»,
    а самый частый повод взяться за сравнение двух прогонов: сайт может резать по
    стране молча. */
export function runIsSilent(scan) {
  return !scan || (scan.blocked.length === 0 && scan.geo.length === 0);
}
