package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/whalebot/adaptertelegram/internal/configstore"
	"github.com/whalebot/adaptertelegram/internal/imfmt"
	"github.com/whalebot/adaptertelegram/internal/registerclient"
)

var (
	reChannelToMessage = regexp.MustCompile(`(?is)<\|channel\|?>[\s\S]*?<\|message\|?>`)
	reChannelTailBlock = regexp.MustCompile(`(?is)<\|channel\|?>[\s\S]*$`)
	reMessageTag       = regexp.MustCompile(`(?is)<\|/?message\|?>`)
	reThinkTag         = regexp.MustCompile(`(?is)<think>(.*?)</think>`)
	reThoughtTag       = regexp.MustCompile(`(?is)<thought>(.*?)</thought>`)
	reReasoningTag     = regexp.MustCompile(`(?is)<reasoning>(.*?)</reasoning>`)
	reLooseMarkerTag   = regexp.MustCompile(`(?is)</?\|?(?:channel|message|think|thought|reasoning)\|?>`)
)

const (
	htmlSendAttempts  = 4
	plainSendAttempts = 4
	telegramChannel   = "telegram"
)

// telegramSessionID is the session_id used by runtime/session (channel_chatKey).
func telegramSessionID(localChatSessionKey string) string {
	return telegramChannel + "_" + localChatSessionKey
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// pollMgr owns at most one telegram long-poll goroutine; restart waits for the previous to exit.
type pollMgr struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (m *pollMgr) stopLocked() {
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
		m.cancel = nil
	}
}

func (m *pollMgr) restart(appCtx context.Context, st *configstore.Store, orchURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	token := strings.TrimSpace(st.GetBotToken())
	if token == "" {
		slog.Warn("no bot token in config; skipping telegram long poll (service still registered)")
		return
	}
	allowed := st.AllowedUserIDSet()
	pollCtx, cancel := context.WithCancel(appCtx)
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		pollLoop(pollCtx, token, orchURL, allowed)
	}()
}

func allowedTelegramUser(allowed map[int64]struct{}, from *tgbotapi.User) bool {
	if len(allowed) == 0 {
		return true
	}
	if from == nil {
		return false
	}
	_, ok := allowed[from.ID]
	return ok
}

