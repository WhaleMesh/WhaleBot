package httpapi

// Userdocker APIs are node-scoped: every user-docker-manager connects as a
// tunnel node (see internal/nodes) and containers are addressed by the
// composite name "<node>/<container>". List/images fan out to all nodes;
// create picks a node; everything container-scoped routes to its node.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/whalebot/orchestrator/internal/nodes"
)

const noNodesMsg = "no userdocker nodes connected"

func (s *Server) handleUserDockerNodes(w http.ResponseWriter, _ *http.Request) {
	list := s.Nodes.List()
	out := make([]map[string]any, 0, len(list))
	for _, n := range list {
		out = append(out, map[string]any{
			"node":         n.Hello.Node,
			"version":      n.Hello.Version,
			"capabilities": n.Hello.Capabilities,
			"meta":         n.Hello.Meta,
			"connected_at": n.ConnectedAt,
		})
	}
	writeJSON(w, 200, map[string]any{"success": true, "nodes": out})
}

// handleUserDockerContainer proxies any container-scoped request
// ({node}/{cname}[/action...]) to its node, preserving method, body and query.
func (s *Server) handleUserDockerContainer(w http.ResponseWriter, r *http.Request) {
	nodeName := chi.URLParam(r, "node")
	cname := chi.URLParam(r, "cname")
	n := s.Nodes.Get(nodeName)
	if n == nil {
		writeError(w, 503, fmt.Sprintf("unknown or offline node %q; container names are node-scoped: use \"<node>/<name>\" as returned by list", nodeName))
		return
	}
	target := "http://" + nodeName + "/api/v1/user-dockers/" + url.PathEscape(cname)
	if rest := chi.URLParam(r, "*"); rest != "" {
		target += "/" + rest
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "body read failed: "+err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		writeError(w, 502, fmt.Sprintf("node %s error: %s", nodeName, err.Error()))
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleUserDockerList(w http.ResponseWriter, r *http.Request) {
	nodeList := s.Nodes.List()
	if len(nodeList) == 0 {
		writeError(w, 503, noNodesMsg)
		return
	}
	query := r.URL.RawQuery

	type result struct {
		node       string
		containers []map[string]any
		err        string
	}
	results := make([]result, len(nodeList))
	var wg sync.WaitGroup
	for i, n := range nodeList {
		wg.Add(1)
		go func(i int, n *nodes.Node) {
			defer wg.Done()
			res := result{node: n.Hello.Node}
			var payload struct {
				Success    bool             `json:"success"`
				Containers []map[string]any `json:"containers"`
				Error      string           `json:"error"`
			}
			target := "http://" + n.Hello.Node + "/api/v1/user-dockers"
			if query != "" {
				target += "?" + query
			}
			if err := s.nodeGetJSON(r, n, target, &payload); err != nil {
				res.err = err.Error()
			} else if !payload.Success {
				res.err = payload.Error
			} else {
				res.containers = payload.Containers
			}
			results[i] = res
		}(i, n)
	}
	wg.Wait()

	merged := make([]map[string]any, 0)
	nodeErrors := map[string]string{}
	for _, res := range results {
		if res.err != "" {
			nodeErrors[res.node] = res.err
			continue
		}
		for _, c := range res.containers {
			if name, _ := c["name"].(string); name != "" {
				c["name"] = res.node + "/" + name
			}
			c["node"] = res.node
			merged = append(merged, c)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		ni, _ := merged[i]["name"].(string)
		nj, _ := merged[j]["name"].(string)
		return ni < nj
	})
	out := map[string]any{"success": true, "containers": merged}
	if len(nodeErrors) > 0 {
		out["node_errors"] = nodeErrors
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleUserDockerCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid json: "+err.Error())
		return
	}
	nodeName, _ := body["node"].(string)
	delete(body, "node")
	var n *nodes.Node
	if nodeName == "" {
		n = s.Nodes.First()
		if n == nil {
			writeError(w, 503, noNodesMsg)
			return
		}
		nodeName = n.Hello.Node
	} else if n = s.Nodes.Get(nodeName); n == nil {
		writeError(w, 503, fmt.Sprintf("unknown or offline node %q", nodeName))
		return
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://"+nodeName+"/api/v1/user-dockers", bytes.NewReader(raw))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		writeError(w, 502, fmt.Sprintf("node %s error: %s", nodeName, err.Error()))
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if json.Unmarshal(b, &payload) != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
		return
	}
	if name, _ := payload["name"].(string); name != "" {
		payload["name"] = nodeName + "/" + name
	}
	payload["node"] = nodeName
	writeJSON(w, resp.StatusCode, payload)
}

func (s *Server) handleUserDockerImages(w http.ResponseWriter, r *http.Request) {
	nodeList := s.Nodes.List()
	if len(nodeList) == 0 {
		writeError(w, 503, noNodesMsg)
		return
	}
	out := make([]map[string]any, len(nodeList))
	var wg sync.WaitGroup
	for i, n := range nodeList {
		wg.Add(1)
		go func(i int, n *nodes.Node) {
			defer wg.Done()
			var payload map[string]any
			if err := s.nodeGetJSON(r, n, "http://"+n.Hello.Node+"/api/v1/user-dockers/images", &payload); err != nil {
				payload = map[string]any{"success": false, "error": err.Error()}
			}
			delete(payload, "success")
			payload["node"] = n.Hello.Node
			out[i] = payload
		}(i, n)
	}
	wg.Wait()
	writeJSON(w, 200, map[string]any{"success": true, "nodes": out})
}

func (s *Server) handleUserDockerInterfaceContract(w http.ResponseWriter, r *http.Request) {
	n := s.Nodes.First() // contract is identical across nodes (userdocker.v1)
	if n == nil {
		writeError(w, 503, noNodesMsg)
		return
	}
	s.proxyNodeGet(w, r, n, "/api/v1/user-dockers/interface-contract")
}

func (s *Server) handleUserDockerImageEstimate(w http.ResponseWriter, r *http.Request) {
	n, ok := s.nodeFromQuery(w, r)
	if !ok {
		return
	}
	s.proxyNodeGet(w, r, n, "/api/v1/user-dockers/images/estimate")
}

func (s *Server) handleUserDockerImagesLocal(w http.ResponseWriter, r *http.Request) {
	n, ok := s.nodeFromQuery(w, r)
	if !ok {
		return
	}
	var payload map[string]any
	target := "http://" + n.Hello.Node + "/api/v1/user-dockers/images/local"
	if err := s.nodeGetJSON(r, n, target, &payload); err != nil {
		writeError(w, 502, fmt.Sprintf("node %s error: %s", n.Hello.Node, err.Error()))
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["node"] = n.Hello.Node
	writeJSON(w, 200, payload)
}

// handleUserDockerCopy proxies a cross-container file copy to the node hosting
// both containers. Names arrive composite ("<node>/<name>"); both must resolve
// to the same node (the manager copies within its own Docker network only).
func (s *Server) handleUserDockerCopy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromName  string `json:"from_name"`
		FromPath  string `json:"from_path"`
		ToName    string `json:"to_name"`
		ToPath    string `json:"to_path"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid json: "+err.Error())
		return
	}
	fromNode, fromName, okFrom := strings.Cut(body.FromName, "/")
	toNode, toName, okTo := strings.Cut(body.ToName, "/")
	if !okFrom || !okTo {
		writeError(w, 400, "from_name and to_name must be composite \"<node>/<name>\" as returned by list/create")
		return
	}
	if fromNode != toNode {
		writeError(w, 400, "cross-node copy is not supported: both containers must be on the same node")
		return
	}
	n := s.Nodes.Get(fromNode)
	if n == nil {
		writeError(w, 503, fmt.Sprintf("unknown or offline node %q", fromNode))
		return
	}
	raw, err := json.Marshal(map[string]any{
		"from_name":  fromName,
		"from_path":  body.FromPath,
		"to_name":    toName,
		"to_path":    body.ToPath,
		"session_id": body.SessionID,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://"+fromNode+"/api/v1/user-dockers/copy", bytes.NewReader(raw))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		writeError(w, 502, fmt.Sprintf("node %s error: %s", fromNode, err.Error()))
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleUserDockerPull(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid json: "+err.Error())
		return
	}
	nodeName, _ := body["node"].(string)
	delete(body, "node")
	var n *nodes.Node
	if nodeName == "" {
		if n = s.Nodes.First(); n == nil {
			writeError(w, 503, noNodesMsg)
			return
		}
	} else if n = s.Nodes.Get(nodeName); n == nil {
		writeError(w, 503, fmt.Sprintf("unknown or offline node %q", nodeName))
		return
	}
	nodeName = n.Hello.Node
	raw, err := json.Marshal(body)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://"+nodeName+"/api/v1/user-dockers/pull", bytes.NewReader(raw))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		writeError(w, 502, fmt.Sprintf("node %s error: %s", nodeName, err.Error()))
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if json.Unmarshal(b, &payload) != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
		return
	}
	// Pull jobs are node-local; expose the composite id so status polls route back.
	if job, _ := payload["job_id"].(string); job != "" {
		payload["job_id"] = nodeName + "/" + job
	}
	payload["node"] = nodeName
	writeJSON(w, resp.StatusCode, payload)
}

func (s *Server) handleUserDockerPullStatus(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	nodeName, localJob, found := strings.Cut(jobID, "/")
	if !found {
		writeError(w, 400, `job_id must be the composite "<node>/<job>" returned by pull`)
		return
	}
	n := s.Nodes.Get(nodeName)
	if n == nil {
		writeError(w, 503, fmt.Sprintf("unknown or offline node %q", nodeName))
		return
	}
	target := "http://" + nodeName + "/api/v1/user-dockers/pull/status?job_id=" + url.QueryEscape(localJob)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.doProxyVia(w, n.Client, req)
}

func (s *Server) handleUserDockerTouchCreatorSession(w http.ResponseWriter, r *http.Request) {
	nodeList := s.Nodes.List()
	if len(nodeList) == 0 {
		writeError(w, 503, noNodesMsg)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "body read failed: "+err.Error())
		return
	}
	// A session's temp containers may live on any node: broadcast, best effort.
	total := 0.0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range nodeList {
		wg.Add(1)
		go func(n *nodes.Node) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
				"http://"+n.Hello.Node+"/api/v1/user-dockers/touch-creator-session", bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := n.Client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var payload struct {
				Success bool    `json:"success"`
				Touched float64 `json:"touched"`
			}
			if json.NewDecoder(resp.Body).Decode(&payload) == nil && payload.Success {
				mu.Lock()
				total += payload.Touched
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	writeJSON(w, 200, map[string]any{"success": true, "touched": int(total)})
}

// --- helpers ---

func (s *Server) nodeFromQuery(w http.ResponseWriter, r *http.Request) (*nodes.Node, bool) {
	nodeName := r.URL.Query().Get("node")
	if nodeName == "" {
		n := s.Nodes.First()
		if n == nil {
			writeError(w, 503, noNodesMsg)
			return nil, false
		}
		return n, true
	}
	n := s.Nodes.Get(nodeName)
	if n == nil {
		writeError(w, 503, fmt.Sprintf("unknown or offline node %q", nodeName))
		return nil, false
	}
	return n, true
}

func (s *Server) proxyNodeGet(w http.ResponseWriter, r *http.Request, n *nodes.Node, path string) {
	target := "http://" + n.Hello.Node + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.doProxyVia(w, n.Client, req)
}

func (s *Server) nodeGetJSON(r *http.Request, n *nodes.Node, target string, out any) error {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("node returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *Server) doProxyVia(w http.ResponseWriter, client *http.Client, req *http.Request) {
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, 502, "node error: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
