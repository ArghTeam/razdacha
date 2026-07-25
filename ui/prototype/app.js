/* razdacha — прототип панели. Ванильный JS, без сборки и без внешних зависимостей.
   Всё, что здесь «происходит», происходит в памяти вкладки: реальный демон отдаёт
   те же формы данных по REST (docs/05-api.md), но прототип не ходит в сеть. */

'use strict';

/* ==========================================================================
   1. QR-кодер: byte mode, уровень коррекции L, версии 1–40.
   Написан здесь, а не подключён библиотекой: панель встраивается в бинарник
   через go:embed, тянуть CDN некуда и незачем.
   ========================================================================== */

const QR = (() => {
  const EXP = new Uint8Array(512);
  const LOG = new Uint8Array(256);
  (function initGF() {
    let x = 1;
    for (let i = 0; i < 255; i++) { EXP[i] = x; LOG[x] = i; x <<= 1; if (x & 0x100) x ^= 0x11d; }
    for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
  })();
  const mul = (a, b) => (a === 0 || b === 0) ? 0 : EXP[LOG[a] + LOG[b]];

  // Всего кодовых слов по версиям 1..40.
  const TOTAL = [26, 44, 70, 100, 134, 172, 196, 242, 292, 346, 404, 466, 532, 581, 655,
    733, 815, 901, 991, 1085, 1156, 1258, 1364, 1474, 1588, 1706, 1828, 1921, 2051, 2185,
    2323, 2465, 2611, 2761, 2876, 3034, 3196, 3362, 3532, 3706];
  // Кодовых слов коррекции на блок, уровень L.
  const ECC = [7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28,
    28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30];
  // Число блоков коррекции, уровень L.
  const BLOCKS = [1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10,
    12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25];
  // Центры выравнивающих узоров.
  const ALIGN = [[], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42],
    [6, 26, 46], [6, 28, 50], [6, 30, 54], [6, 32, 58], [6, 34, 62], [6, 26, 46, 66],
    [6, 26, 48, 70], [6, 26, 50, 74], [6, 30, 54, 78], [6, 30, 56, 82], [6, 30, 58, 86],
    [6, 34, 62, 90], [6, 28, 50, 72, 94], [6, 26, 50, 74, 98], [6, 30, 54, 78, 102],
    [6, 28, 54, 80, 106], [6, 32, 58, 84, 110], [6, 30, 58, 86, 114], [6, 34, 62, 90, 118],
    [6, 26, 50, 74, 98, 122], [6, 30, 54, 78, 102, 126], [6, 26, 52, 78, 104, 130],
    [6, 30, 56, 82, 108, 134], [6, 34, 60, 86, 112, 138], [6, 30, 58, 86, 114, 142],
    [6, 34, 62, 90, 118, 146], [6, 30, 54, 78, 102, 126, 150], [6, 24, 50, 76, 102, 128, 154],
    [6, 28, 54, 80, 106, 132, 158], [6, 32, 58, 84, 110, 136, 162],
    [6, 26, 54, 82, 110, 138, 166], [6, 30, 58, 86, 114, 142, 170]];

  function genPoly(n) {
    let p = [1];
    for (let i = 0; i < n; i++) {
      const r = new Array(p.length + 1).fill(0);
      for (let j = 0; j < p.length; j++) { r[j] ^= p[j]; r[j + 1] ^= mul(p[j], EXP[i]); }
      p = r;
    }
    return p;
  }

  function rsRemainder(data, ecLen) {
    const gen = genPoly(ecLen);
    const res = new Array(data.length + ecLen).fill(0);
    for (let i = 0; i < data.length; i++) res[i] = data[i];
    for (let i = 0; i < data.length; i++) {
      const c = res[i];
      if (c === 0) continue;
      for (let j = 0; j < gen.length; j++) res[i + j] ^= mul(gen[j], c);
    }
    return res.slice(data.length);
  }

  const MASKS = [
    (r, c) => (r + c) % 2 === 0,
    (r) => r % 2 === 0,
    (r, c) => c % 3 === 0,
    (r, c) => (r + c) % 3 === 0,
    (r, c) => (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0,
    (r, c) => ((r * c) % 2) + ((r * c) % 3) === 0,
    (r, c) => (((r * c) % 2) + ((r * c) % 3)) % 2 === 0,
    (r, c) => (((r + c) % 2) + ((r * c) % 3)) % 2 === 0,
  ];

  function bchFormat(data) {
    let d = data << 10;
    for (let i = 14; i >= 10; i--) if ((d >> i) & 1) d ^= 0x537 << (i - 10);
    return ((data << 10) | d) ^ 0x5412;
  }
  function bchVersion(v) {
    let d = v << 12;
    for (let i = 17; i >= 12; i--) if ((d >> i) & 1) d ^= 0x1f25 << (i - 12);
    return (v << 12) | d;
  }

  function skeleton(version) {
    const size = version * 4 + 17;
    const m = [], fn = [];
    for (let i = 0; i < size; i++) { m.push(new Uint8Array(size)); fn.push(new Uint8Array(size)); }
    const set = (r, c, v) => { if (r < 0 || c < 0 || r >= size || c >= size) return; m[r][c] = v ? 1 : 0; fn[r][c] = 1; };

    const finder = (R, C) => {
      for (let r = -1; r <= 7; r++) for (let c = -1; c <= 7; c++) {
        const on = (r >= 0 && r <= 6 && (c === 0 || c === 6))
          || (c >= 0 && c <= 6 && (r === 0 || r === 6))
          || (r >= 2 && r <= 4 && c >= 2 && c <= 4);
        set(R + r, C + c, on);
      }
    };
    finder(0, 0); finder(0, size - 7); finder(size - 7, 0);

    const centers = ALIGN[version - 1];
    for (const r of centers) for (const c of centers) {
      const nearFinder = (r <= 8 && c <= 8) || (r <= 8 && c >= size - 9) || (r >= size - 9 && c <= 8);
      if (nearFinder) continue;
      for (let dr = -2; dr <= 2; dr++) for (let dc = -2; dc <= 2; dc++) {
        set(r + dr, c + dc, Math.max(Math.abs(dr), Math.abs(dc)) !== 1);
      }
    }

    for (let i = 8; i < size - 8; i++) { set(6, i, i % 2 === 0); set(i, 6, i % 2 === 0); }
    set(size - 8, 8, true); // тёмный модуль

    // резерв под формат
    for (let i = 0; i < 9; i++) { if (!fn[8][i]) set(8, i, false); if (!fn[i][8]) set(i, 8, false); }
    for (let i = size - 8; i < size; i++) { if (!fn[8][i]) set(8, i, false); if (!fn[i][8]) set(i, 8, false); }

    if (version >= 7) {
      const bits = bchVersion(version);
      for (let i = 0; i < 18; i++) {
        const on = ((bits >> i) & 1) === 1;
        set(Math.floor(i / 3), (i % 3) + size - 11, on);
        set((i % 3) + size - 11, Math.floor(i / 3), on);
      }
    }
    return { m, fn, size };
  }

  function penalty(m, size) {
    let score = 0;
    // 1. серии одного цвета
    for (let pass = 0; pass < 2; pass++) {
      for (let a = 0; a < size; a++) {
        let run = 1, prev = -1;
        for (let b = 0; b < size; b++) {
          const v = pass === 0 ? m[a][b] : m[b][a];
          if (v === prev) { run++; } else { if (run >= 5) score += 3 + (run - 5); run = 1; prev = v; }
        }
        if (run >= 5) score += 3 + (run - 5);
      }
    }
    // 2. блоки 2×2
    for (let r = 0; r < size - 1; r++) for (let c = 0; c < size - 1; c++) {
      const v = m[r][c];
      if (v === m[r][c + 1] && v === m[r + 1][c] && v === m[r + 1][c + 1]) score += 3;
    }
    // 3. узор 1:1:3:1:1 с полем
    const pat = [1, 0, 1, 1, 1, 0, 1, 0, 0, 0, 0];
    const rpat = pat.slice().reverse();
    const match = (get, i) => {
      let a = true, b = true;
      for (let k = 0; k < 11; k++) { const v = get(i + k); if (v !== pat[k]) a = false; if (v !== rpat[k]) b = false; }
      return a || b;
    };
    for (let r = 0; r < size; r++) for (let c = 0; c + 11 <= size; c++) if (match(i => m[r][i], c)) score += 40;
    for (let c = 0; c < size; c++) for (let r = 0; r + 11 <= size; r++) if (match(i => m[i][c], r)) score += 40;
    // 4. баланс тёмного
    let dark = 0;
    for (let r = 0; r < size; r++) for (let c = 0; c < size; c++) dark += m[r][c];
    score += Math.floor(Math.abs(dark * 100 / (size * size) - 50) / 5) * 10;
    return score;
  }

  /** Возвращает матрицу модулей (массив Uint8Array) для строки text. */
  function matrix(text) {
    const bytes = new TextEncoder().encode(text);
    let version = 1, dataCw = 0;
    for (; version <= 40; version++) {
      dataCw = TOTAL[version - 1] - ECC[version - 1] * BLOCKS[version - 1];
      const cc = version < 10 ? 8 : 16;
      if (4 + cc + 8 * bytes.length <= dataCw * 8) break;
    }
    if (version > 40) throw new Error('слишком длинные данные для QR-кода');

    // битовый поток
    const bits = [];
    const push = (val, len) => { for (let i = len - 1; i >= 0; i--) bits.push((val >> i) & 1); };
    push(4, 4);
    push(bytes.length, version < 10 ? 8 : 16);
    for (const b of bytes) push(b, 8);
    for (let i = 0; i < 4 && bits.length < dataCw * 8; i++) bits.push(0);
    while (bits.length % 8) bits.push(0);
    const cw = [];
    for (let i = 0; i < bits.length; i += 8) {
      let v = 0; for (let j = 0; j < 8; j++) v = (v << 1) | bits[i + j];
      cw.push(v);
    }
    const PAD = [0xec, 0x11];
    for (let i = 0; cw.length < dataCw; i++) cw.push(PAD[i % 2]);

    // блоки, коррекция, чередование
    const nBlocks = BLOCKS[version - 1], ecLen = ECC[version - 1];
    const shortLen = Math.floor(dataCw / nBlocks), longCount = dataCw % nBlocks;
    const dBlocks = [], eBlocks = [];
    let off = 0;
    for (let i = 0; i < nBlocks; i++) {
      const len = shortLen + (i >= nBlocks - longCount ? 1 : 0);
      const d = cw.slice(off, off + len); off += len;
      dBlocks.push(d);
      eBlocks.push(rsRemainder(d, ecLen));
    }
    const out = [];
    for (let i = 0; i < shortLen + 1; i++) for (const d of dBlocks) if (i < d.length) out.push(d[i]);
    for (let i = 0; i < ecLen; i++) for (const e of eBlocks) out.push(e[i]);

    const stream = [];
    for (const b of out) for (let i = 7; i >= 0; i--) stream.push((b >> i) & 1);

    // размещение по зигзагу + выбор маски
    let best = null, bestScore = Infinity;
    for (let mask = 0; mask < 8; mask++) {
      const { m, fn, size } = skeleton(version);
      const maskFn = MASKS[mask];
      let idx = 0, row = size - 1, dir = -1;
      for (let col = size - 1; col > 0; col -= 2) {
        if (col === 6) col--;
        for (;;) {
          for (let k = 0; k < 2; k++) {
            const cc = col - k;
            if (!fn[row][cc]) {
              let dark = idx < stream.length ? stream[idx++] : 0;
              if (maskFn(row, cc)) dark ^= 1;
              m[row][cc] = dark;
            }
          }
          row += dir;
          if (row < 0 || row >= size) { row -= dir; dir = -dir; break; }
        }
      }
      const fmt = bchFormat((0b01 << 3) | mask);
      for (let i = 0; i < 15; i++) {
        const on = ((fmt >> i) & 1) === 1 ? 1 : 0;
        if (i < 6) m[i][8] = on; else if (i < 8) m[i + 1][8] = on; else m[size - 15 + i][8] = on;
        if (i < 8) m[8][size - i - 1] = on; else if (i < 9) m[8][15 - i] = on; else m[8][14 - i] = on;
      }
      m[size - 8][8] = 1;
      const sc = penalty(m, size);
      if (sc < bestScore) { bestScore = sc; best = { m, size }; }
    }
    return best;
  }

  /** Рисует QR на canvas, подгоняя размер модуля под целевую ширину. */
  function draw(canvas, text) {
    const { m, size } = matrix(text);
    const quiet = 4;
    const total = size + quiet * 2;
    const scale = Math.max(2, Math.floor(560 / total));
    canvas.width = canvas.height = total * scale;
    const ctx = canvas.getContext('2d');
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = '#000';
    for (let r = 0; r < size; r++) for (let c = 0; c < size; c++) {
      if (m[r][c]) ctx.fillRect((c + quiet) * scale, (r + quiet) * scale, scale, scale);
    }
  }

  return { matrix, draw };
})();

