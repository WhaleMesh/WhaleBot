package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := bot.GetUpdatesChan(u)

	cli := &http.Client{Timeout: 90 * time.Second}

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
			reply, err := callOrchestrator(ctx, cli, orchURL, chatRequest{
				UserID:  strconv.FormatInt(msg.From.ID, 10),
				Channel: "telegram",
				ChatID:  strconv.FormatInt(msg.Chat.ID, 10),
				Message: msg.Text,
			})
			if err != nil {
				slog.Error("orchestrator chat failed", "err", err)
				reply = "抱歉，我暂时无法回应：" + err.Error()
			}
			// Keep session storage in standard markdown; conversion is IM-specific at send time.
			out := tgbotapi.NewMessage(msg.Chat.ID, imfmt.MarkdownToTelegramHTML(reply))
			out.ParseMode = "HTML"
			out.DisableWebPagePreview = true
			if _, err := bot.Send(out); err != nil {
				slog.Error("telegram send failed", "err", err)
			}
		}
	}
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
