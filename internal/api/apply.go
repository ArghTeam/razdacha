package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Applier применяет состояние БД к работающему sing-box. Интерфейс, а не
// *singbox.Applier, ровно ради тестов: в них ни рантайма, ни systemd нет.
type Applier interface {
	Apply(ctx context.Context, snap store.Snapshot) (singbox.ApplyResult, error)
}

// NftApplier перезаливает таблицу nft подсетями из состояния БД и отвечает,
// сколько подсетей ушло в сет.
//
// Замыкание, а не тип слоя netstack: про nftables слой api не знает, проводку
// даёт демон (`cmd/razdachad/netfilter_linux.go`). Заодно это единственный
// способ проверить ручку тестами — файл проводки платформенный и на macOS не
// собирается вовсе.
type NftApplier func(ctx context.Context) (int, error)

// applyResponse — тело `POST /api/apply`.
//
// `changed` отвечает на вопрос «а было ли что применять»: изменения через REST
// ложатся в БД сразу, но применяются пакетно, и UI показывает плашку с кнопкой
// (docs/05-api.md). Нажатие без изменений — обычное дело, и это не ошибка.
type applyResponse struct {
	Changed  bool         `json:"changed"`
	Reloaded bool         `json:"reloaded"`
	Path     string       `json:"path"`
	Detail   string       `json:"detail"`
	Nft      *nftResponse `json:"nft"`
}

// nftResponse — вторая половина применения: перезаливка таблицы nft. `null`
// означает, что подсистема правил не поднята и залит только конфиг.
type nftResponse struct {
	Subnets int `json:"subnets"`
}

// handleApply пересобирает конфиг sing-box из состояния и применяет его, а
// следом перезаливает таблицу nft: без второй половины подсети, дописанные в
// правило руками, ждали бы планового прогона списков — до суток (issue #119).
//
// Отказ `sing-box check` — 422 с текстом ошибки рантайма: прежний конфиг остался
// в силе, и пользователю нужна причина, а не код (docs/05-api.md).
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	snap, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.storeError(w, err, "Состояние не прочитано")
		return
	}

	res, err := s.applier.Apply(r.Context(), snap)
	switch {
	case errors.Is(err, singbox.ErrCheckFailed):
		s.log.Error("конфиг sing-box не применён", "ошибка", err)
		writeError(w, s.log, http.StatusUnprocessableEntity, codeInvalidConfig, err.Error())
		return
	case errors.Is(err, singbox.ErrReloadFailed):
		// Конфиг записан и валиден, перечитать его не удалось. Это не 422:
		// возвращать нечего откатывать, но и «применено» сказать нельзя.
		s.log.Error("sing-box не перезагружен", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, err.Error())
		return
	case errors.Is(err, singbox.ErrGenerateFailed):
		s.log.Error("конфиг sing-box не собран", "ошибка", err)
		writeError(w, s.log, http.StatusUnprocessableEntity, codeInvalidConfig, err.Error())
		return
	case err != nil:
		// Всё прочее — окружение, а не конфиг: нет прав на каталог, диск
		// заполнен, файл занят. На стенде такая ошибка приехала как
		// «invalid_config», и по ответу выходило, будто пользователь ввёл
		// что-то не то, хотя `ReadWritePaths` в юните не пускал в /etc/sing-box.
		s.log.Error("применение конфигурации", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	// Таблица nft заливается после конфига, а не до: помеченный трафик, для
	// которого в конфиге ещё нет правила, доезжает до tproxy и уходит под
	// `final: direct-out` — напрямую с адреса сервера.
	nft, err := s.applyNftRules(r.Context())
	if err != nil {
		// Конфиг уже применён, поэтому это не 422 и не «применено»: в ядре
		// осталась прежняя таблица целиком, и пользователь обязан узнать, что
		// половина применения не доехала.
		s.log.Error("правила nft не перезалиты", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal,
			fmt.Sprintf("Конфигурация sing-box применена, правила nft не перезалиты: %v", err))
		return
	}

	writeJSON(w, s.log, http.StatusOK, applyResponse{
		Changed:  res.Changed,
		Reloaded: res.Reloaded,
		Path:     res.Path,
		Detail:   applyDetail(res, nft),
		Nft:      nft,
	})
}

// applyNftRules перезаливает таблицу nft, если демон дал заливку.
//
// Заливка идёт и при `changed == false`: сет расходится с правилами независимо
// от конфига — правка подсетей его не меняет, — а ждать планового прогона
// списков сутки незачем (issue #119). Пустая заливка ошибкой не считается:
// подсистема правил есть только в Linux, а тесты слоя ходят в ручку без неё.
func (s *Server) applyNftRules(ctx context.Context) (*nftResponse, error) {
	if s.applyNft == nil {
		return nil, nil
	}
	subnets, err := s.applyNft(ctx)
	if err != nil {
		return nil, err
	}
	return &nftResponse{Subnets: subnets}, nil
}

// applyDetail — строка для плашки в UI. Текст здесь, а не в UI, потому что
// различить «нечего применять» и «применено» может только эта сторона.
func applyDetail(res singbox.ApplyResult, nft *nftResponse) string {
	var detail string
	switch {
	case !res.Changed:
		detail = "Конфигурация не изменилась, sing-box не перезапускался"
	case !res.Reloaded:
		detail = "Конфигурация записана, но sing-box не перезагружен"
	default:
		detail = "Конфигурация применена, sing-box перезагружен"
	}
	if nft == nil {
		return detail
	}
	return fmt.Sprintf("%s; подсетей в nft-сете: %d", detail, nft.Subnets)
}