type chatRequest struct {
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

type chatResponse struct {
	Success     bool             `json:"success"`
	SessionID   string           `json:"session_id"`
	Reply       string           `json:"reply"`
	TraceID     string           `json:"trace_id"`
	Attachments []chatAttachment `json:"attachments,omitempty"`
	Error       string           `json:"error,omitempty"`
}

type chatAttachment struct {
	Filename      string `json:"filename"`
	MimeType      string `json:"mime_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
	SourcePath    string `json:"source_path,omitempty"`
}

type loggerEvent struct {
	ID      int64             `json:"id"`
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

type loggerEventsResponse struct {
	Success bool          `json:"success"`
	Events  []loggerEvent `json:"events"`
	Error   string        `json:"error,omitempty"`
}

type sessionMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type chatState struct {
	CurrentSessionID  string
	Ended             bool
	NotifiedExpiredID string
}

type commandResult struct {
	Handled bool
	Reply   string
}

type conversationManager struct {
	mu    sync.RWMutex
	chats map[int64]*chatState
}

func newConversationManager() *conversationManager {
	return &conversationManager{chats: map[int64]*chatState{}}
}

func (m *conversationManager) getOrCreate(chatID int64) *chatState {
	st, ok := m.chats[chatID]
	if ok {
		return st
	}
	st = &chatState{
		CurrentSessionID: buildSessionID(chatID),
		Ended:            false,
	}
	m.chats[chatID] = st
	return st
}

func (m *conversationManager) resolveSessionID(chatID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrCreate(chatID).CurrentSessionID
}

// ensureActiveForMessage starts a new logical session if the user had ended the previous one.
func (m *conversationManager) ensureActiveForMessage(chatID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.getOrCreate(chatID)
	if st.Ended {
		st.Ended = false
		st.CurrentSessionID = buildSessionID(chatID)
		st.NotifiedExpiredID = ""
	}
	return st.CurrentSessionID
}

func (m *conversationManager) newSession(chatID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.getOrCreate(chatID)
	st.Ended = false
	st.NotifiedExpiredID = ""
	st.CurrentSessionID = buildSessionID(chatID)
	return st.CurrentSessionID
}

func buildSessionID(chatID int64) string {
	randomPart := "fallback"
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err == nil {
		randomPart = hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%d-%d-%s", chatID, time.Now().UnixMilli(), randomPart)
}

func (m *conversationManager) endSession(chatID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.getOrCreate(chatID)
	st.Ended = true
	return st.CurrentSessionID
}

func (m *conversationManager) status(chatID int64) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.chats[chatID]
	if !ok {
		return "（尚未开始）", false
	}
	return st.CurrentSessionID, st.Ended
}

func (m *conversationManager) snapshotSessions() []struct {
	ChatID    int64
	SessionID string
	Ended     bool
} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]struct {
		ChatID    int64
		SessionID string
		Ended     bool
	}, 0, len(m.chats))
	for id, st := range m.chats {
		out = append(out, struct {
			ChatID    int64
			SessionID string
			Ended     bool
		}{id, st.CurrentSessionID, st.Ended})
	}
	return out
}

func fetchSessionExpired(ctx context.Context, cli *http.Client, orchURL, sessionID string) (bool, error) {
	u := strings.TrimRight(orchURL, "/") + "/api/v1/sessions/" + url.PathEscape(sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("sessions status %d", resp.StatusCode)
	}
	var out struct {
		Session struct {
			Expired bool `json:"expired"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Session.Expired, nil
}

func sessionExpiryLoop(ctx context.Context, bot *tgbotapi.BotAPI, conv *conversationManager, cli *http.Client, orchURL string) {
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			snaps := conv.snapshotSessions()
			for _, sn := range snaps {
				if sn.Ended {
					continue
				}
				full := "telegram_" + sn.SessionID
				expired, err := fetchSessionExpired(ctx, cli, orchURL, full)
				if err != nil || !expired {
					continue
				}
				var newID string
				conv.mu.Lock()
				st, ok := conv.chats[sn.ChatID]
				if !ok || st.CurrentSessionID != sn.SessionID {
					conv.mu.Unlock()
					continue
				}
				if st.NotifiedExpiredID == full {
					conv.mu.Unlock()
					continue
				}
				st.NotifiedExpiredID = full
				newID = buildSessionID(sn.ChatID)
				st.CurrentSessionID = newID
				st.Ended = false
				conv.mu.Unlock()
				text := fmt.Sprintf(
					"会话已因闲置过期（`%s`）。\n已自动开启新会话。\n新 session_id: `%s`",
					full,
					newID,
				)
				if err := sendTelegramReply(ctx, bot, sn.ChatID, text, "", ""); err != nil {
					slog.Error("session expiry notice failed", "chat_id", sn.ChatID, "err", err)
				}
			}
		}
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("ADAPTER_TELEGRAM_PORT", "8084")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "adapter-telegram")
	self := "http://" + selfHost + ":" + port
	cfgPath := getenv("ADAPTER_CONFIG_PATH", "/data/adapter-config.json")

	st, err := configstore.Open(cfgPath)
	if err != nil {
		slog.Error("adapter config store", "err", err, "path", cfgPath)
		os.Exit(1)
	}

	appCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var polls pollMgr

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "adapter-telegram"})
	})

	r.Route("/api/v1/adapter", func(sr chi.Router) {
		sr.Get("/config", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "config": st.GetPublic()})
		})
		sr.Put("/config", func(w http.ResponseWriter, req *http.Request) {
			var body configstore.PutBody
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "invalid json: " + err.Error()})
				return
			}
			if err := st.ApplyPut(body); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
				return
			}
			polls.restart(appCtx, st, orchURL)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "config": st.GetPublic()})
		})
	})

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "adapter-telegram",
		Type:           "adapter",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"telegram_text"},
	})
	rc.Start(appCtx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("adapter-telegram listening", "port", port, "config", cfgPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	polls.restart(appCtx, st, orchURL)

	<-appCtx.Done()
	polls.mu.Lock()
	polls.stopLocked()
	polls.mu.Unlock()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(shCtx)
}

func pollLoop(ctx context.Context, token, orchURL string, allowed map[int64]struct{}) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		slog.Error("telegram bot init failed", "err", err)
		return
	}
	slog.Info("telegram bot connected", "username", bot.Self.UserName)
	registerBotCommands(bot)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	chatTimeoutSec := getenvInt("ADAPTER_TELEGRAM_CHAT_TIMEOUT_SEC", 240)
	cli := &http.Client{Timeout: time.Duration(chatTimeoutSec) * time.Second}
	orchSessCLI := &http.Client{Timeout: 15 * time.Second}
	sessionURL := getenv("SESSION_URL", "http://session:8090")
	sessionCLI := &http.Client{Timeout: 20 * time.Second}
	conv := newConversationManager()
	go sessionExpiryLoop(ctx, bot, conv, orchSessCLI, orchURL)

	for {
		select {
		case <-ctx.Done():
			bot.StopReceivingUpdates()
			return
		case update := <-updates:
			msg := update.Message
			if msg == nil || msg.Text == "" {
				continue
			}
			if !allowedTelegramUser(allowed, msg.From) {
				var uid any
				if msg.From != nil {
					uid = msg.From.ID
				}
				slog.Debug("telegram message ignored (user not in whitelist)", "user_id", uid)
				continue
			}
			cmd := handleCommand(conv, msg)
			if cmd.Handled {
				if err := sendTelegramReply(ctx, bot, msg.Chat.ID, cmd.Reply, "", ""); err != nil {
					slog.Error("telegram command reply failed", "chat_id", msg.Chat.ID, "err", err)
				}
				continue
			}
			localKey := conv.ensureActiveForMessage(msg.Chat.ID)
			fullSID := telegramSessionID(localKey)
			traceID := newTraceID()
			progressDone := make(chan struct{})
			tracker := newProgressTracker(bot, msg.Chat.ID)
			tracker.Start(ctx, cli, orchURL, fullSID, traceID)
			go telegramTypingLoop(ctx, bot, msg.Chat.ID, progressDone)
			chatResp, err := callOrchestrator(ctx, cli, orchURL, chatRequest{
				UserID:  strconv.FormatInt(msg.From.ID, 10),
				Channel: telegramChannel,
				ChatID:  localKey,
				Message: msg.Text,
				TraceID: traceID,
			})
			close(progressDone)
			reply := chatResp.Reply
			if chatResp.TraceID != "" {
				traceID = chatResp.TraceID
			}
			sidForLog := fullSID
			if strings.TrimSpace(chatResp.SessionID) != "" {
				sidForLog = chatResp.SessionID
			}
			if err != nil {
				slog.Error("orchestrator chat failed", "err", err)
				if strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded") {
					reply = fmt.Sprintf("执行超时（等待运行结果超过 %ds）。\nsession_id: `%s`\ntrace_id: `%s`\n请到 Session 页面查看中间步骤（tool_call/runtime事件）继续排查。", chatTimeoutSec, sidForLog, defaultText(traceID, "unknown"))
				} else {
					reply = fmt.Sprintf("抱歉，我暂时无法回应：%s\nsession_id: `%s`\ntrace_id: `%s`", err.Error(), sidForLog, defaultText(traceID, "unknown"))
				}
				tracker.FinishErr(reply)
				if !tracker.hasPlaceholder() {
					if err := sendTelegramReply(ctx, bot, msg.Chat.ID, reply, sidForLog, traceID); err != nil {
						slog.Error("telegram reply failed", "chat_id", msg.Chat.ID, "session_id", sidForLog, "trace_id", traceID, "err", err)
					}
				}
				_ = appendAssistantMessage(ctx, sessionCLI, sessionURL, fullSID, reply)
			} else {
				tracker.FinishOK()
				if err := sendTelegramReply(ctx, bot, msg.Chat.ID, reply, sidForLog, traceID); err != nil {
					slog.Error("telegram reply failed", "chat_id", msg.Chat.ID, "session_id", sidForLog, "trace_id", traceID, "err", err)
				}
				if len(chatResp.Attachments) > 0 {
					sendTelegramAttachments(ctx, bot, msg.Chat.ID, fullSID, traceID, chatResp.Attachments, sessionCLI, sessionURL)
				}
			}
		}
	}
}

