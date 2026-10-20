package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/protocol/socks"
)

// A SOCKS UDP ASSOCIATE control address is a bind hint, not the UDP destination.
// The pinned client uses an IPv6-unspecified bind for a loopback DNS destination,
// even though the actual DNS payload destination and relay socket are IPv4.
func readEgressSOCKSRequest(r io.Reader) (byte, string, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, "", err
	}
	if header[0] != 5 || header[2] != 0 {
		return 0, "", fmt.Errorf("invalid SOCKS request header")
	}
	var size int
	switch header[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	default:
		return 0, "", fmt.Errorf("unsupported SOCKS request address type")
	}
	address := make([]byte, size+2)
	if _, err := io.ReadFull(r, address); err != nil {
		return 0, "", err
	}
	return header[1], net.JoinHostPort(net.IP(address[:size]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(address[size:])))), nil
}

func TestEgressFixtureParsesPinnedSOCKSUDPAssociation(t *testing.T) {
	for _, destination := range []string{"203.0.113.20:12080", "127.0.0.1:15353"} {
		t.Run(destination, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			_ = server.SetDeadline(time.Now().Add(time.Second))
			done := make(chan error, 1)
			go func() {
				_, err := socks.ClientHandshake5(client, 3, M.ParseSocksaddr(destination), "", "")
				done <- err
			}()
			var auth [3]byte
			if _, err := io.ReadFull(server, auth[:]); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(auth[:], []byte{5, 1, 0}) {
				t.Fatal("unexpected pinned authentication request")
			}
			if _, err := server.Write([]byte{5, 0}); err != nil {
				t.Fatal(err)
			}
			command, target, err := readEgressSOCKSRequest(server)
			if err != nil {
				t.Fatal("fixture rejected pinned UDP control request", err)
			}
			want := "0.0.0.0:0"
			if destination == "127.0.0.1:15353" {
				want = "[::]:0"
			}
			if command != 3 || target != want {
				t.Fatalf("association command=%d target=%s want=%s", command, target, want)
			}
			if _, err := server.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal("pinned client did not accept relay response", err)
			}
		})
	}
}

func TestEgressFixtureRejectsMalformedSOCKSRequest(t *testing.T) {
	for _, raw := range [][]byte{{5, 3, 0}, {4, 3, 0, 1, 0, 0, 0, 0, 0, 0}, {5, 3, 1, 1, 0, 0, 0, 0, 0, 0}, {5, 3, 0, 3, 1, 'x', 0, 0}, {5, 3, 0, 4, 0, 0, 0}} {
		if _, _, err := readEgressSOCKSRequest(bytes.NewReader(raw)); err == nil {
			t.Fatal("malformed or unsupported request accepted")
		}
	}
}
