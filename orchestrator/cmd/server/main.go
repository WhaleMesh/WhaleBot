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

	"github.com/whalesbot/orchestrator/internal/httpapi"
	"github.com/whalesbot/orchestrator/internal/logs"
	"github.com/whalesbot/orchestrator/internal/registry"
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

	port := getenv("ORCHESTRATOR_PORT", "8080")
	interval := time.Duration(getenvInt("HEALTHCHECK_INTERVAL_SEC", 5)) * time.Second
	threshold := getenvInt("HEALTHCHECK_FAIL_THRESHOLD", 3)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	reg := registry.New()
	ring := logs.NewRing(200)
	ring.Append(logs.Entry{Time: time.Now(), Level: "info", Message: "orchestrator starting",
		Fields: map[string]string{"port": port}})

	hc := registry.NewHealthChecker(reg, interval, threshold, func(event string, c *registry.Component) {
		ring.Append(logs.Entry{
			Time:    time.Now(),
			Level:   "info",
			Message: event,
			Fields: map[string]string{
				"component":     c.Name,
				"type":          c.Type,
				"status":        string(c.Status),
				"failure_count": strconv.Itoa(c.FailureCount),
			},
		})
	})
	hc.Start(ctx)

	srv := httpapi.NewServer(reg, ring)
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("orchestrator listening", "port", port, "interval", interval.String(), "threshold", threshold)
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
