package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Ключи резервного копирования в таблице settings.
//
// Как токен телеграма и хеш пароля, они лежат в той же таблице, но вне
// [Settings]: парольная фраза — секрет, и в `Settings` она переписывалась бы из
// `PATCH /api/settings` и уезжала бы обратно в `GET /api/settings`.
const (
	keyBackupEnabled    = "backup_enabled"
	keyBackupInterval   = "backup_interval"
	keyBackupPassphrase = "backup_passphrase"
	keyBackupLastSentAt = "backup_last_sent_at"
	keyBackupLastError  = "backup_last_error"

	backupEnabledTrue = "1"
)

const (
	// MinBackupPassphraseLen — короче нечего и шифровать: перебор такой фразы
	// стоит дешевле, чем argon2id за него просит, а копия уезжает в чужое
	// облако и лежит там неопределённо долго.
	MinBackupPassphraseLen = 8

	// MinBackupInterval — чаще часа копия не отправляется. Каждая отправка
	// снимает всю базу и грузит файл в телеграм; расписание раз в минуту
	// превратило бы бэкап в нагрузку и в спам.
	MinBackupInterval = time.Hour

	// DefaultBackupInterval — сутки. Копия суточной давности теряет день
	// работы, и это приемлемо; час теряет ещё меньше, но платить за него
	// придётся каждым файлом со всеми ключами пиров в чате.
	DefaultBackupInterval = 24 * time.Hour
)

// BackupConfig — расписание отправки копии состояния наружу (ADR 0016).
//
// Транспорт тот же, что у оповещений: [NotifyConfig] отвечает за то, куда и чем
// отправлять, здесь — когда и чем шифровать. Второго канала не заводится.
type BackupConfig struct {
	// Enabled — включено ли расписание. Заводится выключенным.
	Enabled bool
	// Interval — как часто уходит копия.
	Interval time.Duration
	// Passphrase — парольная фраза шифрования. Пустая означает, что расписание
	// включить нельзя: это условие работы, а не галочка «зашифровать».
	Passphrase string
	// LastSentAt — когда копия ушла в последний раз. Нулевое время означает
	// «ещё не отправляли»: расписание считается от него, и перезапуск демона
	// не превращается в лишнюю отправку.
	LastSentAt time.Time
	// LastError — чем кончилась последняя попытка. Пустая строка означает, что
	// последняя попытка удалась либо её не было.
	LastError string
}

// Ready отвечает, есть ли чем шифровать включённое расписание.
func (c BackupConfig) Ready() bool {
	return c.Enabled && c.Passphrase != ""
}

// DefaultBackupConfig — расписание по умолчанию: выключено, сутки, без фразы.
func DefaultBackupConfig() BackupConfig {
	return BackupConfig{Interval: DefaultBackupInterval}
}

