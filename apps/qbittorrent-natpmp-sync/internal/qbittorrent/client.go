package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Preferences struct {
	ListenPort   *int  `json:"listen_port"`
	RandomPort   *bool `json:"random_port"`
	UPnP         *bool `json:"upnp"`
	AnnouncePort *int  `json:"announce_port"`
}

type API interface {
	Preferences(context.Context) (Preferences, error)
	SetAnnouncePort(context.Context, uint16) error
}

type Client struct {
	base string
	http *http.Client
}

func New(raw string) (*Client, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("QBITTORRENT_URL must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return nil, errors.New("QBITTORRENT_URL has an invalid port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("QBITTORRENT_URL has an empty port")
	}
	// The configured URL is trusted operator input. Keep standard HTTP(S) host
	// resolution and default ports, but never follow redirects or use a proxy.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 2 * time.Second}
	return &Client{base: strings.TrimRight(u.String(), "/"), http: &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("api_rejection: status=%d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, fmt.Errorf("read_response: %w", err)
	}
	return data, nil
}

func (c *Client) Preferences(ctx context.Context) (Preferences, error) {
	data, err := c.request(ctx, "GET", "/api/v2/app/preferences", nil)
	if err != nil {
		return Preferences{}, err
	}
	var p Preferences
	if err = json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("invalid_preferences: %w", err)
	}
	return p, nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	data, err := c.request(ctx, "GET", "/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(data))
	if len(version) > 64 || !regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`).MatchString(version) {
		return "", errors.New("invalid_version")
	}
	return version, nil
}

// This is intentionally the only mutator; never serialize a Preferences object.
func (c *Client) SetAnnouncePort(ctx context.Context, port uint16) error {
	payload, _ := json.Marshal(struct {
		AnnouncePort uint16 `json:"announce_port"`
	}{port})
	v := url.Values{"json": {string(payload)}}
	_, err := c.request(ctx, "POST", "/api/v2/app/setPreferences", strings.NewReader(v.Encode()))
	return err
}

func (p Preferences) Validate(port uint16) error {
	if p.ListenPort == nil || p.RandomPort == nil || p.UPnP == nil || p.AnnouncePort == nil {
		return errors.New("settings_drift: missing required preference")
	}
	if *p.ListenPort != int(port) || *p.RandomPort || *p.UPnP {
		return fmt.Errorf("settings_drift: listen_port expected=%d observed=%d random_port expected=false observed=%t upnp expected=false observed=%t", port, *p.ListenPort, *p.RandomPort, *p.UPnP)
	}
	return nil
}
