// Package qr — кодер QR-кодов: byte mode, уровень коррекции L, версии 1–40.
//
// Своя реализация, а не библиотека: единственное место, где QR нужен со стороны
// Go, — печать конфига первого пира в терминал при установке, и ради двадцати
// строк вывода тянуть зависимость в статический бинарник незачем. Тот же кодер
// уже есть в панели (`ui/dist/js/qr.js`), его вывод сверен с `qrencode`; здесь
// он повторён строка в строку, а golden-векторы в `testdata` сняты с него.
//
// Чего здесь нет и не нужно: режимы numeric/alphanumeric/kanji (конфиг
// WireGuard — произвольный текст, byte mode кодирует его целиком) и уровни
// коррекции выше L (экран терминала не мнётся и не выцветает, а лишняя
// коррекция только увеличивает картинку).
package qr

import (
	"errors"
	"fmt"
)

// ErrTooLong — данные не помещаются даже в версию 40.
var ErrTooLong = errors.New("слишком длинные данные для QR-кода")

// Матрица версии v имеет сторону 4v+17 модулей.
const (
	minVersion = 1
	maxVersion = 40
)

// total — всего кодовых слов по версиям 1..40.
var total = [maxVersion]int{
	26, 44, 70, 100, 134, 172, 196, 242, 292, 346, 404, 466, 532, 581, 655,
	733, 815, 901, 991, 1085, 1156, 1258, 1364, 1474, 1588, 1706, 1828, 1921, 2051, 2185,
	2323, 2465, 2611, 2761, 2876, 3034, 3196, 3362, 3532, 3706,
}

// eccPerBlock — кодовых слов коррекции на блок, уровень L.
var eccPerBlock = [maxVersion]int{
	7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28,
	28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30,
}

// blocks — число блоков коррекции, уровень L.
var blocks = [maxVersion]int{
	1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10,
	12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25,
}

// align — центры выравнивающих узоров по версиям.
var align = [maxVersion][]int{
	{},
	{6, 18},
	{6, 22},
	{6, 26},
	{6, 30},
	{6, 34},
	{6, 22, 38},
	{6, 24, 42},
	{6, 26, 46},
	{6, 28, 50},
	{6, 30, 54},
	{6, 32, 58},
	{6, 34, 62},
	{6, 26, 46, 66},
	{6, 26, 48, 70},
	{6, 26, 50, 74},
	{6, 30, 54, 78},
	{6, 30, 56, 82},
	{6, 30, 58, 86},
	{6, 34, 62, 90},
	{6, 28, 50, 72, 94},
	{6, 26, 50, 74, 98},
	{6, 30, 54, 78, 102},
	{6, 28, 54, 80, 106},
	{6, 32, 58, 84, 110},
	{6, 30, 58, 86, 114},
	{6, 34, 62, 90, 118},
	{6, 26, 50, 74, 98, 122},
	{6, 30, 54, 78, 102, 126},
	{6, 26, 52, 78, 104, 130},
	{6, 30, 56, 82, 108, 134},
	{6, 34, 60, 86, 112, 138},
	{6, 30, 58, 86, 114, 142},
	{6, 34, 62, 90, 118, 146},
	{6, 30, 54, 78, 102, 126, 150},
	{6, 24, 50, 76, 102, 128, 154},
	{6, 28, 54, 80, 106, 132, 158},
	{6, 32, 58, 84, 110, 136, 162},
	{6, 26, 54, 82, 110, 138, 166},
	{6, 30, 58, 86, 114, 142, 170},
}

// Matrix — готовая матрица модулей. Тёмный модуль печатается, светлый — фон;
// зона тишины в матрицу не входит, её добавляет отрисовка.
type Matrix struct {
	size int
	dark []bool
}

// Size — сторона матрицы в модулях.
func (m Matrix) Size() int { return m.size }

// Dark отвечает, тёмный ли модуль. Координаты вне матрицы считаются светлыми:
// отрисовке удобно спрашивать про зону тишины теми же координатами.
func (m Matrix) Dark(row, col int) bool {
	if row < 0 || col < 0 || row >= m.size || col >= m.size {
		return false
	}
	return m.dark[row*m.size+col]
}

func (m *Matrix) set(row, col int, v bool) {
	if row < 0 || col < 0 || row >= m.size || col >= m.size {
		return
	}
	m.dark[row*m.size+col] = v
}

