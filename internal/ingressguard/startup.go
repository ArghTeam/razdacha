package ingressguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
)

// ConfigEndpoints identifies all supported client listeners. Legacy DNS binds an
// explicit IPv4 address; TProxy and controllers remain loopback-scoped. Unknown
// inbound types fail closed.
// This is not a replacement for sing-box check or authentication generation.
func ConfigEndpoints(config []byte) ([]Endpoint, error) {
	if len(config) == 0 || len(config) > maxConfigBytes {
		return nil, errors.New("public ingress configuration size is invalid")
	}
	if err := uniqueJSON(config); err != nil {
		return nil, fmt.Errorf("decode public ingress configuration: %w", err)
	}
	var options struct {
		Inbounds []struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			Listen     string `json:"listen"`
			ListenPort uint16 `json:"listen_port"`
			TLS        struct {
				Enabled bool `json:"enabled"`
				Reality struct {
					Enabled bool `json:"enabled"`
				} `json:"reality"`
			} `json:"tls"`
		} `json:"inbounds"`
		Experimental map[string]json.RawMessage `json:"experimental"`
	}
	if err := json.Unmarshal(config, &options); err != nil {
		return nil, fmt.Errorf("decode public ingress listeners: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil || root == nil {
		return nil, errors.New("public ingress configuration must be an object")
	}
	if err := privateControllers(options.Experimental); err != nil {
		return nil, err
	}
	var endpoints []Endpoint
	for _, inbound := range options.Inbounds {
		address, parseErr := netip.ParseAddr(inbound.Listen)
		switch inbound.Type {
		case "direct":
			if inbound.Tag != "dns-in" || inbound.ListenPort != 53 || parseErr != nil || !legacyDNSAddress(address) {
				return nil, errors.New("legacy DNS listener must bind an explicit unicast IPv4 address on port 53")
			}
		case "tproxy":
			if inbound.Tag != "tproxy-in" || inbound.ListenPort == 0 || parseErr != nil || address.Zone() != "" || !address.IsLoopback() {
				return nil, errors.New("legacy transparent listener must be loopback-scoped")
			}
		case "vless":
			if !inbound.TLS.Enabled || !inbound.TLS.Reality.Enabled {
				return nil, errors.New("public VLESS listener requires REALITY TLS")
			}
			endpoints = append(endpoints, Endpoint{Address: inbound.Listen, Port: inbound.ListenPort})
		default:
			return nil, errors.New("unsupported inbound listener in public ingress configuration")
		}
	}
	return NormalizeEndpoints(endpoints)
}

// The existing WG address contract includes CGNAT and public IPv4 ranges. The
// startup helper cannot depend on a live wg0 or DB before service startup; only
// the exact legacy DNS identity from owner-controlled config gets this exception.
func legacyDNSAddress(address netip.Addr) bool {
	return address.Is4() && address.Zone() == "" && !address.IsUnspecified() &&
		!address.IsMulticast() && address != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func privateControllers(experimental map[string]json.RawMessage) error {
	for name, data := range experimental {
		switch name {
		case "cache_file":
			continue
		case "clash_api", "v2ray_api":
			var api struct {
				ExternalController string `json:"external_controller"`
				Listen             string `json:"listen"`
			}
			if err := json.Unmarshal(data, &api); err != nil {
				return errors.New("invalid runtime controller configuration")
			}
			for _, listener := range []string{api.ExternalController, api.Listen} {
				if listener == "" {
					continue
				}
				address, err := netip.ParseAddrPort(listener)
				if err != nil || !address.Addr().IsLoopback() || address.Addr().Zone() != "" || address.Port() == 0 {
					return errors.New("runtime controller must remain loopback-scoped")
				}
			}
		default:
			return errors.New("unsupported experimental service in public ingress configuration")
		}
	}
	return nil
}

// ValidateConfig requires exact checked bytes and complete listener coverage.
// Extra old endpoints are intentional during a guarded listener transition.
func ValidateConfig(config []byte, manifest Manifest) error {
	manifest, err := normalizeManifest(manifest)
	if err != nil {
		return err
	}
	hash := HashConfig(config)
	approved := false
	for _, candidate := range manifest.ConfigHashes {
		approved = approved || candidate == hash
	}
	if !approved {
		return errors.New("runtime configuration is not authorized by public ingress manifest")
	}
	endpoints, err := ConfigEndpoints(config)
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		covered := false
		for _, allowed := range manifest.Endpoints {
			covered = covered || covers(allowed, endpoint)
		}
		if !covered {
			return errors.New("runtime listener is outside public ingress guard coverage")
		}
	}
	return nil
}

// StartupEndpoints reads exact private configuration and startup authority.
// The caller must hold WithLock until the returned endpoints are installed.
// A missing manifest is accepted only for the explicit legacy WG-only shape.
func StartupEndpoints(configPath, manifestPath string) ([]Endpoint, error) {
	_, endpoints, err := startupState(configPath, manifestPath)
	return endpoints, err
}

func startupState(configPath, manifestPath string) (*Manifest, []Endpoint, error) {
	config, err := readControlled(configPath, maxConfigBytes, false)
	if err != nil {
		return nil, nil, fmt.Errorf("read runtime ingress configuration: %w", err)
	}
	endpoints, err := ConfigEndpoints(config)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := Read(manifestPath)
	if errors.Is(err, os.ErrNotExist) && len(endpoints) == 0 {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read runtime ingress authority: %w", err)
	}
	if err := ValidateConfig(config, manifest); err != nil {
		return nil, nil, err
	}
	return &manifest, manifest.Endpoints, nil
}

// Startup closes ingress before an independently started runtime can bind. It
// performs no DB access, daemon RPC, runtime restart or application-gate locking.
// The short file lock includes authority reads, the marker and kernel callback.
func Startup(configPath, manifestPath string, install func([]Endpoint) error) error {
	if install == nil {
		return errors.New("public ingress installer is required")
	}
	return WithLock(manifestPath, func() error {
		manifest, endpoints, err := startupState(configPath, manifestPath)
		if err != nil {
			return err
		}
		if manifest == nil {
			// Never clear an earlier barrier merely because a legacy config is seen.
			return nil
		}
		manifest.Guarded = true
		if err := Write(manifestPath, *manifest); err != nil {
			return err
		}
		if err := install(endpoints); err != nil {
			return fmt.Errorf("install startup public ingress guard: %w", err)
		}
		return nil
	})
}
