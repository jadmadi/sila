package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TelegramAdapter relays notifications to Telegram chat groups or private channels.
type TelegramAdapter struct {
	BotToken    string
	ChatID      string
	BaseURL     string
	EnabledFlag bool
	Client      *http.Client
}

// NewTelegramAdapter creates a new Telegram Bot API adapter.
func NewTelegramAdapter(botToken, chatID string, enabled bool) *TelegramAdapter {
	return &TelegramAdapter{
		BotToken:    strings.TrimSpace(botToken),
		ChatID:      strings.TrimSpace(chatID),
		BaseURL:     "https://api.telegram.org",
		EnabledFlag: enabled && strings.TrimSpace(botToken) != "" && strings.TrimSpace(chatID) != "",
		Client:      &http.Client{Timeout: 5 * time.Second},
	}
}

// Name returns the human-readable identifier for the Telegram adapter.
func (t *TelegramAdapter) Name() string {
	return "Telegram Bot"
}

// Surface returns the Telegram gateway surface type.
func (t *TelegramAdapter) Surface() Surface {
	return SurfaceTelegram
}

// Enabled reports whether the Telegram adapter is active and configured.
func (t *TelegramAdapter) Enabled() bool {
	return t.EnabledFlag
}

type telegramSendMessagePayload struct {
	ChatID      string `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type telegramInlineKeyboardMarkup struct {
	InlineKeyboard [][]telegramInlineKeyboardButton `json:"inline_keyboard"`
}

type telegramInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// SendNotification sends an HTML-formatted message to the configured Telegram chat.
func (t *TelegramAdapter) SendNotification(ctx context.Context, n Notification) error {
	if !t.Enabled() {
		return fmt.Errorf("telegram gateway is not enabled")
	}

	cleanTitle := Redact(n.Title)
	cleanBody := Redact(n.Body)

	var sb strings.Builder
	projectPrefix := ""
	if n.Project != "" {
		projectPrefix = fmt.Sprintf(" · %s", Redact(n.Project))
	}
	sb.WriteString(fmt.Sprintf("<b>[Sila%s] %s</b>\n\n", projectPrefix, cleanTitle))
	sb.WriteString(cleanBody)

	if n.Category != "" || len(n.Metadata) > 0 {
		sb.WriteString("\n\n<i>")
		if n.Category != "" {
			sb.WriteString(fmt.Sprintf("Category: %s", Redact(n.Category)))
		}
		for k, v := range n.Metadata {
			sb.WriteString(fmt.Sprintf(" · %s: %s", Redact(k), Redact(v)))
		}
		sb.WriteString("</i>")
	}

	payload := telegramSendMessagePayload{
		ChatID:    t.ChatID,
		Text:      sb.String(),
		ParseMode: "HTML",
	}

	if n.Category == "approval" && n.Metadata != nil && n.Metadata["approval_id"] != "" {
		apprID := n.Metadata["approval_id"]
		payload.ReplyMarkup = telegramInlineKeyboardMarkup{
			InlineKeyboard: [][]telegramInlineKeyboardButton{
				{
					{Text: "✅ Approve", CallbackData: fmt.Sprintf("appr:%s:approve", apprID)},
					{Text: "❌ Reject", CallbackData: fmt.Sprintf("appr:%s:reject", apprID)},
				},
			},
		}
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram payload: %w", err)
	}

	base := strings.TrimRight(t.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", base, t.BotToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram api request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram api returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// Test validates Telegram bot token validity via the getMe API method.
func (t *TelegramAdapter) Test(ctx context.Context) (*TestResult, error) {
	if !t.Enabled() {
		return &TestResult{
			Surface: SurfaceTelegram,
			Success: false,
			Message: "telegram gateway is disabled or credentials missing",
		}, nil
	}

	start := time.Now()
	base := strings.TrimRight(t.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/bot%s/getMe", base, t.BotToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &TestResult{
			Surface: SurfaceTelegram,
			Success: false,
			Message: err.Error(),
		}, err
	}

	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return &TestResult{
			Surface:   SurfaceTelegram,
			Success:   false,
			LatencyMs: latency,
			Message:   err.Error(),
		}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &TestResult{
			Surface:   SurfaceTelegram,
			Success:   false,
			LatencyMs: latency,
			Message:   fmt.Sprintf("telegram getMe returned HTTP %d", resp.StatusCode),
		}, fmt.Errorf("telegram HTTP %d", resp.StatusCode)
	}

	return &TestResult{
		Surface:   SurfaceTelegram,
		Success:   true,
		LatencyMs: latency,
		Message:   "telegram bot token authenticated successfully",
	}, nil
}

// TelegramUpdate models an incoming Telegram update from webhook or polling.
type TelegramUpdate struct {
	UpdateID      int                    `json:"update_id"`
	Message       *TelegramMessage       `json:"message,omitempty"`
	CallbackQuery *TelegramCallbackQuery `json:"callback_query,omitempty"`
}

// TelegramMessage models a message object.
type TelegramMessage struct {
	MessageID int           `json:"message_id"`
	From      *TelegramUser `json:"from,omitempty"`
	Text      string        `json:"text"`
}

// TelegramCallbackQuery models an incoming callback query from an inline button.
type TelegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    TelegramUser     `json:"from"`
	Message *TelegramMessage `json:"message,omitempty"`
	Data    string           `json:"data"`
}

// TelegramUser models a user or bot.
type TelegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name"`
}

