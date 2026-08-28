/* Проверки чистых функций из lib/. Без фреймворка: открыл test.html — увидел
   список. Всё, что здесь проверяется, не трогает ни chrome, ни сеть. */

import { pathKey, normalizeSegment, entryKey, hostOf } from './lib/pathkey.js';
import { geoHeaders, geoFields, parseCdnTrace, isGeoEndpoint, countryCode, runVerdict } from './lib/geo.js';
import { etldPlusOne, isSharedCDN, foldHost, exportDomains } from './lib/domains.js';
import { diffSessions, tierOf, isAnalytics, isStaticEntry, localePrefix, redirectDiffers, WEIGHTS } from './lib/diff.js';
import { pruneSessions, defaultName } from './lib/storage.js';
import { makePersistQueue, PERSIST_DELAY_MS } from './lib/persist.js';

const results = [];

function check(name, got, want) {
  const g = JSON.stringify(got);
  const w = JSON.stringify(want);
  results.push({ name, ok: g === w, got: g, want: w });
}

function checkTrue(name, got) {
  check(name, !!got, true);
}

/* --- pathKey ------------------------------------------------------------- */

check('pathKey отбрасывает query', pathKey('https://a.io/api/v1/list?x=1&y=2'), '/api/v1/list');
check('pathKey схлопывает hex-сегмент', pathKey('https://a.io/u/deadbeefcafe/profile'), '/u/*/profile');
check('pathKey схлопывает длинный числовой сегмент', pathKey('https://a.io/p/1234567890'), '/p/*');
check('pathKey не трогает короткие числа', pathKey('https://a.io/p/1234'), '/p/1234');
check('pathKey схлопывает UUID', pathKey('https://a.io/x/6f9619ff-8b86-d011-b42d-00c04fc964ff/y'), '/x/*/y');
check('pathKey сохраняет хвостовой слеш', pathKey('https://a.io/ru/news/'), '/ru/news/');
check('pathKey на корне', pathKey('https://a.io'), '/');
check('pathKey на относительном пути', pathKey('/api/v2?z=1'), '/api/v2');
check('normalizeSegment оставляет слово', normalizeSegment('checkout'), 'checkout');
check('normalizeSegment схлопывает hex 8', normalizeSegment('abcdef01'), '*');
check('entryKey приводит метод к верхнему регистру', entryKey('post', 'https://a.io/f?q=1'), 'POST /f');
check('hostOf в нижнем регистре', hostOf('https://API.Example.COM/x'), 'api.example.com');

/* --- гео ----------------------------------------------------------------- */

check('geoHeaders берёт только гео и нижний регистр имени',
  geoHeaders([
    { name: 'CF-IPCountry', value: 'RU' },
    { name: 'Content-Type', value: 'text/html' },
    { name: 'x-vercel-ip-country', value: 'NL' },
  ]),
  { 'cf-ipcountry': 'RU', 'x-vercel-ip-country': 'NL' });
check('geoHeaders пропускает пустое значение',
  geoHeaders([{ name: 'x-country', value: '  ' }]), {});
check('geoFields достаёт вложенное поле с путём',
  geoFields({ data: { user: { country: 'RU', name: 'aa' } } }), { 'data.user.country': 'RU' });
check('geoFields берёт число',
  geoFields({ region: 77 }), { region: '77' });
check('geoFields не берёт объект как значение',
  geoFields({ geo: { country: 'NL' } }), { 'geo.country': 'NL' });
check('geoFields не берёт значение длиннее 64',
  geoFields({ country: 'x'.repeat(65) }), {});
check('geoFields помечает индекс массива',
  geoFields({ items: [{ country: 'DE' }] }), { 'items[0].country': 'DE' });
check('geoFields не спускается глубже шести',
  geoFields({ a: { b: { c: { d: { e: { f: { country: 'RU' } } } } } } }), {});
check('parseCdnTrace достаёт loc',
  parseCdnTrace('fl=12a\nip=1.2.3.4\nloc=NL\ncolo=AMS\ntls=TLSv1.3'),
  { loc: 'NL', colo: 'AMS', ip: '1.2.3.4', tls: 'TLSv1.3' });
