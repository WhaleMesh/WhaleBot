// Package tunnel maintains an outbound connection to the orchestrator and
// serves the manager's normal HTTP API over it. Only outbound connectivity is
// needed, so a node behind NAT can host userdockers while the orchestrator is
// the single public server. Connection presence doubles as liveness (no
// registerclient heartbeat needed for the manager).
package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/yamux"
)

// Hello mirrors the orchestrator nodes.Hello.
type Hello struct {
	Node         string            `json:"node"`
	Version      string            `json:"version"`
	Capabilities []string          `json:"capabilities"`
	Meta         map[string]string `json:"meta"`
}

type Config struct {
	OrchestratorURL string
	Token           string
	Hello           Hello
	Handler         http.Handler
}

// Run connects and serves until ctx is done, reconnecting with backoff.
func Run(ctx context.Context, cfg Config) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := connectAndServe(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("node tunnel disconnected, reconnecting", "err", err, "backoff", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func connectAndServe(ctx context.Context, cfg Config) error {
	u, err := url.Parse(cfg.OrchestratorURL)
	if err != nil {
		return fmt.Errorf("parse orchestrator url: %w", err)
	}
	addr := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if u.Scheme == "https" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, nil)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	helloBody, err := json.Marshal(cfg.Hello)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, cfg.OrchestratorURL+"/api/v1/nodes/connect", bytes.NewReader(helloBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", cfg.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "whalebot-node-tunnel")
	if err := req.Write(conn); err != nil {
		return fmt.Errorf("write connect request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return fmt.Errorf("read connect response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return fmt.Errorf("orchestrator refused tunnel: %s %s", resp.Status, bytes.TrimSpace(body))
	}

	ycfg := yamux.DefaultConfig()
	ycfg.KeepAliveInterval = 10 * time.Second
	ycfg.LogOutput = io.Discard
	// The bufio reader may already hold yamux bytes; keep reading through it.
	sess, err := yamux.Server(bufferedConn{br, conn}, ycfg)
	if err != nil {
		return fmt.Errorf("yamux server: %w", err)
	}
	defer sess.Close()
	slog.Info("node tunnel connected", "node", cfg.Hello.Node, "orchestrator", cfg.OrchestratorURL)

	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Close()
		case <-sess.CloseChan():
		}
	}()
	// Serve returns when the session closes.
	err = http.Serve(sess, cfg.Handler)
	if err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

type bufferedConn struct {
	r io.Reader
	net.Conn
}

func (b bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
