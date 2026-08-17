// Package geoip определяет страну сервера по IP-адресу офлайн, из встроенной в
// бинарник базы DB-IP Country Lite. Сеть при этом не трогается: база уезжает в
// бинарник через go:embed, ридер — чистый Go (без CGO), совместимо с
// CGO_ENABLED=0. Это фундамент страновых пулов (ADR 0017).
//
// База DB-IP Country Lite распространяется под лицензией CC-BY-4.0 и требует
// атрибуции — см. Attribution и NOTICE.md.
package geoip

import (
	_ "embed"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// Attribution — обязательная атрибуция базы DB-IP Country Lite (CC-BY-4.0).
// Строка доступна в бинарнике и печатается в лог при первой инициализации
// ридера, чтобы условие лицензии выполнялось и в собранном демоне.
const Attribution = "IP Geolocation by DB-IP (https://db-ip.com), DB-IP Country Lite, CC-BY-4.0"

//go:embed dbip-country-lite.mmdb
var database []byte

var (
	once   sync.Once
	reader *maxminddb.Reader
)

// countryRecord — минимальная выжимка из записи DB-IP: нужен только ISO-код
// страны, остальные поля базы не читаем.
type countryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// load открывает встроенную базу один раз. Ридер потокобезопасен для
// параллельных Lookup, поэтому держим один экземпляр на пакет.
func load() {
	once.Do(func() {
		r, err := maxminddb.FromBytes(database)
		if err != nil {
			// База встроена на этапе сборки и проверяется тестом — сюда можно
			// попасть только при порче бинарника. Оставляем reader == nil:
			// Country вернёт "", утечки страны при этом не происходит.
			slog.Error("geoip: встроенная база не открылась", "err", err)
			return
		}
		reader = r
		slog.Info("geoip: база загружена",
			"type", r.Metadata.DatabaseType,
			"built", time.Unix(int64(r.Metadata.BuildEpoch), 0).UTC().Format("2006-01-02"),
			"attribution", Attribution,
		)
	})
}

// Country возвращает ISO-код страны (в верхнем регистре, как в базе: «NL»,
// «DE», …) для адреса ip или "" — если адрес не найден, некорректен или база
// недоступна. Определение полностью офлайн, сети не касается.
func Country(ip net.IP) string {
	if ip == nil {
		return ""
	}
	load()
	if reader == nil {
		return ""
	}
	var rec countryRecord
	if err := reader.Lookup(ip, &rec); err != nil {
		slog.Warn("geoip: поиск по адресу не удался", "ip", ip.String(), "err", err)
		return ""
	}
	return rec.Country.ISOCode
}
