package lists

import "testing"

func TestIgareckTitle(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "фрагмент с эмодзи и percent-encoding",
			line: "vless://uuid@sof.edgeptr.org:443?type=tcp#%F0%9F%87%A7%F0%9F%87%AC%20Bulgaria%2C%20Sofia%20%7C%20%5BBL%5D",
			want: "🇧🇬 Bulgaria, Sofia | [BL]",
		},
		{
			name: "фрагмент с %20 и запятыми",
			line: "vless://uuid@example.org:443?type=tcp#Germany%2C%20Frankfurt",
			want: "Germany, Frankfurt",
		},
		{
			name: "без фрагмента — fallback на хост:порт",
			line: "vless://uuid@example.org:443?type=tcp",
			want: "example.org:443",
		},
		{
			name: "пустой фрагмент — fallback на хост:порт",
			line: "vless://uuid@example.org:443?type=tcp#",
			want: "example.org:443",
		},
		{
			name: "фрагмент из пробелов сжимается в пустую строку",
			line: "vless://uuid@example.org:443#%20%20",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := igareckTitle(tc.line); got != tc.want {
				t.Fatalf("igareckTitle(%q) = %q, хотели %q", tc.line, got, tc.want)
			}
		})
	}
}
