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

	// ExternalPanelURL — адрес панели снаружи VPN, публичный режим. Тот же
	// адрес, что попал в SAN сертификата (internal/packaging/install.go,
	// Installer.certIPs) — вторично его не определяем. Пусто, если режим не
	// публичный или внешний адрес определить не удалось: тогда его не с чем
	// печатать, и вывод обязан сказать об этом честно, а не подставить адрес.
	ExternalPanelURL string

	// PanelPublic — режим панели: публичный означает, что nginx слушает все
	// интерфейсы (ADR 0009).
	PanelPublic bool

	// PanelModeChanged — режим изменён этим запуском. Печатается отдельно:
	// молча уводить панель в интернет или из интернета нельзя (issue #81).
	PanelModeChanged bool

	// PanelModeInferred — режим не был записан и выведен из конфига nginx,
	// оставшегося от предыдущей установки. Пользователь должен видеть, что
	// режим угадан по конфигу, а не задан заново.
	PanelModeInferred bool

	// PanelModeUnknown — причина, по которой режим вывести не удалось. Непустая
	// означает приватный режим, не записанный в настройки.
	PanelModeUnknown string

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

	// В публичном режиме первым называется внешний адрес: это тот, по которому
	// человек реально попадает на панель без VPN. `10.8.0.1` печатается вторым
	// и с пометкой — он работает только для тех, кто уже подключён (issue #60,
	// иначе первым по адресу из вывода уходили и не попадали никуда).
	switch {
	case s.PanelPublic && s.ExternalPanelURL != "":
		line("Панель доступна по адресу:")
		line("  %s", s.ExternalPanelURL)
		nl()
		line("Для тех, кто уже подключён к VPN, панель также открыта по адресу:")
		line("  %s", s.PanelURL)
	case s.PanelPublic:
		// Внешний адрес определить не удалось (Installer.certIPs) — сертификат
		// выписан на один адрес VPN. Подставлять адрес нечем, поэтому вывод
		// говорит об этом прямо, а не молчит и не выдумывает.
		line("Внешний адрес сервера не определён — панель отвечает на всех")
		line("интерфейсах, но пока доступна только по адресу VPN:")
		line("  %s", s.PanelURL)
	default:
		line("После подключения интерфейс будет доступен по адресу:")
		line("  %s", s.PanelURL)
	}
	nl()

	// Режим печатается всегда, а его смена — отдельной строкой. Пользователь,
	// обновившийся однострочником из README, должен видеть, открыта его панель
	// в интернет или нет, и не узнавать об этом от сканеров.
	line("Режим панели:  %s.", s.panelMode())
	switch {
	case s.PanelModeChanged:
		line("  Режим изменён этим запуском; вернуть прежний: RAZDACHA_PUBLIC=%s",
			s.previousPanelMode())
	case s.PanelModeInferred:
		// Установка от версии до 0.2.1: режим не был записан, и мы вычитали его
		// из конфига nginx. Сказать об этом обязаны — догадка, пусть и
		// обоснованная, должна быть видна тому, чью панель она открывает.
		line("  Режим взят из существующего конфига nginx и записан в настройки;")
		line("  сменить: RAZDACHA_PUBLIC=%s", s.previousPanelMode())
	case s.PanelModeUnknown != "":
		line("  Прежний режим определить не удалось, поднят приватный:")
		line("  %s", s.PanelModeUnknown)
		line("  Если панель была открыта в интернет, верните: RAZDACHA_PUBLIC=1")
	default:
		// Режим этим запуском не менялся и не выводился — но переключатель всё
		// равно стоит назвать: сейчас человек как раз читает вывод установщика,
		// а не документацию, и это единственный момент, когда он о нём узнаёт.
		line("  Переключить: RAZDACHA_PUBLIC=1 — публичный режим, RAZDACHA_PUBLIC=0 — приватный.")
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
