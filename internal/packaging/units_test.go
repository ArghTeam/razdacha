package packaging

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newUnitInstaller(t *testing.T) *UnitInstaller {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DefaultUnitDir), 0o755); err != nil {
		t.Fatalf("подготовка каталога юнитов: %v", err)
	}
	// Пакетные каталоги существуют, но пусты: так выглядит машина без пакета
	// sing-box.
	return &UnitInstaller{Root: root, PackagedDirs: []string{"/lib/systemd/system"}}
}

// TestDaemonUnitContent — в юните демона обязаны быть Type=notify и
// /etc/sing-box в ReadWritePaths: без первого установщик печатает «готово» до
// того, как поднят wg0, без второго применение конфига падает по правам.
func TestDaemonUnitContent(t *testing.T) {
	out, err := RenderDaemonUnit("/usr/local/bin/razdachad")
	if err != nil {
		t.Fatalf("генерация юнита: %v", err)
	}
	if !strings.HasPrefix(out, marker) {
		t.Fatal("юнит не начинается с маркера, свой файл не отличить от чужого")
	}
	for _, want := range []string{
		"Type=notify",
		"ExecStart=/usr/local/bin/razdachad\n",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/lib/razdacha /etc/razdacha /etc/sing-box",
		"CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("в юните нет %q:\n%s", want, out)
		}
	}
}

// TestSingboxUnitNoReload — ExecReload в юните быть не должно: `reload-or-restart`
// при его наличии сделал бы reload, а конфиг генерируется целиком, и кэш FakeIP
// обязан обнулиться вместе с ним.
func TestSingboxUnitNoReload(t *testing.T) {
	out, err := RenderSingboxUnit("/usr/local/bin/sing-box", "/etc/sing-box/config.json")
	if err != nil {
		t.Fatalf("генерация юнита: %v", err)
	}
	if strings.Contains(out, "ExecReload") {
		t.Fatalf("в юните sing-box оказался ExecReload:\n%s", out)
	}
	if !strings.Contains(out, "ExecStart=/usr/local/bin/sing-box -D /var/lib/sing-box run -c /etc/sing-box/config.json") {
		t.Fatalf("ExecStart собран не так:\n%s", out)
	}
}

// TestUnitPathValidation — путь с пробелом разорвал бы ExecStart на аргументы.
func TestUnitPathValidation(t *testing.T) {
	for _, bad := range []string{"", "razdachad", "/usr/local/bin/razdacha d", "/usr/bin/x\ny"} {
		if _, err := RenderDaemonUnit(bad); !errors.Is(err, ErrBadConfig) {
			t.Fatalf("путь %q принят: %v", bad, err)
		}
	}
}

// TestEnsureUnitsIdempotent — повторная установка не переписывает файл.
func TestEnsureUnitsIdempotent(t *testing.T) {
	u := newUnitInstaller(t)

	written, err := u.EnsureDaemonUnit("/usr/local/bin/razdachad")
	if err != nil || !written {
		t.Fatalf("первая установка: written=%v err=%v", written, err)
	}
	written, err = u.EnsureDaemonUnit("/usr/local/bin/razdachad")
	if err != nil || written {
		t.Fatalf("повторная установка переписала юнит: written=%v err=%v", written, err)
	}

	// Смена пути к бинарнику — уже изменение, файл обязан обновиться.
	written, err = u.EnsureDaemonUnit("/usr/bin/razdachad")
	if err != nil || !written {
		t.Fatalf("смена пути не переписала юнит: written=%v err=%v", written, err)
	}
}

// TestEnsureUnitForeign — чужой юнит с тем же именем не перезаписывается.
func TestEnsureUnitForeign(t *testing.T) {
	u := newUnitInstaller(t)
	path := u.Path(DaemonUnit)
	if err := os.WriteFile(path, []byte("[Unit]\nDescription=чужой\n"), 0o644); err != nil {
		t.Fatalf("подготовка чужого юнита: %v", err)
	}
	if _, err := u.EnsureDaemonUnit("/usr/local/bin/razdachad"); !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("чужой юнит перезаписан: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "чужой") {
		t.Fatalf("чужой юнит изменён: %q %v", content, err)
	}
}

