package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

const testVersion = "1.12.25"

// singboxTarball собирает архив в том же виде, в каком его отдаёт GitHub:
// бинарник лежит внутри каталога `sing-box-<версия>-linux-<арх>`.
func singboxTarball(t *testing.T, version, arch string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	dir := "sing-box-" + version + "-linux-" + arch
	files := []struct {
		name string
		body []byte
	}{
		{dir + "/LICENSE", []byte("GPL")},
		{dir + "/" + SingboxBinary, payload},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: f.name, Mode: 0o755, Size: int64(len(f.body)),
		}); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	return buf.Bytes()
}

// singboxInstaller собирает установщик поверх временного корня и локального
// «GitHub»: в реальный /usr/local/bin и в сеть не ходит ни один тест.
func singboxInstaller(t *testing.T, tarball []byte) (*SingboxInstaller, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/v" + testVersion + "/sing-box-" + testVersion + "-linux-amd64.tar.gz"
		if r.URL.Path != want {
			http.NotFound(w, r)
			return
		}
		hits++
		_, _ = w.Write(tarball)
	}))
	t.Cleanup(srv.Close)

	return &SingboxInstaller{
		Root:        t.TempDir(),
		BinDir:      DefaultBinDir,
		Version:     testVersion,
		Arch:        "amd64",
		ReleaseBase: srv.URL,
		HTTP:        srv.Client(),
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		LookPath:    func(string) (string, error) { return "", exec.ErrNotFound },
	}, &hits
}

func TestEnsureSingboxDownloads(t *testing.T) {
	payload := []byte("не настоящий бинарник, но с содержимым")
	i, hits := singboxInstaller(t, singboxTarball(t, testVersion, "amd64", payload))

	res, err := i.EnsureSingbox(context.Background())
	if err != nil {
		t.Fatalf("EnsureSingbox: %v", err)
	}
	if !res.Downloaded || res.FromSystem {
		t.Fatalf("downloaded=%v fromSystem=%v, ожидалось true/false", res.Downloaded, res.FromSystem)
	}
	if res.Version != testVersion {
		t.Fatalf("версия %q, ожидалась %q", res.Version, testVersion)
	}

	want := filepath.Join(i.Root, DefaultBinDir, SingboxBinary)
	if res.Path != want {
		t.Fatalf("путь %q, ожидался %q", res.Path, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("бинарник не записан: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("из архива достали не тот файл")
	}
	st, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Fatalf("права %v, ожидались 0755: бинарник запускает systemd", st.Mode().Perm())
	}
	if *hits != 1 {
		t.Fatalf("скачиваний %d, ожидалось 1", *hits)
	}
}

func TestEnsureSingboxKeepsMatchingSystemBinary(t *testing.T) {
	i, hits := singboxInstaller(t, singboxTarball(t, testVersion, "amd64", []byte("x")))
	i.LookPath = func(string) (string, error) { return "/usr/bin/sing-box", nil }
	i.RuntimeVersion = func(context.Context, string) (string, error) { return testVersion, nil }

	res, err := i.EnsureSingbox(context.Background())
	if err != nil {
		t.Fatalf("EnsureSingbox: %v", err)
	}
	if !res.FromSystem || res.Downloaded {
		t.Fatalf("fromSystem=%v downloaded=%v, ожидалось true/false", res.FromSystem, res.Downloaded)
	}
	if res.Path != "/usr/bin/sing-box" {
		t.Fatalf("путь %q: пакет из репозитория должен оставаться на месте", res.Path)
	}
	if *hits != 0 {
		t.Fatal("подходящий пакет уже стоит, а мы всё равно полезли в сеть")
	}
	if _, err := os.Stat(filepath.Join(i.Root, DefaultBinDir, SingboxBinary)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("бинарник положен поверх подходящего пакета")
	}
}

func TestEnsureSingboxReplacesWrongVersion(t *testing.T) {
	i, hits := singboxInstaller(t, singboxTarball(t, testVersion, "amd64", []byte("новый")))
	i.LookPath = func(string) (string, error) { return "/usr/bin/sing-box", nil }
	i.RuntimeVersion = func(context.Context, string) (string, error) { return "1.10.0", nil }

	res, err := i.EnsureSingbox(context.Background())
	if err != nil {
		t.Fatalf("EnsureSingbox: %v", err)
	}
	if !res.Downloaded {
		t.Fatal("версия пакета не совпала с библиотекой, а бинарник не поставлен")
	}
	if *hits != 1 {
		t.Fatalf("скачиваний %d, ожидалось 1", *hits)
	}
}

func TestEnsureSingboxMissingRelease(t *testing.T) {
	i, _ := singboxInstaller(t, nil)
	i.Version = "0.0.1"

	if _, err := i.EnsureSingbox(context.Background()); !errors.Is(err, ErrSingboxDownload) {
		t.Fatalf("ошибка %v, ожидалась ErrSingboxDownload", err)
	}
}

func TestEnsureSingboxUnknownArch(t *testing.T) {
	i, _ := singboxInstaller(t, nil)
	i.Arch = "riscv64"

	_, err := i.EnsureSingbox(context.Background())
	if !errors.Is(err, ErrSingboxArch) {
		t.Fatalf("ошибка %v, ожидалась ErrSingboxArch", err)
	}
	if !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("в тексте ошибки %q нет архитектуры", err.Error())
	}
}

func TestExtractBinaryWithoutBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "d/LICENSE", Size: 3}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if _, err := tw.Write([]byte("GPL")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = tw.Close()
	_ = gz.Close()

	if _, err := extractBinary(&buf); err == nil {
		t.Fatal("архив без бинарника принят")
	}
}

func TestSingboxLibraryVersionFromBuildInfo(t *testing.T) {
	bi := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "modernc.org/sqlite", Version: "v1.0.0"},
		{Path: singboxModule, Version: "v" + testVersion},
	}}
	got, err := singboxLibraryVersion(bi)
	if err != nil {
		t.Fatalf("singboxLibraryVersion: %v", err)
	}
	// Версия рантайма пишется без «v», тег релиза — с ней; хранятся обе формы,
	// как и в CI.
	if got != testVersion {
		t.Fatalf("версия %q, ожидалась %q", got, testVersion)
	}
}

func TestSingboxLibraryVersionMissing(t *testing.T) {
	if _, err := singboxLibraryVersion(&debug.BuildInfo{}); !errors.Is(err, ErrSingboxVersion) {
		t.Fatalf("ошибка %v, ожидалась ErrSingboxVersion", err)
	}
	if _, err := singboxLibraryVersion(nil); !errors.Is(err, ErrSingboxVersion) {
		t.Fatalf("ошибка %v, ожидалась ErrSingboxVersion", err)
	}
}

func TestSingboxRuntimeVersionParsesFirstLine(t *testing.T) {
	// Разбор тот же, что в CI: последнее поле первой строки. Вместо рантайма —
	// echo, тесты идут без установленного sing-box.
	script := filepath.Join(t.TempDir(), "fake-sing-box")
	body := "#!/bin/sh\necho 'sing-box version " + testVersion + "'\necho 'Environment: go1.24'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := SingboxRuntimeVersion(context.Background(), script)
	if err != nil {
		t.Fatalf("SingboxRuntimeVersion: %v", err)
	}
	if got != testVersion {
		t.Fatalf("версия %q, ожидалась %q", got, testVersion)
	}
}

func TestNewSingboxInstallerDefaults(t *testing.T) {
	i := NewSingboxInstaller("")
	if i.BinDir != DefaultBinDir {
		t.Fatalf("BinDir %q, ожидался %q", i.BinDir, DefaultBinDir)
	}
	if i.binPath() != filepath.Join(DefaultBinDir, SingboxBinary) {
		t.Fatalf("путь бинарника %q", i.binPath())
	}
	if !strings.HasPrefix(i.ReleaseBase, "https://") {
		t.Fatalf("база релизов %q", i.ReleaseBase)
	}
}
