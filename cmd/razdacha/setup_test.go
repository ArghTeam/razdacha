package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), store.MemoryPath)
	if err != nil {
		t.Fatalf("открытие БД: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestIsFresh — признак первой установки это наличие state.db, и ничего больше
// (docs/08-install-upgrade.md, «Идемпотентность»).
func TestIsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	fresh, err := isFresh(path)
	if err != nil || !fresh {
		t.Fatalf("пустой каталог: fresh=%v err=%v", fresh, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("подготовка БД: %v", err)
	}
	fresh, err = isFresh(path)
	if err != nil || fresh {
		t.Fatalf("с существующей БД: fresh=%v err=%v", fresh, err)
	}
}

// TestEnsurePasswordOnce — пароль генерируется при первой установке и не
// меняется при повторной: повторный запуск install.sh это обновление, а не
// сброс доступа к панели.
func TestEnsurePasswordOnce(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	log := slog.Default()

	password, err := ensurePassword(ctx, st, log)
	if err != nil {
		t.Fatalf("генерация пароля: %v", err)
	}
	if len(password) != passwordLength {
		t.Fatalf("длина пароля %d, ожидалась %d", len(password), passwordLength)
	}
	// Пароль обязан подходить к сохранённому хешу, иначе в панель не войти.
	hash, err := st.PasswordHash(ctx)
	if err != nil {
		t.Fatalf("чтение хеша: %v", err)
	}
	ok, err := api.VerifyPassword(hash, password)
	if err != nil || !ok {
		t.Fatalf("сгенерированный пароль не подходит к хешу: ok=%v err=%v", ok, err)
	}

	again, err := ensurePassword(ctx, st, log)
	if err != nil {
		t.Fatalf("повторный вызов: %v", err)
	}
	if again != "" {
		t.Fatalf("пароль перегенерирован на повторном запуске: %q", again)
	}
	newHash, err := st.PasswordHash(ctx)
	if err != nil || newHash != hash {
		t.Fatalf("хеш пароля переписан: %v", err)
	}
}

// TestGeneratePasswordAlphabet — в пароле нет символов, которые в терминале не
// отличить друг от друга: его переписывают руками.
func TestGeneratePasswordAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := generatePassword()
		if err != nil {
			t.Fatalf("генерация: %v", err)
		}
		if strings.ContainsAny(p, "0Ol1I") {
			t.Fatalf("в пароле %q неразличимые символы", p)
		}
		if seen[p] {
			t.Fatalf("пароль %q сгенерирован дважды", p)
		}
		seen[p] = true
	}
}

// TestEnsureFirstPeerOnce — пир создаётся один раз. На обновлении пиры
// сохраняются, и второй «client-1» появиться не должен.
func TestEnsureFirstPeerOnce(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	log := slog.Default()

	peer, created, err := ensureFirstPeer(ctx, st, "", log)
	if err != nil || !created {
		t.Fatalf("первый пир: created=%v err=%v", created, err)
	}
	if peer.Name != firstPeerName {
		t.Fatalf("имя пира %q, ожидалось %q", peer.Name, firstPeerName)
	}
	if peer.PrivateKey == "" || peer.PublicKey == "" || peer.Address == "" {
		t.Fatalf("пир заведён без ключей или адреса: %+v", peer)
	}

	_, created, err = ensureFirstPeer(ctx, st, "", log)
	if err != nil || created {
		t.Fatalf("повторный вызов создал пира: created=%v err=%v", created, err)
	}
	peers, err := st.Peers(ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("пиров %d, ожидался один: %v", len(peers), err)
	}
}