func defaultText(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func newTraceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("trace_%d", time.Now().UnixNano())
	}
	return "trace_" + hex.EncodeToString(buf)
}

// telegramTypingLoop sends periodic "typing" chat actions until done is closed.
func telegramTypingLoop(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, done <-chan struct{}) {
	tick := time.NewTicker(4 * time.Second)
	defer tick.Stop()
	send := func() {
		if _, err := bot.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)); err != nil {
			slog.Debug("telegram chat action typing failed", "chat_id", chatID, "err", err)
		}
	}
	send()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-tick.C:
			send()
		}
	}
}

const (
	progressMaxTelegramChars = 4000
	progressEditMinInterval  = 2000 * time.Millisecond
)

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// describeToolStart returns a concise, tool-specific line (no leading "Step").
func describeToolStart(toolName, argsJSON string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "tool"
	}
	raw := strings.TrimSpace(argsJSON)
	if raw == "" {
		return toolName
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return toolName
	}
	action, _ := m["action"].(string)
	action = strings.TrimSpace(action)
	if toolName != "manage_user_docker" {
		if action != "" {
			return fmt.Sprintf("%s · %s", toolName, action)
		}
		return toolName
	}
	if action == "" {
		action = "?"
	}
	switch action {
	case "create":
		img, _ := m["image"].(string)
		if strings.TrimSpace(img) == "" {
			img, _ = m["framework"].(string)
		}
		return fmt.Sprintf("manage_user_docker · create · image=%s", truncateRunes(strings.TrimSpace(img), 72))
	case "start", "stop", "touch", "remove", "restart":
		name, _ := m["name"].(string)
		return fmt.Sprintf("manage_user_docker · %s · name=%s", action, truncateRunes(strings.TrimSpace(name), 56))
	case "exec":
		cwd, _ := m["cwd"].(string)
		cmd := ""
		if sh, ok := m["command_sh"].(string); ok && strings.TrimSpace(sh) != "" {
			cmd = strings.TrimSpace(sh)
		} else if arr, ok := m["command"].([]any); ok && len(arr) > 0 {
			parts := make([]string, 0, len(arr))
			for _, x := range arr {
				if s, ok := x.(string); ok {
					parts = append(parts, s)
				}
			}
			cmd = strings.Join(parts, " ")
		}
		return fmt.Sprintf("manage_user_docker · exec · cwd=%s · cmd=%s",
			truncateRunes(strings.TrimSpace(cwd), 40), truncateRunes(cmd, 48))
	case "read_file", "write_file", "delete_file", "list_files", "mkdir", "move", "export_artifact":
		path, _ := m["path"].(string)
		if strings.TrimSpace(path) == "" {
			path, _ = m["file_path"].(string)
		}
		return fmt.Sprintf("manage_user_docker · %s · path=%s", action, truncateRunes(strings.TrimSpace(path), 64))
	case "list_images", "list", "get_interface", "switch_scope":
		return fmt.Sprintf("manage_user_docker · %s", action)
	default:
		return fmt.Sprintf("manage_user_docker · %s", action)
	}
}