check('parseCdnTrace на мусоре пуст', parseCdnTrace('чепуха без знака равенства'), {});
checkTrue('isGeoEndpoint по хосту', isGeoEndpoint('ipapi.co', '/json'));
checkTrue('isGeoEndpoint по поддомену', isGeoEndpoint('pro.ip-api.com', '/json'));
checkTrue('isGeoEndpoint по пути cdn-cgi', isGeoEndpoint('example.com', '/cdn-cgi/trace'));
check('isGeoEndpoint на обычном хосте', isGeoEndpoint('example.com', '/api'), false);
check('countryCode берёт две заглавные', countryCode('NL'), 'NL');
check('countryCode отвергает строчные (это язык)', countryCode('ru'), null);
check('countryCode отвергает XX', countryCode('XX'), null);
check('countryCode понимает название', countryCode('Russia'), 'RU');
check('countryCode отвергает POP', countryCode('AMS'), null);

check('runVerdict считает хосты, а не запросы',
  runVerdict([
    { host: 'a.io', geoHeaders: { 'cf-ipcountry': 'RU' }, geoFields: {} },
    { host: 'a.io', geoHeaders: { 'cf-ipcountry': 'RU' }, geoFields: {} },
    { host: 'b.io', geoHeaders: {}, geoFields: { 'data.country': 'RU' } },
    { host: 'c.io', geoHeaders: { 'x-served-by': 'NL' }, geoFields: {} },
  ]).top,
  { code: 'RU', hosts: 2 });
check('runVerdict без сигнала пуст', runVerdict([{ host: 'a.io' }]).top, null);

/* --- домены -------------------------------------------------------------- */

check('etldPlusOne сворачивает поддомен', etldPlusOne('api.v2.example.com'), 'example.com');
check('etldPlusOne учитывает co.uk', etldPlusOne('shop.example.co.uk'), 'example.co.uk');
check('etldPlusOne учитывает com.br', etldPlusOne('a.b.example.com.br'), 'example.com.br');
check('etldPlusOne не трогает IPv4', etldPlusOne('192.0.2.10'), '192.0.2.10');
check('etldPlusOne не трогает двухметочный хост', etldPlusOne('example.com'), 'example.com');
checkTrue('isSharedCDN по eTLD+1', isSharedCDN('d111.cloudfront.net'));
checkTrue('isSharedCDN по самому хосту', isSharedCDN('vercel.app'));
check('isSharedCDN на обычном домене', isSharedCDN('api.example.com'), false);
check('foldHost сворачивает обычный хост',
  foldHost('api.example.com').domain, 'example.com');
check('foldHost помечает свёртку', foldHost('api.example.com').folded, true);
check('foldHost не сворачивает общий CDN',
  foldHost('d111.cloudfront.net').domain, 'd111.cloudfront.net');
check('foldHost объясняет отказ свёртки',
  foldHost('d111.cloudfront.net').folded, false);
check('exportDomains без дублей и по алфавиту',
  exportDomains(['b.example.com', 'a.example.com', 'x.cloudfront.net', 'zed.org']),
  ['example.com', 'x.cloudfront.net', 'zed.org']);

/* --- диф ----------------------------------------------------------------- */

check('tierOf высокая', tierOf(100), 'высокая');
check('tierOf средняя', tierOf(50), 'средняя');
check('tierOf низкая', tierOf(49), 'низкая');
checkTrue('isAnalytics по поддомену', isAnalytics('www.google-analytics.com'));
check('isAnalytics на обычном хосте', isAnalytics('example.com'), false);
checkTrue('isStaticEntry по mime и типу',
  isStaticEntry({ mime: 'image/png', resourceType: 'image' }));