/* ==========================================================================
   2. Мелкие помощники
   ========================================================================== */

const $ = (sel, root = document) => root.querySelector(sel);
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

function bytes(n) {
  if (!n) return '0 Б';
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`;
}

function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

function since(iso) {
  if (!iso) return 'никогда';
  const sec = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (sec < 60) return `${sec} ${plural(sec, 'секунду', 'секунды', 'секунд')} назад`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} ${plural(min, 'минуту', 'минуты', 'минут')} назад`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} ${plural(h, 'час', 'часа', 'часов')} назад`;
  const d = Math.floor(h / 24);
  return `${d} ${plural(d, 'день', 'дня', 'дней')} назад`;
}

// Онлайн — хендшейк менее 3 минут назад. То же правило, что в docs/02-data-model.md.
const isOnline = (peer) => peer.enabled && peer.last_handshake
  && (Date.now() - new Date(peer.last_handshake).getTime()) < 180_000;

const TUNNEL_LABEL = {
  wireguard: 'WireGuard', vless: 'VLESS', shadowsocks: 'Shadowsocks',
  trojan: 'Trojan', hysteria2: 'Hysteria2', socks: 'SOCKS', raw: 'JSON',
};

function toast(text, kind) {
  const el = document.createElement('div');
  el.className = 'toast' + (kind === 'err' ? ' err' : '');
  el.textContent = text;
  $('#toasts').appendChild(el);
  setTimeout(() => el.remove(), 3200);
}

/* ==========================================================================
   3. Состояние
   ========================================================================== */

const state = {
  screen: 'peers',
  peers: structuredClone(MOCK.peers),
  tunnels: structuredClone(MOCK.tunnels),
  rules: structuredClone(MOCK.rules),
  settings: structuredClone(MOCK.settings),
  diag: structuredClone(MOCK.diag),
  dirty: false,
  openMenu: null,
};

function markDirty() {
  state.dirty = true;
  $('#apply-bar').hidden = false;
}

const tunnelById = (id) => state.tunnels.find((t) => t.id === id);
const peerById = (id) => state.peers.find((p) => p.id === id);
const listTitle = (key) => (MOCK.community_lists.find((l) => l.key === key) || {}).title || key;

/* ==========================================================================
   4. Клиентский конфиг WireGuard
   Все поля фиксированы: DNS, MTU, AllowedIPs и keepalive не настраиваются
   per-peer — это принцип «разумные дефолты вместо опций».
   ========================================================================== */

function peerConfig(peer) {
  const s = state.settings;
  return [
    '[Interface]',
    `PrivateKey = ${peer.private_key}`,
    `Address = ${peer.address}/32`,
    `DNS = ${s.wg_server_address}`,
    `MTU = ${s.client_mtu}`,
    '',
    '[Peer]',
    `PublicKey = ${MOCK.server_public_key}`,
    `PresharedKey = ${peer.preshared_key}`,
    `Endpoint = ${s.endpoint_host}:${s.wg_listen_port}`,
    'AllowedIPs = 0.0.0.0/0, ::/0',
    'PersistentKeepalive = 25',
    '',
  ].join('\n');
}

/* ==========================================================================
   5. Разбор вставленного конфига туннеля — то, что в проде делает
   POST /api/tunnels/parse. Здесь урезанная копия, чтобы форма вела себя так же.
   ========================================================================== */

const SCHEME_TYPE = {
  vless: 'vless', vmess: 'raw', ss: 'shadowsocks', trojan: 'trojan',
  hysteria2: 'hysteria2', hy2: 'hysteria2', socks5: 'socks', socks: 'socks',
};

function parseRaw(raw) {
  const s = (raw || '').trim();
  if (!s) return { state: 'idle', text: 'Вставьте ссылку vless://, ss://, trojan://, hysteria2://, socks5://, конфиг WireGuard или JSON outbound.' };

  const scheme = /^([a-z0-9]+):\/\//i.exec(s);
  if (scheme) {
    const proto = scheme[1].toLowerCase();
    const type = SCHEME_TYPE[proto];
    if (!type) return { state: 'err', text: `Схема «${proto}://» не поддерживается.` };
    const rest = s.slice(scheme[0].length);
    const hostPart = rest.split(/[?#]/)[0];
    const at = hostPart.lastIndexOf('@');
    const hostPort = at >= 0 ? hostPart.slice(at + 1) : hostPart;
    const hp = /^\[?([^\]]+?)\]?:(\d+)$/.exec(hostPort.replace(/\/$/, ''));
    if (!hp) return { state: 'err', text: 'Не разобран адрес: ожидается host:port после «@».' };
    const q = new URLSearchParams((rest.split('?')[1] || '').split('#')[0]);
    const parts = [TUNNEL_LABEL[type]];
    const sec = q.get('security');
    if (sec && sec !== 'none') parts.push(sec.toUpperCase());
    const tr = q.get('type') || q.get('transport');
    if (tr) parts.push(tr.toUpperCase());
    parts.push(`${hp[1]}:${hp[2]}`);
    const warnings = [];
    if (type === 'vless' && !at) warnings.push('в ссылке нет UUID');
    if (sec === 'reality' && !q.get('pbk')) warnings.push('REALITY без публичного ключа (pbk)');
    return { state: 'ok', type, source: 'url', host: hp[1], port: +hp[2], text: 'Распознан ' + parts.join(' · '), warnings };
  }

  if (/^\s*\[Interface\]/i.test(s)) {
    const ep = /^\s*Endpoint\s*=\s*(.+)$/im.exec(s);
    if (!ep) return { state: 'err', text: 'В конфиге WireGuard нет строки Endpoint в секции [Peer].' };
    const hp = /^\[?([^\]]+?)\]?:(\d+)$/.exec(ep[1].trim());
    if (!hp) return { state: 'err', text: `Endpoint «${ep[1].trim()}» не похож на host:port.` };
    const warnings = [];
    if (!/^\s*PrivateKey\s*=/im.test(s)) warnings.push('нет PrivateKey');
    if (!/^\s*PublicKey\s*=/im.test(s)) warnings.push('нет PublicKey пира');
    if (/^\s*MTU\s*=/im.test(s)) warnings.push('MTU из конфига игнорируется, туннель поднимается userspace');
    return {
      state: 'ok', type: 'wireguard', source: 'wg_conf', host: hp[1], port: +hp[2],
      text: `Распознан WireGuard · userspace · ${hp[1]}:${hp[2]}`, warnings,
    };
  }

  if (s.startsWith('{')) {
    let obj;
    try { obj = JSON.parse(s); } catch (e) { return { state: 'err', text: 'JSON не разбирается: ' + e.message }; }
    const t = String(obj.type || '').toLowerCase();
    const type = TUNNEL_LABEL[t] ? t : 'raw';
    const host = obj.server || '—';
    const port = obj.server_port || '';
    return {
      state: 'ok', type, source: 'json', host, port,
      text: `Распознан JSON outbound · ${TUNNEL_LABEL[type]}${port ? ` · ${host}:${port}` : ''}`,
      warnings: type === 'raw' ? ['тип неизвестен, JSON будет вставлен в конфиг как есть'] : [],
    };
  }

  return { state: 'err', text: 'Не похоже ни на ссылку, ни на конфиг WireGuard, ни на JSON.' };
}

