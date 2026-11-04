package main

import (
	"path/filepath"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/packaging"
)

// closeRestoreIngress is the production restore boundary. It guards WG first
// and then public ingress without opening the source or replacement database.
func closeRestoreIngress(dbPath, configPath string, guardWG func() error, guardPublic func([]ingressguard.Endpoint) error) error {
	if err := guardWG(); err != nil {
		return err
	}
	path, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	return packaging.GuardSavedIngress(configPath, filepath.Join(filepath.Dir(path), "public-ingress.json"), guardPublic)
}
