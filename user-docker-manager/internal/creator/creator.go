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
	"strconv"
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
	Port         int               `json:"port,omitempty"`
}

type CreateResult struct {
	ContainerID string
	Name        string
	Port        int
	Interface   InterfaceDescriptor
}

type ContainerSummary struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	State   string            `json:"state"`
	Status  string            `json:"status"`
	Created int64             `json:"created"`
	Labels  map[string]string `json:"labels,omitempty"`
}

type InterfaceEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type InterfaceCapability struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type InterfaceDescriptor struct {
	InterfaceVersion string                `json:"interface_version"`
	ServiceName      string                `json:"service_name"`
	ServiceType      string                `json:"service_type"`
	Description      string                `json:"description"`
	Endpoints        []InterfaceEndpoint   `json:"endpoints"`
	Capabilities     []InterfaceCapability `json:"capabilities"`
}

var requiredInterface = InterfaceDescriptor{
	InterfaceVersion: "userdocker.v1",
	ServiceType:      "userdocker",
	Description:      "Standardized User Docker runtime contract for WhalesBot MVP.",
	Endpoints: []InterfaceEndpoint{
		{Method: "GET", Path: "/health", Description: "Container health check endpoint."},
		{Method: "GET", Path: "/api/v1/userdocker/interface", Description: "Returns full user docker interface descriptor."},
	},
	Capabilities: []InterfaceCapability{
		{Name: "introspection", Description: "Describes supported endpoints and capabilities."},
		{Name: "long_running", Description: "Supports long-running process mode for user tasks."},
	},
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
	Image            string              `json:"Image"`
	Cmd              []string            `json:"Cmd,omitempty"`
	Env              []string            `json:"Env,omitempty"`
	Labels           map[string]string   `json:"Labels,omitempty"`
	ExposedPorts     map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig       *hostConfig         `json:"HostConfig,omitempty"`
	NetworkingConfig *networkingConfig   `json:"NetworkingConfig,omitempty"`
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

type listContainersResponse struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
}

type inspectContainerResponse struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

type dockerError struct {
	Message string `json:"message"`
}

