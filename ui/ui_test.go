package ui

import (
	"io/fs"
	"strings"
	"testing"
)

// TestCSSBracesBalanced — сторож против незакрытого блока.
//
// Однажды это уже стоило дня: при разведении конфликта в `app.css` осталась
// незакрытая `@media (max-width: 620px)`, и весь блок стилей модалки правила
// оказался внутри медиазапроса — на десктопе он не применялся вовсе. Выглядело
// это как «раскладку не сделали», хотя раскладка была написана и лежала рядом.
//
// Go-тесты CSS не видят, линтеры в CI тоже: браузер молча съедает такой файл.
// Поэтому проверка примитивная, но ловит ровно тот случай.
func TestCSSBracesBalanced(t *testing.T) {
	files, err := fs.Glob(dist, "dist/*.css")
	if err != nil {
		t.Fatalf("поиск css: %v", err)
	}
	if len(files) == 0 {
		t.Skip("статики нет — сборка интерфейса не положена в dist")
	}

	for _, name := range files {
		data, err := fs.ReadFile(dist, name)
		if err != nil {
			t.Fatalf("чтение %s: %v", name, err)
		}
		depth, line := 0, 1
		for _, ch := range string(data) {
			switch ch {
			case '\n':
				line++
			case '{':
				depth++
			case '}':
				depth--
				if depth < 0 {
					t.Fatalf("%s: лишняя `}` на строке %d", name, line)
				}
			}
		}
		if depth != 0 {
			t.Errorf("%s: %d незакрытых `{` — блок ниже окажется внутри чужого правила", name, depth)
		}
	}
}

// TestIndexReferencesExistingFiles — ссылка на несуществующий файл в index.html
// даёт пустой экран без единой ошибки в консоли сервера: браузер просто не
// загрузит модуль. Дешевле поймать здесь.
func TestIndexReferencesExistingFiles(t *testing.T) {
	index, err := fs.ReadFile(dist, "dist/index.html")
	if err != nil {
		t.Skip("index.html нет — сборка интерфейса не положена в dist")
	}

	for _, attr := range []string{`src="`, `href="`} {
		rest := string(index)
		for {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			end := strings.IndexByte(rest, '"')
			if end < 0 {
				break
			}
			ref := rest[:end]
			rest = rest[end:]

			// Внешние ссылки и data: в статике запрещены отдельным решением
			// (всё уезжает в go:embed), но проверять это — не задача теста.
			if ref == "" || strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") {
				continue
			}
			path := "dist/" + strings.TrimPrefix(ref, "/")
			if _, err := fs.Stat(dist, path); err != nil {
				t.Errorf("index.html ссылается на %s, которого нет в сборке", ref)
			}
		}
	}
}
