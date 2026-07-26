package main

import (
	"strings"
	"testing"
)

const sampleConfig = `[Interface]
PrivateKey = 4Hf6Ga8Sq0We2Rt4Yu6Io8Pa0Sd2Fg4Hj6Kl8Zx0Cv=
Address    = 10.8.0.2/32
DNS        = 10.8.0.1
MTU        = 1280

[Peer]
PublicKey           = 9Uu1Ii3Oo5Pp7Aa9Ss1Dd3Ff5Gg7Hh9Jj1Kk3Ll5Mm=
Endpoint            = 198.51.100.7:51820
AllowedIPs          = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`

// TestSummaryFresh — первая установка печатает всё, ради чего фаза вывода и
// существует: QR, путь к конфигу, адрес панели, пароль и строку про UDP-порт.
func TestSummaryFresh(t *testing.T) {
	out, err := summary{
		Fresh:        true,
		Version:      "1.2.3",
		PeerName:     "client-1",
		PeerConfig:   "/var/lib/razdacha/client-1.conf",
		ClientConfig: sampleConfig,
		PanelURL:     "https://10.8.0.1",
		Password:     "SecretPassw0rd",
		WGPort:       51820,
	}.Render()
	if err != nil {
		t.Fatalf("сборка вывода: %v", err)
	}

	for _, want := range []string{
		"Готово.",
		"Отсканируйте QR-код клиентом WireGuard:",
		"/var/lib/razdacha/client-1.conf",
		"https://10.8.0.1",
		"SecretPassw0rd",
		"Сертификат самоподписанный",
		"Не забудьте открыть UDP-порт 51820 в файрволе провайдера.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "█") {
		t.Fatalf("QR-код не нарисован:\n%s", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("в бесцветном выводе оказалась управляющая последовательность")
	}
}

// TestSummaryUpgrade — на обновлении ни QR, ни пароля быть не должно: пиры на
// месте, а пароля мы не знаем, он хранится хешем.
func TestSummaryUpgrade(t *testing.T) {
	out, err := summary{
		Version:  "1.2.3",
		PanelURL: "https://10.8.0.1",
		WGPort:   51820,
	}.Render()
	if err != nil {
		t.Fatalf("сборка вывода: %v", err)
	}
	if !strings.Contains(out, "Обновление завершено, версия 1.2.3.") {
		t.Fatalf("нет строки об обновлении:\n%s", out)
	}
	for _, unwanted := range []string{"Отсканируйте", "Пароль панели", "█"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("на обновлении напечатано лишнее %q:\n%s", unwanted, out)
		}
	}
	// Строка про порт обязательна в обоих случаях: группа безопасности
	// провайдера могла измениться и без нас.
	if !strings.Contains(out, "UDP-порт 51820") {
		t.Fatalf("нет строки про UDP-порт:\n%s", out)
	}
}

// TestSummaryPanelMode — режим панели печатается всегда, а его смена — отдельной
// строкой с командой возврата. Молча уводить панель в интернет или из интернета
// нельзя (#81).
func TestSummaryPanelMode(t *testing.T) {
	private, err := summary{PanelURL: "https://10.8.0.1", WGPort: 51820}.Render()
	if err != nil {
		t.Fatalf("сборка вывода: %v", err)
	}
	if !strings.Contains(private, "Режим панели:  приватный, панель доступна только из VPN.") {
		t.Fatalf("нет строки о приватном режиме:\n%s", private)
	}
	if strings.Contains(private, "Режим изменён") {
		t.Fatalf("режим не менялся, а вывод говорит обратное:\n%s", private)
	}

	public, err := summary{
		PanelURL:         "https://10.8.0.1",
		WGPort:           51820,
		PanelPublic:      true,
		PanelModeChanged: true,
	}.Render()
	if err != nil {
		t.Fatalf("сборка вывода: %v", err)
	}
	for _, want := range []string{
		"Режим панели:  публичный, панель отвечает на всех интерфейсах.",
		"Режим изменён этим запуском; вернуть прежний: RAZDACHA_PUBLIC=0",
	} {
		if !strings.Contains(public, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, public)
		}
	}
}

// TestSummaryColor — на терминале QR раскрашивается, иначе его не прочитает
// сканер в тёмной теме.
func TestSummaryColor(t *testing.T) {
	out, err := summary{
		Fresh:        true,
		ClientConfig: sampleConfig,
		PanelURL:     "https://10.8.0.1",
		WGPort:       51820,
		Color:        true,
	}.Render()
	if err != nil {
		t.Fatalf("сборка вывода: %v", err)
	}
	if !strings.Contains(out, "\x1b[47;30m") {
		t.Fatalf("QR не раскрашен:\n%s", out)
	}
}
