package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/whalesbot/imtelegram/internal/imfmt"
	"github.com/whalesbot/imtelegram/internal/registerclient"
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
)

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
	CurrentSessionID string
	Ended            bool
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
	base := strconv.FormatInt(chatID, 10)
	st = &chatState{
		CurrentSessionID: base,
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

func (m *conversationManager) isEnded(chatID int64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.chats[chatID]
	return ok && st.Ended
}

func (m *conversationManager) newSession(chatID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.getOrCreate(chatID)
	st.Ended = false
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
		return strconv.FormatInt(chatID, 10), false
	}
	return st.CurrentSessionID, st.Ended
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	port := getenv("IM_TELEGRAM_PORT", "8084")
	orchURL := getenv("ORCHESTRATOR_URL", "http://orchestrator:8080")
	selfHost := getenv("SERVICE_HOST", "im-telegram")
	self := "http://" + selfHost + ":" + port
	token := os.Getenv("TELEGRAM_BOT_TOKEN")

	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "im-telegram"})
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rc := registerclient.New(orchURL, registerclient.RegisterRequest{
		Name:           "im-telegram",
		Type:           "im_gateway",
		Version:        "0.1.0",
		Endpoint:       self,
		HealthEndpoint: self + "/health",
		Capabilities:   []string{"telegram_text"},
	})
	rc.Start(ctx)

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("im-telegram listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	if token == "" {
		slog.Warn("TELEGRAM_BOT_TOKEN empty; skipping long poll. Service still runs and is registered.")
	} else {
		go pollLoop(ctx, token, orchURL)
	}

	<-ctx.Done()
	shCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = srv.Shutdown(shCtx)
}

func pollLoop(ctx context.Context, token, orchURL string) {
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

	chatTimeoutSec := getenvInt("IM_TELEGRAM_CHAT_TIMEOUT_SEC", 240)
	cli := &http.Client{Timeout: time.Duration(chatTimeoutSec) * time.Second}
	sessionURL := getenv("SESSION_URL", "http://session:8090")
	sessionCLI := &http.Client{Timeout: 20 * time.Second}
	conv := newConversationManager()

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
			cmd := handleCommand(conv, msg)
			if cmd.Handled {
				if err := sendTelegramReply(ctx, bot, msg.Chat.ID, cmd.Reply, "", ""); err != nil {
					slog.Error("telegram command reply failed", "chat_id", msg.Chat.ID, "err", err)
				}
				continue
			}
			if conv.isEnded(msg.Chat.ID) {
				if err := sendTelegramReply(ctx, bot, msg.Chat.ID, "当前会话已结束。发送 /new 开启新会话，或发送 /help 查看命令。", "", ""); err != nil {
					slog.Error("telegram ended-session hint failed", "chat_id", msg.Chat.ID, "err", err)
				}
				continue
			}
			sessionID := conv.resolveSessionID(msg.Chat.ID)
			traceID := newTraceID()
			progressDone := make(chan struct{})
			go streamProgressUpdates(ctx, cli, orchURL, bot, msg.Chat.ID, sessionID, traceID, progressDone, sessionCLI, sessionURL)
			chatResp, err := callOrchestrator(ctx, cli, orchURL, chatRequest{
				UserID:  strconv.FormatInt(msg.From.ID, 10),
				Channel: "telegram",
				ChatID:  sessionID,
				Message: msg.Text,
				TraceID: traceID,
			})
			close(progressDone)
			reply := chatResp.Reply
			if chatResp.TraceID != "" {
				traceID = chatResp.TraceID
			}
			if err != nil {
				slog.Error("orchestrator chat failed", "err", err)
				if strings.Contains(strings.ToLower(err.Error()), "context deadline exceeded") {
					reply = fmt.Sprintf("执行超时（等待运行结果超过 %ds）。\nsession_id: `%s`\ntrace_id: `%s`\n请到 Session 页面查看中间步骤（tool_call/runtime事件）继续排查。", chatTimeoutSec, sessionID, defaultText(traceID, "unknown"))
				} else {
					reply = fmt.Sprintf("抱歉，我暂时无法回应：%s\nsession_id: `%s`\ntrace_id: `%s`", err.Error(), sessionID, defaultText(traceID, "unknown"))
				}
			}
			if err := sendTelegramReply(ctx, bot, msg.Chat.ID, reply, sessionID, traceID); err != nil {
				slog.Error("telegram reply failed", "chat_id", msg.Chat.ID, "session_id", sessionID, "trace_id", traceID, "err", err)
			}
			if err != nil {
				_ = appendAssistantMessage(ctx, sessionCLI, sessionURL, sessionID, reply)
			}
			if len(chatResp.Attachments) > 0 {
				sendTelegramAttachments(ctx, bot, msg.Chat.ID, sessionID, traceID, chatResp.Attachments, sessionCLI, sessionURL)
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

func streamProgressUpdates(
	ctx context.Context,
	cli *http.Client,
	orchURL string,
	bot *tgbotapi.BotAPI,
	chatID int64,
	sessionID, traceID string,
	done <-chan struct{},
	sessionCLI *http.Client,
	sessionURL string,
) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	seen := map[int64]struct{}{}
	updates := 0
	maxUpdates := 24
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			events, err := fetchLoggerEvents(ctx, cli, orchURL, 120)
			if err != nil {
				continue
			}
			for i := len(events) - 1; i >= 0; i-- {
				evt := events[i]
				if evt.Fields["trace_id"] != traceID {
					continue
				}
				if evt.ID > 0 {
					if _, ok := seen[evt.ID]; ok {
						continue
					}
					seen[evt.ID] = struct{}{}
				}
				msg, ok := renderProgressEvent(evt, sessionID)
				if !ok {
					continue
				}
				if err := sendTelegramReply(ctx, bot, chatID, msg, sessionID, traceID); err != nil {
					slog.Warn("telegram progress update failed", "chat_id", chatID, "trace_id", traceID, "err", err)
					continue
				}
				_ = appendAssistantMessage(ctx, sessionCLI, sessionURL, sessionID, msg)
				updates++
				if updates >= maxUpdates {
					return
				}
			}
		}
	}
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