/* ==========================================================================
   6. Валидация доменов и подсетей — построчно, как в описании экрана правил.
   ========================================================================== */

const RE_DOMAIN = /^(\*\.)?([a-z0-9_-]+\.)+[a-z]{2,}$/i;

function badLines(text, kind) {
  return (text || '').split('\n').map((l, i) => [l.trim(), i + 1])
    .filter(([l]) => l !== '')
    .filter(([l]) => (kind === 'domain' ? !RE_DOMAIN.test(l) : !validCidr(l)))
    .map(([, n]) => n);
}

function validCidr(s) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\/(\d{1,2}))?$/.exec(s);
  if (!m) return false;
  for (let i = 1; i <= 4; i++) if (+m[i] > 255) return false;
  return m[5] === undefined || +m[5] <= 32;
}

const lines = (text) => (text || '').split('\n').map((l) => l.trim()).filter(Boolean);

/* ==========================================================================
   7. Экраны
   ========================================================================== */

function render() {
  document.querySelectorAll('.tab').forEach((t) => {
    t.setAttribute('aria-selected', String(t.dataset.screen === state.screen));
  });
  const el = $('#screen');
  el.innerHTML = ({ peers: viewPeers, tunnels: viewTunnels, rules: viewRules, diag: viewDiag })[state.screen]();
  el.scrollTop = 0;
}

/* --- 7.1 Клиенты ---------------------------------------------------------- */

