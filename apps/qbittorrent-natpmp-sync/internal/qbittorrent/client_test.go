package qbittorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestURLValidation(t *testing.T) {
	for _, raw := range []string{"ftp://example.com", "http://user:pass@localhost:8080", "http://127.0.0.1:8080?key=secret", "http://localhost:8080#fragment", "http://localhost:0", "http://localhost:99999", "http://localhost:", "http://"} {
		if _, err := New(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost", "http://192.0.2.1:8080", "https://qb.example.com", "https://qb.example.com/qbt/"} {
		if _, err := New(raw); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}

func TestAPIBasePath(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/qbt/api/v2/app/preferences" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"listen_port":55873,"random_port":false,"upnp":false,"announce_port":52837}`))
	}))
	defer s.Close()
	c, err := New(s.URL + "/qbt/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Preferences(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyAnnouncePortWritten(t *testing.T) {
	var sent map[string]int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/preferences" {
			_, _ = w.Write([]byte(`{"listen_port":55873,"random_port":false,"upnp":false,"announce_port":52837}`))
			return
		}
		if r.URL.Path != "/api/v2/app/setPreferences" {
			t.Errorf("path %s", r.URL.Path)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if len(r.PostForm) != 1 || r.PostForm.Get("json") == "" {
			t.Errorf("form %v", r.PostForm)
		}
		if err := json.Unmarshal([]byte(r.PostForm.Get("json")), &sent); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetAnnouncePort(context.Background(), 52837); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent["announce_port"] != 52837 {
		t.Fatalf("mutation: %v", sent)
	}
	p, err := c.Preferences(context.Background())
	if err != nil || p.Validate(55873) != nil {
		t.Fatalf("preferences %+v: %v", p, err)
	}
}

func TestRejectedAndTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("session cookie: secret"))
	}))
	defer s.Close()
	c, _ := New(s.URL)
	_, err := c.Preferences(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("error %v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	c, _ = New(slow.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = c.Preferences(ctx)
	if err == nil {
		t.Fatal("expected timeout")
	}
}

func TestNoRedirectOrProxy(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	}))
	defer s.Close()
	c, _ := New(s.URL)
	_, err := c.Preferences(context.Background())
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("error %v", err)
	}
}
