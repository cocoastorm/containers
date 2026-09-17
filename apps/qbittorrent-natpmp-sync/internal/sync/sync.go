package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/lease"
	"github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/qbittorrent"
)

type LoopStatus struct {
	LastSuccess *time.Time `json:"last_success"`
	LastError   string     `json:"last_error,omitempty"`
	Attempts    uint64     `json:"failure_attempts"`
}
type Status struct {
	State               string     `json:"state"`
	Reason              string     `json:"reason,omitempty"`
	InternalPort        uint16     `json:"internal_port"`
	PublicPort          uint16     `json:"public_port,omitempty"`
	TCPExpiresInSeconds float64    `json:"tcp_expires_in_seconds"`
	UDPExpiresInSeconds float64    `json:"udp_expires_in_seconds"`
	Mapping             LoopStatus `json:"mapping"`
	QBittorrent         LoopStatus `json:"qbittorrent"`
}

type Service struct {
	mu                 sync.Mutex
	Mapper             lease.Mapper
	API                qbittorrent.API
	Internal           uint16
	Log                *slog.Logger
	tcp, udp           lease.Grant
	epoch              uint32
	retryTCP, retryUDP time.Time
	mapping, qb        LoopStatus
	synced             uint16
	changed            chan struct{}
	now                func() time.Time
}

func New(mapper lease.Mapper, api qbittorrent.API, internal uint16, log *slog.Logger) *Service {
	return &Service{Mapper: mapper, API: api, Internal: internal, Log: log, changed: make(chan struct{}, 1), now: time.Now}
}

func remaining(g lease.Grant, now time.Time) time.Duration {
	if g.Public == 0 || !g.Expiry.After(now) {
		return 0
	}
	return g.Expiry.Sub(now)
}
func (s *Service) current(now time.Time) uint16 {
	if remaining(s.tcp, now) == 0 || remaining(s.udp, now) == 0 || s.tcp.Public != s.udp.Public {
		return 0
	}
	return s.tcp.Public
}
func (s *Service) port() uint16 { s.mu.Lock(); defer s.mu.Unlock(); return s.current(s.now()) }
func (s *Service) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	tcp, udp := remaining(s.tcp, now), remaining(s.udp, now)
	p := s.current(now)
	st := Status{State: "degraded", InternalPort: s.Internal, PublicPort: p, TCPExpiresInSeconds: tcp.Seconds(), UDPExpiresInSeconds: udp.Seconds(), Mapping: s.mapping, QBittorrent: s.qb}
	switch {
	case p == 0:
		st.Reason = "mapping_unavailable"
	case s.mapping.LastError != "":
		st.Reason = "mapping_error"
	case s.qb.LastError != "":
		st.Reason = "qbittorrent_error"
	case s.synced != p:
		st.Reason = "announce_port_not_synchronized"
	default:
		st.State = "healthy"
	}
	return st
}
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Snapshot())
	})
}

func (s *Service) failure(loop *LoopStatus, event, reason string) {
	if loop.LastError != reason {
		s.Log.Warn(event, "reason", reason, "attempts", loop.Attempts+1)
	}
	loop.LastError = reason
	loop.Attempts++
}
func (s *Service) success(loop *LoopStatus, event string) {
	if loop.LastError != "" {
		s.Log.Info(event, "recovered_from", loop.LastError, "attempts", loop.Attempts)
	}
	now := s.now().UTC()
	loop.LastSuccess = &now
	loop.LastError = ""
	loop.Attempts = 0
}