// Encode кодирует текст в матрицу: минимальная подходящая версия, byte mode,
// уровень коррекции L, маска выбирается по штрафной функции из стандарта.
func Encode(text string) (Matrix, error) {
	version, err := pickVersion(len(text))
	if err != nil {
		return Matrix{}, err
	}
	stream := bitStream(text, version)

	// Все восемь масок строятся целиком, побеждает наименьший штраф: это
	// требование стандарта, а не оптимизация — плохая маска даёт узоры,
	// похожие на поисковые, и сканер путается.
	var (
		best      Matrix
		bestScore = -1
	)
	for mask := 0; mask < 8; mask++ {
		m := place(version, stream, mask)
		score := penalty(m)
		if bestScore < 0 || score < bestScore {
			best, bestScore = m, score
		}
	}
	return best, nil
}

// pickVersion выбирает наименьшую версию, в которую влезают length байт.
// Счётчик длины — 8 бит до версии 10 и 16 бит с неё, отсюда две ветки.
func pickVersion(length int) (int, error) {
	for v := minVersion; v <= maxVersion; v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*length <= dataCodewords(v)*8 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%w: %d байт", ErrTooLong, length)
}

// dataCodewords — сколько кодовых слов версии отведено под данные.
func dataCodewords(version int) int {
	return total[version-1] - eccPerBlock[version-1]*blocks[version-1]
}

// bitStream собирает итоговый поток битов: заголовок, данные, заполнение,
// коррекция Рида — Соломона и чередование блоков.
func bitStream(text string, version int) []byte {
	data := []byte(text)
	dataCw := dataCodewords(version)
	countBits := 8
	if version >= 10 {
		countBits = 16
	}

	bits := make([]byte, 0, dataCw*8)
	push := func(value, length int) {
		for i := length - 1; i >= 0; i-- {
			bits = append(bits, byte((value>>i)&1))
		}
	}
	push(4, 4) // индикатор режима byte mode
	push(len(data), countBits)
	for _, b := range data {
		push(int(b), 8)
	}
	// Терминатор: до четырёх нулей, но не дальше границы поля данных.
	for i := 0; i < 4 && len(bits) < dataCw*8; i++ {
		bits = append(bits, 0)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, 0)
	}

	cw := make([]byte, 0, dataCw)
	for i := 0; i < len(bits); i += 8 {
		var v byte
		for j := 0; j < 8; j++ {
			v = v<<1 | bits[i+j]
		}
		cw = append(cw, v)
	}
	pad := [2]byte{0xec, 0x11}
	for i := 0; len(cw) < dataCw; i++ {
		cw = append(cw, pad[i%2])
	}

	// Блоки разной длины: длинные идут в конце — так задан стандартом порядок
	// чередования, и от него зависит, что прочитает сканер.
	nBlocks, ecLen := blocks[version-1], eccPerBlock[version-1]
	shortLen, longCount := dataCw/nBlocks, dataCw%nBlocks
	dataBlocks := make([][]byte, nBlocks)
	ecBlocks := make([][]byte, nBlocks)
	off := 0
	for i := 0; i < nBlocks; i++ {
		length := shortLen
		if i >= nBlocks-longCount {
			length++
		}
		dataBlocks[i] = cw[off : off+length]
		off += length
		ecBlocks[i] = rsRemainder(dataBlocks[i], ecLen)
	}

	out := make([]byte, 0, total[version-1])
	for i := 0; i < shortLen+1; i++ {
		for _, d := range dataBlocks {
			if i < len(d) {
				out = append(out, d[i])
			}
		}
	}
	for i := 0; i < ecLen; i++ {
		for _, e := range ecBlocks {
			out = append(out, e[i])
		}
	}

	stream := make([]byte, 0, len(out)*8)
	for _, b := range out {
		for i := 7; i >= 0; i-- {
			stream = append(stream, (b>>i)&1)
		}
	}
	return stream
}

// maskFn — восемь масок стандарта. Индекс маски уезжает в поле формата, поэтому
// порядок здесь менять нельзя.
func maskFn(mask, row, col int) bool {
	switch mask {
	case 0:
		return (row+col)%2 == 0
	case 1:
		return row%2 == 0
	case 2:
		return col%3 == 0
	case 3:
		return (row+col)%3 == 0
	case 4:
		return (row/2+col/3)%2 == 0
	case 5:
		return (row*col)%2+(row*col)%3 == 0
	case 6:
		return ((row*col)%2+(row*col)%3)%2 == 0
	default:
		return ((row+col)%2+(row*col)%3)%2 == 0
	}
}