func RequiredInterfaceContract() InterfaceDescriptor {
	contract := requiredInterface
	contract.Endpoints = append([]InterfaceEndpoint(nil), requiredInterface.Endpoints...)
	contract.Capabilities = append([]InterfaceCapability(nil), requiredInterface.Capabilities...)
	return contract
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
	port := req.Port
	if port <= 0 {
		port = 9000
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
	labels["mvp.managed_by"] = "user-docker-manager"
	labels["mvp.userdocker.interface_version"] = requiredInterface.InterfaceVersion
	labels["mvp.userdocker.port"] = strconv.Itoa(port)

	envList := make([]string, 0, len(req.Env)+4)
	for k, v := range req.Env {
		envList = append(envList, k+"="+v)
	}
	envList = append(envList, "PORT="+strconv.Itoa(port))
	if req.AutoRegister {
		envList = append(envList,
			"ORCHESTRATOR_URL="+c.OrchestratorURL,
			"COMPONENT_NAME="+req.Name,
			"COMPONENT_TYPE="+labels["mvp.type"],
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

	contract, err := c.waitForContract(ctx, req.Name, port)
	if err != nil {
		_ = c.Remove(context.Background(), req.Name, true)
		return CreateResult{}, fmt.Errorf("userdocker contract validation failed: %w", err)
	}

	return CreateResult{ContainerID: cr.ID, Name: req.Name, Port: port, Interface: contract}, nil
}

func (c *Creator) List(ctx context.Context, includeStopped bool) ([]ContainerSummary, error) {
	filters, _ := json.Marshal(map[string][]string{
		"label": {"mvp.type=userdocker", "mvp.component=true"},
	})
	listURL := fmt.Sprintf("%s/containers/json?all=%d&filters=%s",
		c.baseURL, boolAsInt(includeStopped), url.QueryEscape(string(filters)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, dockerAPIError("containers list", resp.StatusCode, body)
	}
	var raw []listContainersResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	out := make([]ContainerSummary, 0, len(raw))
	for _, item := range raw {
		name := ""
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		out = append(out, ContainerSummary{
			ID:      item.ID,
			Name:    name,
			Image:   item.Image,
			State:   item.State,
			Status:  item.Status,
			Created: item.Created,
			Labels:  item.Labels,
		})
	}
	return out, nil
}

func (c *Creator) Remove(ctx context.Context, name string, force bool) error {
	if name == "" {
		return errors.New("name is required")
	}
	rmURL := fmt.Sprintf("%s/containers/%s?force=%d", c.baseURL, url.PathEscape(name), boolAsInt(force))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, rmURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return dockerAPIError("container remove", resp.StatusCode, body)
	}
	return nil
}

func (c *Creator) Restart(ctx context.Context, name string, timeoutSec int) error {
	if name == "" {
		return errors.New("name is required")
	}
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	restartURL := fmt.Sprintf("%s/containers/%s/restart?t=%d", c.baseURL, url.PathEscape(name), timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, restartURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return dockerAPIError("container restart", resp.StatusCode, body)
	}
	return nil
}

func (c *Creator) FetchInterfaceDescriptor(ctx context.Context, name string, port int) (InterfaceDescriptor, error) {
	if name == "" {
		return InterfaceDescriptor{}, errors.New("name is required")
	}
	if port <= 0 {
		detectedPort, err := c.userDockerPort(ctx, name)
		if err != nil {
			return InterfaceDescriptor{}, err
		}
		port = detectedPort
	}
	return c.fetchContract(ctx, name, port)
}

func (c *Creator) waitForContract(ctx context.Context, name string, port int) (InterfaceDescriptor, error) {
	attempts := 12
	var lastErr error
	for i := 0; i < attempts; i++ {
		contract, err := c.fetchContract(ctx, name, port)
		if err == nil {
			if err := validateInterfaceContract(contract); err == nil {
				return contract, nil
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return InterfaceDescriptor{}, ctx.Err()
		case <-time.After(800 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("contract not ready")
	}
	return InterfaceDescriptor{}, lastErr
}

func (c *Creator) fetchContract(ctx context.Context, name string, port int) (InterfaceDescriptor, error) {
	url := fmt.Sprintf("http://%s:%d/api/v1/userdocker/interface", name, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return InterfaceDescriptor{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return InterfaceDescriptor{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return InterfaceDescriptor{}, fmt.Errorf("contract endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var contract InterfaceDescriptor
	if err := json.Unmarshal(body, &contract); err != nil {
		return InterfaceDescriptor{}, fmt.Errorf("decode contract: %w", err)
	}
	return contract, nil
}

func validateInterfaceContract(contract InterfaceDescriptor) error {
	if contract.InterfaceVersion != requiredInterface.InterfaceVersion {
		return fmt.Errorf("unsupported interface_version %q", contract.InterfaceVersion)
	}
	if contract.ServiceType != requiredInterface.ServiceType {
		return fmt.Errorf("invalid service_type %q", contract.ServiceType)
	}
	hasHealth := false
	hasDescriptor := false
	for _, ep := range contract.Endpoints {
		if ep.Method == "GET" && ep.Path == "/health" {
			hasHealth = true
		}
		if ep.Method == "GET" && ep.Path == "/api/v1/userdocker/interface" {
			hasDescriptor = true
		}
	}
	if !hasHealth || !hasDescriptor {
		return errors.New("contract missing required endpoints")
	}
	return nil
}

func (c *Creator) userDockerPort(ctx context.Context, name string) (int, error) {
	inspectURL := fmt.Sprintf("%s/containers/%s/json", c.baseURL, url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, inspectURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return 0, dockerAPIError("container inspect", resp.StatusCode, body)
	}
	var item inspectContainerResponse
	if err := json.Unmarshal(body, &item); err != nil {
		return 0, fmt.Errorf("decode inspect response: %w", err)
	}
	if item.Config.Labels != nil {
		if portText := item.Config.Labels["mvp.userdocker.port"]; portText != "" {
			if port, err := strconv.Atoi(portText); err == nil && port > 0 {
				return port, nil
			}
		}
	}
	return 9000, nil
}

func boolAsInt(v bool) int {
	if v {
		return 1
	}
	return 0
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
