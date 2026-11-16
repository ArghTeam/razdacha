//go:build !linux

package dnsruntime

import "testing"

func realityNamespace(t *testing.T) { t.Helper() }
