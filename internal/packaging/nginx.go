package packaging

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const (
	// SiteName — имя нашего файла в sites-available и sites-enabled.
	SiteName = "razdacha"

	// DefaultPanelAddr — адрес wg0, на котором видна панель.
	DefaultPanelAddr = "10.8.0.1"

	// AllInterfaces — адрес публичного режима: слушаем всё, включая wg0.
	AllInterfaces = "0.0.0.0"

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

	// LoginPath — единственный путь, который nginx ограничивает по частоте.
	// Всё остальное под /api/ уже закрыто сессией, ограничивать там нечего, а
	// ограничение на «/» било бы по работающей панели: одна страница тянет
	// десятки запросов.
	LoginPath = "/api/login"

	// loginZone, loginZoneSize, loginRate и loginBurst — параметры limit_req на
	// вход. 10 запросов в минуту с адреса при всплеске в 5 — это заведомо выше
	// живого человека (даже с опечатками это 3–4 попытки) и заведомо ниже
	// перебора, ради которого сюда приходят.
	//
	// Это грубый отсекатель ботов перед Go, а не единственная защита: точная
	// защита — блокировка по адресу в демоне (internal/api/throttle.go), она
	// знает об успехах и неудачах, а nginx считает только запросы. Поэтому
	// значения выбраны с запасом вверх: ошибиться в сторону «пропустили лишний
	// запрос» дешевле, чем запереть хозяина панели.
	loginZone     = "razdacha_login"
	loginZoneSize = "1m" // ~16 тыс. адресов, больше нам не понадобится
	loginRate     = "10r/m"
	loginBurst    = 5

	// loginStatus — что отдаётся при превышении. Дефолтные 503 означают
	// «сервер сломался», а сломался не сервер.
	loginStatus = 429
)

// marker — первая строка сгенерированного файла. По ней установщик отличает
// свой конфиг от чужого с тем же именем и решает, можно ли его перезаписать.
const marker = "# razdacha: файл сгенерирован автоматически, правки будут перезаписаны"

// SiteConfig — параметры генерации конфига nginx. Ничего из этого в текст не
// зашито: адрес, порты и пути подставляются из настроек.
type SiteConfig struct {
	// ListenAddr — адрес, на котором слушает nginx. Только IPv4. Приватный или
	// loopback принимается всегда; публичный и `0.0.0.0` — только при Public.
	ListenAddr string

	// Public — публичный режим панели (ADR 0009). Флаг существует затем, чтобы
	// выход панели в интернет был отдельным осознанным решением, а не
	// последствием опечатки в адресе: без него публичный адрес по-прежнему даёт
	// ErrPublicListen.
	Public bool

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

// PublicSiteConfig — конфигурация публичного режима (ADR 0009): nginx слушает
// все интерфейсы, а не один адрес. Отдельного listen на 10.8.0.1 не нужно —
// `0.0.0.0` его уже покрывает, и изнутри VPN панель открывается как раньше.
func PublicSiteConfig() SiteConfig {
	c := DefaultSiteConfig()
	c.ListenAddr = AllInterfaces
	c.Public = true
	return c
}

// listenOn — значение директивы listen. На `0.0.0.0` адрес не пишется вовсе:
// nginx понимает такой listen как «все интерфейсы», а форма без адреса читается
// в конфиге однозначнее.
func (c SiteConfig) listenOn(port int) string {
	if c.allInterfaces() {
		return strconv.Itoa(port)
	}
	return net.JoinHostPort(c.ListenAddr, strconv.Itoa(port))
}

// serverName — `_` на всех интерфейсах: заходят и по публичному адресу, и по
// 10.8.0.1, и по имени, если оно у машины есть.
func (c SiteConfig) serverName() string {
	if c.allInterfaces() {
		return "_"
	}
	return c.ListenAddr
}

func (c SiteConfig) allInterfaces() bool {
	addr, err := netip.ParseAddr(c.ListenAddr)
	return err == nil && addr.IsUnspecified()
}

// Validate проверяет параметры до генерации.
//
// Главная проверка здесь — адрес. Публичный режим (ADR 0009) снял запрет на
// публичный адрес, но не сделал его умолчанием: адрес вне приватного диапазона
// и `0.0.0.0` принимаются только при Public. Без флага работает прежний белый
// список из ADR 0008 — приватный, loopback или link-local.
func (c SiteConfig) Validate() error {
	addr, err := netip.ParseAddr(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("%w: адрес панели %q не разобран: %w", ErrBadConfig, c.ListenAddr, err)
	}
	if !addr.Is4() {
		// IPv6 у клиентов выключен целиком (ADR 0005), слушать по нему нечего.
		return fmt.Errorf("%w: адрес панели %s должен быть IPv4", ErrBadConfig, addr)
	}
	if !c.Public {
		if addr.IsUnspecified() {
			return fmt.Errorf("%w: %s открывает панель на всех интерфейсах, а публичный режим не включён",
				ErrPublicListen, addr)
		}
		if !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() {
			return fmt.Errorf("%w: %s не входит в приватный диапазон, а публичный режим не включён",
				ErrPublicListen, addr)
		}
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
# Решения: docs/decisions/0008-nginx-before-panel.md,
#          docs/decisions/0009-public-panel-access.md

upstream razdacha_panel {
    server {{ .Upstream }};
}

# Перебор пароля: грубый отсекатель ботов до Go. Точная защита — блокировка по
# адресу в демоне, она знает об успехах и неудачах.
limit_req_zone $binary_remote_addr zone={{ .LoginZone }}:{{ .LoginZoneSize }} rate={{ .LoginRate }};

# WebSocket: Connection обязан зависеть от того, пришёл ли Upgrade. Без этой
# пары заголовков апгрейд не состоится, REST продолжит работать, а живые
# обновления в UI молча перестанут приходить.
map $http_upgrade $razdacha_connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen {{ .ListenHTTP }};
    server_name {{ .ServerName }};

    return 301 https://$host$request_uri;
}

server {
    listen {{ .ListenHTTPS }} ssl;
    server_name {{ .ServerName }};

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

    # Настройки прокси стоят на уровне server: локаций стало две, а точная
    # локация ничего не наследует от location /.
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

    # Ограничение только на вход: панель тянет десятки запросов на экран, и
    # limit_req на «/» ломал бы её вместо перебора.
    location = {{ .LoginPath }} {
        limit_req zone={{ .LoginZone }} burst={{ .LoginBurst }} nodelay;
        limit_req_status {{ .LoginStatus }};

        proxy_pass http://razdacha_panel;
    }

    location / {
        proxy_pass http://razdacha_panel;
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
		ListenHTTP     string
		ListenHTTPS    string
		ServerName     string
		LoginPath      string
		LoginZone      string
		LoginZoneSize  string
		LoginRate      string
		LoginBurst     int
		LoginStatus    int
	}{
		SiteConfig:     c,
		Marker:         marker,
		TimeoutSeconds: int(c.ReadTimeout.Seconds()),
		ListenHTTP:     c.listenOn(c.HTTPPort),
		ListenHTTPS:    c.listenOn(c.HTTPSPort),
		ServerName:     c.serverName(),
		LoginPath:      LoginPath,
		LoginZone:      loginZone,
		LoginZoneSize:  loginZoneSize,
		LoginRate:      loginRate,
		LoginBurst:     loginBurst,
		LoginStatus:    loginStatus,
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
