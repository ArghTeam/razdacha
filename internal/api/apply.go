package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Applier применяет состояние БД к работающему sing-box. Интерфейс, а не
// *singbox.Applier, ровно ради тестов: в них ни рантайма, ни systemd нет.
type Applier interface {
	Apply(ctx context.Context, snap store.Snapshot) (singbox.ApplyResult, error)
}

// CoherentApplier serializes snapshot acquisition and peer address allocation with
// network publication. Production wiring must supply this interface.
type CoherentApplier interface {
	Applier
	ApplyCurrent(context.Context, *store.Store) (singbox.ApplyResult, error)
	CreatePeer(context.Context, *store.Store, string, time.Time) (store.Peer, error)
}

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

// handleApply delegates the complete network publication to the daemon gate.
// The API never independently reads or publishes nft policy.
//
// Отказ `sing-box check` — 422 с текстом ошибки рантайма: прежний конфиг остался
// в силе, и пользователю нужна причина, а не код (docs/05-api.md).
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var res singbox.ApplyResult
	var err error
	if coherent, ok := s.applier.(CoherentApplier); ok {
		res, err = coherent.ApplyCurrent(r.Context(), s.store)
	} else {
		var snap store.Snapshot
		snap, err = s.store.Snapshot(r.Context())
		if err == nil {
			res, err = s.applier.Apply(r.Context(), snap)
		}
	}
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

	var nft *nftResponse
	if res.NftSubnets != nil {
		nft = &nftResponse{Subnets: *res.NftSubnets}
	}

	writeJSON(w, s.log, http.StatusOK, applyResponse{
		Changed:  res.Changed,
		Reloaded: res.Reloaded,
		Path:     res.Path,
		Detail:   applyDetail(res, nft),
		Nft:      nft,
	})
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

// PendingApplier compares editable state with committed authority without publication.
type PendingApplier interface {
	Pending(context.Context, *store.Store) (bool, error)
}

func (s *Server) handleApplyStatus(w http.ResponseWriter, r *http.Request) {
	pending, ok := s.applier.(PendingApplier)
	if !ok {
		writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady, "Applied network status is unavailable")
		return
	}
	dirty, err := pending.Pending(r.Context(), s.store)
	if err != nil {
		s.storeError(w, err, "Applied network status unavailable")
		return
	}
	writeJSON(w, s.log, http.StatusOK, map[string]bool{"dirty": dirty})
}
