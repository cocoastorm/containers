package lease

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const Lifetime = 60

type Protocol uint8

const (
	UDP Protocol = 1
	TCP Protocol = 2
)

func (p Protocol) String() string {
	if p == TCP {
		return "tcp"
	}
	return "udp"
}

// Grant's Expiry retains Go's monotonic clock component.
type Grant struct {
	Internal, Public uint16
	Lifetime         uint32
	Epoch            uint32
	Expiry           time.Time
}

type Mapper interface {
	Map(context.Context, Protocol, uint16, uint16) (Grant, error)
}

type Client struct {
	Gateway net.IP
	port    int
}

// Map uses a fresh connected UDP socket for every exchange: only the configured
// gateway can reply, and an old reply cannot be mistaken for a new request.
func (c Client) Map(ctx context.Context, proto Protocol, internal, public uint16) (Grant, error) {
	if c.Gateway.To4() == nil || internal == 0 || (proto != TCP && proto != UDP) {
		return Grant{}, errors.New("invalid mapping request")
	}
	port := c.port
	if port == 0 {
		port = 5351
	}
	addr := &net.UDPAddr{IP: c.Gateway.To4(), Port: port}
	var req [12]byte
	req[1] = byte(proto)
	binary.BigEndian.PutUint16(req[4:6], internal)
	binary.BigEndian.PutUint16(req[6:8], public)
	binary.BigEndian.PutUint32(req[8:12], Lifetime)
	// RFC 6886 retransmissions are bounded by a single short operation deadline.
	opctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var last error
	for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second} {
		if err := opctx.Err(); err != nil {
			return Grant{}, fmt.Errorf("transport_timeout: %w", err)
		}
		conn, err := (&net.Dialer{}).DialContext(opctx, "udp4", addr.String())
		if err != nil {
			return Grant{}, fmt.Errorf("transport: %w", err)
		}
		stop := context.AfterFunc(opctx, func() { _ = conn.Close() })
		deadline := time.Now().Add(delay)
		if d, ok := opctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)
		_, err = conn.Write(req[:])
		var buf [32]byte
		var n int
		if err == nil {
			n, err = conn.Read(buf[:])
		}
		stop()
		_ = conn.Close()
		if err != nil {
			last = err
			if opctx.Err() != nil {
				break
			}
			continue
		}
		if n != 16 || buf[0] != 0 || buf[1] != byte(proto)+128 {
			return Grant{}, fmt.Errorf("reply_mismatch: length=%d opcode=%d", n, buf[1])
		}
		result := binary.BigEndian.Uint16(buf[2:4])
		if result != 0 {
			return Grant{}, fmt.Errorf("natpmp_rejection: code=%d", result)
		}
		g := Grant{Internal: binary.BigEndian.Uint16(buf[8:10]), Public: binary.BigEndian.Uint16(buf[10:12]), Lifetime: binary.BigEndian.Uint32(buf[12:16]), Epoch: binary.BigEndian.Uint32(buf[4:8])}
		if g.Internal != internal || g.Public == 0 || g.Lifetime == 0 {
			return Grant{}, fmt.Errorf("reply_mismatch: expected_internal=%d returned_internal=%d returned_public=%d lifetime=%d", internal, g.Internal, g.Public, g.Lifetime)
		}
		g.Expiry = time.Now().Add(time.Duration(g.Lifetime) * time.Second)
		return g, nil
	}
	return Grant{}, fmt.Errorf("transport_timeout: %w", last)
}
