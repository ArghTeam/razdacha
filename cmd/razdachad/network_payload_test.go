package main

import (
	"strings"
	"testing"
)

// wirePayloadDetector carries enough bytes across TCP reads to identify forbidden
// epoch markers regardless of fragmentation or coalescing. Each connection owns one.
type wirePayloadDetector struct {
	tail string
	seen bool
}

func (d *wirePayloadDetector) feed(chunk []byte) bool {
	const longestMarker = len("blocked-final-")
	combined := d.tail + string(chunk)
	d.seen = d.seen || strings.Contains(combined, "guarded-") || strings.Contains(combined, "blocked-final-")
	if len(combined) >= longestMarker {
		d.tail = combined[len(combined)-(longestMarker-1):]
	} else {
		d.tail = combined
	}
	return d.seen
}

func TestWirePayloadDetectorReassemblesForbiddenEpochs(t *testing.T) {
	for _, marker := range []string{"guarded-established-tcp4", "blocked-final-established-tcp4"} {
		for split := 0; split <= len(marker); split++ {
			var detector wirePayloadDetector
			detector.feed([]byte("initial-control-" + marker[:split]))
			if !detector.feed([]byte(marker[split:] + "coalesced-control")) {
				t.Fatalf("forbidden epoch missed at split %d: %s", split, marker)
			}
		}
		var detector wirePayloadDetector
		for _, b := range []byte("initial" + marker + "control") {
			detector.feed([]byte{b})
		}
		if !detector.seen {
			t.Fatalf("one-byte TCP reads concealed marker: %s", marker)
		}
	}
	var benign, other wirePayloadDetector
	for _, chunk := range []string{"initial-", "control-final-", "unrelated.example"} {
		if benign.feed([]byte(chunk)) {
			t.Fatal("benign epoch flagged")
		}
	}
	var first wirePayloadDetector
	if first.feed([]byte("guar")) || other.feed([]byte("ded-")) {
		t.Fatal("marker incorrectly crossed connection boundaries")
	}
}