// HandleInbound processes inbound webhook updates from Telegram.
func (t *TelegramAdapter) HandleInbound(ctx context.Context, msg InboundMessage) error {
	if strings.TrimSpace(msg.Content) == "" {
		return nil
	}

	// 1. Try parsing as TelegramUpdate JSON
	var update TelegramUpdate
	if err := json.Unmarshal([]byte(msg.Content), &update); err == nil {
		if update.CallbackQuery != nil && strings.HasPrefix(update.CallbackQuery.Data, "appr:") {
			parts := strings.Split(update.CallbackQuery.Data, ":")
			if len(parts) >= 3 {
				apprID := parts[1]
				action := parts[2]
				approved := (action == "approve" || action == "yes")
				username := update.CallbackQuery.From.Username
				if username == "" {
					username = update.CallbackQuery.From.FirstName
				}
				if username == "" {
					username = fmt.Sprintf("tg_%d", update.CallbackQuery.From.ID)
				}
				coord := GetDefaultCoordinator(nil, nil)
				if coord != nil {
					_, _ = coord.Resolve(apprID, approved, "telegram:@"+username, "")
				}
				// Answer callback query to dismiss loading indicator
				_ = t.answerCallbackQuery(ctx, update.CallbackQuery.ID, fmt.Sprintf("Decision recorded: %s", action))
			}
			return nil
		}

		if update.Message != nil {
			text := strings.TrimSpace(update.Message.Text)
			username := ""
			if update.Message.From != nil {
				username = update.Message.From.Username
				if username == "" {
					username = update.Message.From.FirstName
				}
			}
			if strings.HasPrefix(text, "/approve ") || strings.HasPrefix(text, "/reject ") {
				fields := strings.Fields(text)
				if len(fields) >= 2 {
					action := strings.TrimPrefix(fields[0], "/")
					apprID := fields[1]
					approved := (action == "approve")
					reason := ""
					if len(fields) > 2 {
						reason = strings.Join(fields[2:], " ")
					}
					coord := GetDefaultCoordinator(nil, nil)
					if coord != nil {
						_, _ = coord.Resolve(apprID, approved, "telegram:@"+username, reason)
					}
				}
				return nil
			}
		}
	}

	// 2. Direct command format fallback (e.g. from tests or simple webhooks)
	content := strings.TrimSpace(msg.Content)
	if strings.HasPrefix(content, "appr:") {
		parts := strings.Split(content, ":")
		if len(parts) >= 3 {
			apprID := parts[1]
			action := parts[2]
			approved := (action == "approve" || action == "yes")
			sender := msg.Sender
			if sender == "" {
				sender = "telegram"
			}
			coord := GetDefaultCoordinator(nil, nil)
			if coord != nil {
				_, _ = coord.Resolve(apprID, approved, sender, "")
			}
		}
	}

	return nil
}

func (t *TelegramAdapter) answerCallbackQuery(ctx context.Context, queryID, text string) error {
	if !t.Enabled() || queryID == "" {
		return nil
	}
	base := strings.TrimRight(t.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/bot%s/answerCallbackQuery", base, t.BotToken)
	body, _ := json.Marshal(map[string]string{
		"callback_query_id": queryID,
		"text":              text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