// bchFormat — код БЧХ поля формата (уровень коррекции и маска) с итоговой
// маской 0x5412 из стандарта.
func bchFormat(data int) int {
	d := data << 10
	for i := 14; i >= 10; i-- {
		if (d>>i)&1 == 1 {
			d ^= 0x537 << (i - 10)
		}
	}
	return ((data << 10) | d) ^ 0x5412
}

// bchVersion — код БЧХ поля версии, он есть только с версии 7.
func bchVersion(v int) int {
	d := v << 12
	for i := 17; i >= 12; i-- {
		if (d>>i)&1 == 1 {
			d ^= 0x1f25 << (i - 12)
		}
	}
	return (v << 12) | d
}

// skeleton рисует служебные узоры версии и отмечает занятые ими модули: данные
// кладутся только туда, где `functional` не выставлен.
func skeleton(version int) (Matrix, []bool) {
	size := version*4 + 17
	m := Matrix{size: size, dark: make([]bool, size*size)}
	functional := make([]bool, size*size)

	set := func(row, col int, v bool) {
		if row < 0 || col < 0 || row >= size || col >= size {
			return
		}
		m.dark[row*size+col] = v
		functional[row*size+col] = true
	}
	isFunctional := func(row, col int) bool { return functional[row*size+col] }

	finder := func(baseRow, baseCol int) {
		for r := -1; r <= 7; r++ {
			for c := -1; c <= 7; c++ {
				on := (r >= 0 && r <= 6 && (c == 0 || c == 6)) ||
					(c >= 0 && c <= 6 && (r == 0 || r == 6)) ||
					(r >= 2 && r <= 4 && c >= 2 && c <= 4)
				set(baseRow+r, baseCol+c, on)
			}
		}
	}
	finder(0, 0)
	finder(0, size-7)
	finder(size-7, 0)

	centers := align[version-1]
	for _, r := range centers {
		for _, c := range centers {
			// Углы уже заняты поисковыми узорами: выравнивающий туда не ставится.
			if (r <= 8 && c <= 8) || (r <= 8 && c >= size-9) || (r >= size-9 && c <= 8) {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					set(r+dr, c+dc, maxInt(absInt(dr), absInt(dc)) != 1)
				}
			}
		}
	}

	for i := 8; i < size-8; i++ {
		set(6, i, i%2 == 0)
		set(i, 6, i%2 == 0)
	}
	set(size-8, 8, true) // тёмный модуль, он всегда чёрный

	// Поле формата резервируется нулями: сами биты кладутся после выбора маски.
	for i := 0; i < 9; i++ {
		if !isFunctional(8, i) {
			set(8, i, false)
		}
		if !isFunctional(i, 8) {
			set(i, 8, false)
		}
	}
	for i := size - 8; i < size; i++ {
		if !isFunctional(8, i) {
			set(8, i, false)
		}
		if !isFunctional(i, 8) {
			set(i, 8, false)
		}
	}

	if version >= 7 {
		bits := bchVersion(version)
		for i := 0; i < 18; i++ {
			on := (bits>>i)&1 == 1
			set(i/3, i%3+size-11, on)
			set(i%3+size-11, i/3, on)
		}
	}
	return m, functional
}

// place раскладывает поток по зигзагу с наложенной маской и дописывает поле
// формата.
func place(version int, stream []byte, mask int) Matrix {
	m, functional := skeleton(version)
	size := m.size

	idx, row, dir := 0, size-1, -1
	for col := size - 1; col > 0; col -= 2 {
		// Шестая колонка — вертикальный синхроузор, пара колонок её перешагивает.
		if col == 6 {
			col--
		}
		for {
			for k := 0; k < 2; k++ {
				c := col - k
				if functional[row*size+c] {
					continue
				}
				var dark byte
				if idx < len(stream) {
					dark = stream[idx]
					idx++
				}
				if maskFn(mask, row, c) {
					dark ^= 1
				}
				m.dark[row*size+c] = dark == 1
			}
			row += dir
			if row < 0 || row >= size {
				row -= dir
				dir = -dir
				break
			}
		}
	}

	// Уровень коррекции L кодируется как 0b01, дальше три бита маски.
	format := bchFormat(0b01<<3 | mask)
	for i := 0; i < 15; i++ {
		on := (format>>i)&1 == 1
		switch {
		case i < 6:
			m.set(i, 8, on)
		case i < 8:
			m.set(i+1, 8, on)
		default:
			m.set(size-15+i, 8, on)
		}
		switch {
		case i < 8:
			m.set(8, size-i-1, on)
		case i < 9:
			m.set(8, 15-i, on)
		default:
			m.set(8, 14-i, on)
		}
	}
	m.set(size-8, 8, true)
	return m
}

