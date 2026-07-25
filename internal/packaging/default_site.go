package packaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// DefaultSiteName — штатный сайт Debian, который включает `apt install nginx`.
	DefaultSiteName = "default"

	// defaultSiteStateFile — отметка о том, что дефолтный сайт выключили мы.
	// Отметка нужна явная: по одному лишь отсутствию symlink не отличить
	// «мы сняли» от «его никогда не было» или «пользователь снял сам», а
	// возвращать при удалении чужое состояние нельзя.
	defaultSiteStateFile = "nginx-default-disabled.json"
)

// defaultSiteState — что именно мы сняли, чтобы вернуть это же.
type defaultSiteState struct {
	Link   string `json:"link"`
	Target string `json:"target"`
}

// readSymlink отдаёт цель symlink. false — если по пути ничего нет или это не
// symlink: для нас обе причины значат одно — снимать нечего.
func readSymlink(path string) (string, bool) {
	dst, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	return dst, true
}

func (i *Installer) defaultLinkPath() string {
	return filepath.Join(i.Root, i.NginxDir, "sites-enabled", DefaultSiteName)
}

func (i *Installer) defaultStatePath() string {
	return filepath.Join(i.Root, i.StateDir, defaultSiteStateFile)
}

// isDebianDefaultSite отвечает, ведёт ли symlink на штатный файл Debian
// `sites-available/default`. Ссылка может быть абсолютной (так делает пакет)
// или относительной, поэтому разбираем оба случая.
func (i *Installer) isDebianDefaultSite(link, dst string) bool {
	rooted := filepath.Join(i.Root, i.NginxDir, "sites-available", DefaultSiteName)
	if !filepath.IsAbs(dst) {
		return filepath.Join(filepath.Dir(link), dst) == rooted
	}
	// При пустом Root оба варианта совпадают; при подменённом корне пакетный
	// путь остаётся без префикса.
	unrooted := filepath.Join(i.NginxDir, "sites-available", DefaultSiteName)
	return dst == rooted || dst == unrooted
}

// disableDefaultSite снимает штатный сайт Debian.
//
// Без этого панель торчит наружу мимо нашего конфига: `apt install nginx`
// включает sites-enabled/default с `listen 80 default_server` и
// `listen [::]:80 default_server`, то есть nginx слушает 0.0.0.0:80 и [::]:80
// на публичном интерфейсе. Наш собственный файл при этом безупречен —
// дыру создаёт чужой, и юнит-тест на рендеринг её увидеть не может.
//
// Symlink, ведущий куда-то ещё, не трогается: файл с таким именем мог
// сделать пользователь.
func (i *Installer) disableDefaultSite() (bool, error) {
	link := i.defaultLinkPath()

	dst, ok := readSymlink(link)
	if !ok {
		// Ничего нет — снимать нечего; лежит не symlink — это чужая настройка.
		if _, err := os.Lstat(link); err == nil {
			i.log().Warn("по пути дефолтного сайта nginx лежит не symlink, оставлено", "путь", link)
		}
		return false, nil
	}

	if !i.isDebianDefaultSite(link, dst) {
		i.log().Warn("symlink дефолтного сайта ведёт не на штатный файл Debian, оставлен",
			"ссылка", link, "цель", dst)
		return false, nil
	}

	// Отметка пишется до снятия: если процесс прервётся между двумя
	// операциями, лишняя отметка безобиднее потерянной — по ней сайт
	// вернётся, а без неё останется выключенным навсегда.
	if err := i.saveDefaultSiteState(defaultSiteState{Link: link, Target: dst}); err != nil {
		return false, err
	}
	if err := os.Remove(link); err != nil {
		return false, fmt.Errorf("снятие дефолтного сайта nginx %s: %w", link, err)
	}
	i.log().Info("дефолтный сайт nginx выключен: он слушал 0.0.0.0:80", "ссылка", link)
	return true, nil
}

func (i *Installer) saveDefaultSiteState(st defaultSiteState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("сериализация отметки о дефолтном сайте: %w", err)
	}
	dir := filepath.Join(i.Root, i.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("создание каталога состояния %s: %w", dir, err)
	}
	if err := writeFile(i.defaultStatePath(), append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("запись отметки о дефолтном сайте: %w", err)
	}
	return nil
}

// restoreDefaultSite возвращает штатный сайт Debian, но только если снимали
// его мы — то есть если на диске лежит наша отметка.
func (i *Installer) restoreDefaultSite() error {
	path := i.defaultStatePath()
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Мы его не снимали — возвращать нечего.
		return nil
	case err != nil:
		return fmt.Errorf("чтение отметки о дефолтном сайте %s: %w", path, err)
	}

	var st defaultSiteState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("разбор отметки о дефолтном сайте %s: %w", path, err)
	}

	if _, err := os.Lstat(st.Link); err == nil {
		i.log().Warn("на месте дефолтного сайта уже что-то есть, восстановление пропущено",
			"ссылка", st.Link)
	} else if err := os.Symlink(st.Target, st.Link); err != nil {
		return fmt.Errorf("восстановление дефолтного сайта nginx %s: %w", st.Link, err)
	} else {
		i.log().Info("дефолтный сайт nginx возвращён", "ссылка", st.Link, "цель", st.Target)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("удаление отметки о дефолтном сайте %s: %w", path, err)
	}
	return nil
}
