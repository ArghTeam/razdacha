package packaging

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// validSite — конфигурация, проходящая проверку; тесты меняют в ней одно поле.
func validSite() SiteConfig {
	c := DefaultSiteConfig()
	c.CertFile = "/etc/razdacha/tls/cert.pem"
	c.KeyFile = "/etc/razdacha/tls/key.pem"
	return c
}

// publicValidSite — то же в публичном режиме.
func publicValidSite() SiteConfig {
	c := PublicSiteConfig()
	c.CertFile = "/etc/razdacha/tls/cert.pem"
	c.KeyFile = "/etc/razdacha/tls/key.pem"
	return c
}

func TestRenderListen(t *testing.T) {
	out, err := validSite().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"listen 10.8.0.1:80;",
		"listen 10.8.0.1:443 ssl;",
		"return 301 https://$host$request_uri;",
		"server 127.0.0.1:8080;",
		"ssl_certificate     /etc/razdacha/tls/cert.pem;",
		"ssl_certificate_key /etc/razdacha/tls/key.pem;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в конфиге нет %q", want)
		}
	}
}

// Без этих трёх строк апгрейд до WebSocket не состоится, а UI не покажет
// ничего: REST продолжит работать, пропадут только живые обновления. Живого
// канала сейчас нет (docs/05-api.md), заголовки — сохранённый задел, и тест
// стоит здесь, чтобы их не выкинули как мусор.
func TestRenderWebSocketHeaders(t *testing.T) {
	out, err := validSite().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"proxy_http_version 1.1;",
		"proxy_set_header Upgrade    $http_upgrade;",
		"proxy_set_header Connection $razdacha_connection_upgrade;",
		"map $http_upgrade $razdacha_connection_upgrade {",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в конфиге нет %q", want)
		}
	}
}

func TestRenderProxyHeadersAndTimeouts(t *testing.T) {
	c := validSite()
	c.ReadTimeout = 90 * time.Second
	out, err := c.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"proxy_set_header Host              $host;",
		"proxy_set_header X-Real-IP         $remote_addr;",
		"proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
		"proxy_read_timeout 90s;",
		"proxy_send_timeout 90s;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в конфиге нет %q", want)
		}
	}
}

// В режиме по умолчанию панель не открывается наружу: публичный адрес не
// доходит до текста конфига, а listen привязан к адресу wg0.
func TestRenderNeverPublic(t *testing.T) {
	out, err := validSite().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// «listen 443 ssl» без адреса — это тоже все интерфейсы.
	for _, forbidden := range []string{"0.0.0.0", "[::]", "listen 80", "listen 443", "listen ["} {
		if strings.Contains(out, forbidden) {
			t.Errorf("конфиг открывает панель наружу: найдено %q", forbidden)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "listen ") && !strings.HasPrefix(trimmed, "listen 10.8.0.1:") {
			t.Errorf("listen не привязан к адресу wg0: %q", trimmed)
		}
	}
}

func TestValidateRejectsPublicAddresses(t *testing.T) {
	cases := map[string]string{
		"все интерфейсы":  "0.0.0.0",
		"публичный":       "203.0.113.10",
		"публичный CGNAT": "100.64.0.1", // не RFC1918, наружу маршрутизируется
	}
	for name, addr := range cases {
		t.Run(name, func(t *testing.T) {
			c := validSite()
			c.ListenAddr = addr
			if _, err := c.Render(); !errors.Is(err, ErrPublicListen) {
				t.Fatalf("ожидалась ErrPublicListen, получено: %v", err)
			}
		})
	}
}

// Тот же адрес, что отвергается без флага режима, с ним принимается: выход
// панели в интернет — отдельное решение, а не следствие опечатки.
func TestPublicModeAllowsPublicAddresses(t *testing.T) {
	for name, addr := range map[string]string{
		"все интерфейсы": AllInterfaces,
		"публичный":      "203.0.113.10",
	} {
		t.Run(name, func(t *testing.T) {
			c := validSite()
			c.ListenAddr = addr
			if _, err := c.Render(); !errors.Is(err, ErrPublicListen) {
				t.Fatalf("без публичного режима ожидалась ErrPublicListen, получено: %v", err)
			}

			c.Public = true
			if _, err := c.Render(); err != nil {
				t.Fatalf("в публичном режиме адрес %s отвергнут: %v", addr, err)
			}
		})
	}
}

// Демон на loopback — часть ADR 0008, которую ADR 0009 не отменял: публичный
// режим открывает наружу nginx, а не razdachad.
func TestValidateRejectsNonLoopbackUpstream(t *testing.T) {
	for name, public := range map[string]bool{"обычный режим": false, "публичный режим": true} {
		t.Run(name, func(t *testing.T) {
			c := validSite()
			c.Public = public
			c.Upstream = "10.8.0.1:8080"
			if _, err := c.Render(); !errors.Is(err, ErrPublicListen) {
				t.Fatalf("ожидалась ErrPublicListen, получено: %v", err)
			}
		})
	}
}

// Публичный режим: слушаем все интерфейсы обоими портами. Отдельный listen на
// 10.8.0.1 не нужен и был бы ошибкой конфигурации — адрес уже покрыт.
func TestRenderPublicListensEverywhere(t *testing.T) {
	c := PublicSiteConfig()
	c.CertFile, c.KeyFile = "/etc/razdacha/tls/cert.pem", "/etc/razdacha/tls/key.pem"
	out, err := c.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"listen 80;",
		"listen 443 ssl;",
		"server_name _;",
		"return 301 https://$host$request_uri;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в конфиге нет %q", want)
		}
	}
	if strings.Contains(out, "listen 10.8.0.1:") {
		t.Error("в публичном режиме остался отдельный listen на адрес wg0")
	}
}