// TestEnsureSingboxUnitPackaged — пакетный юнит не подменяется: его обновляет
// пакетный менеджер, а наш файл перекрыл бы его навсегда.
func TestEnsureSingboxUnitPackaged(t *testing.T) {
	u := newUnitInstaller(t)
	packaged := filepath.Join(u.Root, "/lib/systemd/system", SingboxUnit)
	if err := os.MkdirAll(filepath.Dir(packaged), 0o755); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}
	if err := os.WriteFile(packaged, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("подготовка пакетного юнита: %v", err)
	}

	written, err := u.EnsureSingboxUnit("/usr/bin/sing-box", "/etc/sing-box/config.json")
	if !errors.Is(err, ErrUnitPackaged) {
		t.Fatalf("ожидалась ErrUnitPackaged, получено: %v", err)
	}
	if written {
		t.Fatal("при пакетном юните файл всё равно записан")
	}
	if _, err := os.Stat(u.Path(SingboxUnit)); err == nil {
		t.Fatal("свой юнит sing-box записан поверх пакетного")
	}
}

// TestRemoveUnits — снимаются только наши файлы, отсутствие файла не ошибка.
func TestRemoveUnits(t *testing.T) {
	u := newUnitInstaller(t)
	if _, err := u.EnsureDaemonUnit("/usr/local/bin/razdachad"); err != nil {
		t.Fatalf("установка юнита демона: %v", err)
	}
	foreign := u.Path(SingboxUnit)
	if err := os.WriteFile(foreign, []byte("[Unit]\nDescription=чужой\n"), 0o644); err != nil {
		t.Fatalf("подготовка чужого юнита: %v", err)
	}

	removed, err := u.RemoveUnits()
	if err != nil {
		t.Fatalf("снятие юнитов: %v", err)
	}
	if len(removed) != 1 || removed[0] != u.Path(DaemonUnit) {
		t.Fatalf("снято не то: %v", removed)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("чужой юнит удалён: %v", err)
	}

	removed, err = u.RemoveUnits()
	if err != nil || len(removed) != 0 {
		t.Fatalf("повторное снятие: %v %v", removed, err)
	}
}

