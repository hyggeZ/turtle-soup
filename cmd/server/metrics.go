package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"turtle-soup/internal/telemetry"
)

// 指标监听在独立回环端口，业务路由和代理不转发这个接口。
func startMetrics(raw string) (*telemetry.Metrics, *http.Server, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return nil, nil, nil
	}
	local, err := netip.ParseAddrPort(addr)
	if err != nil || !local.Addr().IsLoopback() {
		return nil, nil, errors.New("METRICS_ADDR 必须是回环 IP 和端口")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	metrics := telemetry.NewMetrics()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	server := &http.Server{
		Addr: listener.Addr().String(), Handler: mux,
		ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second,
		MaxHeaderBytes: 8 << 10,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("监控监听出错: ", err)
		}
	}()
	return metrics, server, nil
}
