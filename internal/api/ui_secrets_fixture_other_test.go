//go:build !linux

package api

import "testing"

func isolateUIFixture(t *testing.T) bool {
	t.Helper()
	t.Fatal("isolated UI fixture requires Linux")
	return true
}
