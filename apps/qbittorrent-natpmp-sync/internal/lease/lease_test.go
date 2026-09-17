package lease

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMapReplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte)
		want   string
	}{
		{"valid", nil, ""},
		{"rejected", func(b []byte) { b[3] = 2 }, "natpmp_rejection"},
		{"wrong internal", func(b []byte) { b[9] = 9 }, "reply_mismatch"},
		{"zero public", func(b []byte) { b[10] = 0; b[11] = 0 }, "reply_mismatch"},
		{"zero lifetime", func(b []byte) { b[12] = 0; b[13] = 0; b[14] = 0; b[15] = 0 }, "reply_mismatch"},
		{"wrong opcode", func(b []byte) { b[1] = 129 }, "reply_mismatch"},
		{"wrong version", func(b []byte) { b[0] = 1 }, "reply_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			go func() {
				var req [12]byte
				n, addr, err := server.ReadFromUDP(req[:])
				if err != nil || n != 12 {
					return
				}
				b := make([]byte, 16)
				b[1] = 130
				binary.BigEndian.PutUint32(b[4:8], 100)
				copy(b[8:10], req[4:6])
				binary.BigEndian.PutUint16(b[10:12], 52837)
				binary.BigEndian.PutUint32(b[12:16], 5)
				if tc.mutate != nil {
					tc.mutate(b)
				}
				_, _ = server.WriteToUDP(b, addr)
			}()
			g, err := (Client{Gateway: net.IPv4(127, 0, 0, 1), port: server.LocalAddr().(*net.UDPAddr).Port}).Map(context.Background(), TCP, 55873, 0)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil || g.Internal != 55873 || g.Public != 52837 || g.Lifetime != 5 || remainingTime(g) < 4*time.Second {
				t.Fatalf("grant=%+v error=%v", g, err)
			}
		})
	}
}
func remainingTime(g Grant) time.Duration { return time.Until(g.Expiry) }

func TestCancellation(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = (Client{Gateway: net.IPv4(127, 0, 0, 1), port: server.LocalAddr().(*net.UDPAddr).Port}).Map(ctx, TCP, 55873, 0)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("error=%v elapsed=%s", err, time.Since(start))
	}
}