function viewPeers() {
  const online = state.peers.filter(isOnline).length;
  const rx = state.peers.reduce((a, p) => a + p.rx_bytes, 0);
  const tx = state.peers.reduce((a, p) => a + p.tx_bytes, 0);

  const rows = state.peers.map((p) => {
    const on = isOnline(p);
    const meta = !p.enabled
      ? 'отключён'
      : on
        ? `↓ ${bytes(p.rx_bytes)} · ↑ ${bytes(p.tx_bytes)} · ${esc(p.endpoint || '')}`
        : `был ${since(p.last_handshake)}`;
    return `
      <div class="row${p.enabled ? '' : ' dim'}" data-peer="${p.id}">
        <span class="dot ${on ? 'on' : 'off'}" title="${on ? 'онлайн' : 'офлайн'}"></span>
        <div class="row-main">
          <div class="row-title">${esc(p.name)}
            <span class="mono" style="color:var(--fg-dim);font-weight:400">${esc(p.address)}</span>
          </div>
          <div class="row-meta">${meta}</div>
        </div>
        <div class="row-actions">
          <button class="btn btn-sm" data-act="qr" data-id="${p.id}">Конфиг</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-peer" data-id="${p.id}" aria-label="Ещё">⋮</button>
          </div>
        </div>
      </div>`;
  }).join('');

  return `
    <div class="screen-head">
      <h1>Клиенты</h1>
      <div class="spacer"></div>
      <button class="btn btn-primary" data-act="add-peer">+ Добавить</button>
    </div>
    <div class="summary">
      <div class="stat"><div class="stat-label">Устройств</div><div class="stat-value">${state.peers.length}</div></div>
      <div class="stat"><div class="stat-label">Онлайн сейчас</div><div class="stat-value">${online}</div></div>
      <div class="stat"><div class="stat-label">Принято, всего</div><div class="stat-value">${bytes(rx)}</div></div>
      <div class="stat"><div class="stat-label">Отдано, всего</div><div class="stat-value">${bytes(tx)}</div></div>
    </div>
    <div class="card">${rows || '<div class="empty">Клиентов пока нет.</div>'}</div>
    <p class="screen-sub" style="margin-top:10px">
      Трафик считается накопительно с момента создания пира. Адрес, ключи, MTU и DNS
      выставляются сами — их нельзя задать вручную.
    </p>`;
}

/* --- 7.2 Туннели ---------------------------------------------------------- */

function viewTunnels() {
  const rows = state.tunnels.map((t) => {
    const p = t.parsed || {};
    const where = p.server ? `${p.server}${p.server_port ? ':' + p.server_port : ''}` : '—';
    const used = state.rules.filter((r) => r.tunnel_id === t.id).length;
    const st = !t.enabled ? { cls: 'off', badge: '', label: 'выключен' }
      : t.status === 'up' ? { cls: 'on', badge: `<span class="badge ok">${t.latency_ms} мс</span>`, label: '' }
        : { cls: 'bad', badge: '<span class="badge err">нет ответа</span>', label: '' };
    return `
      <div class="row${t.enabled ? '' : ' dim'}">
        <span class="dot ${st.cls}"></span>
        <div class="row-main">
          <div class="row-title">${esc(t.name)}
            <span class="badge">${TUNNEL_LABEL[t.type]}</span>
            ${st.badge}
          </div>
          <div class="row-meta mono">${esc(where)}</div>
          <div class="row-meta">${used ? `используют ${used} ${plural(used, 'правило', 'правила', 'правил')}` : 'ни одно правило не ссылается'}${st.label ? ' · ' + st.label : ''}</div>
        </div>
        <div class="row-actions">
          <button class="btn btn-sm" data-act="check-tunnel" data-id="${t.id}">Проверить</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-tunnel" data-id="${t.id}" aria-label="Ещё">⋮</button>
          </div>
        </div>
      </div>`;
  }).join('');

  return `
    <div class="screen-head">
      <h1>Туннели</h1>
      <div class="spacer"></div>
      <button class="btn btn-primary" data-act="add-tunnel">+ Добавить</button>
    </div>
    <div class="card">${rows || '<div class="empty">Туннелей пока нет.</div>'}</div>
    <p class="screen-sub" style="margin-top:10px">
      Тип определяется по вставленному конфигу — выбирать его руками не нужно.
      Исходящие WireGuard-туннели поднимаются в userspace внутри sing-box.
    </p>`;
}

/* --- 7.3 Правила ---------------------------------------------------------- */

// Правило ниже перекрыто, если правило выше делит с ним хотя бы один список
// или домен. Показываем это прямо в строке: иначе смысл порядка не виден.
function shadowedBy(index) {
  const r = state.rules[index];
  const mine = new Set([...r.community_lists, ...r.domains, ...r.subnets]);
  const hits = [];
  for (let i = 0; i < index; i++) {
    const up = state.rules[i];
    if (!up.enabled) continue;
    const shared = [...up.community_lists, ...up.domains, ...up.subnets].filter((k) => mine.has(k));
    if (shared.length) hits.push({ rule: up, shared });
  }
  return hits;
}

function viewRules() {
  const rows = state.rules.map((r, i) => {
    const shadow = shadowedBy(i);
    const shadowed = new Set(shadow.flatMap((h) => h.shared));
    const dest = r.action === 'direct' ? '<span class="badge">напрямую</span>'
      : r.action === 'block' ? '<span class="badge err">блокировать</span>'
        : `<span class="badge accent">→ ${esc((tunnelById(r.tunnel_id) || {}).name || 'туннель удалён')}</span>`;

    const chips = [
      ...r.community_lists.map((k) => `<span class="chip${shadowed.has(k) ? ' shadowed' : ''}">${esc(listTitle(k))}</span>`),
      ...r.domains.map((d) => `<span class="chip${shadowed.has(d) ? ' shadowed' : ''}">${esc(d)}</span>`),
      ...r.subnets.map((n) => `<span class="chip${shadowed.has(n) ? ' shadowed' : ''}">${esc(n)}</span>`),
      ...r.remote_lists.map(() => '<span class="chip">внешний список</span>'),
    ].join('');

    const scope = r.peer_scope === 'all' ? 'все клиенты'
      : r.peer_ids.map((id) => (peerById(id) || {}).name || '?').join(', ');

    const note = shadow.length ? `
      <div class="overlap-note">
        <span>⚠</span>
        <span>перекрыто правилом ${shadow.map((h) => `«${esc(h.rule.name)}»`).join(', ')}
        по ${shadow.flatMap((h) => h.shared).map((k) => esc(listTitle(k))).join(', ')} —
        для этих ресурсов сработает то правило, а не это</span>
      </div>` : '';

    return `
      <div class="row${r.enabled ? '' : ' dim'}">
        <div class="rule-order">
          <button class="ord-btn" data-act="up" data-id="${r.id}" ${i === 0 ? 'disabled' : ''} aria-label="Выше">▲</button>
          <button class="ord-btn" data-act="down" data-id="${r.id}" ${i === state.rules.length - 1 ? 'disabled' : ''} aria-label="Ниже">▼</button>
        </div>
        <span class="rule-num">${i + 1}</span>
        <div class="row-main">
          <div class="row-title">${esc(r.name)} ${dest}</div>
          <div class="row-meta">${esc(scope)}${r.resolve_real_ip ? ' · реальный IP' : ''}</div>
          <div class="chips">${chips || '<span class="chip">условий нет — правило не попадёт в конфиг</span>'}</div>
          ${note}
        </div>
        <div class="row-actions">
          <button class="toggle" role="switch" aria-checked="${r.enabled}" data-act="toggle-rule" data-id="${r.id}" aria-label="Включено"></button>
          <button class="btn btn-sm" data-act="edit-rule" data-id="${r.id}">Изменить</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-rule" data-id="${r.id}" aria-label="Ещё">⋮</button>
          </div>
        </div>
      </div>`;
  }).join('');

  return `
    <div class="screen-head">
      <h1>Правила</h1>
      <div class="spacer"></div>
      <button class="btn btn-primary" data-act="add-rule">+ Добавить</button>
      <div class="screen-sub">Проверяются сверху вниз, первое совпадение выигрывает.</div>
    </div>
    <div class="card">${rows || '<div class="empty">Правил пока нет — весь трафик идёт напрямую.</div>'}</div>`;
}

