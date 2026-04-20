package creator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CreateRequest struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Cmd          []string          `json:"cmd"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Network      string            `json:"network"`
	AutoRegister bool              `json:"auto_register"`
}

type CreateResult struct {
	ContainerID string
	Name        string
}

// Creator talks to the Docker Engine HTTP API over a Unix socket. Using raw
// HTTP avoids pulling in the heavyweight docker SDK and its transitive Go
// version constraints.
type Creator struct {
	socketPath      string
	baseURL         string
	httpClient      *http.Client
	DefaultImage    string
	DefaultNetwork  string
	OrchestratorURL string
}

func New(defaultImage, defaultNetwork, orchestratorURL string) (*Creator, error) {
	socketPath := "/var/run/docker.sock"
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Creator{
		socketPath:      socketPath,
		baseURL:         "http://docker",
		httpClient:      &http.Client{Transport: tr, Timeout: 120 * time.Second},
		DefaultImage:    defaultImage,
		DefaultNetwork:  defaultNetwork,
		OrchestratorURL: orchestratorURL,
	}, nil
}

// --- Docker API types (only the fields we use) ---

type containerConfig struct {
	Image        string            `json:"Image"`
	Cmd          []string          `json:"Cmd,omitempty"`
	Env          []string          `json:"Env,omitempty"`
	Labels       map[string]string `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig   *hostConfig       `json:"HostConfig,omitempty"`
	NetworkingConfig *networkingConfig `json:"NetworkingConfig,omitempty"`
}

type hostConfig struct {
	RestartPolicy struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
}

type networkingConfig struct {
	EndpointsConfig map[string]struct{} `json:"EndpointsConfig"`
}

type createContainerResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type dockerError struct {
	Message string `json:"message"`
}

// Create pulls the image if missing, then creates+starts a container attached
// to the target network with the MVP labels and (when requested) the env vars
// our userdocker-base image uses to self-register.
func (c *Creator) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	if req.Name == "" {
		return CreateResult{}, errors.New("name is required")
	}
	img := req.Image
	if img == "" {
		img = c.DefaultImage
	}
	netName := req.Network
	if netName == "" {
		netName = c.DefaultNetwork
	}

	if !isLocalImage(img) {
		if err := c.ensureImage(ctx, img); err != nil {
			return CreateResult{}, fmt.Errorf("pull image: %w", err)
		}
	}

	labels := map[string]string{}
	for k, v := range req.Labels {
		labels[k] = v
	}
	labels["mvp.component"] = "true"
	if _, ok := labels["mvp.type"]; !ok {
		labels["mvp.type"] = "userdocker"
	}

	envList := make([]string, 0, len(req.Env)+4)
	for k, v := range req.Env {
		envList = append(envList, k+"="+v)
	}
	if req.AutoRegister {
		envList = append(envList,
			"ORCHESTRATOR_URL="+c.OrchestratorURL,
			"COMPONENT_NAME="+req.Name,
			"COMPONENT_TYPE="+labels["mvp.type"],
			"PORT=9000",
		)
	}

	cfg := containerConfig{
		Image:  img,
		Cmd:    req.Cmd,
		Env:    envList,
		Labels: labels,
		HostConfig: &hostConfig{
			RestartPolicy: struct {
				Name string `json:"Name"`
			}{Name: "unless-stopped"},
		},
		NetworkingConfig: &networkingConfig{
			EndpointsConfig: map[string]struct{}{
				netName: {},
			},
		},
	}

	body, _ := json.Marshal(cfg)
	createURL := c.baseURL + "/containers/create?name=" + url.QueryEscape(req.Name)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return CreateResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return CreateResult{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return CreateResult{}, dockerAPIError("container create", resp.StatusCode, respBody)
	}
	var cr createContainerResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return CreateResult{}, fmt.Errorf("decode create response: %w", err)
	}

	startURL := c.baseURL + "/containers/" + cr.ID + "/start"
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, nil)
	if err != nil {
		return CreateResult{}, err
	}
	startResp, err := c.httpClient.Do(startReq)
	if err != nil {
		return CreateResult{}, err
	}
	defer startResp.Body.Close()
	if startResp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(startResp.Body)
		return CreateResult{}, dockerAPIError("container start", startResp.StatusCode, bodyBytes)
	}

	return CreateResult{ContainerID: cr.ID, Name: req.Name}, nil
}

func (c *Creator) ensureImage(ctx context.Context, ref string) error {
	inspectURL := c.baseURL + "/images/" + url.PathEscape(ref) + "/json"
	inspectReq, err := http.NewRequestWithContext(ctx, http.MethodGet, inspectURL, nil)
	if err != nil {
		return err
	}
	insResp, err := c.httpClient.Do(inspectReq)
	if err == nil {
		insResp.Body.Close()
		if insResp.StatusCode == 200 {
			return nil
		}
	}

	image, tag := parseRef(ref)
	pullURL := fmt.Sprintf("%s/images/create?fromImage=%s&tag=%s",
		c.baseURL, url.QueryEscape(image), url.QueryEscape(tag))
	pullReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pullURL, nil)
	if err != nil {
		return err
	}
	pullResp, err := c.httpClient.Do(pullReq)
	if err != nil {
		return err
	}
	defer pullResp.Body.Close()
	if pullResp.StatusCode >= 300 {
		body, _ := io.ReadAll(pullResp.Body)
		return dockerAPIError("image pull", pullResp.StatusCode, body)
	}
	// Drain the streaming response so the pull completes synchronously.
	_, _ = io.Copy(io.Discard, pullResp.Body)
	return nil
}

func parseRef(ref string) (image, tag string) {
	image = ref
	tag = "latest"
	// Split on the last ':' only if it appears after the last '/'. This
	// preserves "host:port/name" registry paths.
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		slash := strings.LastIndex(ref, "/")
		if i > slash {
			image = ref[:i]
			tag = ref[i+1:]
		}
	}
	return
}

func dockerAPIError(op string, status int, body []byte) error {
	var e dockerError
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return fmt.Errorf("%s failed (status %d): %s", op, status, e.Message)
	}
	return fmt.Errorf("%s failed (status %d): %s", op, status, strings.TrimSpace(string(body)))
}

// isLocalImage heuristically identifies images we built locally (e.g. via
// docker compose build) whose names typically do not contain a registry host
// slash or are tagged with a well-known prefix. For these, skip ImagePull.
func isLocalImage(ref string) bool {
	if strings.HasPrefix(ref, "whalesbot/") {
		return true
	}
	if strings.HasPrefix(ref, "mvp/") {
		return true
	}
	return false
}
