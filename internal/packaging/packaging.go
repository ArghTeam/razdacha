// Package packaging настраивает окружение razdacha на целевой машине: конфиг
// nginx перед панелью и самоподписанный сертификат для него.
//
// Демон сидит на loopback, наружу его открывает nginx
// (docs/decisions/0008-nginx-before-panel.md). Адрес nginx зависит от режима:
// по умолчанию это адрес wg0, то есть панель видна только из VPN; в публичном
// режиме (docs/decisions/0009-public-panel-access.md) nginx слушает все
// интерфейсы, а вход прикрыт limit_req. Все операции идемпотентны: повторная
// установка не плодит дубликатов, а чужие файлы в /etc/nginx не трогаются ни
// при установке, ни при удалении.
package packaging

import "errors"

var (
	// ErrNginxNotInstalled — в системе нет nginx: настраивать нечего.
	ErrNginxNotInstalled = errors.New("nginx не установлен")

	// ErrForeignConfig — по нашему пути лежит файл, который писали не мы.
	// Перезаписывать его нельзя: это чужая конфигурация с тем же именем.
	ErrForeignConfig = errors.New("чужой конфиг nginx")

	// ErrPublicListen — попытка привязать панель к публичному адресу без
	// публичного режима либо демона — к чему-то кроме loopback. Публичный
	// режим (ADR 0009) снял запрет на первое, но не на второе.
	ErrPublicListen = errors.New("панель нельзя открыть на публичном адресе")

	// ErrBadConfig — параметры генерации конфига не проходят проверку.
	ErrBadConfig = errors.New("некорректные параметры конфига nginx")

	// ErrBadCertificate — существующий сертификат не читается или не годится.
	ErrBadCertificate = errors.New("некорректный сертификат панели")
)