/* --- 7.4 Диагностика ------------------------------------------------------ */

const CHECK_ICON = { ok: '✓', warn: '⚠', error: '✕', unknown: '?' };

function viewDiag() {
  const d = state.diag;
  const rows = d.checks.map((c) => `
    <div class="check">
      <span class="check-icon ${c.status}">${CHECK_ICON[c.status]}</span>
      <div>
        <div class="check-title">${esc(c.title)}</div>
        <div class="check-detail">${esc(c.detail || '')}</div>
      </div>
    </div>`).join('');

  const overallText = { ok: 'Всё в порядке', warn: 'Есть замечания', error: 'Есть ошибки', unknown: 'Состояние неизвестно' }[d.overall];

  return `
    <div class="screen-head">
      <h1>Диагностика</h1>
      <div class="spacer"></div>
      <button class="btn" data-act="rerun-diag">Проверить заново</button>
    </div>
    <div class="overall">
      <span class="check-icon ${d.overall}">${CHECK_ICON[d.overall]}</span>
      <strong>${overallText}</strong>
      <span style="color:var(--fg-dim);font-size:13px;margin-left:auto">проверено ${since(new Date().toISOString())}</span>
    </div>
    <div class="card">
      ${rows}
      <div class="toolbar">
        <button class="btn" data-act="download-report">Скачать отчёт</button>
        <button class="btn" data-act="logs">Логи</button>
        <button class="btn" data-act="singbox-config">Конфиг sing-box</button>
      </div>
    </div>
    <p class="screen-sub" style="margin-top:10px">
      В отчёте маскируются ключи, внешние адреса эндпоинтов и UUID: такие отчёты
      вставляют в публичные чаты.
    </p>`;
}

/* ==========================================================================
   8. Модальные окна
   ========================================================================== */

function openModal(html, onMount) {
  $('#modal').innerHTML = html;
  $('#modal-backdrop').hidden = false;
  if (onMount) onMount($('#modal'));
}
function closeModal() {
  $('#modal-backdrop').hidden = true;
  $('#modal').innerHTML = '';
}

function modalShell(title, body, foot) {
  return `
    <div class="modal-head"><h2>${esc(title)}</h2>
      <button class="icon-btn" data-act="close-modal" aria-label="Закрыть">✕</button></div>
    <div class="modal-body">${body}</div>
    <div class="modal-foot">${foot}</div>`;
}

/* --- 8.1 Новый клиент: спрашиваем только имя ------------------------------ */

function modalAddPeer() {
  openModal(modalShell('Новый клиент', `
    <div class="field">
      <label for="peer-name">Имя устройства</label>
      <input type="text" id="peer-name" placeholder="iPad Кати" autocomplete="off">
      <div class="hint">Адрес из пула ${esc(state.settings.wg_pool)}, ключи, MTU ${state.settings.client_mtu} и DNS
        ${esc(state.settings.wg_server_address)} выставятся сами.</div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="create-peer">Создать</button>`),
  (m) => m.querySelector('#peer-name').focus());
}

