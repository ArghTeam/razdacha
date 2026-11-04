package store

import "testing"

func TestPoolFilterStructuredAndTitleCountryAreConservative(t *testing.T) {
	f := PoolFilter{Countries: DefaultPoolCountryBlocklist()}
	for _, s := range []PoolServer{{Country: " ru ", Title: "DE"}, {Country: "DE", Title: "RU"}, {Country: "BY", Title: "US"}} {
		if f.Allows(s) {
			t.Fatalf("conflicting country accepted: %+v", s)
		}
	}
	for _, s := range []PoolServer{{Country: "DE", Title: "DE"}, {Title: "Frankfurt by provider"}, {Country: "", Title: "unknown"}} {
		if !f.Allows(s) {
			t.Fatalf("unblocked country rejected: %+v", s)
		}
	}
}
