package singbox

import (
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// TestRuleInConfig — попадёт ли правило в конфиг. Ответ нужен пробнику
// маршрута: он сверяет число правил в живом ядре с состоянием БД, и правило
// без единого условия расхождением не является — применять там нечего
// (issue #149).
//
// Условие проверяется тем же проходом, что и генерация (buildRuleSets),
// поэтому разъехаться с генератором функция может только вместе с ним.
func TestRuleInConfig(t *testing.T) {
	tests := []struct {
		name  string
		rule  store.Rule
		plain PlainLists
		want  bool
	}{
		{
			name: "свои домены",
			rule: store.Rule{ID: "r1", Enabled: true, Domains: []string{"example.com"}},
			want: true,
		},
		{
			name: "community-список",
			rule: store.Rule{ID: "r2", Enabled: true, CommunityLists: []string{"youtube"}},
			want: true,
		},
		{
			name: "внешний .srs ведёт сам sing-box",
			rule: store.Rule{ID: "r3", Enabled: true, RemoteLists: []string{"https://ex.com/a.srs"}},
			want: true,
		},
		{
			name:  "plain-список скачан",
			rule:  store.Rule{ID: "r4", Enabled: true, RemoteLists: []string{plainListURL}},
			plain: testPlain,
			want:  true,
		},
		{
			// Ровно тот случай, ради которого функция и заведена: условий не
			// осталось, генератор правило пропускает, применять нечего.
			name: "plain-список не скачан",
			rule: store.Rule{ID: "r5", Enabled: true, RemoteLists: []string{plainListURL}},
			want: false,
		},
		{
			name: "правило без условий",
			rule: store.Rule{ID: "r6", Enabled: true},
			want: false,
		},
		{
			name: "выключенное правило",
			rule: store.Rule{ID: "r7", Enabled: false, Domains: []string{"example.com"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RuleInConfig(tt.rule, tt.plain); got != tt.want {
				t.Errorf("RuleInConfig = %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

// TestRuleInConfigAgreesWithGenerator — сторож против расхождения: то, что
// функция считает попадающим в конфиг, обязано совпасть с числом правил,
// которые генератор действительно положил в route.rules (без служебного
// перехвата DNS). Разойдись они — пробник врал бы про непримененные правки.
func TestRuleInConfigAgreesWithGenerator(t *testing.T) {
	snap := plainOnly()
	snap.Rules = append(snap.Rules, store.Rule{
		ID: "r2", Name: "Свои домены", Action: store.ActionTunnel, TunnelID: "aaaa",
		Enabled: true, Domains: []string{"example.com"}, PeerScope: store.ScopeAll,
	})

	for _, tt := range []struct {
		name  string
		plain PlainLists
	}{
		{name: "plain-список скачан", plain: testPlain},
		{name: "plain-список не скачан", plain: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts, _, err := GenerateWithDiag(snap, tt.plain)
			if err != nil {
				t.Fatalf("генерация: %v", err)
			}
			// Первое правило route.rules — перехват DNS, он не из БД.
			inConfig := len(opts.Route.Rules) - 1

			counted := 0
			for _, r := range snap.Rules {
				if RuleInConfig(r, tt.plain) {
					counted++
				}
			}
			if counted != inConfig {
				t.Errorf("RuleInConfig насчитал %d, генератор положил %d", counted, inConfig)
			}
		})
	}
}