func TestIngressGuardDropInSupportsPackagedRuntime(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root, PackagedDirs: []string{"/usr/lib/systemd/system"}}
	vendor := filepath.Join(root, "usr/lib/systemd/system/sing-box.service")
	if err := os.MkdirAll(filepath.Dir(vendor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vendor, []byte("[Service]\nExecStart=/usr/bin/sing-box -C /unowned/config-directory -c /unowned/alternate.json run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(u.Path(SingboxUnit + ".d/50-razdacha-ingress.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ExecStartPre=+/usr/local/bin/razdachad --guard-ingress") || !strings.Contains(string(data), "--guard-manifest /var/lib/razdacha/public-ingress.json") {
		t.Fatal("missing independent pre-start hook")
	}
	unchanged, err := os.ReadFile(vendor)
	if err != nil || string(unchanged) != "[Service]\nExecStart=/usr/bin/sing-box -C /unowned/config-directory -c /unowned/alternate.json run\n" {
		t.Fatal("vendor unit was modified")
	}
	if !strings.Contains(string(data), "ExecStart=\nExecStart=/usr/bin/sing-box -D /var/lib/sing-box run -c /etc/sing-box/config.json\n") {
		t.Fatal("vendor directory/alternate config invocation was not neutralized")
	}
	if strings.Count(string(data), "ExecStart=") != 2 || strings.Count(string(data), " run -c ") != 1 {
		t.Fatal("runtime invocation is not one explicit managed command")
	}
	if strings.Contains(string(data), " -C ") {
		t.Fatal("managed runtime can load unapproved directory configs")
	}
	if changed, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil || changed {
		t.Fatal("managed drop-in is not idempotent", err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/bad runtime", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrBadConfig) {
		t.Fatal("unsafe runtime path accepted")
	}
	if err := os.WriteFile(u.Path(SingboxUnit+".d/50-razdacha-ingress.conf"), []byte("foreign fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
		t.Fatal("foreign drop-in overwritten", err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/bad path", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrBadConfig) {
		t.Fatal("unsafe unit argument accepted")
	}
}

func TestManagedIngressRejectsConflictingLaterDropInsBeforeWriting(t *testing.T) {
	for _, scope := range []string{"sing-box.service.d", "sing-.service.d", "service.d"} {
		for _, directive := range []string{"ExecStart=", "ExecStart=/foreign/runtime -C /foreign/config run", "ExecStartPre=", "ExecStartPre=/foreign/mutate-config", "ExecReload=/foreign/reload"} {
			t.Run(scope+"/"+directive, func(t *testing.T) {
				root := t.TempDir()
				u := &UnitInstaller{Root: root}
				path := filepath.Join(root, "etc/systemd/system", scope, "90-local.conf")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				body := "[Service]\n" + directive + "\n"
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
					t.Fatal("conflicting override allowed", err)
				}
				if _, err := os.Stat(u.Path(SingboxUnit + ".d/50-razdacha-ingress.conf")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("preflight wrote managed drop-in")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != body {
					t.Fatal("preflight changed foreign override")
				}
			})
		}
	}
}

func TestManagedIngressPreservesHarmlessLaterDropIns(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	path := u.Path(SingboxUnit + ".d/90-limits.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# ExecStart=/comment\n[Unit]\nDescription=ExecStartPre=not-a-service-directive\n[Service]\nLimitNOFILE=65536\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil {
		t.Fatal("harmless drop-in blocked", err)
	}
}

func TestManagedIngressDetectsLogicalLinesAndHigherPrioritySameName(t *testing.T) {
	for _, name := range []string{"90-local.conf", "50-razdacha-ingress.conf"} {
		for _, body := range []string{
			"[Service]\nExecStart \\\n =/foreign/runtime -C /unowned/config run\n",
			"[Service]\nExecStartPre \\\n# intervening comment\n =\n",
		} {
			t.Run(name+"/"+body, func(t *testing.T) {
				root := t.TempDir()
				u := &UnitInstaller{Root: root}
				path := filepath.Join(root, "run/systemd/system.control/sing-box.service.d", name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
					t.Fatal("logical-line or same-name override bypassed managed source", err)
				}
				if _, err := os.Stat(u.Path(SingboxUnit + ".d/50-razdacha-ingress.conf")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("preflight wrote managed drop-in")
				}
			})
		}
	}
}

func TestManagedIngressRejectsRemappedAuthorityInBaseAndEveryDropIn(t *testing.T) {
	directives := []string{"RootDirectory", "RootImage", "RootMStack", "BindPaths", "BindReadOnlyPaths", "TemporaryFileSystem", "MountImages", "ExtensionImages", "ExtensionDirectories"}
	for _, location := range []string{
		"usr/lib/systemd/system/sing-box.service",
		"etc/systemd/system/sing-box.service",
		"etc/systemd/system/sing-box.service.d/10-early.conf",
		"run/systemd/system/sing-box.service.d/90-local.conf",
		"usr/lib/systemd/system/service.d/10-vendor.conf",
		"etc/systemd/system/sing-.service.d/10-prefix.conf",
	} {
		for _, directive := range directives {
			t.Run(location+"/"+directive, func(t *testing.T) {
				root := t.TempDir()
				u := &UnitInstaller{Root: root}
				path := filepath.Join(root, location)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				body := "[Service]\n" + directive + "=/alternate/config:/etc/sing-box/config.json /alternate/manifest:/var/lib/razdacha/public-ingress.json\n"
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
					t.Fatal("remapped authority accepted", err)
				}
				if _, err := os.Stat(u.Path(SingboxUnit + ".d/50-razdacha-ingress.conf")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("preflight wrote managed drop-in")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != body {
					t.Fatal("preflight changed foreign unit")
				}
			})
		}
	}
}

func TestManagedIngressPreservesHostViewReadOnlyHardeningAndEmptyRemaps(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	path := filepath.Join(root, "usr/lib/systemd/system/sing-box.service")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[Service]\nExecStart=/vendor/runtime -C /vendor/config run\nProtectSystem=strict\nReadOnlyPaths=/etc/sing-box\nReadWritePaths=/var/lib/sing-box\nLimitNOFILE=65536\nRootDirectory=\nRootImage=\nBindReadOnlyPaths=\nTemporaryFileSystem=\nExtensionDirectories=\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil {
		t.Fatal("same-byte hardening was refused", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != body {
		t.Fatal("vendor hardening changed")
	}
}

func TestManagedIngressRejectsContinuedEarlyFilesystemRemap(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	path := u.Path(SingboxUnit + ".d/10-early.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[Service]\nBindReadOnlyPaths \\\n# intervening comment\n =/alternate/config:/etc/sing-box/config.json\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
		t.Fatal("continued filesystem remap accepted", err)
	}
}

func TestManagedIngressRejectsNonHostNetworkInBaseAndAllDropIns(t *testing.T) {
	for _, location := range []string{"usr/lib/systemd/system/sing-box.service", "etc/systemd/system/sing-box.service.d/10-early.conf", "run/systemd/system/service.d/90-local.conf"} {
		for _, body := range []string{"[Service]\nNetworkNamespacePath=/run/netns/alternate\n", "[Service]\nPrivateNetwork=yes\n", "[Service]\nPrivateNetwork=1\n", "[Unit]\nJoinsNamespaceOf=alternate.service\n"} {
			t.Run(location+"/"+body, func(t *testing.T) {
				root := t.TempDir()
				u := &UnitInstaller{Root: root}
				path := filepath.Join(root, location)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
					t.Fatal("non-host network allowed", err)
				}
				if _, err := os.Stat(u.Path(SingboxUnit + ".d/50-razdacha-ingress.conf")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("network preflight wrote managed files")
				}
			})
		}
	}
}

func TestManagedIngressAllowsHostNetworkResetsAndFilters(t *testing.T) {
	for _, value := range []string{"", "0", "no", "false", "off"} {
		t.Run(value, func(t *testing.T) {
			root := t.TempDir()
			u := &UnitInstaller{Root: root}
			path := u.Path(SingboxUnit + ".d/10-local.conf")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "[Unit]\nJoinsNamespaceOf=\n[Service]\nPrivateNetwork=" + value + "\nNetworkNamespacePath=\nRestrictAddressFamilies=AF_INET AF_INET6 AF_NETLINK AF_UNIX\nIPAddressDeny=10.0.0.0/8\n"
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil {
				t.Fatal("host-network filtering blocked", err)
			}
		})
	}
}

func TestManagedIngressRejectsPostStartHooksAndClearsReload(t *testing.T) {
	for _, location := range []string{"usr/lib/systemd/system/sing-box.service", "etc/systemd/system/sing-box.service.d/10-early.conf", "etc/systemd/system/sing-box.service.d/90-local.conf"} {
		t.Run(location, func(t *testing.T) {
			root := t.TempDir()
			u := &UnitInstaller{Root: root}
			path := filepath.Join(root, location)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("[Service]\nExecStartPost=/foreign/restore-config\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
				t.Fatal("post-start rewrite allowed", err)
			}
		})
	}
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	path := filepath.Join(root, "usr/lib/systemd/system/sing-box.service")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[Service]\nExecReload=/bin/kill -HUP $MAINPID\nExecStartPost=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(u.Path(SingboxUnit + ".d/50-razdacha-ingress.conf"))
	if err != nil || !strings.Contains(string(content), "[Service]\nExecReload=\n") {
		t.Fatal("independent reload can bypass startup guard", err)
	}
}

func TestManagedIngressRefusesUninspectedLegacyIncludes(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	path := filepath.Join(root, "usr/lib/systemd/system/sing-box.service")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(".include /foreign/remapped-service.conf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
		t.Fatal("uninspected fragment delegation allowed", err)
	}
}

func TestManagedIngressRefusesOtherServiceNameAliases(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	alias := u.Path(SingboxUnit)
	target := filepath.Join(root, "usr/lib/systemd/system/alternate.service")
	for _, path := range []string{alias, target} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(target, []byte("[Service]\nExecStart=/vendor/runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); !errors.Is(err, ErrForeignConfig) {
		t.Fatal("uninspected canonical service scopes allowed", err)
	}
}

func TestManagedIngressPreservesSameNameDistributionUnitSymlinks(t *testing.T) {
	root := t.TempDir()
	u := &UnitInstaller{Root: root}
	alias := u.Path(SingboxUnit)
	target := filepath.Join(root, "usr/lib/systemd/system", SingboxUnit)
	for _, path := range []string{alias, target} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := "[Service]\nExecStart=/vendor/runtime -C /vendor/config run\nProtectSystem=strict\n"
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := u.EnsureSingboxIngressGuard("/usr/local/bin/razdachad", "/usr/bin/sing-box", "/etc/sing-box/config.json", "/var/lib/razdacha/public-ingress.json"); err != nil {
		t.Fatal("same-name distribution link refused", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != body {
		t.Fatal("distribution unit changed")
	}
	if info, err := os.Lstat(alias); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("distribution symlink changed")
	}
}
