package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Копия снимается с работающей БД и содержит то же, что оригинал. Проверяется
// именно чтение из копии, а не размер файла: смысл бэкапа в том, что из него
// поднимаются пиры с их приватными ключами.
func TestBackupKeepsState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openAt(t, filepath.Join(dir, "state.db"))

	peer, err := s.CreatePeer(ctx, samplePeer("ноутбук", "10.8.0.2/32"))
	if err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}

	dst := filepath.Join(dir, "copy", "state.db")
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// В копии приватные ключи пиров — права те же, что у оригинала.
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права копии %v, ожидались 0600", info.Mode().Perm())
	}

	copyStore := openAt(t, dst)
	got, err := copyStore.Peer(ctx, peer.ID)
	if err != nil {
		t.Fatalf("Peer из копии: %v", err)
	}
	if got.PrivateKey != peer.PrivateKey || got.PrivateKey == "" {
		t.Error("приватный ключ пира не переехал в копию")
	}
	if got.Address != peer.Address {
		t.Errorf("адрес пира в копии %q, ожидался %q", got.Address, peer.Address)
	}
}

// Копия не должна молча затирать чужой файл: имя выбирает вызывающий, и
// перезапись здесь означала бы потерю прежней копии.
func TestBackupRefusesExistingFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openAt(t, filepath.Join(dir, "state.db"))

	dst := filepath.Join(dir, "copy.db")
	if err := os.WriteFile(dst, []byte("занято"), 0o600); err != nil {
		t.Fatalf("подготовка файла: %v", err)
	}
	if err := s.Backup(ctx, dst); err == nil {
		t.Fatal("Backup перезаписал существующий файл")
	}
}

func TestBackupConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	got, err := s.BackupConfig(ctx)
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if got.Enabled || got.Passphrase != "" {
		t.Errorf("пустая БД дала %+v, ожидалось «выключено и без фразы»", got)
	}
	if got.Interval != DefaultBackupInterval {
		t.Errorf("интервал по умолчанию %v, ожидался %v", got.Interval, DefaultBackupInterval)
	}

	want := BackupConfig{Enabled: true, Interval: 6 * time.Hour, Passphrase: " длинная фраза "}
	if err := s.SaveBackupConfig(ctx, want); err != nil {
		t.Fatalf("SaveBackupConfig: %v", err)
	}
	got, err = s.BackupConfig(ctx)
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if got.Passphrase != "длинная фраза" || !got.Enabled || got.Interval != 6*time.Hour {
		t.Errorf("прочитано %+v", got)
	}
	if !got.Ready() {
		t.Error("Ready() = false при заданной фразе и включённом расписании")
	}
}

// Включить отправку без парольной фразы нельзя: это условие работы расписания,
// а не галочка «зашифровать» (ADR 0016).
func TestBackupEnabledRequiresPassphrase(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	err := s.SaveBackupConfig(ctx, BackupConfig{Enabled: true, Interval: time.Hour})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("SaveBackupConfig без фразы = %v, ожидалась ErrInvalid", err)
	}

	// Стирание фразы у включённого расписания — тот же случай: настройка
	// осталась бы включённой и молча ничего не отправляла.
	if err := s.SaveBackupConfig(ctx,
		BackupConfig{Enabled: true, Interval: time.Hour, Passphrase: "фраза подлиннее"}); err != nil {
		t.Fatalf("SaveBackupConfig: %v", err)
	}
	err = s.SaveBackupConfig(ctx, BackupConfig{Enabled: true, Interval: time.Hour})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("стирание фразы при включённом расписании = %v, ожидалась ErrInvalid", err)
	}
}

func TestBackupConfigRejectsShortPassphraseAndInterval(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	err := s.SaveBackupConfig(ctx, BackupConfig{Interval: time.Hour, Passphrase: "коротк"})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("короткая фраза = %v, ожидалась ErrInvalid", err)
	}
	err = s.SaveBackupConfig(ctx, BackupConfig{Interval: time.Minute, Passphrase: "фраза подлиннее"})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("интервал в минуту = %v, ожидалась ErrInvalid", err)
	}
}

// Отметка времени двигается только удачей: иначе отказ откладывал бы следующую
// попытку на целый интервал.
func TestMarkBackupSent(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	at := time.Date(2026, 8, 15, 3, 15, 0, 0, time.UTC)

	if err := s.MarkBackupSent(ctx, at, ""); err != nil {
		t.Fatalf("MarkBackupSent: %v", err)
	}
	got, err := s.BackupConfig(ctx)
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if !got.LastSentAt.Equal(at) || got.LastError != "" {
		t.Errorf("после удачи %+v", got)
	}

	if err := s.MarkBackupSent(ctx, at.Add(time.Hour), "телеграм отказал"); err != nil {
		t.Fatalf("MarkBackupSent: %v", err)
	}
	got, err = s.BackupConfig(ctx)
	if err != nil {
		t.Fatalf("BackupConfig: %v", err)
	}
	if !got.LastSentAt.Equal(at) {
		t.Errorf("неудача сдвинула время последней отправки: %v", got.LastSentAt)
	}
	if got.LastError != "телеграм отказал" {
		t.Errorf("текст отказа = %q", got.LastError)
	}
}

func TestStateSummaryAt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s := openAt(t, path)

	if _, err := s.CreatePeer(ctx, samplePeer("ноутбук", "10.8.0.2/32")); err != nil {
		t.Fatalf("CreatePeer: %v", err)
	}

	got, err := StateSummaryAt(ctx, path)
	if err != nil {
		t.Fatalf("StateSummaryAt: %v", err)
	}
	if got.Peers != 1 || got.Tunnels != 0 || got.Rules != 0 {
		t.Errorf("сводка %+v", got)
	}
	if got.SchemaVersion != schemaVersion() {
		t.Errorf("версия схемы %d, ожидалась %d", got.SchemaVersion, schemaVersion())
	}
	if got.Empty() {
		t.Error("база с пиром считается пустой")
	}

	// Файла нет вовсе — это пустое состояние, а не сбой: так выглядит чистая
	// установка, поверх которой и восстанавливают.
	empty, err := StateSummaryAt(ctx, filepath.Join(dir, "нет.db"))
	if err != nil {
		t.Fatalf("StateSummaryAt для отсутствующего файла: %v", err)
	}
	if !empty.Empty() || empty.SchemaVersion != 0 {
		t.Errorf("отсутствующий файл дал %+v", empty)
	}
}
