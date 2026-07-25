package packaging

import "testing"

// Маршрут до loopback даёт loopback-адрес — в SAN сертификата такому адресу
// делать нечего, и это ошибка, а не «внешний адрес 127.0.0.1».
func TestDetectExternalAddrRejectsLoopback(t *testing.T) {
	addr, err := detectExternalAddr("127.0.0.1:9")
	if err == nil {
		t.Fatalf("loopback принят как внешний адрес: %s", addr)
	}
}

func TestDetectExternalAddrBadTarget(t *testing.T) {
	if addr, err := detectExternalAddr("не адрес"); err == nil {
		t.Fatalf("ожидалась ошибка, получен адрес %s", addr)
	}
}
