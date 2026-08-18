package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/notify"
	"github.com/ArghTeam/razdacha/internal/store"
)

// backupResponse — расписание отправки копии для UI.
//
// Парольной фразы здесь нет и не будет — как токена бота: вместо значения
// признак `passphrase_set`, по которому интерфейс показывает «сохранена» вместо
// пустого поля.
type backupResponse struct {
	Enabled       bool       `json:"enabled"`
	IntervalHours int        `json:"interval_hours"`
	PassphraseSet bool       `json:"passphrase_set"`
	TelegramReady bool       `json:"telegram_ready"`
	LastSentAt    *time.Time `json:"last_sent_at"`
	LastError     string     `json:"last_error"`
}

func newBackupResponse(c store.BackupConfig, n store.NotifyConfig) backupResponse {
	out := backupResponse{
		Enabled:       c.Enabled,
		IntervalHours: int(c.Interval / time.Hour),
		PassphraseSet: c.Passphrase != "",
		TelegramReady: n.Token != "" && n.ChatID != "",
		LastError:     c.LastError,
	}
	if !c.LastSentAt.IsZero() {
		at := c.LastSentAt.UTC()
		out.LastSentAt = &at
	}
	return out
}

// backupRequest — тело `PUT /api/backup`.
//
// Фраза — указатель по той же причине, что токен бота: «не прислали» означает
// «оставить прежнюю», иначе сохранение галочки стирало бы секрет, которого в
// форме и не было. Присланная пустая строка — «стереть».
type backupRequest struct {
	Enabled       *bool   `json:"enabled"`
	IntervalHours *int    `json:"interval_hours"`
	Passphrase    *string `json:"passphrase"`
}

func (req backupRequest) apply(c store.BackupConfig) store.BackupConfig {
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.IntervalHours != nil {
		c.Interval = time.Duration(*req.IntervalHours) * time.Hour
	}
	if req.Passphrase != nil {
		c.Passphrase = strings.TrimSpace(*req.Passphrase)
	}
	return c
}

// handleBackup — `GET /api/backup`.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	cfg, notifyCfg, ok := s.backupConfigs(w, r)
	if !ok {
		return
	}
	writeJSON(w, s.log, http.StatusOK, newBackupResponse(cfg, notifyCfg))
}

// handleUpdateBackup — `PUT /api/backup`.
func (s *Server) handleUpdateBackup(w http.ResponseWriter, r *http.Request) {
	var req backupRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	cfg, notifyCfg, ok := s.backupConfigs(w, r)
	if !ok {
		return
	}

	next := req.apply(cfg)
	// Условие про фразу держит слой хранения — оно про саму копию. Условие про
	// телеграм держится здесь: слой хранения не решает, куда её отправлять, а
	// расписание без чата выглядит рабочим и молча ничего не шлёт.
	if next.Enabled && (notifyCfg.Token == "" || notifyCfg.ChatID == "") {
		writeError(w, s.log, http.StatusConflict, codeNotReady,
			"Сначала настройте бота телеграма: нужны токен и идентификатор чата.")
		return
	}
	if err := s.store.SaveBackupConfig(r.Context(), next); err != nil {
		s.storeError(w, err, "Расписание копии не сохранено")
		return
	}
	writeJSON(w, s.log, http.StatusOK, newBackupResponse(next, notifyCfg))
}

// handleBackupDownload — `GET /api/backup/download`.
//
// Отдаётся файл SQLite как есть: запрос авторизован и сделан человеком, который
// и так вошёл в панель (ADR 0016). Шифрование здесь ничего не добавляет, а вот
// сказать, что в файле приватные ключи всех пиров, обязан интерфейс.
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	data, err := s.snapshotFile(r.Context())
	if err != nil {
		s.log.Error("снятие копии состояния", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal,
			"Копия состояния не снята")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", backupFileName(s.now(), false)))
	if _, err := w.Write(data); err != nil {
		s.log.Debug("копия состояния не дописана", "ошибка", err)
	}
}