func renderProgressEvent(evt loggerEvent, sessionID string) (string, bool) {
	f := evt.Fields
	if f == nil || f["session_id"] != sessionID {
		return "", false
	}
	switch evt.Message {
	case "runtime_run_start":
		return fmt.Sprintf("开始执行任务。\ntrace_id: `%s`", f["trace_id"]), true
	case "runtime_context_loaded":
		return fmt.Sprintf("上下文加载完成，历史消息 %s 条。", defaultText(f["history_count"], "0")), true
	case "react_step_start":
		return fmt.Sprintf("执行到 Step %s。", defaultText(f["step"], "?")), true
	case "tool_call_start":
		return fmt.Sprintf("调用工具：`%s`（step %s）", defaultText(f["tool_name"], "unknown"), defaultText(f["step"], "?")), true
	case "tool_call_end":
		return fmt.Sprintf("工具完成：`%s`（耗时 %sms）", defaultText(f["tool_name"], "unknown"), defaultText(f["duration_ms"], "?")), true
	case "tool_call_error":
		return fmt.Sprintf("工具报错：`%s`\n%s", defaultText(f["tool_name"], "unknown"), defaultText(f["error_message"], "unknown error")), true
	case "runtime_react_loop_error":
		return fmt.Sprintf("执行失败：%s", defaultText(f["error_message"], "react loop error")), true
	case "runtime_run_completed":
		return fmt.Sprintf("任务执行完成（reply_latency_ms=%s）。", defaultText(f["reply_latency_ms"], "?")), true
	default:
		return "", false
	}
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
- /end: 结束当前会话（普通消息将暂停处理）
- /status: 查看当前会话状态
- /help: 查看命令帮助
`),
		}
	case "/new", "/reset":
		sessionID := conv.newSession(msg.Chat.ID)
		return commandResult{
			Handled: true,
			Reply:   fmt.Sprintf("已开启新会话。\nsession_id: `%s`", sessionID),
		}
	case "/end", "/stop":
		sessionID := conv.endSession(msg.Chat.ID)
		return commandResult{
			Handled: true,
			Reply:   fmt.Sprintf("已结束当前会话。\nsession_id: `%s`\n发送 /new 可开启新会话。", sessionID),
		}
	case "/status":
		sessionID, ended := conv.status(msg.Chat.ID)
		state := "active"
		if ended {
			state = "ended"
		}
		return commandResult{
			Handled: true,
			Reply:   fmt.Sprintf("当前状态：**%s**\nsession_id: `%s`", state, sessionID),
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
