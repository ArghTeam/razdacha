package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

// qualityPayload is printable and newline-framed so the independent sink capture
// can record it without losing binary delimiters. Identity includes the stage,
// source and protocol supplied by the caller rather than just the payload size.
func qualityPayload(size int, identity string) []byte {
	if size < 16 || size > 1400 {
		panic("quality payload outside bounded matrix")
	}
	seed := sha256.Sum256([]byte(identity))
	encoded := fmt.Sprintf("%x", seed)
	body := make([]byte, size)
	for i := range body {
		body[i] = encoded[i%len(encoded)]
	}
	body[size-1] = '\n'
	return body
}

func qualityExact(actual, expected []byte) bool {
	return bytes.Equal(actual, expected) && sha256.Sum256(actual) == sha256.Sum256(expected)
}

func TestQualityPayloadOracle(t *testing.T) {
	payload := qualityPayload(1253, "client-A/tcp/frame-3")
	if len(payload) != 1253 || payload[len(payload)-1] != '\n' {
		t.Fatal("payload framing is not exact")
	}
	for _, b := range payload[:len(payload)-1] {
		if b < '0' || b > 'f' || b > '9' && b < 'a' {
			t.Fatal("payload is not capture-safe hexadecimal")
		}
	}
	if !qualityExact(append([]byte(nil), payload...), payload) {
		t.Fatal("exact copy rejected")
	}
	corrupted := append([]byte(nil), payload...)
	corrupted[701] ^= 1
	cases := map[string][]byte{"corrupted": corrupted, "truncated": payload[:len(payload)-1], "extended": append(append([]byte(nil), payload...), 'x'), "wrong-source": qualityPayload(1253, "client-B/tcp/frame-3"), "wrong-frame": qualityPayload(1253, "client-A/tcp/frame-4")}
	for name, actual := range cases {
		t.Run(name, func(t *testing.T) {
			if qualityExact(actual, payload) {
				t.Fatal("invalid payload passed exact integrity oracle")
			}
		})
	}
}
