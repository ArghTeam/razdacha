package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// Ключи установочных фактов — того, что решено при установке и должно её
// пережить. Как и хеш пароля (auth.go), они намеренно не входят в [Settings]:
// `SaveSettings` переписывает только свои ключи, а `Settings` игнорирует чужие,
// поэтому `PATCH /api/settings` из панели не уводит панель из интернета и не
// подделывает версию установки.
const (
	// keyPanelPublic — публичный режим панели (ADR 0009). Режим это свойство
	// установки, а не аргумент запуска: обновление документированным
	// однострочником не передаёт флагов, и без сохранённого значения панель
	// молча уходила из интернета (issue #81).
	keyPanelPublic = "panel_public"

	// keyInstalledVersion — версия, которая сейчас установлена. По ней
	// называется резервная копия БД при следующем обновлении: копия — путь
	// отката, и назвать её версией, на которую обновляемся, значит соврать.
	keyInstalledVersion = "installed_version"
)

// PanelPublic отвечает, стоит ли панель в публичном режиме, и записан ли режим
// вообще.
//
// Второе значение обязано быть отдельным. Версии до 0.2.1 ключа не писали, и
// свернув «ключа нет» в «приватный режим», мы уводили публичную установку из
// интернета на первом же обновлении — молча, потому что менять было нечего
// (issue #81). Кто решает, чем считать отсутствие ключа, тот и знает контекст:
// здесь это `razdacha setup`, который умеет посмотреть в конфиг nginx.
func (s *Store) PanelPublic(ctx context.Context) (public, saved bool, err error) {
	value, err := s.settingValue(ctx, keyPanelPublic)
	if err != nil {
		return false, false, err
	}
	if value == "" {
		return false, false, nil
	}
	public, err = strconv.ParseBool(value)
	if err != nil {
		return false, false, fmt.Errorf("%w: настройка %s = %q не булево",
			ErrInvalid, keyPanelPublic, value)
	}
	return public, true, nil
}

// SetPanelPublic запоминает режим панели.
func (s *Store) SetPanelPublic(ctx context.Context, public bool) error {
	return s.setSettingValue(ctx, keyPanelPublic, strconv.FormatBool(public))
}

// InstalledVersion возвращает версию, записанную последней установкой. Пустая
// строка означает, что версия неизвестна: до 0.2.1 её никто не записывал.
func (s *Store) InstalledVersion(ctx context.Context) (string, error) {
	return s.settingValue(ctx, keyInstalledVersion)
}

// SetInstalledVersion записывает версию текущей установки.
func (s *Store) SetInstalledVersion(ctx context.Context, version string) error {
	if version == "" {
		return fmt.Errorf("%w: пустая версия установки", ErrInvalid)
	}
	return s.setSettingValue(ctx, keyInstalledVersion, version)
}

// InstalledVersionAt читает версию установки из файла БД, не открывая его через
// [Open] и не накатывая миграции.
//
// Так приходится делать ровно в одном месте: резервная копия снимается до
// открытия БД (миграции назад не откатываются), а называться она обязана
// версией, состояние которой в ней лежит. Открыть, прочитать и потом копировать
// — значит скопировать уже мигрированный файл.
//
// Ничего не найдено — пустая строка без ошибки: БД от версии, которая ключа не
// знала, это не сбой.
func InstalledVersionAt(ctx context.Context, path string) (string, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return "", fmt.Errorf("чтение версии установки из %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	// Таблицы может не быть вовсе: файл мог остаться от версии до первой
	// миграции. Проверяем схему, а не разбираем текст ошибки драйвера.
	var name string
	err = db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'settings'`).Scan(&name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("чтение версии установки из %s: %w", path, err)
	}

	var value string
	err = db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, keyInstalledVersion).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("чтение версии установки из %s: %w", path, err)
	}
	return value, nil
}

// settingValue читает одно значение; отсутствие ключа — пустая строка.
func (s *Store) settingValue(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("чтение настройки %s: %w", key, err)
	}
	return value, nil
}

// setSettingValue пишет одно значение, не трогая остальные ключи.
func (s *Store) setSettingValue(ctx context.Context, key, value string) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value); err != nil {
		return fmt.Errorf("запись настройки %s: %w", key, err)
	}
	return nil
}
