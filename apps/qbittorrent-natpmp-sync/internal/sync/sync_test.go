package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/lease"
	"github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/qbittorrent"
)

type mapperFunc func(context.Context, lease.Protocol, uint16, uint16) (lease.Grant, error)

func (f mapperFunc) Map(ctx context.Context, p lease.Protocol, i, e uint16) (lease.Grant, error) {
	return f(ctx, p, i, e)
}

type fakeAPI struct {
	mu               sync.Mutex
	listen, announce int
	random, upnp     bool
	writes           int
	err              error
	block            chan struct{}
	started          chan struct{}
}

func (a *fakeAPI) Preferences(ctx context.Context) (qbittorrent.Preferences, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return qbittorrent.Preferences{}, a.err
	}
	return qbittorrent.Preferences{ListenPort: &a.listen, RandomPort: &a.random, UPnP: &a.upnp, AnnouncePort: &a.announce}, nil
}
func (a *fakeAPI) SetAnnouncePort(ctx context.Context, p uint16) error {
	if a.started != nil {
		select {
		case a.started <- struct{}{}:
		default:
		}
	}
	if a.block != nil {
		select {
		case <-a.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.writes++
	a.announce = int(p)
	return nil
}
func newService(m lease.Mapper, a qbittorrent.API) *Service {
	return New(m, a, 55873, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}
func grant(port uint16) lease.Grant {
	return lease.Grant{Internal: 55873, Public: port, Lifetime: 60, Epoch: 100, Expiry: time.Now().Add(time.Minute)}
}
func setPair(s *Service, port uint16) {
	s.store(lease.TCP, grant(port))
	s.store(lease.UDP, grant(port))
}

func TestReconcileAndDrift(t *testing.T) {
	a := &fakeAPI{listen: 55873, announce: 2}
	s := newService(nil, a)
	setPair(s, 52837)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(); st.State != "healthy" || st.PublicPort != 52837 || st.Mapping.LastSuccess == nil || st.QBittorrent.LastSuccess == nil {
		t.Fatalf("status %+v", st)
	}
	if err := s.Reconcile(context.Background()); err != nil || a.writes != 1 {
		t.Fatalf("no-op %v writes=%d", err, a.writes)
	}
	a.announce = 3
	if err := s.Reconcile(context.Background()); err != nil || a.writes != 2 {
		t.Fatalf("repair %v writes=%d", err, a.writes)
	}
	a.random = true
	if err := s.Reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "random_port expected=false observed=true") || a.writes != 2 {
		t.Fatalf("drift %v writes=%d", err, a.writes)
	}
	a.random = false
	a.listen = 1
	if err := s.Reconcile(context.Background()); err == nil || a.writes != 2 {
		t.Fatalf("listener drift %v", err)
	}
	a.listen = 55873
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPortChangedDuringSlowWrite(t *testing.T) {
	a := &fakeAPI{listen: 55873, announce: 2, block: make(chan struct{}), started: make(chan struct{}, 1)}
	s := newService(nil, a)
	setPair(s, 52837)
	done := make(chan error, 1)
	go func() { done <- s.Reconcile(context.Background()) }()
	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatal("write did not begin")
	}
	setPair(s, 53000)
	close(a.block)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "mapping_changed") {
		t.Fatalf("stale result %v", err)
	}
	if s.Snapshot().State == "healthy" {
		t.Fatal("stale sync healthy")
	}
	if err := s.Reconcile(context.Background()); err != nil || a.announce != 53000 {
		t.Fatalf("retry %v port=%d", err, a.announce)
	}
}

func TestPartialRenewalEpochAndExpiry(t *testing.T) {
	s := newService(nil, &fakeAPI{listen: 55873})
	clock := time.Now()
	s.now = func() time.Time { return clock }
	setPair(s, 52837)
	s.store(lease.TCP, grant(53000))
	if s.Snapshot().PublicPort != 0 {
		t.Fatal("mismatched pair published")
	}
	s.store(lease.UDP, grant(53000))
	if s.Snapshot().PublicPort != 53000 {
		t.Fatal("new pair not published")
	}
	g := grant(53000)
	g.Epoch = 1
	s.store(lease.TCP, g)
	if s.Snapshot().PublicPort != 0 {
		t.Fatal("epoch reset left old UDP grant")
	}
	s.store(lease.UDP, lease.Grant{Internal: 55873, Public: 53000, Lifetime: 1, Epoch: 1, Expiry: clock.Add(10 * time.Millisecond)})
	clock = clock.Add(20 * time.Millisecond)
	if st := s.Snapshot(); st.PublicPort != 0 || st.UDPExpiresInSeconds != 0 {
		t.Fatalf("expired status %+v", st)
	}
}

func TestShortGrantSchedulesAtHalfLife(t *testing.T) {
	clock := time.Now()
	s := newService(mapperFunc(func(context.Context, lease.Protocol, uint16, uint16) (lease.Grant, error) {
		t.Fatal("premature renewal")
		return lease.Grant{}, nil
	}), &fakeAPI{listen: 55873})
	s.now = func() time.Time { return clock }
	g := lease.Grant{Internal: 55873, Public: 52837, Lifetime: 4, Epoch: 10, Expiry: clock.Add(4 * time.Second)}
	s.store(lease.TCP, g)
	s.store(lease.UDP, g)
	if delay := s.MapStep(context.Background(), clock); delay != 2*time.Second {
		t.Fatalf("renewal delay=%s", delay)
	}
	clock = clock.Add(4 * time.Second)
	if st := s.Snapshot(); st.State != "degraded" || st.TCPExpiresInSeconds != 0 || st.UDPExpiresInSeconds != 0 {
		t.Fatalf("expired %+v", st)
	}
}

func TestMappingContinuesWhileAPIBlocked(t *testing.T) {
	a := &fakeAPI{listen: 55873, announce: 1, block: make(chan struct{}), started: make(chan struct{}, 1)}
	var mu sync.Mutex
	calls := 0
	m := mapperFunc(func(ctx context.Context, p lease.Protocol, i, e uint16) (lease.Grant, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		g := grant(52837)
		g.Lifetime = 1
		g.Expiry = time.Now().Add(100 * time.Millisecond)
		return g, nil
	})
	s := newService(m, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunMapping(ctx)
	go s.RunQB(ctx)
	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatal("API write did not block")
	}
	time.Sleep(240 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 4 {
		t.Fatalf("renewal blocked by API: %d calls", n)
	}
	close(a.block)
}

func TestFailuresAndStatus(t *testing.T) {
	a := &fakeAPI{listen: 55873, err: errors.New("api unavailable")}
	s := newService(mapperFunc(func(context.Context, lease.Protocol, uint16, uint16) (lease.Grant, error) {
		return lease.Grant{}, errors.New("transport_timeout")
	}), a)
	if d := s.MapStep(context.Background(), time.Now()); d == 0 {
		t.Fatal("missing retry")
	}
	if st := s.Snapshot(); st.State != "degraded" || st.Mapping.LastError == "" {
		t.Fatalf("status %+v", st)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	if w.Code != 200 {
		t.Fatalf("status code %d", w.Code)
	}
	var st Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Mapping.LastError == "" {
		t.Fatalf("status %s %v", w.Body.String(), err)
	}
	setPair(s, 52837)
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("expected API failure")
	}
	if st := s.Snapshot(); st.State == "healthy" {
		t.Fatal("API outage marked healthy")
	}
}

type apiFunc struct {
	prefs func(context.Context) (qbittorrent.Preferences, error)
	write func(context.Context, uint16) error
}

func (a apiFunc) Preferences(ctx context.Context) (qbittorrent.Preferences, error) {
	return a.prefs(ctx)
}
func (a apiFunc) SetAnnouncePort(ctx context.Context, p uint16) error { return a.write(ctx, p) }

func TestMissingSettingsRejectedWriteAndReadback(t *testing.T) {
	a := &fakeAPI{listen: 55873, announce: 1}
	s := newService(aMapper{}, a)
	setPair(s, 52837)
	missing := apiFunc{prefs: func(context.Context) (qbittorrent.Preferences, error) { return qbittorrent.Preferences{}, nil }, write: func(context.Context, uint16) error { t.Fatal("wrote with missing settings"); return nil }}
	s.API = missing
	if err := s.Reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "missing required") {
		t.Fatalf("missing settings %v", err)
	}
	s.API = apiFunc{prefs: a.Preferences, write: func(context.Context, uint16) error { return errors.New("api_rejection: status=403") }}
	if err := s.Reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("rejected write %v", err)
	}
	s.API = apiFunc{prefs: a.Preferences, write: func(context.Context, uint16) error { return nil }}
	if err := s.Reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "readback_mismatch") {
		t.Fatalf("readback %v", err)
	}
}

type aMapper struct{}

func (aMapper) Map(context.Context, lease.Protocol, uint16, uint16) (lease.Grant, error) {
	return lease.Grant{}, nil
}

func TestRenewalRequestsCounterpartAndRejectsInvalidGrant(t *testing.T) {
	var gotProto lease.Protocol
	var gotPublic uint16
	s := newService(mapperFunc(func(_ context.Context, p lease.Protocol, _ uint16, e uint16) (lease.Grant, error) {
		gotProto = p
		gotPublic = e
		g := grant(53000)
		g.Internal = 1
		return g, nil
	}), &fakeAPI{listen: 55873})
	setPair(s, 52837)
	s.store(lease.TCP, grant(53000))
	s.MapStep(context.Background(), time.Now())
	if gotProto != lease.UDP || gotPublic != 53000 {
		t.Fatalf("requested %v/%d", gotProto, gotPublic)
	}
	if st := s.Snapshot(); st.PublicPort != 0 || !strings.Contains(st.Mapping.LastError, "reply_mismatch") {
		t.Fatalf("invalid grant %+v", st)
	}
}