// handleBackupSend — `POST /api/backup/send`.
//
// Работает при выключенном расписании: проверять канал приходится до того, как
// его включишь. Без фразы отправки нет — в чат уходит только зашифрованное.
func (s *Server) handleBackupSend(w http.ResponseWriter, r *http.Request) {
	cfg, notifyCfg, ok := s.backupConfigs(w, r)
	if !ok {
		return
	}
	if cfg.Passphrase == "" {
		writeError(w, s.log, http.StatusConflict, codeNotReady,
			"Сначала задайте парольную фразу: копия уходит наружу только зашифрованной.")
		return
	}
	if notifyCfg.Token == "" || notifyCfg.ChatID == "" {
		writeError(w, s.log, http.StatusConflict, codeNotReady,
			"Сначала настройте бота телеграма: нужны токен и идентификатор чата.")
		return
	}

	if err := s.sendBackup(r.Context(), cfg, notifyCfg); err != nil {
		if errors.Is(err, notify.ErrNotConfigured) {
			writeError(w, s.log, http.StatusConflict, codeNotReady,
				"Сначала настройте бота телеграма: нужны токен и идентификатор чата.")
			return
		}
		s.log.Warn("отправка копии состояния", "ошибка", err)
		writeError(w, s.log, http.StatusBadGateway, codeInternal,
			"Отправить не удалось. "+err.Error())
		return
	}
	writeJSON(w, s.log, http.StatusOK, map[string]string{"detail": "Копия отправлена."})
}

// backupConfigs читает обе настройки разом: расписание без телеграма ничего не
// значит, и почти каждый обработчик здесь нуждается в обеих.
func (s *Server) backupConfigs(
	w http.ResponseWriter, r *http.Request,
) (store.BackupConfig, store.NotifyConfig, bool) {
	cfg, err := s.store.BackupConfig(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки копии не прочитаны")
		return store.BackupConfig{}, store.NotifyConfig{}, false
	}
	notifyCfg, err := s.store.NotifyConfig(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки оповещений не прочитаны")
		return store.BackupConfig{}, store.NotifyConfig{}, false
	}
	return cfg, notifyCfg, true
}

// sendBackup снимает копию, шифрует её и отдаёт транспорту. Итог записывается в
// БД: панель показывает время последней удачи и текст последнего отказа.
//
// Ни фраза, ни содержимое копии в лог не попадают ни на одной ветке.
func (s *Server) sendBackup(ctx context.Context, cfg store.BackupConfig, n store.NotifyConfig) error {
	data, err := s.snapshotFile(ctx)
	if err != nil {
		return err
	}
	enc, err := store.EncryptBackup(data, cfg.Passphrase)
	if err != nil {
		return err
	}

	name := backupFileName(s.now(), true)
	err = s.notifier(n).SendDocument(ctx, name, enc,
		"razdacha: копия состояния. Восстановление: razdacha restore "+name)
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	if markErr := s.store.MarkBackupSent(ctx, s.now(), detail); markErr != nil {
		s.log.Error("запись итога отправки копии", "ошибка", markErr)
	}
	return err
}

// snapshotFile снимает копию состояния во временный файл и возвращает её байты.
//
// Через файл, потому что снимает её SQLite (`VACUUM INTO`), а не мы: копирование
// файла базы на ходу при включённом WAL даёт состояние произвольной давности.
// Каталог создаётся с правами 0700, файл — 0600, и удаляется он в любом случае:
// в нём приватные ключи всех пиров.
func (s *Server) snapshotFile(ctx context.Context) ([]byte, error) {
	dir, err := os.MkdirTemp("", "razdacha-backup-")
	if err != nil {
		return nil, fmt.Errorf("временный каталог для копии: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "state.db")
	if err := s.store.Backup(ctx, path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение копии состояния: %w", err)
	}
	return data, nil
}

// backupFileName — имя файла копии. Время в имени, потому что копий бывает
// много и лежат они рядом; расширение `.enc` отличает зашифрованную от обычной
// на глаз, а по-настоящему их различают первые байты файла.
func backupFileName(at time.Time, encrypted bool) string {
	name := "razdacha-state-" + at.UTC().Format("20060102-150405") + ".db"
	if encrypted {
		name += ".enc"
	}
	return name
}
