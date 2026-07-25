// Package lists качает и кэширует списки ресурсов: бинарные наборы `.srs` и
// plain-списки доменов и подсетей.
//
// Слой отвечает за то, что sing-box сделать не может: подсети из списков нужны
// в nft-сете, потому что FakeIP работает только когда клиент резолвит домен
// (docs/04-dns-fakeip.md). Наборы типа `remote` в конфиге sing-box обновляет он
// сам — их здесь нет.
package lists

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// Ошибки слоя. Проверяются через errors.Is; тексты доходят до UI как есть.
var (
	// ErrNotCached — списка нет в кэше и скачать его не удалось.
	ErrNotCached = errors.New("список отсутствует в кэше")
	// ErrBadResponse — источник ответил так, что кэш обновлять нельзя.
	ErrBadResponse = errors.New("некорректный ответ источника")
)

// Format — вид списка. Определяется по расширению в URL: сервер отдаёт
// .srs с Content-Type octet-stream, по заголовку формат не разобрать.
type Format string

// Виды списков.
const (
	FormatSRS   Format = "srs"   // бинарный rule-set sing-box
	FormatJSON  Format = "json"  // текстовый rule-set sing-box
	FormatPlain Format = "plain" // домены и подсети построчно
)

// URL-шаблоны источника community-списков — itdoginfo/allow-domains, тот же,
// что у Podkop (podkop/files/usr/lib/constants.sh:54-55).
const (
	communitySRSURL    = "https://github.com/itdoginfo/allow-domains/releases/latest/download/%s.srs"
	communitySubnetURL = "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Subnets/IPv4/%s.lst"
)

// communitySubnets — ключи сервисов, у которых в allow-domains есть список
// подсетей (podkop/files/usr/lib/constants.sh:56-65). У остальных сервисов
// только домены, и их целиком отрабатывает sing-box набором типа remote.
var communitySubnets = map[string]bool{
	"twitter":      true,
	"meta":         true,
	"discord":      true,
	"roblox":       true,
	"telegram":     true,
	"cloudflare":   true,
	"hetzner":      true,
	"ovh":          true,
	"digitalocean": true,
	"cloudfront":   true,
}

// CommunitySubnetURL — адрес списка подсетей сервиса. Второе значение false
// означает, что подсетей у сервиса нет и качать нечего.
func CommunitySubnetURL(key string) (string, bool) {
	if !communitySubnets[key] {
		return "", false
	}
	return fmt.Sprintf(communitySubnetURL, key), true
}

// CommunitySRSURL — адрес бинарного набора доменов сервиса. Тот же адрес
// строит генератор конфига: sing-box качает такие наборы сам, демону они нужны
// только когда из набора вынимаются подсети.
func CommunitySRSURL(key string) string {
	return fmt.Sprintf(communitySRSURL, key)
}

// FormatOf определяет вид списка по расширению в адресе.
func FormatOf(raw string) Format {
	p := raw
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		p = u.Path
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".srs":
		return FormatSRS
	case ".json":
		return FormatJSON
	default:
		return FormatPlain
	}
}

// List — разобранное содержимое списка.
type List struct {
	// Domains — суффиксы доменов, пригодные для domain_suffix.
	Domains []string
	// Subnets — префиксы IPv4, пригодные для элементов nft-сета. Только v4:
	// сет объявлен ipv4_addr, а IPv6 у клиентов выключен (ADR 0005).
	Subnets []string
	// Skipped — сколько строк отброшено как неразбираемые.
	Skipped int
}
