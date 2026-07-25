package singbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeChecker — подмена `sing-box check`: тестам не нужен установленный рантайм.
type fakeChecker struct {
	err   error
	calls int
	// seen — содержимое файла, который проверяли: проверять обязаны ровно тот
	// файл, который станет конфигом.
	seen string
}

func (c *fakeChecker) Check(_ context.Context, path string) error {
	c.calls++
	if b, err := os.ReadFile(path); err == nil {
		c.seen = string(b)
	}
	return c.err
}

// fakeReloader — подмена `systemctl reload`.
type fakeReloader struct {
	err   error
	calls int
}

func (r *fakeReloader) Reload(context.Context) error {
	r.calls++
	return r.err
}

// applier собирает применятель поверх временного каталога: в реальный
// /etc/sing-box не пишет ни один тест.
func applier(t *testing.T, c Checker, r Reloader) (*Applier, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sing-box", "config.json")
	return &Applier{
		ConfigPath: path,
		Checker:    c,
		Reloader:   r,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, path
}

func TestApplyWritesConfig(t *testing.T) {
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, path := applier(t, check, reload)

	res, err := a.Apply(context.Background(), fixture())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Changed || !res.Reloaded {
		t.Fatalf("первое применение: changed=%v reloaded=%v, ожидалось true/true",
			res.Changed, res.Reloaded)
	}
	if res.Path != path {
		t.Fatalf("путь %q, ожидался %q", res.Path, path)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("конфиг не записан: %v", err)
	}
	want := generate(t, fixture())
	if string(got) != string(want) {
		t.Fatal("на диске лежит не то, что генерирует Generate")
	}
	if check.seen != string(want) {
		t.Fatal("check вызван не на том файле, который стал конфигом")
	}
	if check.calls != 1 || reload.calls != 1 {
		t.Fatalf("check=%d reload=%d, ожидалось 1/1", check.calls, reload.calls)
	}

	// Ключи туннелей лежат в конфиге, читать его посторонним нечего.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("права %v, ожидались 0600", st.Mode().Perm())
	}
}

func TestApplyUnchangedDoesNotReload(t *testing.T) {
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, path := applier(t, check, reload)

	if _, err := a.Apply(context.Background(), fixture()); err != nil {
		t.Fatalf("первое Apply: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	res, err := a.Apply(context.Background(), fixture())
	if err != nil {
		t.Fatalf("второе Apply: %v", err)
	}
	if res.Changed || res.Reloaded {
		t.Fatalf("неизменившийся конфиг: changed=%v reloaded=%v, ожидалось false/false",
			res.Changed, res.Reloaded)
	}
	if reload.calls != 1 {
		t.Fatalf("reload вызван %d раз, ожидался 1 (только первое применение)", reload.calls)
	}
	if check.calls != 1 {
		t.Fatalf("check вызван %d раз: проверять нечего, файл тот же", check.calls)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("файл переписан, хотя конфиг не менялся")
	}
}

func TestApplyKeepsOldConfigOnCheckFailure(t *testing.T) {
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, path := applier(t, check, reload)

	if _, err := a.Apply(context.Background(), fixture()); err != nil {
		t.Fatalf("первое Apply: %v", err)
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Второе состояние отличается — значит, файл будет переписываться.
	snap := fixture()
	snap.Rules[0].Domains = append(snap.Rules[0].Domains, "example.invalid")
	check.err = wrapCheck("route rule 0: unknown outbound")

	res, applyErr := a.Apply(context.Background(), snap)
	if !errors.Is(applyErr, ErrCheckFailed) {
		t.Fatalf("ошибка %v, ожидалась ErrCheckFailed", applyErr)
	}
	if res.Changed || res.Reloaded {
		t.Fatalf("отказ проверки: changed=%v reloaded=%v, ожидалось false/false",
			res.Changed, res.Reloaded)
	}
	if reload.calls != 1 {
		t.Fatalf("reload после отказа проверки вызван %d раз, ожидался 1 (от первого применения)",
			reload.calls)
	}

	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("рабочий конфиг пропал: %v", err)
	}
	if string(now) != string(good) {
		t.Fatal("битый конфиг затёр рабочий")
	}
	if !leftoversGone(t, filepath.Dir(path)) {
		t.Fatal("после отказа проверки остался временный файл")
	}
	// Текст рантайма доходит до вызывающего как есть — его показывают в UI.
	if want := "unknown outbound"; !strings.Contains(applyErr.Error(), want) {
		t.Fatalf("в тексте ошибки %q нет причины %q", applyErr.Error(), want)
	}
}

func TestApplyReloadFailure(t *testing.T) {
	check := &fakeChecker{}
	reload := &fakeReloader{err: ErrReloadFailed}
	a, path := applier(t, check, reload)

	res, err := a.Apply(context.Background(), fixture())
	if !errors.Is(err, ErrReloadFailed) {
		t.Fatalf("ошибка %v, ожидалась ErrReloadFailed", err)
	}
	// Конфиг валиден и уже на диске: следующий старт сервиса его подхватит.
	if !res.Changed || res.Reloaded {
		t.Fatalf("changed=%v reloaded=%v, ожидалось true/false", res.Changed, res.Reloaded)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("конфиг не записан: %v", err)
	}
}

func TestApplyGenerateError(t *testing.T) {
	check, reload := &fakeChecker{}, &fakeReloader{}
	a, path := applier(t, check, reload)

	snap := fixture()
	snap.Rules[0].TunnelID = "нет такого"

	if _, err := a.Apply(context.Background(), snap); err == nil {
		t.Fatal("ссылка на несуществующий туннель принята")
	}
	if check.calls != 0 || reload.calls != 0 {
		t.Fatalf("check=%d reload=%d, ожидалось 0/0: до диска не дошли",
			check.calls, reload.calls)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("файл создан при ошибке генерации")
	}
}

func TestApplyDefaults(t *testing.T) {
	a := NewApplier()
	if a.path() != DefaultConfigPath {
		t.Fatalf("путь по умолчанию %q", a.path())
	}
	empty := &Applier{}
	if empty.path() != DefaultConfigPath {
		t.Fatal("пустой ConfigPath должен означать DefaultConfigPath")
	}
	if _, ok := empty.checker().(BinaryChecker); !ok {
		t.Fatal("пустой Checker должен означать вызов рантайма")
	}
	if _, ok := empty.reloader().(SystemctlReloader); !ok {
		t.Fatal("пустой Reloader должен означать systemctl")
	}
	if empty.log() == nil {
		t.Fatal("логгер по умолчанию не собран")
	}
}

// wrapCheck собирает ошибку в том же виде, в каком её отдаёт BinaryChecker.
func wrapCheck(detail string) error {
	return errors.Join(ErrCheckFailed, errors.New(detail))
}

// leftoversGone проверяет, что кроме конфига в каталоге ничего не осталось.
func leftoversGone(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			return false
		}
	}
	return true
}
