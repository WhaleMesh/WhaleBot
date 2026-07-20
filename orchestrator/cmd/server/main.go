package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/whalebot/orchestrator/internal/httpapi"
	"github.com/whalebot/orchestrator/internal/logs"
	"github.com/whalebot/orchestrator/internal/nodes"
	"github.com/whalebot/orchestrator/internal/registry"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	port := getenv("ORCHESTRATOR_PORT", "18080")
	heartbeatTTL := time.Duration(getenvInt("HEARTBEAT_TTL_SEC", 30)) * time.Second
	upstreamTimeout := time.Duration(getenvInt("ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC", 240)) * time.Second
	nodeToken := os.Getenv("NODE_TOKEN")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	reg := registry.New(heartbeatTTL)
	ring := logs.NewRing(200)
	ring.Append(logs.Entry{Time: time.Now(), Level: "info", Message: "orchestrator starting",
		Fields: map[string]string{"port": port}})

	hub := nodes.NewHub(nodeToken, upstreamTimeout)
	hub.OnConnect = func(h nodes.Hello) {
		meta := map[string]string{"node": h.Node}
		for k, v := range h.Meta {
			meta[k] = v
		}
		reg.Upsert(&registry.Component{
			Name:         nodes.ComponentName(h.Node),
			Type:         "tool",
			Version:      h.Version,
			Endpoint:     "tunnel://" + h.Node,
			Capabilities: h.Capabilities,
			Meta:         meta,
			Tunnel:       true,
		})
		ring.Append(logs.Entry{Time: time.Now(), Level: "info", Message: "userdocker node connected",
			Fields: map[string]string{"node": h.Node, "version": h.Version}})
	}
	hub.OnDisconnect = func(node string) {
		reg.Delete(nodes.ComponentName(node))
		ring.Append(logs.Entry{Time: time.Now(), Level: "warn", Message: "userdocker node disconnected",
			Fields: map[string]string{"node": node}})
	}

	srv := httpapi.NewServer(reg, ring, hub, upstreamTimeout)
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("orchestrator listening", "port", port, "heartbeat_ttl", heartbeatTTL.String(), "upstream_timeout", upstreamTimeout.String(), "node_tunnel_enabled", nodeToken != "")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_ = httpServer.Shutdown(shutdownCtx)
	slog.Info("orchestrator stopped")
}