// TestSavePeerConfig — конфиг с приватным ключом кладётся только для root.
func TestSavePeerConfig(t *testing.T) {
	root := t.TempDir()
	path, err := savePeerConfig(root, "Ноутбук Ромы", sampleConfig)
	if err != nil {
		t.Fatalf("запись конфига: %v", err)
	}
	if want := filepath.Join(root, defaultStateDir, "peer.conf"); path != want {
		t.Fatalf("путь %q, ожидался %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("чтение конфига: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("права %v, ожидались 0600: в файле приватный ключ", info.Mode().Perm())
	}
}

// TestConfFileName — имя пира приходит из hostname клиента, и в нём бывает что
// угодно вплоть до слэшей.
func TestConfFileName(t *testing.T) {
	cases := map[string]string{
		"client-1":      "client-1.conf",
		"Ноутбук":       "peer.conf",
		"iPhone 15 Pro": "iphone-15-pro.conf",
		"../../etc/x":   "etc-x.conf",
		"":              "peer.conf",
	}
	for in, want := range cases {
		if got := confFileName(in); got != want {
			t.Fatalf("имя %q дало файл %q, ожидался %q", in, got, want)
		}
	}
}

// TestBackupDB — копия снимается рядом с состоянием и с теми же правами: в ней
// те же приватные ключи пиров.
func TestBackupDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	if err := os.WriteFile(dbPath, []byte("состояние"), 0o600); err != nil {
		t.Fatalf("подготовка БД: %v", err)
	}

	path, err := backupDB(dbPath, "1.2.3")
	if err != nil {
		t.Fatalf("резервная копия: %v", err)
	}
	if got := filepath.Dir(path); got != filepath.Join(dir, backupDir) {
		t.Fatalf("копия легла в %q", got)
	}
	if !strings.HasPrefix(filepath.Base(path), "state-1.2.3-") {
		t.Fatalf("имя копии %q не содержит версии", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "состояние" {
		t.Fatalf("содержимое копии: %q %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("права копии %v: там приватные ключи", info.Mode().Perm())
	}
}

// TestBackupDBUnknownVersion — БД от сборки, которая свою версию не писала,
// копируется под именем `unknown`: соврать в имени файла отката нельзя.
func TestBackupDBUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	if err := os.WriteFile(dbPath, []byte("состояние"), 0o600); err != nil {
		t.Fatalf("подготовка БД: %v", err)
	}

	path, err := backupDB(dbPath, "")
	if err != nil {
		t.Fatalf("резервная копия: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "state-"+unknownVersion+"-") {
		t.Fatalf("имя копии %q", filepath.Base(path))
	}
}

// TestBackupNamesSavedVersion — копия называется версией, состояние которой в
// ней лежит, а не той, на которую обновляемся: она и есть путь отката (#82).
func TestBackupNamesSavedVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("открытие БД: %v", err)
	}
	if err := st.SetInstalledVersion(ctx, "0.1.1"); err != nil {
		t.Fatalf("запись версии: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("закрытие БД: %v", err)
	}

	prev, err := store.InstalledVersionAt(ctx, dbPath)
	if err != nil {
		t.Fatalf("чтение версии установки: %v", err)
	}
	path, err := backupDB(dbPath, prev)
	if err != nil {
		t.Fatalf("резервная копия: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "state-0.1.1-") {
		t.Fatalf("имя копии %q: ожидалась версия, сохранённая в копии", filepath.Base(path))
	}
}

// TestEnsurePanelMode — приоритет режима панели: флаг задаёт и запоминает, без
// флага берётся сохранённое, первая установка без флага — приватный режим.
func TestEnsurePanelMode(t *testing.T) {
	ctx := context.Background()
	log := slog.Default()

	cases := []struct {
		name        string
		saved       bool
		opts        setupOptions
		wantPublic  bool
		wantChanged bool
	}{
		{name: "первая установка без флага"},
		{
			name:        "первая установка с флагом",
			opts:        setupOptions{public: true, publicSet: true},
			wantPublic:  true,
			wantChanged: true,
		},
		{
			name:       "обновление без флага сохраняет режим",
			saved:      true,
			wantPublic: true,
		},
		{
			name:       "обновление с тем же флагом ничего не меняет",
			saved:      true,
			opts:       setupOptions{public: true, publicSet: true},
			wantPublic: true,
		},
		{
			name:        "флаг выключает сохранённый режим",
			saved:       true,
			opts:        setupOptions{publicSet: true},
			wantChanged: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := testStore(t)
			if c.saved {
				if err := st.SetPanelPublic(ctx, true); err != nil {
					t.Fatalf("подготовка режима: %v", err)
				}
			}

			public, changed, err := ensurePanelMode(ctx, st, c.opts, log)
			if err != nil {
				t.Fatalf("ensurePanelMode: %v", err)
			}
			if public != c.wantPublic || changed != c.wantChanged {
				t.Fatalf("режим public=%v changed=%v, ожидались %v/%v",
					public, changed, c.wantPublic, c.wantChanged)
			}
			// Решение обязано пережить перезапуск: ради этого оно и в БД.
			saved, err := st.PanelPublic(ctx)
			if err != nil {
				t.Fatalf("чтение режима: %v", err)
			}
			if saved != c.wantPublic {
				t.Fatalf("в БД режим %v, ожидался %v", saved, c.wantPublic)
			}
		})
	}
}

// TestParseSetupFlagsPublic — незаданный флаг и явный `-public=false` решают
// разное: «оставь как было» против «выключи».
func TestParseSetupFlagsPublic(t *testing.T) {
	cases := []struct {
		args      []string
		public    bool
		publicSet bool
	}{
		{args: nil},
		{args: []string{"-public"}, public: true, publicSet: true},
		{args: []string{"-public=true"}, public: true, publicSet: true},
		{args: []string{"-public=false"}, publicSet: true},
	}
	for _, c := range cases {
		opts, err := parseSetupFlags(c.args)
		if err != nil {
			t.Fatalf("разбор %v: %v", c.args, err)
		}
		if opts.public != c.public || opts.publicSet != c.publicSet {
			t.Fatalf("%v дало public=%v publicSet=%v, ожидались %v/%v",
				c.args, opts.public, opts.publicSet, c.public, c.publicSet)
		}
	}
}

// TestStartServicesRestartsUnits — обновление обязано перезапускать юниты.
// `enable --now` на работающем юните не делает ничего, и после обновления в
// памяти оставался старый процесс, а установщик печатал «сервисы запущены» (#80).
func TestStartServicesRestartsUnits(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, args ...string) error {
		calls = append(calls, args)
		return nil
	}
	if err := startServices(context.Background(), run, slog.Default()); err != nil {
		t.Fatalf("startServices: %v", err)
	}

	var got []string
	for _, call := range calls {
		got = append(got, strings.Join(call, " "))
		for _, arg := range call {
			if arg == "--now" {
				t.Fatalf("вызов %q: --now не перезапускает уже работающий юнит", got[len(got)-1])
			}
		}
	}
	want := []string{
		"daemon-reload",
		"enable " + packaging.SingboxUnit,
		"restart " + packaging.SingboxUnit,
		"enable " + packaging.DaemonUnit,
		"restart " + packaging.DaemonUnit,
		"enable " + nginxUnit,
		"restart " + nginxUnit,
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("вызовы systemctl:\n%v\nожидались:\n%v", got, want)
	}
}

// TestStartServicesStopsOnError — неудача одного юнита не должна тянуть за собой
// остальные: следом идёт nginx, а поднимать его поверх неподнявшегося демона
// незачем.
func TestStartServicesStopsOnError(t *testing.T) {
	calls := 0
	run := func(_ context.Context, args ...string) error {
		calls++
		if args[0] == "restart" && args[1] == packaging.SingboxUnit {
			return errors.New("юнит не поднялся")
		}
		return nil
	}
	if err := startServices(context.Background(), run, slog.Default()); err == nil {
		t.Fatal("ошибка запуска не вернулась")
	}
	if calls != 3 {
		t.Fatalf("вызовов %d, ожидалось 3: остановиться надо на первой ошибке", calls)
	}
}

// TestPanelURL — адрес панели изнутри VPN.
func TestPanelURL(t *testing.T) {
	if got := panelURL("10.8.0.1"); got != "https://10.8.0.1" {
		t.Fatalf("адрес панели %q", got)
	}
}