check('isStaticEntry на XHR', isStaticEntry({ mime: 'application/json', resourceType: 'xhr' }), false);
check('localePrefix узнаёт /ru/', localePrefix('https://a.io/ru/news'), 'ru');
check('localePrefix узнаёт /en-US/', localePrefix('https://a.io/en-US/news'), 'en-us');
check('localePrefix на обычном пути', localePrefix('https://a.io/news'), '');
checkTrue('redirectDiffers по хосту',
  redirectDiffers('https://a.io/x', 'https://b.io/x'));
checkTrue('redirectDiffers по языковому префиксу',
  redirectDiffers('https://a.io/ru/x', 'https://a.io/nl/x'));
check('redirectDiffers на одинаковых', redirectDiffers('https://a.io/x', 'https://a.io/x'), false);

const runA = {
  label: 'direct',
  entries: [
    {
      host: 'api.shop.com', method: 'GET', pathKey: '/geo', status: 200,
      geoHeaders: { 'cf-ipcountry': 'RU' }, geoFields: { 'data.country': 'RU' },
      redirectTo: '', resourceType: 'xhr', mime: 'application/json',
    },
    {
      host: 'shop.com', method: 'GET', pathKey: '/catalog', status: 403,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'document', mime: 'text/html',
    },
    {
      host: 'cdn.shop.com', method: 'GET', pathKey: '/logo.png', status: 200,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'image', mime: 'image/png',
    },
    {
      host: 'www.google-analytics.com', method: 'POST', pathKey: '/collect', status: 200,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'xhr', mime: 'text/plain',
    },
    {
      host: 'same.shop.com', method: 'GET', pathKey: '/api/config', status: 200,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'xhr', mime: 'application/json',
    },
  ],
};
const runB = {
  label: 'tunnel',
  entries: [
    {
      host: 'api.shop.com', method: 'GET', pathKey: '/geo', status: 200,
      geoHeaders: { 'cf-ipcountry': 'NL' }, geoFields: { 'data.country': 'NL' },
      redirectTo: '', resourceType: 'xhr', mime: 'application/json',
    },
    {
      host: 'shop.com', method: 'GET', pathKey: '/catalog', status: 200,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'document', mime: 'text/html',
    },
    {
      host: 'cdn.shop.com', method: 'GET', pathKey: '/logo.png', status: 404,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'image', mime: 'image/png',
    },
    {
      host: 'ads.example.net', method: 'GET', pathKey: '/px', status: 200,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'xhr', mime: 'text/plain',
    },
    {
      host: 'same.shop.com', method: 'GET', pathKey: '/api/config', status: 200,
      geoHeaders: {}, geoFields: {}, redirectTo: '', resourceType: 'xhr', mime: 'application/json',
    },
  ],
};
const d = diffSessions(runA, runB);
const byHost = Object.fromEntries(d.hosts.map((h) => [h.host, h]));

check('диф: вердикт A', d.verdictA.top, { code: 'RU', hosts: 1 });
check('диф: вердикт B', d.verdictB.top, { code: 'NL', hosts: 1 });
check('диф: прогоны различимы', d.indistinguishable, false);
check('диф: гео-заголовок плюс гео-поле',
  byHost['api.shop.com'].score, WEIGHTS.geoHeader + WEIGHTS.geoField);
check('диф: гео-хост в высоком тире', byHost['api.shop.com'].tier, 'высокая');
check('диф: 403 → 200 даёт разблокировку',
  byHost['shop.com'].score, WEIGHTS.statusUnblock);
check('диф: разблокировка это средняя уверенность', byHost['shop.com'].tier, 'средняя');
check('диф: прочая смена статуса весит меньше',
  byHost['cdn.shop.com'].score, WEIGHTS.statusOther);
checkTrue('диф: статика помечена шумом', byHost['cdn.shop.com'].noise);
checkTrue('диф: аналитика помечена шумом', byHost['www.google-analytics.com'].noise);
check('диф: аналитика опознана по списку', byHost['www.google-analytics.com'].analytics, true);
check('диф: одинаково ведущий себя хост в выдачу не идёт', 'same.shop.com' in byHost, false);
check('диф: хост только в одном прогоне',
  byHost['ads.example.net'].score, WEIGHTS.onlyOneRun);
