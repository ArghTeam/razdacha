package main

import (
	"fmt"
	"strings"

	"github.com/ArghTeam/razdacha/internal/qr"
)

// summary — фаза вывода установки (docs/08-install-upgrade.md).
//
// Отдельная структура, а не печать по ходу дела: вывод — единственное, что
// пользователь увидит после `curl … | sh`, и собрать его целиком проще, чем
// расставлять Printf между шагами. Заодно он проверяется тестом.
type summary struct {
	// Fresh — первая установка. На обновлении QR и пароль не печатаются: пиры
	// на месте, пароль знает пользователь.
	Fresh bool

	// Version — версия установленного демона.
	Version string

	// PeerName и PeerConfig — имя первого пира и путь к его конфигу.
	// Пустые означают, что пира не создавали.
	PeerName   string
	PeerConfig string

	// ClientConfig — текст конфига, из него рисуется QR.
	ClientConfig string

	// PanelURL — адрес панели изнутри VPN.
	PanelURL string

	// PanelPublic — режим панели: публичный означает, что nginx слушает все
	// интерфейсы (ADR 0009).
	PanelPublic bool

	// PanelModeChanged — режим изменён этим запуском. Печатается отдельно:
	// молча уводить панель в интернет или из интернета нельзя (issue #81).
	PanelModeChanged bool

	// Password — сгенерированный пароль панели. Пустой означает, что пароль уже
	// был задан и мы его не знаем.
	Password string

	// WGPort — UDP-порт WireGuard, тот самый, который надо открыть в файрволе
	// провайдера.
	WGPort int

	// Color — раскрашивать ли QR управляющими последовательностями.
	Color bool
}

// panelMode — режим панели словами.
func (s summary) panelMode() string {
	if s.PanelPublic {
		return "публичный, панель отвечает на всех интерфейсах"
	}
	return "приватный, панель доступна только из VPN"
}

// previousPanelMode — значение RAZDACHA_PUBLIC, возвращающее прежний режим.
func (s summary) previousPanelMode() string {
	if s.PanelPublic {
		return "0"
	}
	return "1"
}

// Render собирает текст фазы вывода.
func (s summary) Render() (string, error) {
	var b strings.Builder
	nl := func() { b.WriteString("\n") }
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, "  "+format+"\n", args...)
	}

	nl()
	if s.Fresh {
		line("Готово.")
	} else {
		line("Обновление завершено, версия %s.", s.Version)
	}
	nl()

	if s.ClientConfig != "" {
		line("Отсканируйте QR-код клиентом WireGuard:")
		nl()
		code, err := qr.Terminal(s.ClientConfig, s.Color)
		if err != nil {
			return "", fmt.Errorf("QR-код конфига пира %s: %w", s.PeerName, err)
		}
		for _, l := range strings.Split(strings.TrimSuffix(code, "\n"), "\n") {
			b.WriteString("  " + l + "\n")
		}
		nl()
	}
	if s.PeerConfig != "" {
		line("Или скачайте конфиг:  %s", s.PeerConfig)
		nl()
	}

	line("После подключения интерфейс будет доступен по адресу:")
	line("  %s", s.PanelURL)
	nl()

	// Режим печатается всегда, а его смена — отдельной строкой. Пользователь,
	// обновившийся однострочником из README, должен видеть, открыта его панель
	// в интернет или нет, и не узнавать об этом от сканеров.
	line("Режим панели:  %s.", s.panelMode())
	if s.PanelModeChanged {
		line("  Режим изменён этим запуском; вернуть прежний: RAZDACHA_PUBLIC=%s",
			s.previousPanelMode())
	}
	nl()

	if s.Password != "" {
		line("Пароль панели:  %s", s.Password)
		line("Он показан один раз и больше нигде не хранится — запишите его.")
		line("Сменить: razdachad -set-password")
		nl()
	}

	line("Сертификат самоподписанный — браузер предупредит при первом заходе.")
	nl()

	// Последняя строка обязательна: у большинства облачных провайдеров есть
	// внешняя группа безопасности, о которой мы ничего не знаем. Это самая
	// частая причина «установил, но не подключается».
	line("Не забудьте открыть UDP-порт %d в файрволе провайдера.", s.WGPort)
	nl()
	return b.String(), nil
}
