package tunnel

// Verifies the outbound tunnel client end-to-end against a minimal acceptor
// that mirrors the orchestrator nodes hub (upgrade + yamux client + one HTTP
// request through the tunnel).

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestConnectAndServe(t *testing.T) {
	gotHello := make(chan Hello, 1)
	reply := make(chan string, 1)

	acceptor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes/connect" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Node-Token") != "tok" {
			http.Error(w, "bad token", 401)
			return
		}
		var h Hello
		_ = json.NewDecoder(r.Body).Decode(&h)
		gotHello <- h
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: whalebot-node-tunnel\r\n\r\n")
		_ = buf.Flush()
		cfg := yamux.DefaultConfig()
		cfg.LogOutput = io.Discard
		sess, err := yamux.Client(conn, cfg)
		if err != nil {
			t.Errorf("yamux client: %v", err)
			return
		}
		cli := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return sess.Open()
			},
		}}
		resp, err := cli.Get("http://node/ping")
		if err != nil {
			t.Errorf("tunnel request: %v", err)
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		reply <- string(b)
	}))
	defer acceptor.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	go Run(ctx, Config{
		OrchestratorURL: acceptor.URL,
		Token:           "tok",
		Hello:           Hello{Node: "n1", Version: "test"},
		Handler:         handler,
	})

	select {
	case h := <-gotHello:
		if h.Node != "n1" {
			t.Fatalf("hello node = %q", h.Node)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no hello received")
	}
	select {
	case got := <-reply:
		if got != "pong" {
			t.Fatalf("reply = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reply through tunnel")
	}
}
