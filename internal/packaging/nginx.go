package packaging

import (
	"fmt"
	"net/netip"
	"strings"
	"text/template"
	"time"
)

const (
	// SiteName — имя нашего файла в sites-available и sites-enabled.
	SiteName = "razdacha"

	// DefaultPanelAddr — адрес wg0, на котором видна панель.
	DefaultPanelAddr = "10.8.0.1"

	// DefaultUpstream — адрес демона. Loopback, а не адрес wg0: этот адрес
	// существует всегда и не зависит от того, поднят ли уже интерфейс.
	DefaultUpstream = "127.0.0.1:8080"

	// DefaultHTTPPort и DefaultHTTPSPort — порты nginx на адресе wg0.
	DefaultHTTPPort  = 80
	DefaultHTTPSPort = 443

	// DefaultReadTimeout — потолок простоя проксируемого соединения. Взят
	// заведомо большим: по /api/ws идут живые обновления, и между двумя
	// сообщениями законно проходят минуты. С дефолтными 60 с nginx рвал бы
	// WebSocket, а UI показывал бы это как «зависло».
	DefaultReadTimeout = time.Hour
)

// marker — первая строка сгенерированного файла. По ней установщик отличает
// свой конфиг от чужого с тем же именем и решает, можно ли его перезаписать.
const marker = "# razdacha: файл сгенерирован автоматически, правки будут перезаписаны"

// SiteConfig — параметры генерации конфига nginx. Ничего из этого в текст не
// зашито: адрес, порты и пути подставляются из настроек.
type SiteConfig struct {
	// ListenAddr — адрес, на котором слушает nginx. Только IPv4 и только
	// приватный или loopback.
	ListenAddr string

	// HTTPPort отдаёт редирект на HTTPSPort.
	HTTPPort  int
	HTTPSPort int

	// Upstream — адрес демона, `host:port`. Только loopback.
	Upstream string

	// CertFile и KeyFile — пути к сертификату и ключу панели.
	CertFile string
	KeyFile  string

	// ReadTimeout — proxy_read_timeout/proxy_send_timeout.
	ReadTimeout time.Duration

	// MaxBodyMB — потолок тела запроса. Формы панели маленькие, но импорт
	// списка правил может быть на сотни килобайт.
	MaxBodyMB int
}

// DefaultSiteConfig — конфигурация по умолчанию; пути к сертификату
// проставляет установщик, он же знает корень файловой системы.
func DefaultSiteConfig() SiteConfig {
	return SiteConfig{
		ListenAddr:  DefaultPanelAddr,
		HTTPPort:    DefaultHTTPPort,
		HTTPSPort:   DefaultHTTPSPort,
		Upstream:    DefaultUpstream,
		ReadTimeout: DefaultReadTimeout,
		MaxBodyMB:   2,
	}
}

// Validate проверяет параметры до генерации.
//
// Главная проверка здесь — адрес: панель не должна оказаться на публичном
// адресе ни при какой комбинации настроек. Отсюда белый список — приватный
// диапазон или loopback, — а не чёрный список из одного «0.0.0.0».
func (c SiteConfig) Validate() error {
	addr, err := netip.ParseAddr(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("%w: адрес панели %q не разобран: %w", ErrBadConfig, c.ListenAddr, err)
	}
	if !addr.Is4() {
		// IPv6 у клиентов выключен целиком (ADR 0005), слушать по нему нечего.
		return fmt.Errorf("%w: адрес панели %s должен быть IPv4", ErrBadConfig, addr)
	}
	if addr.IsUnspecified() {
		return fmt.Errorf("%w: %s открывает панель на всех интерфейсах", ErrPublicListen, addr)
	}
	if !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() {
		return fmt.Errorf("%w: %s не входит в приватный диапазон", ErrPublicListen, addr)
	}

	if err := validatePort("HTTP-порт", c.HTTPPort); err != nil {
		return err
	}
	if err := validatePort("HTTPS-порт", c.HTTPSPort); err != nil {
		return err
	}
	if c.HTTPPort == c.HTTPSPort {
		return fmt.Errorf("%w: порты HTTP и HTTPS совпадают (%d)", ErrBadConfig, c.HTTPPort)
	}

	up, err := netip.ParseAddrPort(c.Upstream)
	if err != nil {
		return fmt.Errorf("%w: адрес демона %q не разобран: %w", ErrBadConfig, c.Upstream, err)
	}
	if !up.Addr().IsLoopback() {
		// Демон снаружи не виден вовсе — это и есть смысл фронта (ADR 0008).
		return fmt.Errorf("%w: демон должен слушать loopback, а не %s", ErrPublicListen, up.Addr())
	}

	if err := validatePath("путь к сертификату", c.CertFile); err != nil {
		return err
	}
	if err := validatePath("путь к ключу", c.KeyFile); err != nil {
		return err
	}
	if c.ReadTimeout <= 0 {
		return fmt.Errorf("%w: таймаут чтения должен быть положительным", ErrBadConfig)
	}
	if c.MaxBodyMB <= 0 {
		return fmt.Errorf("%w: потолок тела запроса должен быть положительным", ErrBadConfig)
	}
	return nil
}

