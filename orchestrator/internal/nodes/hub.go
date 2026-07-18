// Package nodes accepts outbound tunnel connections from userdocker nodes.
//
// A node (user-docker-manager on any machine that can reach the orchestrator)
// POSTs /api/v1/nodes/connect with a token and a JSON hello, the connection is
// upgraded, and the node serves its normal HTTP API over yamux streams. The
// orchestrator gets one http.Client per connected node, so NAT'd machines can
// host userdockers while only the orchestrator is publicly reachable.
// Connection presence doubles as liveness: no health probing is needed.
package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Hello is the JSON body a node sends when connecting.
type Hello struct {
	Node         string            `json:"node"`
	Version      string            `json:"version"`
	Capabilities []string          `json:"capabilities"`
	Meta         map[string]string `json:"meta"`
}

// Node is one connected userdocker node.
type Node struct {
	Hello       Hello
	Client      *http.Client
	ConnectedAt time.Time
	session     *yamux.Session
}

type Hub struct {
	Token   string        // shared secret; empty disables the tunnel endpoint
	Timeout time.Duration // per-request timeout for node clients

	OnConnect    func(h Hello)
	OnDisconnect func(node string)

	mu    sync.RWMutex
	nodes map[string]*Node
}

func NewHub(token string, timeout time.Duration) *Hub {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Hub{Token: token, Timeout: timeout, nodes: map[string]*Node{}}
}

// Node names become URL path segments and container-name prefixes.
var validNodeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// Reserved names collide with static routes under /api/v1/tools/user-dockers.
var reservedNodeNames = map[string]bool{
	"images": true, "pull": true, "nodes": true,
	"interface-contract": true, "touch-creator-session": true,
}

// HandleConnect upgrades the request into a node tunnel.
func (h *Hub) HandleConnect(w http.ResponseWriter, r *http.Request) {
	if h.Token == "" {
		http.Error(w, "node tunnel disabled: orchestrator has no NODE_TOKEN", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("X-Node-Token") != h.Token {
		http.Error(w, "invalid node token", http.StatusUnauthorized)
		return
	}
	var hello Hello
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&hello); err != nil {
		http.Error(w, "invalid hello json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !validNodeName.MatchString(hello.Node) || reservedNodeNames[hello.Node] {
		http.Error(w, "invalid node name (lowercase [a-z0-9_.-], not a reserved word)", http.StatusBadRequest)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "server does not support connection hijacking", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		http.Error(w, "hijack failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: whalebot-node-tunnel\r\n\r\n"
	if _, err := buf.WriteString(resp); err != nil || buf.Flush() != nil {
		_ = conn.Close()
		return
	}

	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = 10 * time.Second
	cfg.LogOutput = io.Discard
	// The node side runs yamux.Server; the orchestrator opens streams.
	sess, err := yamux.Client(conn, cfg)
	if err != nil {
		slog.Warn("node tunnel yamux setup failed", "node", hello.Node, "err", err)
		_ = conn.Close()
		return
	}

	n := &Node{
		Hello:       hello,
		ConnectedAt: time.Now(),
		session:     sess,
		Client: &http.Client{
			Timeout: h.Timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return sess.Open()
				},
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}

	h.mu.Lock()
	if old, exists := h.nodes[hello.Node]; exists {
		_ = old.session.Close() // node reconnected; drop the stale session
	}
	h.nodes[hello.Node] = n
	h.mu.Unlock()
	slog.Info("node connected", "node", hello.Node, "version", hello.Version)
	if h.OnConnect != nil {
		h.OnConnect(hello)
	}

	go func() {
		<-sess.CloseChan()
		h.mu.Lock()
		if cur, exists := h.nodes[hello.Node]; exists && cur.session == sess {
			delete(h.nodes, hello.Node)
			h.mu.Unlock()
			slog.Info("node disconnected", "node", hello.Node)
			if h.OnDisconnect != nil {
				h.OnDisconnect(hello.Node)
			}
			return
		}
		h.mu.Unlock()
	}()
}

// Get returns the connected node or nil.
func (h *Hub) Get(node string) *Node {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.nodes[node]
}

// List returns connected nodes sorted by name.
func (h *Hub) List() []*Node {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Node, 0, len(h.nodes))
	for _, n := range h.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hello.Node < out[j].Hello.Node })
	return out
}

// First returns the alphabetically first connected node or nil.
// ponytail: default "scheduler" is first-by-name; upgrade path is picking the
// node with the fewest containers once that matters.
func (h *Hub) First() *Node {
	l := h.List()
	if len(l) == 0 {
		return nil
	}
	return l[0]
}

// ComponentName is the registry name for a node's manager component.
func ComponentName(node string) string {
	return fmt.Sprintf("user-docker-manager@%s", node)
}
