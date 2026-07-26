package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestPanelPublicUnsaved — отсутствие ключа отдаётся как «не записан», а не как
// приватный режим. Свёрнутые в одно, эти состояния уводили публичную установку
// из интернета при обновлении с версий, которые ключа не писали (issue #81).
func TestPanelPublicUnsaved(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	public, saved, err := s.PanelPublic(ctx)
	if err != nil {
		t.Fatalf("PanelPublic: %v", err)
	}
	if public || saved {
		t.Fatalf("пустая БД отдала public=%v saved=%v, ожидались false/false", public, saved)
	}
}

// TestPanelPublicSavedFalse — записанный приватный режим отличается от
// незаписанного: первый выбран сознательно, второй никто не выбирал.
func TestPanelPublicSavedFalse(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if err := s.SetPanelPublic(ctx, false); err != nil {
		t.Fatalf("SetPanelPublic: %v", err)
	}
	public, saved, err := s.PanelPublic(ctx)
	if err != nil {
		t.Fatalf("PanelPublic: %v", err)
	}
	if public || !saved {
		t.Fatalf("public=%v saved=%v, ожидались false/true", public, saved)
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

	public, saved, err := again.PanelPublic(ctx)
	if err != nil || !public || !saved {
		t.Fatalf("режим после переоткрытия: public=%v saved=%v err=%v", public, saved, err)
	}

	if err := again.SetPanelPublic(ctx, false); err != nil {
		t.Fatalf("выключение режима: %v", err)
	}
	if public, saved, err = again.PanelPublic(ctx); err != nil || public || !saved {
		t.Fatalf("режим не выключился: public=%v saved=%v err=%v", public, saved, err)
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
	public, saved, err := s.PanelPublic(ctx)
	if err != nil || !public || !saved {
		t.Fatalf("сохранение настроек сбросило режим панели: public=%v saved=%v err=%v",
			public, saved, err)
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