func describeToolEnd(toolName, argsJSON, durationMs string) string {
	base := describeToolStart(toolName, argsJSON)
	if strings.TrimSpace(durationMs) == "" || durationMs == "?" {
		return base + " · 完成"
	}
	return fmt.Sprintf("%s · 完成（%sms）", base, durationMs)
}

// eventToProgressText maps one logger row to the single-line status shown in the placeholder.
func eventToProgressText(evt loggerEvent, fullSessionID string) (string, bool) {
	f := evt.Fields
	if f == nil || f["session_id"] != fullSessionID {
		return "", false
	}
	step := defaultText(f["step"], "?")
	switch evt.Message {
	case "runtime_run_start":
		return "已开始处理，准备上下文…", true
	case "runtime_context_loaded":
		return fmt.Sprintf("上下文加载完成（历史 %s 条），开始推理…", defaultText(f["history_count"], "0")), true
	case "runtime_plan":
		return "已生成执行计划，等待你确认后再调用工具。", true
	case "runtime_plan_confirmed":
		return "计划已确认，开始执行…", true
	case "react_step_start":
		// Skip generic "thinking" headline; keep previous meaningful line until tool_call_* / final model response.
		return "", false
	case "react_model_response":
		if f["tool_call_count"] != "0" {
			return "", false
		}
		return fmt.Sprintf("Step %s：生成最终回复…", step), true
	case "tool_call_start":
		detail := describeToolStart(f["tool_name"], f["args"])
		return fmt.Sprintf("Step %s：%s", step, detail), true
	case "tool_call_end":
		detail := describeToolEnd(f["tool_name"], f["args"], f["duration_ms"])
		return fmt.Sprintf("Step %s：%s", step, detail), true
	case "tool_call_error":
		em := truncateRunes(defaultText(f["error_message"], "unknown error"), 140)
		detail := describeToolStart(f["tool_name"], f["args"])
		return fmt.Sprintf("Step %s：%s · 失败：%s", step, detail, em), true
	case "runtime_react_loop_error":
		return fmt.Sprintf("执行失败：%s", truncateRunes(defaultText(f["error_message"], "react loop error"), 220)), true
	case "react_step_limit_fallback":
		return "已达 ReAct 步数上限，正在收尾…", true
	default:
		return "", false
	}
}