// store never treats a partial pair as healthy; each successful protocol
// renewal updates only its own expiry. An epoch rollback invalidates the peer.
func (s *Service) store(proto lease.Protocol, g lease.Grant) {
	s.mu.Lock()
	before := s.current(s.now())
	if s.epoch != 0 && g.Epoch < s.epoch {
		s.tcp = lease.Grant{}
		s.udp = lease.Grant{}
		s.synced = 0
		s.retryTCP = time.Time{}
		s.retryUDP = time.Time{}
		s.Log.Warn("gateway_epoch_reset", "previous_epoch", s.epoch, "new_epoch", g.Epoch)
	}
	s.epoch = g.Epoch
	if proto == lease.TCP {
		s.tcp = g
	} else {
		s.udp = g
	}
	after := s.current(s.now())
	if after != 0 {
		s.success(&s.mapping, "mapping_recovered")
	}
	if after != before {
		s.synced = 0
		if after != 0 {
			s.Log.Info("mapping_available", "internal_port", s.Internal, "public_port", after, "tcp_lifetime", s.tcp.Lifetime, "udp_lifetime", s.udp.Lifetime)
		}
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

// MapStep is one bounded unit of work. It can be exercised with a fake clock
// by supplying now; actual request deadlines remain governed by context.
func (s *Service) MapStep(ctx context.Context, now time.Time) time.Duration {
	s.mu.Lock()
	tcp, udp := s.tcp, s.udp
	retryTCP, retryUDP := s.retryTCP, s.retryUDP
	s.mu.Unlock()
	due := func(g lease.Grant, retry time.Time) time.Time {
		at := g.Expiry.Add(-time.Duration(g.Lifetime) * time.Second / 2)
		if g.Public == 0 {
			at = now
		}
		if retry.After(at) {
			at = retry
		}
		return at
	}
	tcpDue, udpDue := due(tcp, retryTCP), due(udp, retryUDP)
	if tcp.Public != 0 && udp.Public != tcp.Public && tcpDue.After(now) && !retryUDP.After(now) {
		udpDue = now
	}
	if tcpDue.After(now) && udpDue.After(now) {
		if udpDue.Before(tcpDue) {
			tcpDue = udpDue
		}
		return tcpDue.Sub(now)
	}
	proto := lease.TCP
	old := tcp
	if tcpDue.After(now) || (!udpDue.After(now) && (remaining(tcp, now) > 0 && remaining(udp, now) == 0 || tcp.Public != 0 && udp.Public != tcp.Public)) {
		proto = lease.UDP
		old = udp
	}
	want := old.Public
	if proto == lease.UDP && remaining(tcp, now) > 0 {
		want = tcp.Public
	}
	if proto == lease.TCP && (want == 0 || remaining(old, now) == 0) && remaining(udp, now) > 0 {
		want = udp.Public
	}
	start := time.Now()
	g, err := s.Mapper.Map(ctx, proto, s.Internal, want)
	if ctx.Err() != nil {
		return 0
	}
	if err != nil {
		s.mu.Lock()
		if proto == lease.TCP {
			s.retryTCP = s.now().Add(2 * time.Second)
		} else {
			s.retryUDP = s.now().Add(2 * time.Second)
		}
		s.failure(&s.mapping, "mapping_degraded", fmt.Sprintf("%s: %s", proto, err))
		s.mu.Unlock()
		s.Log.Debug("mapping_attempt", "protocol", proto, "requested_internal", s.Internal, "requested_public", want, "duration_ms", time.Since(start).Milliseconds(), "error", err)
		return 2 * time.Second
	}
	s.Log.Debug("mapping_attempt", "protocol", proto, "requested_internal", s.Internal, "requested_public", want, "returned_internal", g.Internal, "returned_public", g.Public, "lifetime", g.Lifetime, "duration_ms", time.Since(start).Milliseconds())
	if g.Internal != s.Internal || g.Public == 0 || g.Lifetime == 0 || remaining(g, s.now()) == 0 {
		s.mu.Lock()
		if proto == lease.TCP {
			s.retryTCP = s.now().Add(2 * time.Second)
		} else {
			s.retryUDP = s.now().Add(2 * time.Second)
		}
		s.failure(&s.mapping, "mapping_degraded", "reply_mismatch")
		s.mu.Unlock()
		return 2 * time.Second
	}
	s.store(proto, g)
	s.mu.Lock()
	if proto == lease.TCP {
		s.retryTCP = time.Time{}
	} else {
		s.retryUDP = time.Time{}
	}
	if s.current(s.now()) == 0 {
		if s.tcp.Public != 0 && s.udp.Public != 0 && s.tcp.Public != s.udp.Public {
			if proto == lease.TCP {
				s.retryTCP = s.now().Add(2 * time.Second)
			} else {
				s.retryUDP = s.now().Add(2 * time.Second)
			}
		}
		s.failure(&s.mapping, "mapping_degraded", "public_port_mismatch_or_partial_lease")
	}
	s.mu.Unlock()
	return 0
}

func (s *Service) RunMapping(ctx context.Context) {
	for ctx.Err() == nil {
		d := s.MapStep(ctx, s.now())
		if d == 0 {
			continue
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return
		case <-t.C:
		}
	}
}

func (s *Service) Reconcile(ctx context.Context) error {
	p := s.port()
	if p == 0 {
		return fmt.Errorf("mapping_unavailable")
	}
	prefs, err := s.API.Preferences(ctx)
	if err != nil {
		return fmt.Errorf("read_preferences: %w", err)
	}
	if err = prefs.Validate(s.Internal); err != nil {
		return err
	}
	if *prefs.AnnouncePort != int(p) {
		if err = s.API.SetAnnouncePort(ctx, p); err != nil {
			return fmt.Errorf("write_announce_port: %w", err)
		}
		s.Log.Info("announce_port_write", "previous", *prefs.AnnouncePort, "public_port", p)
		prefs, err = s.API.Preferences(ctx)
		if err != nil {
			return fmt.Errorf("readback: %w", err)
		}
		if err = prefs.Validate(s.Internal); err != nil {
			return err
		}
		if *prefs.AnnouncePort != int(p) {
			return fmt.Errorf("readback_mismatch: expected=%d observed=%d", p, *prefs.AnnouncePort)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current(s.now()) != p {
		s.synced = 0
		return fmt.Errorf("mapping_changed_during_reconciliation")
	}
	s.synced = p
	s.success(&s.qb, "qbittorrent_recovered")
	return nil
}

func (s *Service) RunQB(ctx context.Context) {
	if versioned, ok := s.API.(interface {
		Version(context.Context) (string, error)
	}); ok {
		if version, err := versioned.Version(ctx); err == nil {
			s.Log.Info("qbittorrent_version", "version", version)
		} else {
			s.Log.Warn("qbittorrent_version_unavailable", "error", err)
		}
	}
	for ctx.Err() == nil {
		err := s.Reconcile(ctx)
		if ctx.Err() != nil {
			return
		}
		d := 30 * time.Second
		if err != nil {
			s.mu.Lock()
			s.synced = 0
			if err.Error() != "mapping_unavailable" {
				s.failure(&s.qb, "qbittorrent_degraded", err.Error())
			}
			s.mu.Unlock()
			d = 3 * time.Second
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return
		case <-s.changed:
			if !t.Stop() {
				<-t.C
			}
		case <-t.C:
		}
	}
}
