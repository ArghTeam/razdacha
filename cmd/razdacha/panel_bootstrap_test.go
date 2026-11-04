package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/packaging"
)

func TestFreshPanelBootstrapAndSavedUpgradeModes(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		fresh, free, explicit, private, saved, want bool
	}{
		{name: "fresh_free", fresh: true, free: true, want: true},
		{name: "fresh_skip", fresh: true, want: true},
		{name: "fresh_explicit_private", fresh: true, free: true, explicit: true, private: true, want: false},
		{name: "fresh_skip_explicit_private", fresh: true, explicit: true, private: true, want: false},
		{name: "upgrade_private", saved: false, want: false},
		{name: "upgrade_public", saved: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := testStore(t)
			if !tc.fresh {
				if err := st.SetPanelPublic(ctx, tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			opts := effectivePanelOptions(tc.fresh, setupOptions{freeStart: tc.free, publicSet: tc.explicit, public: !tc.private && tc.explicit})
			inst := packaging.NewInstaller(t.TempDir())
			mode, err := ensurePanelMode(ctx, st, inst, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil || mode.Public != tc.want {
				t.Fatalf("panel %+v error %v", mode, err)
			}
			saved, known, err := st.PanelPublic(ctx)
			if err != nil || !known || saved != tc.want {
				t.Fatal("effective panel mode not persisted")
			}
		})
	}
}

func TestPanelBootstrapEnvironmentAndExplicitFlags(t *testing.T) {
	t.Setenv("RAZDACHA_PUBLIC", "0")
	opts, err := parseSetupFlags([]string{"--free-start=true"})
	if err != nil {
		t.Fatal(err)
	}
	opts = effectivePanelOptions(true, opts)
	if opts.public || !opts.publicSet {
		t.Fatal("explicit private environment ignored")
	}
	opts, err = parseSetupFlags([]string{"--public=true"})
	if err != nil || !opts.public {
		t.Fatal("explicit public flag lost priority")
	}
	t.Setenv("RAZDACHA_PUBLIC", "1")
	opts, err = parseSetupFlags([]string{"--public=false"})
	if err != nil || effectivePanelOptions(true, opts).public {
		t.Fatal("explicit private flag lost priority")
	}
}

func TestPanelAccessAndPasswordPrecedeVPNConfiguration(t *testing.T) {
	out, err := (summary{Fresh: true, PanelPublic: true, ExternalPanelURL: "https://panel.example", PanelURL: "https://10.8.0.1", Password: "synthetic-test-password", ClientConfig: sampleConfig, PeerConfig: "/example/client.conf"}).Render()
	if err != nil {
		t.Fatal(err)
	}
	public := strings.Index(out, "https://panel.example")
	password := strings.Index(out, "synthetic-test-password")
	client := strings.Index(out, "WireGuard:")
	file := strings.Index(out, "/example/client.conf")
	if public < 0 || password < 0 || client < 0 || file < 0 || public > client || password > client || public > file || password > file {
		t.Fatal("initial panel access is hidden behind client configuration")
	}
}