// progressTracker maintains one Telegram placeholder message and edits it in place.
type progressTracker struct {
	mu sync.Mutex

	bot           *tgbotapi.BotAPI
	chatID        int64
	placeholderID int

	disabled               bool
	loopStarted            bool
	httpCli                *http.Client
	orchURL                string
	fullSessionID, traceID string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	seen        map[int64]struct{}
	currentText string
	lastFlushed string

	finishOnce sync.Once
}

func newProgressTracker(bot *tgbotapi.BotAPI, chatID int64) *progressTracker {
	return &progressTracker{
		bot:    bot,
		chatID: chatID,
		seen:   make(map[int64]struct{}),
	}
}

func (t *progressTracker) hasPlaceholder() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.placeholderID != 0 && !t.disabled
}

func (t *progressTracker) Start(parent context.Context, cli *http.Client, orchURL, fullSID, trace string) {
	t.httpCli = cli
	t.orchURL = orchURL
	t.fullSessionID = fullSID
	t.traceID = trace

	ctx, cancel := context.WithCancel(parent)
	t.ctx = ctx
	t.cancel = cancel

	m := tgbotapi.NewMessage(t.chatID, "已收到，正在处理…")
	m.DisableWebPagePreview = true
	sent, err := t.bot.Send(m)
	if err != nil {
		slog.Warn("progress placeholder send failed", "chat_id", t.chatID, "err", err)
		t.disabled = true
		cancel()
		return
	}
	t.placeholderID = sent.MessageID
	t.loopStarted = true
	t.wg.Add(1)
	go t.loop()
}

func (t *progressTracker) loop() {
	defer t.wg.Done()
	t.pollAndFlush()
	tick := time.NewTicker(progressEditMinInterval)
	defer tick.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-tick.C:
			t.pollAndFlush()
		}
	}
}

