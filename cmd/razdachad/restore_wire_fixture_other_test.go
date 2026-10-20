//go:build !linux

package main

import "testing"

func TestRestoreWireGuardContained(t *testing.T) {
	t.Skip("requires separately reviewed Linux network and mount namespace runner")
}