// limit_req обязан висеть на входе и только на нём: на «/» он ломал бы панель
// (один экран тянет десятки запросов), а перебор пароля идёт не туда.
func TestRenderLimitReqOnLoginOnly(t *testing.T) {
	out, err := validSite().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"limit_req_zone $binary_remote_addr zone=razdacha_login:1m rate=10r/m;",
		"location = /api/login {",
		"limit_req zone=razdacha_login burst=5 nodelay;",
		"limit_req_status 429;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в конфиге нет %q", want)
		}
	}
	if n := strings.Count(out, "limit_req zone="); n != 1 {
		t.Fatalf("limit_req встречается %d раз, ожидался один — на входе", n)
	}

	login := strings.Index(out, "location = "+LoginPath+" {")
	root := strings.Index(out, "location / {")
	limit := strings.Index(out, "limit_req zone=")
	if login < 0 || root < 0 {
		t.Fatal("в конфиге нет обеих локаций")
	}
	if limit < login || limit > root {
		t.Error("limit_req стоит не внутри локации входа")
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := map[string]func(*SiteConfig){
		"IPv6":            func(c *SiteConfig) { c.ListenAddr = "fd00::1" },
		"мусор в адресе":  func(c *SiteConfig) { c.ListenAddr = "не адрес" },
		"порт вне границ": func(c *SiteConfig) { c.HTTPSPort = 70000 },
		"порты совпали":   func(c *SiteConfig) { c.HTTPPort = c.HTTPSPort },
		"пустой сертификат": func(c *SiteConfig) {
			c.CertFile = ""
		},
		"относительный путь": func(c *SiteConfig) { c.KeyFile = "tls/key.pem" },
		"пробел в пути":      func(c *SiteConfig) { c.KeyFile = "/etc/razdacha/my key.pem" },
		"инъекция в путь":    func(c *SiteConfig) { c.CertFile = "/etc/c.pem; listen 0.0.0.0:443" },
		"нулевой таймаут":    func(c *SiteConfig) { c.ReadTimeout = 0 },
		"нулевое тело":       func(c *SiteConfig) { c.MaxBodyMB = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := validSite()
			mutate(&c)
			if _, err := c.Render(); err == nil {
				t.Fatal("ожидалась ошибка, получено nil")
			}
		})
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first, err := validSite().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	second, err := validSite().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if first != second {
		t.Fatal("два вызова Render дали разный текст: конфиг переписывался бы на каждой установке")
	}
	if !isOurs(first) {
		t.Fatal("сгенерированный конфиг не начинается с маркера")
	}
}

// Режим панели выводится из конфига, который сгенерировали мы сами: на
// обновлении с версий до 0.2.1 это единственное место, где он ещё записан
// (issue #81). Проверка идёт от Render, а не от текста руками: разъедься они —
// миграция начнёт врать, а не падать.
func TestPublicFromConfigRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		site SiteConfig
		want bool
	}{
		{name: "приватный", site: validSite()},
		{name: "публичный", site: publicValidSite(), want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := c.site.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			public, err := PublicFromConfig(out)
			if err != nil {
				t.Fatalf("PublicFromConfig: %v", err)
			}
			if public != c.want {
				t.Fatalf("режим %v, ожидался %v", public, c.want)
			}
		})
	}
}

// Публичный адрес в listen — тоже публичный режим: правило то же, что в
// Validate, иначе установка на выделенном адресе после обновления схлопнулась
// бы в приватную.
func TestPublicFromConfigExplicitAddr(t *testing.T) {
	c := validSite()
	c.ListenAddr, c.Public = "203.0.113.7", true
	out, err := c.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	public, err := PublicFromConfig(out)
	if err != nil || !public {
		t.Fatalf("режим public=%v err=%v, ожидался публичный", public, err)
	}
}

// Конфиг без нашего маркера — чужой файл: режим из него не выводится, потому что
// он не про нашу панель.
func TestPublicFromConfigForeign(t *testing.T) {
	_, err := PublicFromConfig("server {\n    listen 443 ssl;\n}\n")
	if !errors.Is(err, ErrForeignConfig) {
		t.Fatalf("ошибка %v, ожидалась ErrForeignConfig", err)
	}
}

// Наш маркер есть, а разбирать нечего: файл правили руками. Догадываться тут
// не о чем — ошибка, и решать её тому, кто читает конфиг.
func TestPublicFromConfigUnparsable(t *testing.T) {
	cases := map[string]string{
		"без listen":        marker + "\nserver {\n    server_name _;\n}\n",
		"мусор в listen":    marker + "\nserver {\n    listen ерунда;\n}\n",
		"разные режимы":     marker + "\nserver {\n    listen 80;\n}\nserver {\n    listen 10.8.0.1:443 ssl;\n}\n",
		"адрес не разобран": marker + "\nserver {\n    listen 10.8.0.300:443 ssl;\n}\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := PublicFromConfig(content); !errors.Is(err, ErrBadConfig) {
				t.Fatalf("ошибка %v, ожидалась ErrBadConfig", err)
			}
		})
	}
}
