/* QR-кодер: byte mode, уровень коррекции L, версии 1–40.
   Написан здесь, а не подключён библиотекой: панель встраивается в бинарник через
   go:embed — тянуть CDN некуда, а CSP панели внешние скрипты и не пустит.
   Вывод проверен против qrencode на прототипе (ui/prototype/app.js). */

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

export { matrix, draw };
