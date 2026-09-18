package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/lease"
	"github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/qbittorrent"
	portsync "github.com/cocoastorm/containers/apps/qbittorrent-natpmp-sync/internal/sync"
)

func config() (net.IP, net.IP, uint16, *qbittorrent.Client, slog.Level, error) {
	ip := net.ParseIP(os.Getenv("NATPMP_GATEWAY"))
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return nil, nil, 0, nil, 0, errors.New("NATPMP_GATEWAY must be a routable IPv4 literal")
	}
	var sourceIP net.IP
	if raw := os.Getenv("SOURCE_IP"); raw != "" {
		sourceIP = net.ParseIP(raw)
		if sourceIP == nil || sourceIP.To4() == nil || sourceIP.IsUnspecified() {
			return nil, nil, 0, nil, 0, errors.New("SOURCE_IP must be an IPv4 literal")
		}
	}
	n, err := strconv.ParseUint(os.Getenv("INTERNAL_PORT"), 10, 16)
	if err != nil || n == 0 {
		return nil, nil, 0, nil, 0, errors.New("INTERNAL_PORT must be 1..65535")
	}
	raw := os.Getenv("QBITTORRENT_URL")
	if raw == "" {
		raw = "http://127.0.0.1:8080"
	}
	api, err := qbittorrent.New(raw)
	if err != nil {
		return nil, nil, 0, nil, 0, err
	}
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "", "info":
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, nil, 0, nil, 0, errors.New("LOG_LEVEL must be debug, info, warn or error")
	}
	return ip, sourceIP, uint16(n), api, level, nil
}
func main() {
	ip, sourceIP, port, api, level, err := config()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level, ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Time(slog.TimeKey, a.Value.Time().UTC())
		}
		return a
	}}))
	log.Info("startup", "gateway", ip.String(), "internal_port", port, "status_address", "127.0.0.1:8099")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	svc := portsync.New(lease.Client{Gateway: ip, SourceIP: sourceIP}, api, port, log)
	server := &http.Server{Addr: "127.0.0.1:8099", Handler: svc.Handler(), ReadHeaderTimeout: 2 * time.Second}
	listener, err := net.Listen("tcp4", server.Addr)
	if err != nil {
		log.Error("status_listen_failed", "error", err)
		os.Exit(1)
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("status_server_failed", "error", err)
			stop()
		}
	}()
	go svc.RunMapping(ctx)
	go svc.RunQB(ctx)
	summary := time.NewTicker(5 * time.Minute)
	defer summary.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
			return
		case <-summary.C:
			st := svc.Snapshot()
			log.Info("status_summary", "state", st.State, "reason", st.Reason, "public_port", st.PublicPort, "tcp_expires_in_seconds", st.TCPExpiresInSeconds, "udp_expires_in_seconds", st.UDPExpiresInSeconds, "mapping_failures", st.Mapping.Attempts, "qbittorrent_failures", st.QBittorrent.Attempts)
		}
	}
}