func (t *progressTracker) pollAndFlush() {
	if t.disabled {
		return
	}
	events, err := fetchLoggerEvents(t.ctx, t.httpCli, t.orchURL, 120)
	if err != nil {
		return
	}
	matched := make([]loggerEvent, 0, 32)
	for _, e := range events {
		if e.Fields == nil {
			continue
		}
		if e.Fields["trace_id"] != t.traceID || e.Fields["session_id"] != t.fullSessionID {
			continue
		}
		matched = append(matched, e)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	t.mu.Lock()
	for _, evt := range matched {
		if evt.ID <= 0 {
			continue
		}
		if _, ok := t.seen[evt.ID]; ok {
			continue
		}
		t.seen[evt.ID] = struct{}{}
		if txt, ok := eventToProgressText(evt, t.fullSessionID); ok {
			t.currentText = txt
		}
	}
	needFlush := t.placeholderID != 0 && t.currentText != "" && t.currentText != t.lastFlushed
	text := t.currentText
	t.mu.Unlock()

	if !needFlush {
		return
	}
	if err := t.editPlaceholderWithRetry(t.ctx, text); err != nil {
		slog.Warn("progress placeholder edit failed", "chat_id", t.chatID, "trace_id", t.traceID, "err", err)
		return
	}
	t.mu.Lock()
	t.lastFlushed = text
	t.mu.Unlock()
}

func (t *progressTracker) editPlaceholderWithRetry(opCtx context.Context, text string) error {
	body := truncateRunes(text, progressMaxTelegramChars)
	var lastErr error
	for attempt := 1; attempt <= 4; attempt++ {
		if opCtx.Err() != nil {
			return opCtx.Err()
		}
		ed := tgbotapi.NewEditMessageText(t.chatID, t.placeholderID, body)
		ed.DisableWebPagePreview = true
		if _, err := t.bot.Request(ed); err == nil {
			return nil
		} else {
			lastErr = err
			var tgErr *tgbotapi.Error
			if errors.As(err, &tgErr) && tgErr != nil && tgErr.RetryAfter > 0 {
				time.Sleep(time.Duration(tgErr.RetryAfter) * time.Second)
				continue
			}
			if isTelegramRetryableErr(err) && attempt < 4 {
				backoff := time.Duration(300*(1<<uint(attempt-1))) * time.Millisecond
				if backoff > 5*time.Second {
					backoff = 5 * time.Second
				}
				if !sleepWithContext(opCtx, backoff) {
					return opCtx.Err()
				}
				continue
			}
			return err
		}
	}
	return lastErr
}

func (t *progressTracker) stopLoop() {
	if t.cancel != nil {
		t.cancel()
	}
	if t.loopStarted {
		t.wg.Wait()
	}
}

func (t *progressTracker) FinishOK() {
	t.finishOnce.Do(func() {
		t.stopLoop()
		t.mu.Lock()
		id := t.placeholderID
		t.mu.Unlock()
		if id != 0 && !t.disabled {
			if _, err := t.bot.Request(tgbotapi.NewDeleteMessage(t.chatID, id)); err != nil {
				slog.Debug("progress placeholder delete failed", "chat_id", t.chatID, "err", err)
			}
		}
	})
}

func (t *progressTracker) FinishErr(text string) {
	t.finishOnce.Do(func() {
		body := truncateRunes(strings.TrimSpace(text), progressMaxTelegramChars)
		if body == "" {
			body = "请求处理失败。"
		}
		t.mu.Lock()
		id := t.placeholderID
		disabled := t.disabled
		t.mu.Unlock()
		// Edit before canceling tracker ctx, otherwise in-flight edit aborts immediately.
		if id != 0 && !disabled {
			_ = t.editPlaceholderWithRetry(context.Background(), body)
		}
		t.stopLoop()
	})
}

func fetchLoggerEvents(ctx context.Context, cli *http.Client, orchURL string, limit int) ([]loggerEvent, error) {
	u := fmt.Sprintf("%s/api/v1/logger/events/recent?limit=%d", orchURL, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("logger events status %d", resp.StatusCode)
	}
	var out loggerEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("logger events success=false: %s", out.Error)
	}
	return out.Events, nil
}

func sendTelegramAttachments(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, sessionID, traceID string, attachments []chatAttachment, sessionCLI *http.Client, sessionURL string) {
	for _, att := range attachments {
		raw, err := base64.StdEncoding.DecodeString(att.ContentBase64)
		if err != nil {
			_ = sendTelegramReply(ctx, bot, chatID, fmt.Sprintf("产物上传失败：`%s` base64 解析失败。", att.Filename), sessionID, traceID)
			continue
		}
		if len(raw) > 45*1024*1024 {
			_ = sendTelegramReply(ctx, bot, chatID, fmt.Sprintf("产物上传失败：`%s` 超过 Telegram 文件上限（%.2f MB）。", att.Filename, float64(len(raw))/1024.0/1024.0), sessionID, traceID)
			continue
		}
		name := att.Filename
		if strings.TrimSpace(name) == "" {
			name = "artifact.bin"
		}
		doc := tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{Name: name, Bytes: raw})
		doc.Caption = fmt.Sprintf("编译产物：%s", name)
		if _, err := bot.Send(doc); err != nil {
			slog.Error("telegram send attachment failed", "chat_id", chatID, "session_id", sessionID, "trace_id", traceID, "filename", name, "err", err)
			_ = sendTelegramReply(ctx, bot, chatID, fmt.Sprintf("产物上传失败：`%s`。", name), sessionID, traceID)
		} else {
			_ = appendAssistantMessage(ctx, sessionCLI, sessionURL, sessionID, fmt.Sprintf("[artifact] %s uploaded", name))
		}
	}
}