// penalty — штраф маски по четырём правилам стандарта. Абсолютное значение
// смысла не имеет, сравниваются только штрафы восьми масок между собой.
func penalty(m Matrix) int {
	size := m.size
	score := 0

	// Правило 1: серии одного цвета длиной от пяти.
	for pass := 0; pass < 2; pass++ {
		for a := 0; a < size; a++ {
			run, prev := 1, -1
			for b := 0; b < size; b++ {
				v := 0
				if (pass == 0 && m.Dark(a, b)) || (pass == 1 && m.Dark(b, a)) {
					v = 1
				}
				if v == prev {
					run++
					continue
				}
				if run >= 5 {
					score += 3 + (run - 5)
				}
				run, prev = 1, v
			}
			if run >= 5 {
				score += 3 + (run - 5)
			}
		}
	}

	// Правило 2: одноцветные блоки 2×2.
	for r := 0; r < size-1; r++ {
		for c := 0; c < size-1; c++ {
			v := m.Dark(r, c)
			if v == m.Dark(r, c+1) && v == m.Dark(r+1, c) && v == m.Dark(r+1, c+1) {
				score += 3
			}
		}
	}

	// Правило 3: узор 1:1:3:1:1 с полем — тот же профиль, что у поискового,
	// и именно на нём сканер ошибается.
	pattern := [11]bool{true, false, true, true, true, false, true, false, false, false, false}
	var reversed [11]bool
	for i := range pattern {
		reversed[i] = pattern[10-i]
	}
	matches := func(get func(int) bool, start int) bool {
		forward, backward := true, true
		for k := 0; k < 11; k++ {
			v := get(start + k)
			if v != pattern[k] {
				forward = false
			}
			if v != reversed[k] {
				backward = false
			}
		}
		return forward || backward
	}
	for r := 0; r < size; r++ {
		for c := 0; c+11 <= size; c++ {
			if matches(func(i int) bool { return m.Dark(r, i) }, c) {
				score += 40
			}
		}
	}
	for c := 0; c < size; c++ {
		for r := 0; r+11 <= size; r++ {
			if matches(func(i int) bool { return m.Dark(i, c) }, r) {
				score += 40
			}
		}
	}

	// Правило 4: перекос доли тёмных модулей от половины.
	dark := 0
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			if m.Dark(r, c) {
				dark++
			}
		}
	}
	// Доля считается без плавающей точки: floor(|dark·100/N − 50| / 5), где
	// N = size². Целочисленное деление на N до взятия модуля округляло бы
	// процент вниз и меняло исход на границах вроде 45,1 %.
	area := size * size
	score += absInt(dark*100-50*area) / (5 * area) * 10
	return score
}

// rsRemainder — остаток от деления на порождающий многочлен: кодовые слова
// коррекции Рида — Соломона для одного блока.
func rsRemainder(data []byte, ecLen int) []byte {
	gen := genPoly(ecLen)
	res := make([]byte, len(data)+ecLen)
	copy(res, data)
	for i := 0; i < len(data); i++ {
		c := res[i]
		if c == 0 {
			continue
		}
		for j := 0; j < len(gen); j++ {
			res[i+j] ^= gfMul(gen[j], c)
		}
	}
	return res[len(data):]
}

// genPoly — порождающий многочлен степени n над GF(256).
func genPoly(n int) []byte {
	p := []byte{1}
	for i := 0; i < n; i++ {
		r := make([]byte, len(p)+1)
		for j := 0; j < len(p); j++ {
			r[j] ^= p[j]
			r[j+1] ^= gfMul(p[j], gfExp[i])
		}
		p = r
	}
	return p
}

// Таблицы GF(256) по образующему многочлену 0x11d — том самом, что задан для QR.
var gfExp, gfLog = func() ([512]byte, [256]byte) {
	var exp [512]byte
	var log [256]byte
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		exp[i] = exp[i-255]
	}
	return exp, log
}()

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
