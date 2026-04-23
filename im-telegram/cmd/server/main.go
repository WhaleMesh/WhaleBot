package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type chatRequest struct {
	UserID  string `json:"user_id"`
	Channel string `json:"channel"`
	ChatID  string `json:"chat_id"`
	Message string `json:"message"`
}

type chatResponse struct {
	Success   bool   `json:"success"`
	SessionID string `json:"session_id"`
	Reply     string `json:"reply"`
	TraceID   string `json:"trace_id"`
	Error     string `json:"error,omitempty"`
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

	cli := &http.Client{Timeout: 90 * time.Second}
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
				sendTelegramReply(bot, msg.Chat.ID, cmd.Reply)
				continue
			}
			if conv.isEnded(msg.Chat.ID) {
				sendTelegramReply(bot, msg.Chat.ID, "当前会话已结束。发送 /new 开启新会话，或发送 /help 查看命令。")
				continue
			}
			sessionID := conv.resolveSessionID(msg.Chat.ID)
			reply, err := callOrchestrator(ctx, cli, orchURL, chatRequest{
				UserID:  strconv.FormatInt(msg.From.ID, 10),
				Channel: "telegram",
				ChatID:  sessionID,
				Message: msg.Text,
			})
			if err != nil {
				slog.Error("orchestrator chat failed", "err", err)
				reply = "抱歉，我暂时无法回应：" + err.Error()
			}
			sendTelegramReply(bot, msg.Chat.ID, reply)
		}
	}
}

func sendTelegramReply(bot *tgbotapi.BotAPI, chatID int64, text string) {
	// Keep session storage in standard markdown; conversion is IM-specific at send time.
	out := tgbotapi.NewMessage(chatID, imfmt.MarkdownToTelegramHTML(text))
	out.ParseMode = "HTML"
	out.DisableWebPagePreview = true
	if _, err := bot.Send(out); err != nil {
		slog.Error("telegram send failed", "err", err)
	}
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

func callOrchestrator(ctx context.Context, cli *http.Client, orchURL string, req chatRequest) (string, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, orchURL+"/api/v1/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := cli.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if !parsed.Success {
		if parsed.Error != "" {
			return "", errContextual(parsed.Error)
		}
		return "", errContextual("orchestrator returned success=false")
	}
	return parsed.Reply, nil
}

type contextualErr string

func (e contextualErr) Error() string { return string(e) }
func errContextual(s string) error    { return contextualErr(s) }
