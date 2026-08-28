/* Свёртка хоста до домена, который можно положить в правило razdacha.

   Правило работает по `domain_suffix`, поэтому хост `api.example.com` разумно
   свернуть до `example.com` — иначе список раздувается и промахивается на первом
   же новом поддомене. Но у общих CDN eTLD+1 общий на весь интернет, и свёрнутый
   `cloudfront.net` заворачивает в туннель чужие сайты вместе с нашим. Такие хосты
   не сворачиваются никогда. */

/** Общие CDN и хостинги: eTLD+1 здесь ничего не говорит о владельце. Свёртка
    запрещена, экспортируется полный хост. */
export const SHARED_CDN = [
  'cloudfront.net',
  'akamaized.net',
  'akamai.net',
  'akamaihd.net',
  'fastly.net',
  'fastlylb.net',
  'b-cdn.net',
  'cdn77.org',
  'azureedge.net',
  'googlevideo.com',
  'appspot.com',
  'herokuapp.com',
  'pages.dev',
  'workers.dev',
  'netlify.app',
  'vercel.app',
  'amazonaws.com',
  'cloudflare.net',
];

const SHARED_CDN_SET = new Set(SHARED_CDN);

/* Приближение публичного списка суффиксов (PSL).

   Полный PSL — это несколько тысяч строк, которые надо где-то держать и время от
   времени обновлять. Расширение ставится распакованным из репозитория, сборщика
   нет, и тащить сюда мегабайт данных ради того, чтобы правильно свернуть
   `example.co.uk`, — плохая сделка. Берём десяток самых частых многосоставных
   зон, всё остальное сворачиваем по правилу «последние две метки». Ошибка стоит
   лишней метки в домене — человек это видит в экспорте и правит руками. */
export const MULTI_TLD = [
  'co.uk',
  'org.uk',
  'ac.uk',
  'gov.uk',
  'com.br',
  'com.au',
  'net.au',
  'co.jp',
  'com.tr',
  'com.ua',
  'net.ru',
  'org.ru',
  'com.ru',
  'co.in',
  'com.cn',
  'com.mx',
  'co.nz',
  'co.za',
  'com.ar',
  'com.pl',
];

const MULTI_TLD_SET = new Set(MULTI_TLD);

/** Хост → eTLD+1 по приближённому правилу выше. IP-адрес и односоставный хост
    возвращаются как есть. */
export function etldPlusOne(host) {
  const h = String(host || '').trim().toLowerCase().replace(/\.$/, '');
  if (!h) return '';
  // IPv4 и всё, что похоже на адрес, не сворачивается: у него нет доменных меток.
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(h) || h.includes(':')) return h;
  const parts = h.split('.');
  if (parts.length <= 2) return h;
  const lastTwo = parts.slice(-2).join('.');
  if (MULTI_TLD_SET.has(lastTwo)) {
    return parts.length >= 3 ? parts.slice(-3).join('.') : h;
  }
  return lastTwo;
}

/** Хост попадает под общий CDN? Проверяем и сам хост, и его eTLD+1. */
export function isSharedCDN(host) {
  const h = String(host || '').trim().toLowerCase();
  if (!h) return false;
  if (SHARED_CDN_SET.has(h)) return true;
  return SHARED_CDN_SET.has(etldPlusOne(h));
}

/** Хост → то, что уедет в правило: eTLD+1 либо полный хост для общих CDN.
    Возвращается `{domain, folded, reason}` — панель показывает причину, почему
    свёртки не случилось. */
export function foldHost(host) {
  const h = String(host || '').trim().toLowerCase().replace(/\.$/, '');
  if (!h) return { domain: '', folded: false, reason: '' };
  if (isSharedCDN(h)) {
    return { domain: h, folded: false, reason: 'общий CDN — свёртка завернула бы чужие сайты' };
  }
  const base = etldPlusOne(h);
  return { domain: base, folded: base !== h, reason: '' };
}

/** Список хостов → список доменов для экспорта: свёрнуто, без дублей, по алфавиту. */
export function exportDomains(hosts) {
  const set = new Set();
  for (const host of hosts || []) {
    const { domain } = foldHost(host);
    if (domain) set.add(domain);
  }
  return [...set].sort();
}
