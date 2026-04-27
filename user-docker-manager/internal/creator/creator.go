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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ScopeSessionScoped = "session_scoped"
	ScopeGlobalService = "global_service"
)

type CreateRequest struct {
	Name                        string            `json:"name"`
	Image                       string            `json:"image"`
	Cmd                         []string          `json:"cmd"`
	Env                         map[string]string `json:"env"`
	Labels                      map[string]string `json:"labels"`
	Network                     string            `json:"network"`
	AutoRegister                bool              `json:"auto_register"`
	Port                        int               `json:"port,omitempty"`
	Scope                       string            `json:"scope,omitempty"`
	SessionID                   string            `json:"session_id,omitempty"`
	Workspace                   string            `json:"workspace,omitempty"`
	ExternalImageApprovedByUser bool              `json:"external_image_approved_by_user,omitempty"`
}

type CreateResult struct {
	ContainerID string
	Name        string
	Port        int
	Interface   InterfaceDescriptor
}

type ContainerSummary struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	Created      int64             `json:"created"`
	Labels       map[string]string `json:"labels,omitempty"`
	Scope        string            `json:"scope,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	Workspace    string            `json:"workspace,omitempty"`
	LastActiveAt string            `json:"last_active_at,omitempty"`
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

type ContainerMeta struct {
	Name         string            `json:"name"`
	Port         int               `json:"port"`
	Scope        string            `json:"scope"`
	SessionID    string            `json:"session_id,omitempty"`
	Workspace    string            `json:"workspace,omitempty"`
	LastActiveAt string            `json:"last_active_at,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
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
	directHTTP      *http.Client
	DefaultImage    string
	DefaultNetwork  string
	OrchestratorURL string
	AllowedImages   map[string]struct{}
	mu              sync.RWMutex
	lastActive      map[string]time.Time
}

