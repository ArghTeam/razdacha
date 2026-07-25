package lists

import (
	"reflect"
	"testing"
)

func TestParsePlain(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		domains []string
		subnets []string
		skipped int
	}{
		{
			name:    "подсети",
			body:    "104.16.0.0/13\n1.1.1.1\n8.8.8.0/24\n",
			subnets: []string{"1.1.1.1/32", "104.16.0.0/13", "8.8.8.0/24"},
		},
		{
			name:    "домены",
			body:    "youtube.com\n*.googlevideo.com\n.ytimg.com\n",
			domains: []string{"googlevideo.com", "youtube.com", "ytimg.com"},
		},
		{
			name:    "домены и подсети вперемешку",
			body:    "telegram.org\n91.108.4.0/22\n",
			domains: []string{"telegram.org"},
			subnets: []string{"91.108.4.0/22"},
		},
		{
			name:    "комментарии и пустые строки",
			body:    "# заголовок\n\n; точка с запятой\n// слэши\nexample.com\n1.2.3.0/24 # cloudflare\n",
			domains: []string{"example.com"},
			subnets: []string{"1.2.3.0/24"},
		},
		{
			name:    "CRLF и пробелы",
			body:    "  example.com  \r\n\t10.0.0.0/8\r\n",
			domains: []string{"example.com"},
			subnets: []string{"10.0.0.0/8"},
		},
		{
			name:    "подсеть с хвостом хоста нормализуется",
			body:    "192.168.1.5/24\n",
			subnets: []string{"192.168.1.0/24"},
		},
		{
			name:    "IPv6 отбрасывается",
			body:    "2606:4700::/32\n2001:db8::1\n1.1.1.0/24\n",
			subnets: []string{"1.1.1.0/24"},
			skipped: 2,
		},
		{
			name:    "мусор считается, но не ломает разбор",
			body:    "!!!\nexample.com\n999.999.999.999/99\n",
			domains: []string{"example.com"},
			skipped: 2,
		},
		{
			name: "пустое тело",
			body: "",
		},
		{
			name:    "повторы схлопываются",
			body:    "example.com\nexample.com\n1.1.1.0/24\n1.1.1.0/24\n",
			domains: []string{"example.com"},
			subnets: []string{"1.1.1.0/24"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePlain([]byte(tt.body))
			if !reflect.DeepEqual(got.Domains, tt.domains) {
				t.Errorf("домены: получено %v, ожидалось %v", got.Domains, tt.domains)
			}
			if !reflect.DeepEqual(got.Subnets, tt.subnets) {
				t.Errorf("подсети: получено %v, ожидалось %v", got.Subnets, tt.subnets)
			}
			if got.Skipped != tt.skipped {
				t.Errorf("пропущено строк: получено %d, ожидалось %d", got.Skipped, tt.skipped)
			}
		})
	}
}

func TestParseJSONRuleSet(t *testing.T) {
	body := `{"version":2,"rules":[{"domain_suffix":["example.com"],"ip_cidr":["1.1.1.0/24","2606:4700::/32"]}]}`

	got, err := Parse([]byte(body), FormatJSON)
	if err != nil {
		t.Fatalf("разбор набора: %v", err)
	}
	if want := []string{"example.com"}; !reflect.DeepEqual(got.Domains, want) {
		t.Errorf("домены: получено %v, ожидалось %v", got.Domains, want)
	}
	if want := []string{"1.1.1.0/24"}; !reflect.DeepEqual(got.Subnets, want) {
		t.Errorf("подсети: получено %v, ожидалось %v", got.Subnets, want)
	}
	if got.Skipped != 1 {
		t.Errorf("пропущено: получено %d, ожидалось 1 (IPv6)", got.Skipped)
	}
}

func TestParseBadInput(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		format Format
	}{
		{name: "не .srs", body: "мусор", format: FormatSRS},
		{name: "не .json", body: "{", format: FormatJSON},
		{name: "набор без версии", body: `{"rules":[]}`, format: FormatJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.body), tt.format); err == nil {
				t.Fatal("ожидалась ошибка разбора")
			}
		})
	}
}

func TestFormatOf(t *testing.T) {
	tests := []struct {
		url  string
		want Format
	}{
		{"https://example.com/list.srs", FormatSRS},
		{"https://example.com/list.SRS", FormatSRS},
		{"https://example.com/list.json", FormatJSON},
		{"https://example.com/list.lst", FormatPlain},
		{"https://example.com/list.txt?v=2", FormatPlain},
		{"https://example.com/list", FormatPlain},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := FormatOf(tt.url); got != tt.want {
				t.Errorf("получено %q, ожидалось %q", got, tt.want)
			}
		})
	}
}

func TestCommunitySubnetURL(t *testing.T) {
	if _, ok := CommunitySubnetURL("youtube"); ok {
		t.Error("у youtube в allow-domains нет списка подсетей")
	}
	url, ok := CommunitySubnetURL("telegram")
	if !ok {
		t.Fatal("у telegram список подсетей есть")
	}
	if want := "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Subnets/IPv4/telegram.lst"; url != want {
		t.Errorf("получено %q, ожидалось %q", url, want)
	}
}
