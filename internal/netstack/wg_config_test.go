package netstack

import (
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// TestClientConfigFixedFields — клиентский конфиг собран по docs/03-networking.md,
// и ни одно из этих полей не настраивается на уровне пира.
func TestClientConfigFixedFields(t *testing.T) {
	settings := store.DefaultSettings()
	settings.EndpointHost = "vpn.example.com"
	p := store.Peer{
		Name:         "iPhone",
		PrivateKey:   "cGVlci1wcml2YXRlLWtleS0zMi1ieXRlcy0wMDAwMDA=",
		PresharedKey: "cHJlc2hhcmVkLWtleS0zMi1ieXRlcy0wMDAwMDAwMDA=",
		Address:      "10.8.0.5",
		Enabled:      true,
	}

	conf, err := ClientConfig(p, settings, "c2VydmVyLXB1YmxpYy1rZXktMzItYnl0ZXMtMDAwMA=")
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	for _, want := range []string{
		"[Interface]",
		"PrivateKey = " + p.PrivateKey,
		"Address    = 10.8.0.5/32",
		"DNS        = 10.8.0.1",
		"MTU        = 1280",
		"[Peer]",
		"PublicKey           = c2VydmVyLXB1YmxpYy1rZXktMzItYnl0ZXMtMDAwMA=",
		"PresharedKey        = " + p.PresharedKey,
		"Endpoint            = vpn.example.com:51820",
		"AllowedIPs          = 0.0.0.0/0, ::/0",
		"PersistentKeepalive = 25",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("в конфиге нет %q:\n%s", want, conf)
		}
	}
}

// TestClientConfigMTUIs1280 — MTU ровно 1280 (ADR 0004), значение не «по
// умолчанию системы» и не 1420.
func TestClientConfigMTUIs1280(t *testing.T) {
	if mtu := store.DefaultSettings().ClientMTU; mtu != 1280 {
		t.Fatalf("дефолтный MTU %d, ADR 0004 требует 1280", mtu)
	}

	settings := store.DefaultSettings()
	settings.EndpointHost = "vpn.example.com"
	conf, err := ClientConfig(store.Peer{Address: "10.8.0.5"}, settings, "ключ")
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if !strings.Contains(conf, "MTU        = 1280") {
		t.Errorf("в конфиге нет MTU 1280:\n%s", conf)
	}
	if strings.Contains(conf, "1420") {
		t.Errorf("в конфиге MTU 1420 — дефолт wg-quick, а не наш:\n%s", conf)
	}
}

// TestClientConfigRefusesWithoutServerKey — конфиг без ключа сервера или адреса
// подключения выглядит рабочим и не подключается; вместо него — отказ.
func TestClientConfigRefusesWithoutServerKey(t *testing.T) {
	settings := store.DefaultSettings()

	_, err := ClientConfig(store.Peer{Address: "10.8.0.5"}, settings, "")
	if err == nil {
		t.Fatal("конфиг выдан без ключа сервера и адреса подключения")
	}
	for _, want := range []string{"ключ сервера", "адрес сервера"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ошибка %q не называет, что не задано (%s)", err, want)
		}
	}
}

// TestWGConfigFromSettings — параметры интерфейса собираются из настроек:
// адрес с маской пула, порт и тот же MTU, что у клиентов.
func TestWGConfigFromSettings(t *testing.T) {
	cfg, err := WGConfigFromSettings(store.DefaultSettings())
	if err != nil {
		t.Fatalf("WGConfigFromSettings: %v", err)
	}
	if cfg.Name != "wg0" {
		t.Errorf("имя интерфейса %q, ожидалось wg0", cfg.Name)
	}
	if got := cfg.Address.String(); got != "10.8.0.1/24" {
		t.Errorf("адрес %s, ожидался 10.8.0.1/24", got)
	}
	if cfg.ListenPort != 51820 {
		t.Errorf("порт %d, ожидался 51820", cfg.ListenPort)
	}
	if cfg.MTU != 1280 {
		t.Errorf("MTU %d, ADR 0004 требует 1280", cfg.MTU)
	}
}

// TestWGConfigFromSettingsRejectsAddressOutsidePool — адрес сервера вне пула
// означает, что клиентам он недоступен, а маршрут не соберётся.
func TestWGConfigFromSettingsRejectsAddressOutsidePool(t *testing.T) {
	s := store.DefaultSettings()
	s.WGServerAddress = "10.9.0.1"
	if _, err := WGConfigFromSettings(s); err == nil {
		t.Fatal("адрес сервера вне пула принят")
	}
}
