package httpapi

// End-to-end check for the node tunnel + multi-node userdocker routing:
// two fake manager nodes connect through the real /api/v1/nodes/connect
// upgrade, then list aggregation, composite-name routing and create node
// selection are exercised through the real router.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/whalebot/orchestrator/internal/logs"
	"github.com/whalebot/orchestrator/internal/nodes"
	"github.com/whalebot/orchestrator/internal/registry"
)

// dialNode mirrors user-docker-manager/internal/tunnel: outbound connect,
// upgrade, then serve the handler over yamux.
func dialNode(t *testing.T, baseURL, token, node string, handler http.Handler) {
	t.Helper()
	addr := strings.TrimPrefix(baseURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hello, _ := json.Marshal(map[string]any{
		"node": node, "version": "test",
		"capabilities": []string{"userdocker_list", "userdocker_create"},
		"meta":         map[string]string{},
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/nodes/connect", bytes.NewReader(hello))
	req.Header.Set("X-Node-Token", token)
	if err := req.Write(conn); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	sess, err := yamux.Server(bufferedTestConn{br, conn}, cfg)
	if err != nil {
		t.Fatalf("yamux: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	go func() { _ = http.Serve(sess, handler) }()
}

type bufferedTestConn struct {
	r io.Reader
	net.Conn
}

func (b bufferedTestConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func fakeManager(node string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user-dockers", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":    true,
			"containers": []map[string]any{{"name": "c-" + node, "purpose": "test"}},
		})
	})
	mux.HandleFunc("POST /api/v1/user-dockers", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "name": "created-" + node, "port": 9000})
	})
	mux.HandleFunc("POST /api/v1/user-dockers/{name}/exec", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "node": node, "name": r.PathValue("name"),
		})
	})
	return mux
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("get %s: status %d body %s", url, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestNodeTunnelRouting(t *testing.T) {
	reg := registry.New(30 * time.Second)
	hub := nodes.NewHub("secret", 10*time.Second)
	hub.OnConnect = func(h nodes.Hello) {
		reg.Upsert(&registry.Component{
			Name: nodes.ComponentName(h.Node), Type: "tool",
			Endpoint: "tunnel://" + h.Node, Capabilities: h.Capabilities, Tunnel: true,
		})
	}
	hub.OnDisconnect = func(node string) { reg.Delete(nodes.ComponentName(node)) }
	srv := NewServer(reg, logs.NewRing(10), hub, 10*time.Second)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// Wrong token is rejected.
	badReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/nodes/connect", strings.NewReader(`{"node":"x"}`))
	badReq.Header.Set("X-Node-Token", "wrong")
	if resp, err := http.DefaultClient.Do(badReq); err != nil || resp.StatusCode != 401 {
		t.Fatalf("expected 401 for wrong token, got %v %v", resp, err)
	}

	dialNode(t, ts.URL, "secret", "alpha", fakeManager("alpha"))
	dialNode(t, ts.URL, "secret", "beta", fakeManager("beta"))
	waitFor(t, func() bool { return hub.Get("alpha") != nil && hub.Get("beta") != nil })

	// Nodes appear as tool components in the registry.
	if reg.FirstReadyByCapability("userdocker_list") == nil {
		t.Fatal("node component not registered")
	}

	// List aggregates both nodes with composite names.
	var list struct {
		Success    bool             `json:"success"`
		Containers []map[string]any `json:"containers"`
	}
	getJSON(t, ts.URL+"/api/v1/tools/user-dockers", &list)
	if !list.Success || len(list.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %+v", list.Containers)
	}
	if name, _ := list.Containers[0]["name"].(string); name != "alpha/c-alpha" {
		t.Fatalf("expected composite name alpha/c-alpha, got %q", name)
	}

	// Container-scoped call routes to the right node.
	var execOut struct {
		Success bool   `json:"success"`
		Node    string `json:"node"`
		Name    string `json:"name"`
	}
	resp, err := http.Post(ts.URL+"/api/v1/tools/user-dockers/beta/c-beta/exec", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&execOut); err != nil {
		t.Fatal(err)
	}
	if !execOut.Success || execOut.Node != "beta" || execOut.Name != "c-beta" {
		t.Fatalf("exec routed wrong: %+v", execOut)
	}

	// Create targets the requested node and returns the composite name.
	var created map[string]any
	cResp, err := http.Post(ts.URL+"/api/v1/tools/user-dockers", "application/json", strings.NewReader(`{"name":"n","node":"beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cResp.Body.Close()
	if err := json.NewDecoder(cResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created["name"] != "beta/created-beta" || created["node"] != "beta" {
		t.Fatalf("create response wrong: %+v", created)
	}

	// Unknown node yields a routable error, not a hang.
	uResp, err := http.Post(ts.URL+"/api/v1/tools/user-dockers/ghost/c/exec", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer uResp.Body.Close()
	if uResp.StatusCode != 503 {
		t.Fatalf("expected 503 for unknown node, got %d", uResp.StatusCode)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(fmt.Errorf("condition not met within deadline"))
}
