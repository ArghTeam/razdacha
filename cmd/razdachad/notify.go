package main

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// notifyEnv — переменная, через которую systemd передаёт адрес сокета
// готовности. Пустая означает «запущены не из-под systemd» — обычный случай при
// разработке, и это не ошибка.
const notifyEnv = "NOTIFY_SOCKET"

// notifyReady сообщает systemd, что демон готов работать.
//
// Юнит объявлен `Type=notify` (docs/08-install-upgrade.md): без этого сообщения
// `systemctl start razdachad` висит до таймаута и объявляет юнит упавшим, хотя
// демон работает. Готовность — это поднятый wg0 и залитые правила, а не
// запущенный процесс: установщик печатает «готово» и QR сразу после старта, и
// на этот момент клиент обязан подключаться.
//
// Протокол — датаграмма `READY=1` в unix-сокет. Библиотеки для трёх строк
// незачем, а go-systemd тянет за собой d-bus.
func notifyReady() error {
	return notify(os.Getenv(notifyEnv), "READY=1")
}

func notify(socket, message string) error {
	if socket == "" {
		return nil
	}
	// Абстрактный сокет systemd передаёт с ведущим «@»; в Go он записывается
	// нулевым байтом.
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("сокет готовности %s: %w", os.Getenv(notifyEnv), err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(message)); err != nil {
		return fmt.Errorf("отправка готовности systemd: %w", err)
	}
	return nil
}
