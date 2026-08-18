package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

const testBackupPhrase = "очень длинная фраза"

func TestBackupRequiresSession(t *testing.T) {
	ts := newTestServer(t)
	for _, r := range []request{
		{method: http.MethodGet, path: "/api/backup"},
		{method: http.MethodPut, path: "/api/backup", body: `{}`},
		{method: http.MethodGet, path: "/api/backup/download"},
		{method: http.MethodPost, path: "/api/backup/send"},
	} {
		requireCode(t, ts.do(t, r), http.StatusUnauthorized)
	}
}

// Скачивание отдаёт файл SQLite, а не JSON и не пустоту: из него поднимается
// состояние, и первые байты это доказывают.
func TestBackupDownloadReturnsDatabase(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodGet, "/api/backup/download", "")
	requireCode(t, resp, http.StatusOK)
	if !strings.HasPrefix(resp.body, "SQLite format 3") {
		t.Fatalf("ответ не похож на базу SQLite: %.32q", resp.body)
	}
	if got := resp.header.Get("Content-Disposition"); !strings.Contains(got, "razdacha-state-") {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := resp.header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got)
	}

	// Копия снимается с работающей БД: пароль панели в ней уже есть, и по нему
	// видно, что скачали именно состояние, а не пустую схему.
	if !bytes.Contains([]byte(resp.body), []byte("argon2id")) {
		t.Error("в копии нет хеша пароля — снята не та база")
	}
}

// Парольная фраза не возвращается ни из чтения, ни из сохранения — как токен
// бота: сессия даёт право менять настройки, а не забирать секрет.
func TestBackupNeverReturnsPassphrase(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"passphrase":"`+testBackupPhrase+`"}`)
	requireCode(t, resp, http.StatusOK)
	if strings.Contains(resp.body, testBackupPhrase) {
		t.Fatalf("фраза вернулась из PUT: %s", resp.body)
	}
	if !strings.Contains(resp.body, `"passphrase_set":true`) {
		t.Errorf("признак сохранённой фразы не выставлен: %s", resp.body)
	}

	resp = ts.auth(t, cookie, http.MethodGet, "/api/backup", "")
	requireCode(t, resp, http.StatusOK)
	if strings.Contains(resp.body, testBackupPhrase) {
		t.Fatalf("фраза вернулась из GET: %s", resp.body)
	}
}

// Расписание не включается без фразы: наружу копия уходит только зашифрованной
// (ADR 0016).
func TestBackupEnableRequiresPassphrase(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)

	resp := ts.auth(t, cookie, http.MethodPut, "/api/backup", `{"enabled":true}`)
	requireCode(t, resp, http.StatusBadRequest)

	cfg, err := ts.st.BackupConfig(context.Background())
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("расписание включилось без фразы")
	}
}

// И без телеграма: расписание без чата выглядит рабочим и молча ничего не шлёт.
func TestBackupEnableRequiresTelegram(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)

	resp := ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"enabled":true,"passphrase":"`+testBackupPhrase+`"}`)
	requireCode(t, resp, http.StatusConflict)
	if code := decodeError(t, resp).Code; code != codeNotReady {
		t.Errorf("код ошибки = %q, ожидался %q", code, codeNotReady)
	}
}

