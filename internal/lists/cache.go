package lists

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// meta — что известно о закэшированном списке. Лежит рядом с телом отдельным
// файлом: валидаторы условного запроса нужны до того, как тело прочитано.
type meta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
	Size         int64     `json:"size"`
}

// cache — каталог с телами списков и их метаданными.
//
// Имя файла — хэш адреса: в URL встречаются символы, недопустимые в имени
// файла, а разные списки одного сервиса должны лежать раздельно.
type cache struct {
	dir string
}

// openCache создаёт каталог кэша. Права 0700: каталог лежит рядом с БД, где
// приватные ключи пиров, и незачем расширять доступ на весь каталог состояния.
func openCache(dir string) (*cache, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: не задан каталог кэша списков", ErrBadResponse)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("создание каталога кэша списков %s: %w", dir, err)
	}
	return &cache{dir: dir}, nil
}

// key — имя файлов списка без расширения.
func (c *cache) key(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:16])
}

func (c *cache) bodyPath(rawURL string) string {
	return filepath.Join(c.dir, c.key(rawURL)+".body")
}

func (c *cache) metaPath(rawURL string) string {
	return filepath.Join(c.dir, c.key(rawURL)+".meta.json")
}

// meta читает метаданные. Второе значение false, если метаданных нет или рядом
// нет тела: без тела условный запрос бессмыслен — на 304 нечего будет отдать.
func (c *cache) meta(rawURL string) (meta, bool) {
	raw, err := os.ReadFile(c.metaPath(rawURL))
	if err != nil {
		return meta{}, false
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return meta{}, false
	}
	st, err := os.Stat(c.bodyPath(rawURL))
	if err != nil || st.Size() == 0 {
		return meta{}, false
	}
	return m, true
}

// read отдаёт закэшированное тело.
func (c *cache) read(rawURL string) ([]byte, error) {
	raw, err := os.ReadFile(c.bodyPath(rawURL))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotCached, rawURL)
		}
		return nil, fmt.Errorf("чтение кэша списка %s: %w", rawURL, err)
	}
	return raw, nil
}

// store кладёт тело и метаданные в кэш. Тело пишется во временный файл рядом и
// переименовывается: прерванная запись не оставляет обрезанного списка на месте
// валидного, потому что rename в пределах каталога атомарен.
func (c *cache) store(rawURL string, body []byte, m meta) error {
	if len(body) == 0 {
		return fmt.Errorf("%w: пустое тело списка %s", ErrBadResponse, rawURL)
	}
	if err := c.writeAtomic(c.bodyPath(rawURL), body); err != nil {
		return fmt.Errorf("запись кэша списка %s: %w", rawURL, err)
	}
	return c.writeMeta(rawURL, m)
}

// writeMeta обновляет метаданные, не трогая тело: после 304 меняется только
// время последней проверки.
func (c *cache) writeMeta(rawURL string, m meta) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("сериализация метаданных списка %s: %w", rawURL, err)
	}
	if err := c.writeAtomic(c.metaPath(rawURL), raw); err != nil {
		return fmt.Errorf("запись метаданных списка %s: %w", rawURL, err)
	}
	return nil
}

// writeAtomic пишет файл через временный в том же каталоге.
func (c *cache) writeAtomic(dst string, data []byte) error {
	tmp, err := os.CreateTemp(c.dir, filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("создание временного файла: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("запись во временный файл: %w", err)
	}
	// Sync до rename: без него после отключения питания на месте валидного
	// списка окажется файл нулевой длины.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("сброс временного файла на диск: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("закрытие временного файла: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("права на временный файл: %w", err)
	}
	if err := os.Rename(name, dst); err != nil {
		return fmt.Errorf("переименование в %s: %w", dst, err)
	}
	return nil
}