// BackupConfig читает расписание отправки. Отсутствующие ключи означают
// «не настроено» — это не ошибка.
func (s *Store) BackupConfig(ctx context.Context) (BackupConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE key IN (?, ?, ?, ?, ?)`,
		keyBackupEnabled, keyBackupInterval, keyBackupPassphrase,
		keyBackupLastSentAt, keyBackupLastError)
	if err != nil {
		return BackupConfig{}, fmt.Errorf("чтение настроек резервной копии: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := DefaultBackupConfig()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return BackupConfig{}, fmt.Errorf("чтение настроек резервной копии: %w", err)
		}
		switch key {
		case keyBackupEnabled:
			out.Enabled = value == backupEnabledTrue
		case keyBackupInterval:
			seconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return BackupConfig{}, fmt.Errorf("%w: настройка %s = %q не число",
					ErrInvalid, keyBackupInterval, value)
			}
			out.Interval = time.Duration(seconds) * time.Second
		case keyBackupPassphrase:
			out.Passphrase = value
		case keyBackupLastSentAt:
			unix, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return BackupConfig{}, fmt.Errorf("%w: настройка %s = %q не число",
					ErrInvalid, keyBackupLastSentAt, value)
			}
			if unix > 0 {
				out.LastSentAt = time.Unix(unix, 0).UTC()
			}
		case keyBackupLastError:
			out.LastError = value
		}
	}
	if err := rows.Err(); err != nil {
		return BackupConfig{}, fmt.Errorf("чтение настроек резервной копии: %w", err)
	}
	if out.Interval <= 0 {
		out.Interval = DefaultBackupInterval
	}
	return out, nil
}

// SaveBackupConfig записывает расписание целиком.
//
// Включить отправку без парольной фразы нельзя: копия содержит приватные ключи
// всех пиров, а телеграм — чужое облако, где сообщение живёт неопределённо долго
// (ADR 0016). Отметки о последней отправке этот вызов не трогает — их ведёт
// [Store.MarkBackupSent].
func (s *Store) SaveBackupConfig(ctx context.Context, c BackupConfig) error {
	c.Passphrase = strings.TrimSpace(c.Passphrase)
	if c.Enabled && c.Passphrase == "" {
		return fmt.Errorf("%w: отправка копии включается только с парольной фразой", ErrInvalid)
	}
	if c.Passphrase != "" && len([]rune(c.Passphrase)) < MinBackupPassphraseLen {
		return fmt.Errorf("%w: парольная фраза короче %d символов",
			ErrInvalid, MinBackupPassphraseLen)
	}
	if c.Interval < MinBackupInterval {
		return fmt.Errorf("%w: интервал отправки копии меньше часа", ErrInvalid)
	}

	enabled := ""
	if c.Enabled {
		enabled = backupEnabledTrue
	}
	values := map[string]string{
		keyBackupEnabled:    enabled,
		keyBackupInterval:   strconv.FormatInt(int64(c.Interval/time.Second), 10),
		keyBackupPassphrase: c.Passphrase,
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for key, value := range values {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
				key, value); err != nil {
				return fmt.Errorf("запись настройки %s: %w", key, err)
			}
		}
		return nil
	})
}

// MarkBackupSent записывает итог попытки отправки: время удачи и текст отказа.
//
// Пустая sendErr означает удачу, и тогда же двигается отметка времени: от неё
// считается следующее срабатывание расписания. Неудача времени не двигает —
// иначе отказ откладывал бы следующую попытку на целый интервал.
func (s *Store) MarkBackupSent(ctx context.Context, at time.Time, sendErr string) error {
	values := map[string]string{keyBackupLastError: sendErr}
	if sendErr == "" {
		values[keyBackupLastSentAt] = strconv.FormatInt(at.UTC().Unix(), 10)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for key, value := range values {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
				key, value); err != nil {
				return fmt.Errorf("запись настройки %s: %w", key, err)
			}
		}
		return nil
	})
}

// Backup снимает копию состояния в файл dst.
//
// `VACUUM INTO`, а не копирование файла: у базы включён WAL, и байты основного
// файла без него — состояние произвольной давности. SQLite делает копию из
// собственной транзакции, поэтому она согласована и не требует остановки демона;
// заодно копия получается сжатой и без свободных страниц.
//
// Файл создаётся правами 0600 до вызова SQLite: там лежат приватные ключи пиров,
// ровно как в оригинале. Существующий файл SQLite перезаписывать отказывается —
// имя выбирает вызывающий.
func (s *Store) Backup(ctx context.Context, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("резервная копия %s: файл уже существует", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("резервная копия %s: %w", dst, err)
	}
	// Файл создаёт сам SQLite: `VACUUM INTO` отказывается писать в
	// существующий, поэтому заранее его не подготовить, как это делает [Open].
	// Каталог, наоборот, наш: пока копия не дописана и права на неё не
	// выставлены, её закрывают права каталога — 0700.
	if dir := filepath.Dir(dst); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, dbDirMode); err != nil {
			return fmt.Errorf("создание каталога %s: %w", dir, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("снятие резервной копии в %s: %w", dst, err)
	}
	if err := os.Chmod(dst, dbFileMode); err != nil {
		return fmt.Errorf("права на резервную копию %s: %w", dst, err)
	}
	return nil
}

// StateSummary — что лежит в файле состояния: сколько сущностей и версия схемы.
// Считается без миграций, поэтому годится и для копии из будущего.
type StateSummary struct {
	SchemaVersion int
	Peers         int
	Tunnels       int
	Rules         int
}

// Empty отвечает, есть ли в базе то, что жалко потерять. Настройки и пароль
// панели сюда не входят намеренно: свежая установка заводит и то, и другое, и
// по ним любая база выглядела бы непустой.
func (v StateSummary) Empty() bool {
	return v.Peers == 0 && v.Tunnels == 0 && v.Rules == 0
}

// StateSummaryAt читает содержимое файла БД, не открывая его через [Open] и не
// накатывая миграции — тем же приёмом, что [InstalledVersionAt].
//
// Миграций здесь быть не должно дважды: файл может оказаться и копией из
// будущего (её надо отвергнуть, а не тронуть), и рабочей базой сервера, которую
// восстановление ещё только собирается заместить.
//
// Отсутствующие таблицы — не ошибка: это пустой или чужой файл, и нули о нём
// честнее отказа.
func StateSummaryAt(ctx context.Context, path string) (StateSummary, error) {
	// Файла нет — состояние пустое, и создавать его запросом нельзя: SQLite
	// заводит базу на первом же обращении, а этот вызов задан вопросом, а не
	// намерением что-то записать.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return StateSummary{}, nil
	} else if err != nil {
		return StateSummary{}, fmt.Errorf("чтение состояния из %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return StateSummary{}, fmt.Errorf("чтение состояния из %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var out StateSummary
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&out.SchemaVersion); err != nil {
		return StateSummary{}, fmt.Errorf("чтение версии схемы из %s: %w", path, err)
	}

	counts := []struct {
		table string
		dst   *int
	}{
		{"peers", &out.Peers},
		{"tunnels", &out.Tunnels},
		{"rules", &out.Rules},
	}
	for _, c := range counts {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, c.table).Scan(&name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return StateSummary{}, fmt.Errorf("чтение состояния из %s: %w", path, err)
		}
		if err := db.QueryRowContext(ctx,
			"SELECT count(*) FROM "+c.table).Scan(c.dst); err != nil {
			return StateSummary{}, fmt.Errorf("чтение состояния из %s: %w", path, err)
		}
	}
	return out, nil
}