function createPeer() {
  const name = $('#peer-name').value.trim();
  if (!name) { toast('Введите имя устройства', 'err'); return; }
  const used = new Set(state.peers.map((p) => p.address));
  let addr = '';
  for (let i = 2; i < 254; i++) { const a = `10.8.0.${i}`; if (!used.has(a) && a !== state.settings.wg_server_address) { addr = a; break; } }
  const rnd = () => Array.from({ length: 43 }, () => 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'[Math.floor(Math.random() * 62)]).join('') + '=';
  const peer = {
    id: 'p-' + Math.random().toString(36).slice(2, 8), name, address: addr, enabled: true,
    public_key: rnd(), private_key: rnd(), preshared_key: rnd(),
    created_at: new Date().toISOString(),
    last_handshake: null, rx_bytes: 0, tx_bytes: 0, endpoint: null,
  };
  state.peers.push(peer);
  render();
  modalPeerConfig(peer.id); // сразу QR, без промежуточного шага
}

/* --- 8.2 Конфиг клиента: QR + скачивание ---------------------------------- */

function modalPeerConfig(id) {
  const peer = peerById(id);
  const conf = peerConfig(peer);
  openModal(modalShell(peer.name, `
    <div class="qr-wrap">
      <div class="qr-box"><canvas id="qr"></canvas></div>
      <div class="hint" style="text-align:center;color:var(--fg-dim);font-size:13px">
        Отсканируйте приложением WireGuard или скачайте файл.<br>
        Адрес ${esc(peer.address)} · MTU ${state.settings.client_mtu} · DNS ${esc(state.settings.wg_server_address)}
      </div>
    </div>
    <details>
      <summary style="cursor:pointer;color:var(--fg-dim);font-size:13px">Показать текст конфига</summary>
      <pre class="pane" style="margin-top:8px">${esc(conf)}</pre>
    </details>`,
  `<button class="btn" data-act="close-modal">Закрыть</button>
     <button class="btn btn-primary" data-act="download-conf" data-id="${peer.id}">Скачать .conf</button>`),
  (m) => {
    try { QR.draw(m.querySelector('#qr'), conf); }
    catch (e) { m.querySelector('.qr-box').innerHTML = `<div style="color:#c33;padding:20px">QR не построен: ${esc(e.message)}</div>`; }
  });
}

function download(name, text) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
  const a = document.createElement('a');
  a.href = url; a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/* --- 8.3 Туннель: одно поле, тип определяется сам ------------------------- */

function modalTunnel(id) {
  const t = id ? tunnelById(id) : null;
  openModal(modalShell(t ? t.name : 'Новый туннель', `
    <div class="field">
      <label for="t-name">Имя</label>
      <input type="text" id="t-name" value="${esc(t ? t.name : '')}" placeholder="Нидерланды" autocomplete="off">
    </div>
    <div class="field">
      <label for="t-raw">Вставьте ссылку или конфиг WireGuard</label>
      <textarea id="t-raw" spellcheck="false" placeholder="vless://…">${esc(t ? t.raw : '')}</textarea>
      <div class="parse-result idle" id="t-parse"></div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-tunnel" data-id="${t ? t.id : ''}">Сохранить</button>`),
  (m) => {
    const raw = m.querySelector('#t-raw');
    const out = m.querySelector('#t-parse');
    const upd = () => {
      const r = parseRaw(raw.value);
      out.className = 'parse-result ' + (r.state === 'ok' ? 'ok' : r.state === 'err' ? 'err' : 'idle');
      const warn = (r.warnings || []).length ? `<div style="color:var(--warn);margin-top:4px">⚠ ${r.warnings.map(esc).join('; ')}</div>` : '';
      out.innerHTML = (r.state === 'ok' ? '✓ ' : r.state === 'err' ? '✕ ' : '') + esc(r.text) + warn;
    };
    raw.addEventListener('input', upd);
    upd();
    (t ? raw : m.querySelector('#t-name')).focus();
  });
}

function saveTunnel(id) {
  const name = $('#t-name').value.trim();
  const raw = $('#t-raw').value.trim();
  const r = parseRaw(raw);
  if (!name) { toast('Введите имя туннеля', 'err'); return; }
  if (r.state !== 'ok') { toast(r.state === 'idle' ? 'Вставьте конфиг туннеля' : r.text, 'err'); return; }
  if (state.tunnels.some((t) => t.name === name && t.id !== id)) { toast(`Туннель «${name}» уже есть`, 'err'); return; }

  const parsed = { type: r.type, server: r.host, server_port: r.port };
  if (id) {
    Object.assign(tunnelById(id), { name, raw, type: r.type, source: r.source, parsed });
  } else {
    state.tunnels.push({
      id: 't-' + Math.random().toString(36).slice(2, 7), name, type: r.type, source: r.source,
      raw, parsed, enabled: true, created_at: new Date().toISOString(),
      status: 'unknown', latency_ms: null, last_check: null,
    });
  }
  closeModal(); markDirty(); render();
  toast(`Туннель «${name}» сохранён`);
}

/* --- 8.4 Правило ---------------------------------------------------------- */

function modalRule(id) {
  const r = id ? state.rules.find((x) => x.id === id) : {
    id: null, name: '', action: 'tunnel', tunnel_id: (state.tunnels[0] || {}).id || null,
    enabled: true, community_lists: [], domains: [], subnets: [], remote_lists: [],
    peer_scope: 'all', peer_ids: [], resolve_real_ip: false,
  };

  const listsHtml = MOCK.community_lists.map((l) => `
    <label><input type="checkbox" name="list" value="${l.key}" ${r.community_lists.includes(l.key) ? 'checked' : ''}>
      ${esc(l.title)}</label>`).join('');

  const tunnelOpts = state.tunnels.map((t) =>
    `<option value="${t.id}" ${r.tunnel_id === t.id ? 'selected' : ''}>${esc(t.name)}</option>`).join('');

  const peersHtml = state.peers.map((p) => `
    <label><input type="checkbox" name="peer" value="${p.id}" ${r.peer_ids.includes(p.id) ? 'checked' : ''}>
      ${esc(p.name)}</label>`).join('');

  openModal(modalShell(id ? r.name : 'Новое правило', `
    <div class="field">
      <label for="r-name">Название</label>
      <input type="text" id="r-name" value="${esc(r.name)}" placeholder="YouTube и Google" autocomplete="off">
    </div>

    <div class="field">
      <label>Куда</label>
      <div class="radios">
        <label class="radio-pill"><input type="radio" name="action" value="direct" ${r.action === 'direct' ? 'checked' : ''}> напрямую</label>
        <label class="radio-pill"><input type="radio" name="action" value="tunnel" ${r.action === 'tunnel' ? 'checked' : ''}> в туннель</label>
        <label class="radio-pill"><input type="radio" name="action" value="block" ${r.action === 'block' ? 'checked' : ''}> блокировать</label>
        <select id="r-tunnel" style="width:auto;min-width:160px" ${r.action === 'tunnel' ? '' : 'disabled'}>${tunnelOpts}</select>
      </div>
    </div>

    <div class="field">
      <label>Готовые списки</label>
      <div class="list-grid">${listsHtml}</div>
    </div>

    <div class="two-col">
      <div class="field">
        <label for="r-domains">Свои домены</label>
        <textarea id="r-domains" spellcheck="false" placeholder="example.com">${esc(r.domains.join('\n'))}</textarea>
        <div class="line-errors" id="r-domains-err"></div>
      </div>
      <div class="field">
        <label for="r-subnets">Свои подсети</label>
        <textarea id="r-subnets" spellcheck="false" placeholder="203.0.113.0/24">${esc(r.subnets.join('\n'))}</textarea>
        <div class="line-errors" id="r-subnets-err"></div>
      </div>
    </div>

    <div class="field">
      <label>Для кого</label>
      <div class="radios">
        <label class="radio-pill"><input type="radio" name="scope" value="all" ${r.peer_scope === 'all' ? 'checked' : ''}> все клиенты</label>
        <label class="radio-pill"><input type="radio" name="scope" value="selected" ${r.peer_scope === 'selected' ? 'checked' : ''}> выбранные</label>
      </div>
      <div class="list-grid" id="r-peers" style="margin-top:6px;${r.peer_scope === 'all' ? 'display:none' : ''}">${peersHtml}</div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-rule" data-id="${id || ''}">Сохранить</button>`),
  (m) => {
    m.querySelectorAll('input[name="action"]').forEach((el) => el.addEventListener('change', () => {
      m.querySelector('#r-tunnel').disabled = m.querySelector('input[name="action"]:checked').value !== 'tunnel';
    }));
    m.querySelectorAll('input[name="scope"]').forEach((el) => el.addEventListener('change', () => {
      m.querySelector('#r-peers').style.display =
        m.querySelector('input[name="scope"]:checked').value === 'selected' ? '' : 'none';
    }));
    const live = (ta, out, kind, word) => {
      const el = m.querySelector(ta), o = m.querySelector(out);
      const upd = () => {
        const bad = badLines(el.value, kind);
        o.textContent = bad.length ? `${plural(bad.length, 'Строка', 'Строки', 'Строки')} ${bad.join(', ')} — не ${word}` : '';
      };
      el.addEventListener('input', upd); upd();
    };
    live('#r-domains', '#r-domains-err', 'domain', 'домен');
    live('#r-subnets', '#r-subnets-err', 'cidr', 'подсеть');
    m.querySelector('#r-name').focus();
  });
}

function saveRule(id) {
  const m = $('#modal');
  const name = m.querySelector('#r-name').value.trim();
  const action = m.querySelector('input[name="action"]:checked').value;
  const scope = m.querySelector('input[name="scope"]:checked').value;
  const domains = lines(m.querySelector('#r-domains').value);
  const subnets = lines(m.querySelector('#r-subnets').value);
  const listsSel = [...m.querySelectorAll('input[name="list"]:checked')].map((e) => e.value);
  const peerIds = [...m.querySelectorAll('input[name="peer"]:checked')].map((e) => e.value);

  if (!name) { toast('Введите название правила', 'err'); return; }
  if (badLines(m.querySelector('#r-domains').value, 'domain').length
    || badLines(m.querySelector('#r-subnets').value, 'cidr').length) {
    toast('Исправьте подсвеченные строки', 'err'); return;
  }
  if (action === 'tunnel' && !m.querySelector('#r-tunnel').value) { toast('Сначала добавьте туннель', 'err'); return; }
  if (scope === 'selected' && !peerIds.length) { toast('Выберите хотя бы одного клиента', 'err'); return; }
  if (!listsSel.length && !domains.length && !subnets.length) {
    toast('Правило без условий поймало бы весь трафик — добавьте списки, домены или подсети', 'err'); return;
  }

  const patch = {
    name, action,
    tunnel_id: action === 'tunnel' ? m.querySelector('#r-tunnel').value : null,
    community_lists: listsSel, domains, subnets,
    peer_scope: scope, peer_ids: scope === 'selected' ? peerIds : [],
  };
  if (id) {
    Object.assign(state.rules.find((r) => r.id === id), patch);
  } else {
    state.rules.push(Object.assign({
      id: 'r-' + Math.random().toString(36).slice(2, 7), enabled: true,
      remote_lists: [], resolve_real_ip: false, priority: state.rules.length,
    }, patch));
  }
  closeModal(); markDirty(); renumber(); render();
  toast(`Правило «${name}» сохранено`);
}

// Приоритет назначает хранилище: он всегда плотный 0..n-1 и совпадает с порядком.
function renumber() { state.rules.forEach((r, i) => { r.priority = i; }); }

function moveRule(id, delta) {
  const i = state.rules.findIndex((r) => r.id === id);
  const j = i + delta;
  if (j < 0 || j >= state.rules.length) return;
  [state.rules[i], state.rules[j]] = [state.rules[j], state.rules[i]];
  renumber(); markDirty(); render();
}

/* --- 8.5 Настройки: пять полей, всё прочее — в config.yaml ---------------- */

function modalSettings() {
  const s = state.settings;
  openModal(modalShell('Настройки', `
    <div class="two-col">
      <div class="field"><label for="s-port">Порт WireGuard</label>
        <input type="number" id="s-port" value="${s.wg_listen_port}"></div>
      <div class="field"><label for="s-host">Внешний адрес</label>
        <input type="text" id="s-host" value="${esc(s.endpoint_host)}"></div>
      <div class="field"><label for="s-mtu">MTU клиентов</label>
        <input type="number" id="s-mtu" value="${s.client_mtu}">
        <div class="hint">1280 — значение по умолчанию, менять без причины не стоит.</div></div>
      <div class="field"><label for="s-dns">DNS-апстрим</label>
        <input type="text" id="s-dns" value="${esc(s.dns_upstream)}"></div>
    </div>
    <div class="field"><label for="s-int">Обновление списков</label>
      <select id="s-int">
        <option value="3600" ${s.list_update_interval === 3600 ? 'selected' : ''}>каждый час</option>
        <option value="21600" ${s.list_update_interval === 21600 ? 'selected' : ''}>каждые 6 часов</option>
        <option value="86400" ${s.list_update_interval === 86400 ? 'selected' : ''}>раз в сутки</option>
        <option value="604800" ${s.list_update_interval === 604800 ? 'selected' : ''}>раз в неделю</option>
      </select></div>
    <div class="parse-result idle">Остальное живёт в config.yaml: пул адресов, тип DNS,
      WAN-интерфейс, уровень логов.</div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-settings">Сохранить</button>`));
}

function saveSettings() {
  const s = state.settings;
  const before = `${s.wg_listen_port}|${s.client_mtu}`;
  s.wg_listen_port = +$('#s-port').value || s.wg_listen_port;
  s.endpoint_host = $('#s-host').value.trim() || s.endpoint_host;
  s.client_mtu = +$('#s-mtu').value || s.client_mtu;
  s.dns_upstream = $('#s-dns').value.trim() || s.dns_upstream;
  s.list_update_interval = +$('#s-int').value;
  closeModal(); markDirty(); render();
  if (before !== `${s.wg_listen_port}|${s.client_mtu}`) {
    toast('Порт или MTU изменены — клиентам нужно перевыдать конфиги', 'err');
  } else {
    toast('Настройки сохранены');
  }
}

/* --- 8.6 Диагностика: логи, конфиг, отчёт --------------------------------- */

function modalLogs() {
  openModal(modalShell('Логи', `
    <div class="radios">
      <label class="radio-pill"><input type="radio" name="logsrc" value="razdachad" checked> razdachad</label>
      <label class="radio-pill"><input type="radio" name="logsrc" value="sing-box"> sing-box</label>
    </div>
    <pre class="pane" id="log-pane"></pre>`,
  '<button class="btn" data-act="close-modal">Закрыть</button>'),
  (m) => {
    const pane = m.querySelector('#log-pane');
    const upd = () => {
      const src = m.querySelector('input[name="logsrc"]:checked').value;
      pane.textContent = MOCK.logs[src].join('\n');
    };
    m.querySelectorAll('input[name="logsrc"]').forEach((e) => e.addEventListener('change', upd));
    upd();
  });
}

function modalSingboxConfig() {
  openModal(modalShell('Конфиг sing-box', `
    <div class="parse-result idle">Только просмотр: конфиг генерируется целиком из состояния
      и никогда не патчится вручную.</div>
    <pre class="pane">${esc(MOCK.singbox_config)}</pre>`,
  '<button class="btn" data-act="close-modal">Закрыть</button>'));
}

const maskIP = (ip) => (ip || '').replace(/^(\d+)\.(\d+)\.\d+\.\d+/, '$1.$2.x.x');
const maskKey = (k) => (k ? k.slice(0, 4) + '…' + k.slice(-4) : '');

function diagReport() {
  const s = state.settings;
  const out = [
    'razdacha — отчёт диагностики',
    `дата: ${new Date().toISOString()}`,
    `общий статус: ${state.diag.overall}`,
    '',
    '## Проверки',
    ...state.diag.checks.map((c) => `${c.status.padEnd(5)} ${c.title}: ${c.detail || ''}`),
    '',
    '## Настройки',
    `порт wg: ${s.wg_listen_port}`,
    `внешний адрес: ${'<замаскирован>'}`,
    `MTU клиентов: ${s.client_mtu}`,
    `DNS: ${s.dns_upstream} (${s.dns_type})`,
    '',
    '## Туннели',
    ...state.tunnels.map((t) => `${t.name}: ${t.type}, ${maskIP((t.parsed || {}).server)}, ${t.status}${t.latency_ms ? `, ${t.latency_ms} мс` : ''}`),
    '',
    '## Клиенты',
    ...state.peers.map((p) => `${p.name}: ${p.address}, ключ ${maskKey(p.public_key)}, endpoint ${maskIP(p.endpoint) || '—'}, ${isOnline(p) ? 'онлайн' : 'офлайн'}`),
    '',
    '## Правила',
    ...state.rules.map((r, i) => `${i + 1}. ${r.name} → ${r.action}${r.tunnel_id ? ' (' + (tunnelById(r.tunnel_id) || {}).name + ')' : ''}${r.enabled ? '' : ' [выкл]'}`),
    '',
  ].join('\n');
  download('razdacha-diag.txt', out);
  toast('Отчёт скачан, ключи и адреса замаскированы');
}

/* ==========================================================================
   9. Контекстные меню ⋮
   ========================================================================== */

function openMenu(anchor, items) {
  closeMenu();
  const menu = document.createElement('div');
  menu.className = 'menu';
  menu.innerHTML = items.map((it) =>
    `<button data-act="${it.act}" data-id="${it.id}" class="${it.danger ? 'danger' : ''}">${esc(it.label)}</button>`).join('');
  anchor.closest('.menu-wrap').appendChild(menu);
  state.openMenu = menu;
}
function closeMenu() { if (state.openMenu) { state.openMenu.remove(); state.openMenu = null; } }

/* ==========================================================================
   10. Обработка событий — одно делегирование на документ
   ========================================================================== */

document.addEventListener('click', (ev) => {
  const btn = ev.target.closest('[data-act]');
  if (!btn) { closeMenu(); return; }
  const act = btn.dataset.act;
  const id = btn.dataset.id;
  if (!act.startsWith('menu-')) closeMenu();

  switch (act) {
    /* клиенты */
    case 'add-peer': modalAddPeer(); break;
    case 'create-peer': createPeer(); break;
    case 'qr': modalPeerConfig(id); break;
    case 'download-conf': {
      const p = peerById(id);
      download(`razdacha-${p.name.replace(/[^\w\dА-Яа-я-]+/g, '-').toLowerCase()}.conf`, peerConfig(p));
      toast('Файл конфига скачан');
      break;
    }
    case 'menu-peer': openMenu(btn, [
      { act: 'rename-peer', id, label: 'Переименовать' },
      { act: 'toggle-peer', id, label: peerById(id).enabled ? 'Отключить' : 'Включить' },
      { act: 'delete-peer', id, label: 'Удалить', danger: true },
    ]); break;
    case 'rename-peer': {
      const p = peerById(id);
      const name = prompt('Новое имя', p.name);
      if (name && name.trim()) { p.name = name.trim(); render(); toast('Переименовано'); }
      break;
    }
    case 'toggle-peer': {
      const p = peerById(id);
      p.enabled = !p.enabled;
      if (!p.enabled) { p.last_handshake = null; p.endpoint = null; }
      markDirty(); render();
      break;
    }
    case 'delete-peer': {
      const p = peerById(id);
      if (!confirm(`Удалить «${p.name}»? Устройство потеряет доступ.`)) break;
      state.peers = state.peers.filter((x) => x.id !== id);
      // Ссылки на пира чистятся и в правилах; правило без пиров переходит на «все».
      state.rules.forEach((r) => {
        if (r.peer_scope !== 'selected') return;
        r.peer_ids = r.peer_ids.filter((x) => x !== id);
        if (!r.peer_ids.length) r.peer_scope = 'all';
      });
      markDirty(); render(); toast('Клиент удалён');
      break;
    }

    /* туннели */
    case 'add-tunnel': modalTunnel(null); break;
    case 'save-tunnel': saveTunnel(id || null); break;
    case 'menu-tunnel': openMenu(btn, [
      { act: 'edit-tunnel', id, label: 'Изменить' },
      { act: 'toggle-tunnel', id, label: tunnelById(id).enabled ? 'Выключить' : 'Включить' },
      { act: 'delete-tunnel', id, label: 'Удалить', danger: true },
    ]); break;
    case 'edit-tunnel': modalTunnel(id); break;
    case 'toggle-tunnel': { const t = tunnelById(id); t.enabled = !t.enabled; markDirty(); render(); break; }
    case 'delete-tunnel': {
      const t = tunnelById(id);
      const used = state.rules.filter((r) => r.tunnel_id === id);
      if (used.length) { toast(`Туннель «${t.name}» используется правилом «${used[0].name}»`, 'err'); break; }
      if (!confirm(`Удалить туннель «${t.name}»?`)) break;
      state.tunnels = state.tunnels.filter((x) => x.id !== id);
      markDirty(); render(); toast('Туннель удалён');
      break;
    }
    case 'check-tunnel': {
      const t = tunnelById(id);
      btn.disabled = true; btn.textContent = 'Проверяю…';
      setTimeout(() => {
        if (t.status === 'down') { t.latency_ms = null; }
        else { t.latency_ms = 15 + Math.floor(Math.random() * 60); t.status = 'up'; }
        t.last_check = new Date().toISOString();
        render();
        toast(t.status === 'up' ? `${t.name}: ${t.latency_ms} мс` : `${t.name}: нет ответа`, t.status === 'up' ? '' : 'err');
      }, 600);
      break;
    }

    /* правила */
    case 'add-rule': modalRule(null); break;
    case 'edit-rule': modalRule(id); break;
    case 'save-rule': saveRule(id || null); break;
    case 'up': moveRule(id, -1); break;
    case 'down': moveRule(id, 1); break;
    case 'toggle-rule': {
      const r = state.rules.find((x) => x.id === id);
      r.enabled = !r.enabled; markDirty(); render();
      break;
    }
    case 'menu-rule': openMenu(btn, [
      { act: 'edit-rule', id, label: 'Изменить' },
      { act: 'duplicate-rule', id, label: 'Дублировать' },
      { act: 'delete-rule', id, label: 'Удалить', danger: true },
    ]); break;
    case 'duplicate-rule': {
      const r = state.rules.find((x) => x.id === id);
      const copy = structuredClone(r);
      copy.id = 'r-' + Math.random().toString(36).slice(2, 7);
      copy.name = r.name + ' (копия)';
      state.rules.splice(state.rules.indexOf(r) + 1, 0, copy);
      renumber(); markDirty(); render();
      break;
    }
    case 'delete-rule': {
      const r = state.rules.find((x) => x.id === id);
      if (!confirm(`Удалить правило «${r.name}»?`)) break;
      state.rules = state.rules.filter((x) => x.id !== id);
      renumber(); markDirty(); render(); toast('Правило удалено');
      break;
    }

    /* диагностика */
    case 'rerun-diag': {
      btn.disabled = true; btn.textContent = 'Проверяю…';
      setTimeout(() => { state.diag = structuredClone(MOCK.diag); render(); toast('Проверки перезапущены'); }, 700);
      break;
    }
    case 'logs': modalLogs(); break;
    case 'singbox-config': modalSingboxConfig(); break;
    case 'download-report': diagReport(); break;

    /* общее */
    case 'save-settings': saveSettings(); break;
    case 'close-modal': closeModal(); break;
    default: break;
  }
});

const SCREENS = ['peers', 'tunnels', 'rules', 'diag'];

$('#tabs').addEventListener('click', (ev) => {
  const tab = ev.target.closest('.tab');
  if (!tab) return;
  location.hash = tab.dataset.screen;
});

function fromHash() {
  const h = location.hash.replace('#', '');
  state.screen = SCREENS.includes(h) ? h : 'peers';
  render();
}
window.addEventListener('hashchange', fromHash);

$('#btn-settings').addEventListener('click', modalSettings);

$('#btn-apply').addEventListener('click', () => {
  const b = $('#btn-apply');
  b.disabled = true; b.textContent = 'Применяю…';
  setTimeout(() => {
    b.disabled = false; b.textContent = 'Применить';
    state.dirty = false;
    $('#apply-bar').hidden = true;
    toast('Конфигурация применена: sing-box check пройден, правила перезалиты');
  }, 900);
});

$('#modal-backdrop').addEventListener('click', (ev) => { if (ev.target.id === 'modal-backdrop') closeModal(); });
document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') { closeModal(); closeMenu(); } });

// Демонстрация живых данных: то, что в проде прилетает по WS каждые 2 секунды.
setInterval(() => {
  let changed = false;
  for (const p of state.peers) {
    if (!isOnline(p)) continue;
    p.last_handshake = new Date().toISOString();
    p.rx_bytes += Math.floor(Math.random() * 240000);
    p.tx_bytes += Math.floor(Math.random() * 40000);
    changed = true;
  }
  if (changed && state.screen === 'peers' && $('#modal-backdrop').hidden) render();
}, 3000);

fromHash();