func validatePort(what string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: %s вне диапазона: %d", ErrBadConfig, what, port)
	}
	return nil
}

// validatePath не пускает в конфиг пути с символами, которые для nginx
// значимы: пробел и `;` завершают директиву, кавычки и перевод строки ломают
// разбор. Проще запретить, чем экранировать.
func validatePath(what, path string) error {
	if path == "" {
		return fmt.Errorf("%w: не задан %s", ErrBadConfig, what)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%w: %s должен быть абсолютным: %q", ErrBadConfig, what, path)
	}
	if strings.ContainsAny(path, " \t\r\n\"'; #{}") {
		return fmt.Errorf("%w: %s содержит недопустимые символы: %q", ErrBadConfig, what, path)
	}
	return nil
}

// siteTemplate — тело конфига. HTTP/2 намеренно не включён: директива меняла
// форму между nginx 1.22 (Debian 12) и 1.25 (`listen ... http2` против
// `http2 on`), и один и тот же файл не подошёл бы обеим версиям.
var siteTemplate = template.Must(template.New("site").Parse(`{{ .Marker }}
# Решение: docs/decisions/0008-nginx-before-panel.md

upstream razdacha_panel {
    server {{ .Upstream }};
}

# WebSocket: Connection обязан зависеть от того, пришёл ли Upgrade. Без этой
# пары заголовков апгрейд не состоится, REST продолжит работать, а живые
# обновления в UI молча перестанут приходить.
map $http_upgrade $razdacha_connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen {{ .ListenAddr }}:{{ .HTTPPort }};
    server_name {{ .ListenAddr }};

    return 301 https://$host$request_uri;
}

server {
    listen {{ .ListenAddr }}:{{ .HTTPSPort }} ssl;
    server_name {{ .ListenAddr }};

    ssl_certificate     {{ .CertFile }};
    ssl_certificate_key {{ .KeyFile }};
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_session_cache   shared:razdacha:1m;
    ssl_session_timeout 1h;

    client_max_body_size {{ .MaxBodyMB }}m;

    gzip on;
    gzip_types text/css application/javascript application/json image/svg+xml;
    gzip_min_length 1024;

    access_log /var/log/nginx/razdacha.access.log;
    error_log  /var/log/nginx/razdacha.error.log;

    location / {
        proxy_pass http://razdacha_panel;

        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection $razdacha_connection_upgrade;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # Живые обновления идут по одному соединению, между сообщениями
        # законно проходят минуты; буферизация их бы задерживала.
        proxy_buffering off;
        proxy_connect_timeout 5s;
        proxy_read_timeout {{ .TimeoutSeconds }}s;
        proxy_send_timeout {{ .TimeoutSeconds }}s;
    }
}
`))

// Render собирает текст конфига nginx.
func (c SiteConfig) Render() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	data := struct {
		SiteConfig
		Marker         string
		TimeoutSeconds int
	}{
		SiteConfig:     c,
		Marker:         marker,
		TimeoutSeconds: int(c.ReadTimeout.Seconds()),
	}

	var buf strings.Builder
	if err := siteTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("генерация конфига nginx: %w", err)
	}
	return buf.String(), nil
}

// isOurs отвечает, наш ли это файл: сгенерированный всегда начинается с
// маркера, чужой — нет.
func isOurs(content string) bool {
	return strings.HasPrefix(content, marker)
}
