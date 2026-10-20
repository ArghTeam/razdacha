//go:build !linux

package main

import "testing"

func TestRestoreWireCLI(t *testing.T) {
	t.Skip("requires the contained Linux parent fixture")
}
