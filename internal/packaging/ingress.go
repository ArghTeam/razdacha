package packaging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

// CheckIngressUninstall refuses removal of the safety hook while a saved public
// listener or uncertain authority could outlive the daemon. It performs no writes.
func CheckIngressUninstall(root string) error {
	configPath := filepath.Join(root, "/etc/sing-box/config.json")
	manifestPath := filepath.Join(root, "/var/lib/razdacha/public-ingress.json")
	manifest, manifestErr := ingressguard.Read(manifestPath)
	config, configErr := os.ReadFile(configPath)
	refuse := func(err error) error {
		return fmt.Errorf("disable Reality and Apply before uninstall; public ingress safety cannot be removed: %w", err)
	}
	if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		return refuse(configErr)
	}
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return refuse(manifestErr)
	}
	if errors.Is(configErr, os.ErrNotExist) {
		if errors.Is(manifestErr, os.ErrNotExist) {
			return nil
		}
		return refuse(errors.New("saved configuration is missing"))
	}
	endpoints, err := ingressguard.ConfigEndpoints(config)
	if err != nil {
		return refuse(err)
	}
	if len(endpoints) > 0 || len(manifest.Endpoints) > 0 {
		return refuse(errors.New("public listener coverage is present"))
	}
	if manifestErr == nil {
		if err := ingressguard.ValidateConfig(config, manifest); err != nil {
			return refuse(err)
		}
	}
	return nil
}
