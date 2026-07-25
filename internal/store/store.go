// Package store — слой хранения razdacha: схема SQLite, миграции и доступ к данным.
//
// Состояние БД — единственный источник правды для остальных слоёв: конфиг sing-box
// генерируется целиком из снимка [Store.Snapshot], а не патчится частично.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // драйвер без cgo, см. docs/decisions/0006-go-daemon.md
)

// Ошибки, по которым вызывающий слой принимает решения. Тексты доходят до UI как есть.
var (
	// ErrNotFound — записи с таким id нет.
	ErrNotFound = errors.New("запись не найдена")
	// ErrInUse — на запись ссылается другая, удаление отклонено.
	ErrInUse = errors.New("запись используется")
	// ErrInvalid — данные не проходят проверку до записи в БД.
	ErrInvalid = errors.New("некорректные данные")
)

// MemoryPath — путь для БД в памяти, используется в тестах.
const MemoryPath = ":memory:"

// dbFileMode — в БД лежат приватные ключи пиров, файл читает только root.
const dbFileMode os.FileMode = 0o600

// dbDirMode — каталог состояния демона.
const dbDirMode os.FileMode = 0o700

// Store — доступ к состоянию демона.
type Store struct {
	db *sql.DB
}

// Open открывает БД по пути path, создаёт её при необходимости и накатывает миграции.
// Файл и его каталог создаются с правами 0600/0700: там хранятся приватные ключи пиров.
// Путь [MemoryPath] открывает БД в памяти.
func Open(ctx context.Context, path string) (*Store, error) {
	if path != MemoryPath {
		if err := prepareFile(path); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("открытие БД %s: %w", path, err)
	}
	// Демон один, писатель один; единственное соединение снимает вопрос блокировок
	// и гарантирует, что PRAGMA применены ко всем запросам.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("открытие БД %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close закрывает соединение с БД.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("закрытие БД: %w", err)
	}
	return nil
}

// DB отдаёт соединение для миграций и тестов внутри слоя.
func (s *Store) DB() *sql.DB { return s.db }

// dsn собирает строку подключения. foreign_keys обязателен: без него ON DELETE RESTRICT
// на rules.tunnel_id не действует и удаление туннеля тихо ломает маршрутизацию.
func dsn(path string) string {
	return "file:" + path + "?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
}

// prepareFile создаёт каталог и файл БД с нужными правами до того, как за них возьмётся
// SQLite: файлы -wal и -shm он создаёт с правами основного файла.
func prepareFile(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, dbDirMode); err != nil {
			return fmt.Errorf("создание каталога %s: %w", dir, err)
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, dbFileMode)
	if err != nil {
		return fmt.Errorf("создание файла БД %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("создание файла БД %s: %w", path, err)
	}
	// Файл мог остаться от прежней версии с более широкими правами.
	if err := os.Chmod(path, dbFileMode); err != nil {
		return fmt.Errorf("права на файл БД %s: %w", path, err)
	}
	return nil
}

// tx выполняет fn в транзакции: коммит при nil-ошибке, откат при любой другой.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("начало транзакции: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("фиксация транзакции: %w", err)
	}
	return nil
}
