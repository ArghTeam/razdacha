package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// versionProbeTimeout — сколько ждём рантайм с ответом о своей версии.
//
// Запрос локальный и мгновенный, а ручку дёргает каждая загрузка панели:
// общий дедлайн клиента Clash API считается от таймаута проверки туннеля
// (секунды) и здесь означал бы висящий экран, если sing-box жив, но занят.
const versionProbeTimeout = 2 * time.Second

// Build — факты сборки демона. Их знает точка входа: версия и коммит приходят
// от линкера (`Makefile`, `release.yml`), версия библиотеки sing-box — из
// вшитой информации о сборке. Слой api их только отдаёт и ничего не выясняет
// сам.
type Build struct {
	// Version — версия работающего бинарника. Пустая означает, что линкер её
	// не подставил; в такой сборке она равна `dev`.
	Version string
	// Commit — коммит сборки. Пустой отдаётся как null: придумывать его нечем.
	Commit string
	// SingboxLibrary — версия библиотеки sing-box без ведущей «v». Пустая
	// отдаётся как null.
	SingboxLibrary string
}

// singboxVersions — блок sing-box в ответе.
type singboxVersions struct {
	// Library — версия библиотеки, с которой собран демон.
	Library *string `json:"library"`
	// Runtime — версия работающего рантайма.
	Runtime *string `json:"runtime"`
	// RuntimeDetail — почему версии рантайма нет. Текст на русском, он
	// показывается пользователю как есть.
	RuntimeDetail *string `json:"runtime_detail"`
}

// versionResponse — тело `GET /api/version`.
type versionResponse struct {
	Version          string          `json:"version"`
	Commit           *string         `json:"commit"`
	InstalledVersion *string         `json:"installed_version"`
	VersionMismatch  bool            `json:"version_mismatch"`
	SchemaVersion    int             `json:"schema_version"`
	Singbox          singboxVersions `json:"singbox"`
}

// handleVersion отвечает, что именно сейчас развёрнуто на сервере.
//
// Недоступный рантайм ручку не роняет: версии демона, схемы и библиотеки от
// него не зависят, а «sing-box не отвечает» — это ответ, а не отказ.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	installed, err := s.store.InstalledVersion(r.Context())
	if err != nil {
		s.log.Error("чтение версии установки", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}
	schema, err := s.store.SchemaVersion(r.Context())
	if err != nil {
		s.log.Error("чтение версии схемы", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}

	running := s.build.Version
	if running == "" {
		running = "dev"
	}

	out := versionResponse{
		Version:          running,
		Commit:           optionalText(s.build.Commit),
		InstalledVersion: optionalText(installed),
		VersionMismatch:  versionMismatch(running, installed),
		SchemaVersion:    schema,
		Singbox: singboxVersions{
			Library: optionalText(s.build.SingboxLibrary),
		},
	}

	ctx, cancel := context.WithTimeout(r.Context(), versionProbeTimeout)
	defer cancel()
	if v, verr := s.clash.Version(ctx); verr == nil {
		out.Singbox.Runtime = optionalText(v)
	} else {
		out.Singbox.RuntimeDetail = optionalText(verr.Error())
	}

	writeJSON(w, s.log, http.StatusOK, out)
}

// versionMismatch отвечает, разошлись ли версия работающего бинарника и та, что
// записал установщик.
//
// Ради этого сравнения ручка и заводилась: расходятся они ровно тогда, когда на
// диске лежит новый бинарник, а systemd крутит старый процесс. Неизвестная
// версия установки (БД от версии до 0.2.1) расхождением не считается: сравнить
// не с чем, и предупреждение было бы выдумкой.
func versionMismatch(running, installed string) bool {
	if installed == "" {
		return false
	}
	return normalizeVersion(running) != normalizeVersion(installed)
}

// normalizeVersion снимает ведущую «v»: установщик пишет версию из тега без
// неё, а `git describe` в сборке из рабочего дерева — с ней.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// optionalText отдаёт пустую строку как null: клиент обязан отличать «данных
// нет» от пустого значения (инвариант слоя).
func optionalText(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