func New(defaultImage, defaultNetwork, orchestratorURL string, allowedImages []string) (*Creator, error) {
	socketPath := "/var/run/docker.sock"
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	allowed := map[string]struct{}{}
	for _, img := range allowedImages {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		allowed[img] = struct{}{}
	}
	if defaultImage != "" {
		allowed[defaultImage] = struct{}{}
	}
	return &Creator{
		socketPath:      socketPath,
		baseURL:         "http://docker",
		httpClient:      &http.Client{Transport: tr, Timeout: 120 * time.Second},
		directHTTP:      &http.Client{Timeout: 15 * time.Second},
		DefaultImage:    defaultImage,
		DefaultNetwork:  defaultNetwork,
		OrchestratorURL: orchestratorURL,
		AllowedImages:   allowed,
		lastActive:      map[string]time.Time{},
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
	Binds []string `json:"Binds,omitempty"`
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
		Image  string            `json:"Image"`
		Cmd    []string          `json:"Cmd"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			NetworkID string `json:"NetworkID"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
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
	scope := req.Scope
	if scope == "" {
		scope = ScopeSessionScoped
	}
	if scope != ScopeSessionScoped && scope != ScopeGlobalService {
		return CreateResult{}, fmt.Errorf("invalid scope %q", scope)
	}
	if scope == ScopeSessionScoped && req.SessionID == "" {
		return CreateResult{}, errors.New("session_id is required for session_scoped containers")
	}
	containerName := req.Name
	if scope == ScopeSessionScoped {
		containerName = appendSessionSuffix(req.Name, req.SessionID)
	}
	img := req.Image
	if img == "" {
		img = c.DefaultImage
	}
	if isFrameworkImage(img) && !c.isAllowedImage(img) {
		return CreateResult{}, fmt.Errorf("framework image %q is not configured. allowed images: %s", img, strings.Join(c.AllowedImageList(), ", "))
	}
	if !isFrameworkImage(img) && !req.ExternalImageApprovedByUser {
		return CreateResult{}, fmt.Errorf(
			"external image %q requires explicit user approval; prefer framework images (for example %q)",
			img, c.DefaultImage,
		)
	}
	netName := req.Network
	if netName == "" {
		netName = c.DefaultNetwork
	}
	port := req.Port
	if port <= 0 {
		port = 9000
	}
	workspace := req.Workspace
	if workspace == "" {
		if scope == ScopeSessionScoped {
			workspace = "workspace_" + req.SessionID
		} else {
			workspace = "workspace_global_" + req.Name
		}
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
	labels["mvp.userdocker.scope"] = scope
	labels["mvp.userdocker.workspace"] = workspace
	labels["mvp.userdocker.last_active_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if scope == ScopeSessionScoped {
		labels["mvp.userdocker.session_id"] = req.SessionID
		labels["mvp.userdocker.creator_session_id"] = req.SessionID
	} else {
		delete(labels, "mvp.userdocker.session_id")
		delete(labels, "mvp.userdocker.creator_session_id")
	}

	envList := make([]string, 0, len(req.Env)+4)
	for k, v := range req.Env {
		envList = append(envList, k+"="+v)
	}
	envList = append(envList, "PORT="+strconv.Itoa(port))
	envList = append(envList, "WORKSPACE_ROOT=/workspace")
	envList = append(envList, "MVP_USERDOCKER_SCOPE="+scope)
	if req.SessionID != "" {
		envList = append(envList, "MVP_SESSION_ID="+req.SessionID)
	}
	if req.AutoRegister {
		envList = append(envList,
			"ORCHESTRATOR_URL="+c.OrchestratorURL,
			"COMPONENT_NAME="+containerName,
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
			Binds: []string{workspace + ":/workspace"},
		},
		NetworkingConfig: &networkingConfig{
			EndpointsConfig: map[string]struct{}{
				netName: {},
			},
		},
	}

	body, _ := json.Marshal(cfg)
	createURL := c.baseURL + "/containers/create?name=" + url.QueryEscape(containerName)
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

	contract, err := c.waitForContract(ctx, containerName, port)
	if err != nil {
		_ = c.Remove(context.Background(), containerName, true)
		if strings.Contains(err.Error(), "contract endpoint returned 404") {
			return CreateResult{}, fmt.Errorf(
				"userdocker contract validation failed: image %q is incompatible (missing /api/v1/userdocker/interface). use a userdocker-compatible image such as %q",
				img, c.DefaultImage,
			)
		}
		return CreateResult{}, fmt.Errorf("userdocker contract validation failed: %w", err)
	}
	c.markActive(containerName, time.Now().UTC())

	return CreateResult{ContainerID: cr.ID, Name: containerName, Port: port, Interface: contract}, nil
}

func appendSessionSuffix(baseName, sessionID string) string {
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		baseName = "userdocker"
	}
	token := sanitizeSessionToken(sessionID)
	if token == "" {
		return baseName
	}
	suffix := "-" + token
	if strings.HasSuffix(baseName, suffix) {
		return baseName
	}
	const maxNameLen = 120
	maxBaseLen := maxNameLen - len(suffix)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}
	if len(baseName) > maxBaseLen {
		baseName = baseName[:maxBaseLen]
	}
	baseName = strings.Trim(baseName, "-_.")
	if baseName == "" {
		baseName = "userdocker"
	}
	return baseName + suffix
}

func sanitizeSessionToken(sessionID string) string {
	const maxTokenLen = 16
	source := extractTailRandomToken(sessionID)
	if source == "" {
		source = sessionID
	}
	var b strings.Builder
	b.Grow(len(source))
	for _, r := range strings.ToLower(source) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '-' || r == '_' || r == '.' {
			b.WriteByte('-')
		}
		if b.Len() >= maxTokenLen {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "session"
	}
	return out
}

func extractTailRandomToken(sessionID string) string {
	raw := strings.TrimSpace(sessionID)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "-")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue
		}
		if isAlphaNumToken(p) && len(p) >= 8 {
			if len(p) > 16 {
				return p[len(p)-16:]
			}
			return p
		}
	}
	return ""
}

func isAlphaNumToken(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
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
		scope := item.Labels["mvp.userdocker.scope"]
		if scope == "" {
			scope = ScopeSessionScoped
		}
		lastActive := item.Labels["mvp.userdocker.last_active_at"]
		if t, ok := c.getLastActive(name); ok {
			lastActive = t.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, ContainerSummary{
			ID:           item.ID,
			Name:         name,
			Image:        item.Image,
			State:        item.State,
			Status:       item.Status,
			Created:      item.Created,
			Labels:       item.Labels,
			Scope:        scope,
			SessionID:    item.Labels["mvp.userdocker.session_id"],
			Workspace:    item.Labels["mvp.userdocker.workspace"],
			LastActiveAt: lastActive,
		})
	}
	return out, nil
}

