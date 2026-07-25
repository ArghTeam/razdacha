// Package packaging настраивает окружение razdacha на целевой машине: конфиг
// nginx перед панелью и самоподписанный сертификат для него.
//
// Панель доступна только из VPN — nginx слушает адрес wg0
// (docs/decisions/0008-nginx-before-panel.md), демон сидит на loopback. Все
// операции идемпотентны: повторная установка не плодит дубликатов, а чужие
// файлы в /etc/nginx не трогаются ни при установке, ни при удалении.
package packaging

import "errors"

var (
	// ErrNginxNotInstalled — в системе нет nginx: настраивать нечего.
	ErrNginxNotInstalled = errors.New("nginx не установлен")

	// ErrForeignConfig — по нашему пути лежит файл, который писали не мы.
	// Перезаписывать его нельзя: это чужая конфигурация с тем же именем.
	ErrForeignConfig = errors.New("чужой конфиг nginx")

	// ErrPublicListen — попытка привязать панель к публичному адресу.
	// Модель доступа (ADR 0001) этого не допускает ни в одной ветке.
	ErrPublicListen = errors.New("панель нельзя открыть на публичном адресе")

	// ErrBadConfig — параметры генерации конфига не проходят проверку.
	ErrBadConfig = errors.New("некорректные параметры конфига nginx")

	// ErrBadCertificate — существующий сертификат не читается или не годится.
	ErrBadCertificate = errors.New("некорректный сертификат панели")
)