func appendAssistantMessage(ctx context.Context, cli *http.Client, sessionURL, sessionID, content string) error {
	if cli == nil || strings.TrimSpace(sessionURL) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"messages": []sessionMessage{
			{
				Role:      "assistant",
				Content:   content,
				Timestamp: time.Now(),
			},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL+"/append_messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("session append status %d", resp.StatusCode)
	}
	return nil
}

func sendTelegramReply(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, text, sessionID, traceID string) error {
	// Keep session storage unchanged; only sanitize outbound IM content.
	sanitized := sanitizeReplyForTelegram(text)
	if strings.TrimSpace(sanitized) == "" {
		sanitized = "（系统回复为空，请稍后重试）"
	}

	htmlPayload := imfmt.MarkdownToTelegramHTML(sanitized)
	htmlBuilder := func() tgbotapi.MessageConfig {
		out := tgbotapi.NewMessage(chatID, htmlPayload)
		out.ParseMode = "HTML"
		out.DisableWebPagePreview = true
		return out
	}
	if err := sendWithRetry(ctx, bot, htmlBuilder, htmlSendAttempts, "html", chatID, sessionID, traceID); err == nil {
		return nil
	} else if isTelegramFormatErr(err) {
		slog.Warn("telegram html send failed, fallback to plain text", "chat_id", chatID, "session_id", sessionID, "trace_id", traceID, "err", err)
		plainBuilder := func() tgbotapi.MessageConfig {
			out := tgbotapi.NewMessage(chatID, sanitized)
			out.DisableWebPagePreview = true
			return out
		}
		if plainErr := sendWithRetry(ctx, bot, plainBuilder, plainSendAttempts, "plain", chatID, sessionID, traceID); plainErr == nil {
			return nil
		} else {
			trySendFailureNotice(ctx, bot, chatID, sessionID, traceID, plainErr)
			return fmt.Errorf("html send failed: %w; plain fallback failed: %v", err, plainErr)
		}
	} else {
		trySendFailureNotice(ctx, bot, chatID, sessionID, traceID, err)
		return err
	}
}

func sanitizeReplyForTelegram(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	out := text
	out = reChannelToMessage.ReplaceAllString(out, "")
	out = reChannelTailBlock.ReplaceAllString(out, "")
	out = reMessageTag.ReplaceAllString(out, "")
	out = reThinkTag.ReplaceAllString(out, "")
	out = reThoughtTag.ReplaceAllString(out, "")
	out = reReasoningTag.ReplaceAllString(out, "")
	out = reLooseMarkerTag.ReplaceAllString(out, "")
	out = strings.TrimSpace(out)
	return out
}

func sendWithRetry(
	ctx context.Context,
	bot *tgbotapi.BotAPI,
	builder func() tgbotapi.MessageConfig,
	maxAttempts int,
	mode string,
	chatID int64,
	sessionID, traceID string,
) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := builder()
		if _, err := bot.Send(msg); err == nil {
			if attempt > 1 {
				slog.Info("telegram send recovered after retry", "mode", mode, "attempt", attempt, "chat_id", chatID, "session_id", sessionID, "trace_id", traceID)
			}
			return nil
		} else {
			lastErr = err
			slog.Warn("telegram send attempt failed", "mode", mode, "attempt", attempt, "max_attempts", maxAttempts, "chat_id", chatID, "session_id", sessionID, "trace_id", traceID, "err", err)
			if isTelegramFormatErr(err) {
				return err
			}
			if !isTelegramRetryableErr(err) || attempt == maxAttempts {
				return err
			}
			backoff := time.Duration(400*(1<<(attempt-1))) * time.Millisecond
			if backoff > 4*time.Second {
				backoff = 4 * time.Second
			}
			if !sleepWithContext(ctx, backoff) {
				return ctx.Err()
			}
		}
	}
	return lastErr
}