check('диф: шум уходит в хвост списка',
  d.hosts[d.hosts.length - 1].noise, true);
check('диф: первым идёт самый весомый', d.hosts[0].host, 'api.shop.com');

const same = diffSessions(runA, JSON.parse(JSON.stringify(runA)));
checkTrue('диф: одинаковые прогоны неразличимы', same.indistinguishable);
check('диф: одинаковые прогоны без расхождений', same.hosts.length, 0);

/* Сигнал не суммируется по десять раз: два запроса с одинаковой сменой заголовка
   дают ровно один вклад. */
const twiceA = { entries: [runA.entries[0], { ...runA.entries[0], pathKey: '/geo2' }] };
const twiceB = { entries: [runB.entries[0], { ...runB.entries[0], pathKey: '/geo2' }] };
check('диф: повтор сигнала не удваивает вес',
  diffSessions(twiceA, twiceB).hosts[0].score, WEIGHTS.geoHeader + WEIGHTS.geoField);

/* --- хранилище ----------------------------------------------------------- */

check('pruneSessions оставляет свежие',
  pruneSessions([{ startedAt: 1 }, { startedAt: 3 }, { startedAt: 2 }], 2)
    .map((s) => s.startedAt), [3, 2]);
check('pruneSessions на пустом', pruneSessions(null, 5), []);
check('defaultName содержит хост', defaultName('shop.com', Date.parse('2026-08-28T14:05:00')),
  'shop.com 14:05');

/* --- отложенная запись ---------------------------------------------------- */

/* Таймер подставной: очередь получает его снаружи, поэтому проверяется без ожидания
   и без chrome. */
function fakeTimers() {
  let queued = null;
  let id = 0;
  return {
    setTimer(fn, delay) { queued = { fn, delay }; return ++id; },
    clearTimer() { queued = null; },
    fire() { const q = queued; queued = null; if (q) q.fn(); },
    get armed() { return queued !== null; },
    get delay() { return queued?.delay ?? null; },
  };
}

/* Прогон, в котором ни одно тело не дало гео-полей — обычный случай: сайт режет по IP
   молча. Такой прогон обязан доехать до storage целиком, а не остаться в памяти. */
const t = fakeTimers();
const written = [];
const session = { id: 's1', entries: [] };
const queue = makePersistQueue(() => written.push(session.entries.length), {
  setTimer: t.setTimer, clearTimer: t.clearTimer,
});

check('очередь взводится с первого запроса', queue.schedule(), true);
check('очередь ждёт две секунды', t.delay, PERSIST_DELAY_MS);
session.entries.push({ host: 'a.io' });
check('пока таймер висит, запрос таймер не переставляет', queue.schedule(), false);
session.entries.push({ host: 'b.io' });
queue.schedule();
check('до срабатывания в storage ничего не ушло', written, []);
t.fire();
check('по срабатыванию пишется вся пачка, включая последнюю запись', written, [2]);
check('после записи таймер снят', queue.pending, false);
check('очередь снова принимает запросы', queue.schedule(), true);
check('cancel гасит висящий таймер', queue.cancel(), true);
check('после cancel таймера нет', [queue.pending, t.armed], [false, false]);
t.fire();
check('погашенный таймер уже ничего не пишет', written, [2]);
check('cancel на пустой очереди безвреден', queue.cancel(), false);

/* --- вывод --------------------------------------------------------------- */

const failed = results.filter((r) => !r.ok);
const list = document.getElementById('results');
list.innerHTML = results.map((r) => (r.ok
  ? `<li class="ok">ok — ${r.name}</li>`
  : `<li class="fail">FAIL — ${r.name}: получили ${r.got}, ждали ${r.want}</li>`)).join('');
const summary = document.getElementById('summary');
summary.textContent = failed.length === 0
  ? `Все ${results.length} проверок прошли.`
  : `Провалено ${failed.length} из ${results.length}.`;
summary.className = failed.length === 0 ? 'ok' : 'fail';
