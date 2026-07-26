package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

// TestNotifyNoSocket — без systemd готовность никуда не отправляется и это не
// ошибка: демон запускают руками при разработке.
func TestNotifyNoSocket(t *testing.T) {
	if err := notify("", "READY=1"); err != nil {
		t.Fatalf("пустой сокет дал ошибку: %v", err)
	}
}

// TestNotifyReady — systemd получает ровно READY=1.
func TestNotifyReady(t *testing.T) {
	// Путь короткий намеренно: у unix-сокета потолок длины пути около сотни
	// байт, и t.TempDir() на macOS в него почти не влезает.
	path := filepath.Join(t.TempDir(), "n.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Skipf("unixgram недоступен: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := notify(path, "READY=1"); err != nil {
		t.Fatalf("отправка готовности: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("таймаут чтения: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("чтение готовности: %v", err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Fatalf("получено %q, ожидалось READY=1", got)
	}
}

// TestNotifyBadSocket — несуществующий сокет означает сломанное окружение, и об
// этом должно быть сказано, а не проглочено.
func TestNotifyBadSocket(t *testing.T) {
	if err := notify(filepath.Join(t.TempDir(), "нет.sock"), "READY=1"); err == nil {
		t.Fatal("несуществующий сокет принят молча")
	}
}
