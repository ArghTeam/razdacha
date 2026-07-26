package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestPanelPublicDefaultsToPrivate — отсутствие ключа означает приватный режим:
// так вела себя каждая установка до того, как режим начали хранить.
func TestPanelPublicDefaultsToPrivate(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	public, err := s.PanelPublic(ctx)
	if err != nil {
		t.Fatalf("PanelPublic: %v", err)
	}
	if public {
		t.Fatal("пустая БД отдала публичный режим")
	}
}

// TestPanelPublicRoundTrip — режим переживает закрытие БД: ради этого он в ней и
// лежит, иначе обновление уводило бы панель из интернета (issue #81).
func TestPanelPublicRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "razdacha.db")

	s := openAt(t, path)
	if err := s.SetPanelPublic(ctx, true); err != nil {
		t.Fatalf("SetPanelPublic: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer func() { _ = again.Close() }()

	public, err := again.PanelPublic(ctx)
	if err != nil || !public {
		t.Fatalf("режим после переоткрытия: public=%v err=%v", public, err)
	}

	if err := again.SetPanelPublic(ctx, false); err != nil {
		t.Fatalf("выключение режима: %v", err)
	}
	if public, err = again.PanelPublic(ctx); err != nil || public {
		t.Fatalf("режим не выключился: public=%v err=%v", public, err)
	}
}

// TestPanelPublicNotInSettings — режим не должен попадать в store.Settings:
// иначе `PATCH /api/settings` из панели переписывал бы его вслепую, как и хеш
// пароля. Проверка от обратного: сохранение настроек ключ не трогает.
func TestPanelPublicNotInSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.SetPanelPublic(ctx, true); err != nil {
		t.Fatalf("SetPanelPublic: %v", err)
	}
	if err := s.SaveSettings(ctx, DefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	public, err := s.PanelPublic(ctx)
	if err != nil || !public {
		t.Fatalf("сохранение настроек сбросило режим панели: public=%v err=%v", public, err)
	}
}

// TestInstalledVersion — версия установки читается обратно и перезаписывается.
func TestInstalledVersion(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	got, err := s.InstalledVersion(ctx)
	if err != nil || got != "" {
		t.Fatalf("пустая БД отдала версию %q, ошибка %v", got, err)
	}
	if err := s.SetInstalledVersion(ctx, "0.2.0"); err != nil {
		t.Fatalf("SetInstalledVersion: %v", err)
	}
	if got, err = s.InstalledVersion(ctx); err != nil || got != "0.2.0" {
		t.Fatalf("версия %q, ожидалась 0.2.0, ошибка %v", got, err)
	}
	if err := s.SetInstalledVersion(ctx, "0.2.1"); err != nil {
		t.Fatalf("перезапись версии: %v", err)
	}
	if got, err = s.InstalledVersion(ctx); err != nil || got != "0.2.1" {
		t.Fatalf("версия %q, ожидалась 0.2.1, ошибка %v", got, err)
	}
}

// TestInstalledVersionAt — чтение версии из файла БД без открытия через Open:
// резервная копия снимается до миграций, а называться обязана версией, чьё
// состояние в ней лежит.
func TestInstalledVersionAt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "razdacha.db")

	s := openAt(t, path)
	if err := s.SetInstalledVersion(ctx, "0.2.0"); err != nil {
		t.Fatalf("SetInstalledVersion: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := InstalledVersionAt(ctx, path)
	if err != nil || got != "0.2.0" {
		t.Fatalf("версия из файла %q, ожидалась 0.2.0, ошибка %v", got, err)
	}
}

// TestInstalledVersionAtUnknown — БД от версии, которая ключа не знала, это не
// сбой: обновление с 0.2.0 должно проходить, просто без имени версии.
func TestInstalledVersionAtUnknown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "razdacha.db")

	s := openAt(t, path)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := InstalledVersionAt(ctx, path)
	if err != nil || got != "" {
		t.Fatalf("версия %q, ошибка %v: ожидалась пустая без ошибки", got, err)
	}
}