func trySendFailureNotice(ctx context.Context, bot *tgbotapi.BotAPI, chatID int64, sessionID, traceID string, rootErr error) {
	notice := "消息发送失败（已多次重试）。请稍后再试，或发送 /status 检查会话状态。"
	plainBuilder := func() tgbotapi.MessageConfig {
		out := tgbotapi.NewMessage(chatID, notice)
		out.DisableWebPagePreview = true
		return out
	}
	if err := sendWithRetry(ctx, bot, plainBuilder, 2, "failure_notice", chatID, sessionID, traceID); err != nil {
		slog.Error("telegram failure notice also failed", "chat_id", chatID, "session_id", sessionID, "trace_id", traceID, "root_err", rootErr, "notice_err", err)
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func isTelegramFormatErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "can't parse entities") ||
		strings.Contains(msg, "unsupported start tag") ||
		strings.Contains(msg, "bad request")
}

func isTelegramRetryableErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "retry after") ||
		strings.Contains(msg, "temporarily unavailable") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "broken pipe")
}

func handleCommand(conv *conversationManager, msg *tgbotapi.Message) commandResult {
	cmd := normalizeCommand(msg.Text)
	switch cmd {
	case "/help", "/commands":
		return commandResult{
			Handled: true,
			Reply: strings.TrimSpace(`
可用命令：
- /new: 开启新会话（不复用旧上下文）
- /end: 结束当前会话（下一条普通消息将自动开始新会话）
- /status: 查看当前会话状态
- /help: 查看命令帮助
`),
		}
	case "/new", "/reset":
		sessionID := conv.newSession(msg.Chat.ID)
		return commandResult{
			Handled: true,
			Reply:   fmt.Sprintf("已开启新会话。\nsession_id: `%s`", telegramSessionID(sessionID)),
		}
	case "/end", "/stop":
		sessionID := conv.endSession(msg.Chat.ID)
		return commandResult{
			Handled: true,
			Reply:   fmt.Sprintf("已结束当前会话。\nsession_id: `%s`\n下一条消息将自动开始新会话，或发送 /new 立即开启。", telegramSessionID(sessionID)),
		}
	case "/status":
		sessionID, ended := conv.status(msg.Chat.ID)
		state := "active"
		if ended {
			state = "ended"
		}
		return commandResult{
			Handled: true,
			Reply:   fmt.Sprintf("当前状态：**%s**\nsession_id: `%s`", state, telegramSessionID(sessionID)),
		}
	default:
		return commandResult{}
	}
}

func normalizeCommand(text string) string {
	raw := strings.TrimSpace(text)
	head := strings.Fields(raw)
	if len(head) == 0 {
		return ""
	}
	first := strings.ToLower(head[0])
	// Telegram command style: /new or /new@botname
	if strings.HasPrefix(first, "/") {
		cmd := first
		if at := strings.Index(cmd, "@"); at > 0 {
			cmd = cmd[:at]
		}
		return cmd
	}
	// Fallback plain-text shortcuts when users type without slash.
	switch first {
	case "new", "reset":
		return "/new"
	case "end", "stop":
		return "/end"
	case "status":
		return "/status"
	case "help", "commands":
		return "/help"
	default:
		return ""
	}
}

func registerBotCommands(bot *tgbotapi.BotAPI) {
	commands := []tgbotapi.BotCommand{
		{Command: "new", Description: "开启新会话"},
		{Command: "end", Description: "结束当前会话"},
		{Command: "status", Description: "查看会话状态"},
		{Command: "help", Description: "查看命令帮助"},
	}
	cfg := tgbotapi.NewSetMyCommands(commands...)
	if _, err := bot.Request(cfg); err != nil {
		slog.Warn("telegram setMyCommands failed", "err", err)
		return
	}
	slog.Info("telegram commands registered", "count", len(commands))
}

func callOrchestrator(ctx context.Context, cli *http.Client, orchURL string, req chatRequest) (chatResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, orchURL+"/api/v1/chat", bytes.NewReader(body))
	if err != nil {
		return chatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := cli.Do(httpReq)
	if err != nil {
		return chatResponse{}, err
	}
	defer resp.Body.Close()
	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return chatResponse{}, err
	}
	if !parsed.Success {
		if parsed.Error != "" {
			return parsed, errContextual(parsed.Error)
		}
		return parsed, errContextual("orchestrator returned success=false")
	}
	return parsed, nil
}

type contextualErr string

func (e contextualErr) Error() string { return string(e) }
func errContextual(s string) error    { return contextualErr(s) }
