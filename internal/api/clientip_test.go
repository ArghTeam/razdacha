package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientKey(t *testing.T) {
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:   "прямое соединение без прокси",
			remote: "203.0.113.7:41233",
			want:   "203.0.113.7",
		},
		{
			// Тот самый случай, ради которого заголовкам верят только от loopback.
			name:    "подделка заголовков не с loopback",
			remote:  "203.0.113.7:41233",
			headers: map[string]string{"X-Real-IP": "10.0.0.1", "X-Forwarded-For": "10.0.0.2"},
			want:    "203.0.113.7",
		},
		{
			name:    "X-Real-IP от nginx",
			remote:  "127.0.0.1:52344",
			headers: map[string]string{"X-Real-IP": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			// В X-Forwarded-For начало списка приходит от клиента, конец
			// дописывает nginx — берём конец.
			name:    "последний элемент X-Forwarded-For",
			remote:  "127.0.0.1:52344",
			headers: map[string]string{"X-Forwarded-For": "10.0.0.9, 198.51.100.4, 203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:   "loopback без заголовков",
			remote: "127.0.0.1:52344",
			want:   "127.0.0.1",
		},
		{
			name:    "мусор в заголовке",
			remote:  "127.0.0.1:52344",
			headers: map[string]string{"X-Real-IP": "не адрес"},
			want:    "127.0.0.1",
		},
		{
			name:   "v4 в v6-обёртке — та же корзина",
			remote: "[::ffff:203.0.113.7]:41233",
			want:   "203.0.113.7",
		},
		{
			name:   "неразобранный адрес",
			remote: "какая-то строка",
			want:   unknownClient,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
			r.RemoteAddr = c.remote
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if got := clientKey(r); got != c.want {
				t.Errorf("адрес клиента = %q, ожидался %q", got, c.want)
			}
		})
	}
}
