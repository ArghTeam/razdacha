package api

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// indexFile — единственная точка входа SPA.
const indexFile = "index.html"

// static отдаёт встроенный интерфейс.
//
// Обработчик висит на `/` и получает только то, что не подошло под `/api/`:
// у [http.ServeMux] более длинный шаблон выигрывает, поэтому API-путь сюда не
// проваливается ни при какой ошибке в интерфейсе. Это важнее, чем кажется:
// иначе опечатка в пути REST вернула бы HTML со статусом 200, и клиент разбирал
// бы страницу входа как JSON.
//
// Сессии статика не требует. Требовать её было бы нечем: экран входа —
// это и есть статика, а всё, что она показывает, приезжает отдельными
// запросами к `/api/`, каждый из которых сессию проверяет.
func (s *Server) static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, s.log, http.StatusMethodNotAllowed, codeBadRequest, "Метод не поддерживается")
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = indexFile
		}

		if f, size, ok := s.openStatic(name); ok {
			defer func() { _ = f.Close() }()
			s.serveStatic(w, r, name, f, size)
			return
		}

		// Неизвестный путь — это маршрут SPA, а не отсутствующий файл: адреса
		// экранов живут в хеше, но пользователь может открыть что угодно, и
		// показать ему надо панель, а не 404.
		f, size, ok := s.openStatic(indexFile)
		if !ok {
			// Каталог сборки пуст: бинарник собран без интерфейса. Это
			// состояние допустимо (см. ui/embed.go), но молчать о нём нельзя.
			s.log.Error("интерфейс не встроен в бинарник")
			http.Error(w, "Интерфейс не собран: каталог ui/dist пуст.", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = f.Close() }()
		s.serveStatic(w, r, indexFile, f, size)
	})
}

// openStatic открывает обычный файл статики. Каталоги не отдаются: листинга у
// панели нет и быть не должно.
func (s *Server) openStatic(name string) (fs.File, int64, bool) {
	if s.ui == nil || !fs.ValidPath(name) {
		return nil, 0, false
	}
	f, err := s.ui.Open(name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Error("чтение статики", "файл", name, "ошибка", err)
		}
		return nil, 0, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		_ = f.Close()
		return nil, 0, false
	}
	return f, info.Size(), true
}

// serveStatic пишет файл в ответ.
//
// [http.ServeContent] здесь не используется: у встроенных файлов нулевое время
// изменения, и условные запросы по нему смысла не имеют. Вместо этого — явный
// Content-Type и `no-cache`: панель обновляется вместе с бинарником, а
// закешированный навсегда app.js после обновления демона означал бы белый экран.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, name string, f fs.File, size int64) {
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		s.log.Debug("статика не дописана", "файл", name, "ошибка", err)
	}
}

// contentType определяет тип по расширению. Список короткий намеренно: всё, что
// лежит в ui/dist, перечислено здесь, а неизвестное расширение — повод не гадать.
func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".woff2":
		return "font/woff2"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}