func (c *Creator) Remove(ctx context.Context, name string, force bool) error {
	if name == "" {
		return errors.New("name is required")
	}
	if _, err := c.inspectManaged(ctx, name); err != nil {
		return err
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
	c.clearLastActive(name)
	return nil
}

func (c *Creator) Restart(ctx context.Context, name string, timeoutSec int) error {
	if name == "" {
		return errors.New("name is required")
	}
	if _, err := c.inspectManaged(ctx, name); err != nil {
		return err
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
	c.markActive(name, time.Now().UTC())
	return nil
}

func (c *Creator) Start(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if _, err := c.inspectManaged(ctx, name); err != nil {
		return err
	}
	startURL := c.baseURL + "/containers/" + url.PathEscape(name) + "/start"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != 304 {
		return dockerAPIError("container start", resp.StatusCode, body)
	}
	c.markActive(name, time.Now().UTC())
	return nil
}

func (c *Creator) Stop(ctx context.Context, name string, timeoutSec int) error {
	if name == "" {
		return errors.New("name is required")
	}
	if _, err := c.inspectManaged(ctx, name); err != nil {
		return err
	}
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	stopURL := fmt.Sprintf("%s/containers/%s/stop?t=%d", c.baseURL, url.PathEscape(name), timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stopURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != 304 {
		return dockerAPIError("container stop", resp.StatusCode, body)
	}
	return nil
}

func (c *Creator) Touch(ctx context.Context, name string) (time.Time, error) {
	if name == "" {
		return time.Time{}, errors.New("name is required")
	}
	if _, err := c.inspectManaged(ctx, name); err != nil {
		return time.Time{}, err
	}
	now := time.Now().UTC()
	c.markActive(name, now)
	return now, nil
}

// TouchByCreatorSessionID extends last-activity for all session_scoped (temporary) userdockers
// created under the given runtime session id, so the idle removal timer resets.
func (c *Creator) TouchByCreatorSessionID(ctx context.Context, creatorSessionID string) (int, error) {
	creatorSessionID = strings.TrimSpace(creatorSessionID)
	if creatorSessionID == "" {
		return 0, errors.New("session_id is required")
	}
	items, err := c.List(ctx, true)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if it.Scope != ScopeSessionScoped {
			continue
		}
		cid := ""
		if it.Labels != nil {
			cid = it.Labels["mvp.userdocker.creator_session_id"]
			if cid == "" {
				cid = it.Labels["mvp.userdocker.session_id"]
			}
		}
		if cid != creatorSessionID {
			continue
		}
		if _, err := c.Touch(ctx, it.Name); err != nil {
			continue
		}
		n++
	}
	return n, nil
}

func (c *Creator) SwitchScope(ctx context.Context, name, targetScope, sessionID string) (CreateResult, error) {
	if name == "" {
		return CreateResult{}, errors.New("name is required")
	}
	if targetScope != ScopeSessionScoped && targetScope != ScopeGlobalService {
		return CreateResult{}, fmt.Errorf("invalid target scope %q", targetScope)
	}
	if targetScope == ScopeSessionScoped && sessionID == "" {
		return CreateResult{}, errors.New("session_id is required when switching to session_scoped")
	}
	meta, err := c.inspectManaged(ctx, name)
	if err != nil {
		return CreateResult{}, err
	}
	port, _ := strconv.Atoi(meta.Config.Labels["mvp.userdocker.port"])
	if port <= 0 {
		port = 9000
	}
	workspace := meta.Config.Labels["mvp.userdocker.workspace"]
	if targetScope == ScopeSessionScoped {
		workspace = "workspace_" + sessionID
	} else if workspace == "" {
		workspace = "workspace_global_" + name
	}
	envMap := envListToMap(meta.Config.Env)
	delete(envMap, "MVP_SESSION_ID")
	envMap["MVP_USERDOCKER_SCOPE"] = targetScope
	if sessionID != "" {
		envMap["MVP_SESSION_ID"] = sessionID
	}
	labels := map[string]string{}
	for k, v := range meta.Config.Labels {
		if !strings.HasPrefix(k, "mvp.") || k == "mvp.type" {
			labels[k] = v
		}
	}
	createReq := CreateRequest{
		Name:         name,
		Image:        meta.Config.Image,
		Cmd:          meta.Config.Cmd,
		Env:          envMap,
		Labels:       labels,
		Network:      meta.firstNetwork(),
		AutoRegister: true,
		Port:         port,
		Scope:        targetScope,
		SessionID:    sessionID,
		Workspace:    workspace,
	}
	if err := c.Remove(ctx, name, true); err != nil {
		return CreateResult{}, err
	}
	return c.Create(ctx, createReq)
}

func (c *Creator) ContainerMeta(ctx context.Context, name string) (ContainerMeta, error) {
	item, err := c.inspectManaged(ctx, name)
	if err != nil {
		return ContainerMeta{}, err
	}
	port := 9000
	if p, err := strconv.Atoi(item.Config.Labels["mvp.userdocker.port"]); err == nil && p > 0 {
		port = p
	}
	scope := item.Config.Labels["mvp.userdocker.scope"]
	if scope == "" {
		scope = ScopeSessionScoped
	}
	lastActive := item.Config.Labels["mvp.userdocker.last_active_at"]
	if t, ok := c.getLastActive(name); ok {
		lastActive = t.UTC().Format(time.RFC3339Nano)
	}
	return ContainerMeta{
		Name:         name,
		Port:         port,
		Scope:        scope,
		SessionID:    item.Config.Labels["mvp.userdocker.session_id"],
		Workspace:    item.Config.Labels["mvp.userdocker.workspace"],
		LastActiveAt: lastActive,
		Labels:       item.Config.Labels,
	}, nil
}

func (c *Creator) FetchInterfaceDescriptor(ctx context.Context, name string, port int) (InterfaceDescriptor, error) {
	if name == "" {
		return InterfaceDescriptor{}, errors.New("name is required")
	}
	if _, err := c.inspectManaged(ctx, name); err != nil {
		return InterfaceDescriptor{}, err
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
	resp, err := c.directHTTP.Do(req)
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

func (c *Creator) inspectManaged(ctx context.Context, name string) (inspectContainerResponse, error) {
	inspectURL := fmt.Sprintf("%s/containers/%s/json", c.baseURL, url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, inspectURL, nil)
	if err != nil {
		return inspectContainerResponse{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return inspectContainerResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return inspectContainerResponse{}, dockerAPIError("container inspect", resp.StatusCode, body)
	}
	var item inspectContainerResponse
	if err := json.Unmarshal(body, &item); err != nil {
		return inspectContainerResponse{}, fmt.Errorf("decode inspect response: %w", err)
	}
	labels := item.Config.Labels
	if labels == nil {
		return inspectContainerResponse{}, errors.New("container labels missing")
	}
	if labels["mvp.type"] != "userdocker" || labels["mvp.managed_by"] != "user-docker-manager" {
		return inspectContainerResponse{}, errors.New("target container is not a managed userdocker")
	}
	return item, nil
}

func (i inspectContainerResponse) firstNetwork() string {
	for name := range i.NetworkSettings.Networks {
		return name
	}
	return ""
}

func (c *Creator) markActive(name string, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastActive[name] = t
}

func (c *Creator) getLastActive(name string) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.lastActive[name]
	return t, ok
}

func (c *Creator) clearLastActive(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.lastActive, name)
}

func envListToMap(in []string) map[string]string {
	out := make(map[string]string, len(in))
	for _, kv := range in {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[0] != "" {
			out[parts[0]] = parts[1]
		}
	}
	return out
}

func (c *Creator) userDockerPort(ctx context.Context, name string) (int, error) {
	item, err := c.inspectManaged(ctx, name)
	if err != nil {
		return 0, err
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
	return isFrameworkImage(ref)
}

func isFrameworkImage(ref string) bool {
	if strings.HasPrefix(ref, "whalesbot/") {
		return true
	}
	if strings.HasPrefix(ref, "mvp/") {
		return true
	}
	return false
}

func (c *Creator) AllowedImageList() []string {
	out := make([]string, 0, len(c.AllowedImages))
	for img := range c.AllowedImages {
		out = append(out, img)
	}
	sort.Strings(out)
	return out
}

func (c *Creator) isAllowedImage(ref string) bool {
	_, ok := c.AllowedImages[ref]
	return ok
}