// Отправка руками: в чат уходит зашифрованный файл, и он расшифровывается той
// же фразой — иначе копия бесполезна ровно тогда, когда она нужна.
func TestBackupSendEncrypts(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	f := &fakeSender{}
	withSender(ts, f)

	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"passphrase":"`+testBackupPhrase+`"}`), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/backup/send", ""), http.StatusOK)

	if len(f.docs) != 1 {
		t.Fatalf("отправлено файлов: %d, ожидался один", len(f.docs))
	}
	doc := f.docs[0]
	if !strings.HasSuffix(doc.name, ".db.enc") {
		t.Errorf("имя файла = %q", doc.name)
	}
	if !store.IsEncryptedBackup(doc.data) {
		t.Fatal("в чат ушёл незашифрованный файл")
	}
	if bytes.Contains(doc.data, []byte("SQLite format 3")) {
		t.Error("содержимое базы видно в отправленном файле")
	}

	plain, err := store.DecryptBackup(doc.data, testBackupPhrase)
	if err != nil {
		t.Fatalf("DecryptBackup: %v", err)
	}
	if !store.IsStateFile(plain) {
		t.Error("расшифрованное не похоже на базу")
	}

	cfg, err := ts.st.BackupConfig(context.Background())
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if cfg.LastSentAt.IsZero() || cfg.LastError != "" {
		t.Errorf("итог отправки записан как %+v", cfg)
	}
}

// Без фразы отправлять нечего: открытый файл со всеми ключами пиров в чат не
// уходит даже по кнопке.
func TestBackupSendRequiresPassphrase(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	f := &fakeSender{}
	withSender(ts, f)

	resp := ts.auth(t, cookie, http.MethodPost, "/api/backup/send", "")
	requireCode(t, resp, http.StatusConflict)
	if len(f.docs) != 0 {
		t.Fatal("файл ушёл без парольной фразы")
	}
}

// Отказ транспорта виден в панели: время последней удачи не двигается, текст
// ошибки сохраняется.
func TestBackupSendRecordsError(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	withSender(ts, &fakeSender{err: errors.New("телеграм отказал: чат не найден")})

	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"passphrase":"`+testBackupPhrase+`"}`), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/backup/send", ""),
		http.StatusBadGateway)

	cfg, err := ts.st.BackupConfig(context.Background())
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if !cfg.LastSentAt.IsZero() {
		t.Error("неудачная отправка сдвинула время последней удачи")
	}
	if !strings.Contains(cfg.LastError, "чат не найден") {
		t.Errorf("текст отказа = %q", cfg.LastError)
	}
	// Фраза не должна попасть в лог ни при каком исходе.
	if strings.Contains(ts.logs.String(), testBackupPhrase) {
		t.Error("парольная фраза попала в лог")
	}
}

// Расписание молчит, пока его не включили: выключенный круг наружу не ходит.
func TestBackupRoundSkipsDisabled(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	f := &fakeSender{}
	withSender(ts, f)

	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"passphrase":"`+testBackupPhrase+`"}`), http.StatusOK)

	var lastTry time.Time
	ts.backupRound(context.Background(), &lastTry)
	if len(f.docs) != 0 {
		t.Fatal("выключенное расписание отправило копию")
	}
}

// Включённое расписание отправляет копию сразу, а следующую — не раньше
// интервала: перезапуск демона не должен превращаться в копию каждый час.
func TestBackupRoundSendsOnSchedule(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	f := &fakeSender{}
	withSender(ts, f)

	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"enabled":true,"interval_hours":6,"passphrase":"`+testBackupPhrase+`"}`),
		http.StatusOK)

	ctx := context.Background()
	var lastTry time.Time
	ts.backupRound(ctx, &lastTry)
	if len(f.docs) != 1 {
		t.Fatalf("первый круг отправил %d файлов, ожидался один", len(f.docs))
	}

	ts.advance(time.Hour)
	ts.backupRound(ctx, &lastTry)
	if len(f.docs) != 1 {
		t.Fatalf("копия ушла раньше интервала: %d файлов", len(f.docs))
	}

	ts.advance(6 * time.Hour)
	ts.backupRound(ctx, &lastTry)
	if len(f.docs) != 2 {
		t.Fatalf("после интервала отправлено %d файлов, ожидалось два", len(f.docs))
	}
}

// Неудача не повторяется каждую минуту: пауза считается от последней попытки.
func TestBackupRoundBacksOffAfterFailure(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	f := &fakeSender{err: errors.New("телеграм недоступен")}
	withSender(ts, f)

	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup",
		`{"enabled":true,"interval_hours":6,"passphrase":"`+testBackupPhrase+`"}`),
		http.StatusOK)

	ctx := context.Background()
	var lastTry time.Time
	ts.backupRound(ctx, &lastTry)
	ts.advance(time.Minute)
	ts.backupRound(ctx, &lastTry)

	cfg, err := ts.st.BackupConfig(ctx)
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if cfg.LastError == "" {
		t.Error("отказ не записан")
	}
	if !lastTry.Equal(ts.clock().Add(-time.Minute)) {
		t.Errorf("время попытки = %v, ожидалось время первого круга", lastTry)
	}
}

// setupTelegram задаёт токен и чат: без них расписание копии не включается.
func setupTelegram(t *testing.T, ts *testServer, cookie *http.Cookie) {
	t.Helper()
	requireCode(t, saveNotify(t, ts, cookie, `{"chat_id":"-1001","token":"123:ABC"}`),
		http.StatusOK)
}
