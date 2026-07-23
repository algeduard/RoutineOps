package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// telegramAPIBase — базовый URL Bot API. Поле, а не константа, чтобы тесты
// могли подменить его на httptest-сервер.
const telegramAPIBase = "https://api.telegram.org"

type Bot struct {
	token   string
	db      *storage.DB
	logger  *slog.Logger
	offset  int64
	baseURL string
	httpc   *http.Client

	// username бота (getMe), кэшируется после первого успешного ответа. Каждый
	// self-hosted-деплой поднимает СВОЕГО бота у @BotFather, поэтому имя нельзя
	// вшивать в UI — его знает только Bot API.
	mu       sync.Mutex
	username string
}

func New(token string, db *storage.DB, logger *slog.Logger) *Bot {
	// send() зовётся из detached-гоурутин (NotifyITAdmins через `go`); без таймаута
	// http.DefaultClient повис бы навсегда на подвисшем/чёрнодырном Bot API и копил
	// бы утёкшие гоурутины при каждом алерте. 10с — best-effort-уведомление.
	// Клиент устойчив к частичной блокировке api.telegram.org (см. telegramHTTPClient).
	return &Bot{token: token, db: db, logger: logger, baseURL: telegramAPIBase,
		httpc: telegramHTTPClient(10 * time.Second)}
}

func (b *Bot) apiURL(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", b.baseURL, b.token, method)
}

// redact вырезает bot-токен из строки ошибки перед логированием. Токен сидит в
// URL-path (/bot<token>/method), а *url.Error от http-клиента печатает полный URL —
// при любой транспортной ошибке (DNS/dial/TLS/deadline) токен утёк бы в JSON-stdout
// (Go редактирует только userinfo-пароль, path-сегменты — нет). Читатель логов без
// доступа к .env.prod иначе получил бы живой токен.
func (b *Bot) redact(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if b.token != "" {
		s = strings.ReplaceAll(s, b.token, "***")
	}
	return s
}

type tgGetMeResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		Username string `json:"username"`
	} `json:"result"`
}

// Username возвращает закэшированный @username бота. НЕ ходит в сеть: метод зовётся
// из HTTP-хендлера, а поход в api.telegram.org на каждый запрос сделал бы страницу
// заложником доступности Telegram (10с таймаута на запрос). Кэш наполняет
// resolveUsername из StartPolling. Пустая строка = ещё не разрезолвили либо Telegram
// недоступен; UI тогда показывает инструкцию без ссылки, а не ссылку на чужого бота.
func (b *Bot) Username(context.Context) string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.username
}

// resolveUsername спрашивает Bot API (getMe) и кладёт username в кэш.
func (b *Bot) resolveUsername(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.apiURL("getMe"), nil)
	if err != nil {
		return err
	}
	resp, err := b.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var me tgGetMeResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return err
	}
	if !me.OK || me.Result.Username == "" {
		return fmt.Errorf("getMe: ok=%v username=%q", me.OK, me.Result.Username)
	}

	b.mu.Lock()
	b.username = me.Result.Username
	b.mu.Unlock()
	return nil
}

// warmUsername добивается username с backoff'ом: Telegram может быть недоступен в
// момент старта сервера, а ссылка в панели нужна и через час.
func (b *Bot) warmUsername(ctx context.Context) {
	const maxDelay = 10 * time.Minute
	for delay := 5 * time.Second; ; {
		err := b.resolveUsername(ctx)
		if err == nil {
			b.logger.Info("telegram: bot username resolved", "username", b.Username(ctx))
			return
		}
		b.logger.Warn("telegram: getMe failed, retrying", "err", b.redact(err), "retry_in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (b *Bot) send(chatID int64, text string) error {
	body, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	resp, err := b.httpc.Post(b.apiURL("sendMessage"), "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// SendToChat отправляет сообщение в КОНКРЕТНЫЙ чат по chat_id — для адресной доставки
// алертов по правилам маршрутизации (в отличие от NotifyITAdmins, который рассылает всем
// привязанным админам). chatID — строковый (как хранится в БД), парсится в int64 (у групп
// он отрицательный). Nil-ресивер (бот не сконфигурён) безопасен: возвращает ошибку, а не
// паникует — маршрутизатор её логирует и продолжает (best-effort). ctx оставлен для
// единообразия сигнатуры; сам send работает на клиенте с собственным таймаутом.
func (b *Bot) SendToChat(_ context.Context, chatID, text string) error {
	if b == nil {
		return fmt.Errorf("telegram bot not configured")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat_id %q", chatID)
	}
	return b.send(id, text)
}

// NotifyITAdmins sends a message to all IT admins with a linked Telegram account.
// Runs synchronously — call with `go` if you don't want to block.
func (b *Bot) NotifyITAdmins(ctx context.Context, text string) {
	if b == nil {
		return // бот не сконфигурён (нет токена) — уведомления просто отключены
	}
	chatIDs, err := b.db.GetITAdminsWithTelegramChatID(ctx)
	if err != nil {
		b.logger.Error("telegram: get IT admins", "err", err)
		return
	}
	for _, chatIDStr := range chatIDs {
		chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
		if err != nil {
			continue
		}
		if err := b.send(chatID, text); err != nil {
			b.logger.Error("telegram: send message", "chat_id", chatID, "err", b.redact(err))
		}
	}
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From *struct {
			Username string `json:"username"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

type tgUpdatesResponse struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

func (b *Bot) StartPolling(ctx context.Context) {
	// Имя бота нужно панели для ссылки t.me/<username>; тянем его фоном, чтобы поллинг
	// не ждал Telegram, а HTTP-хендлер читал только кэш.
	go b.warmUsername(ctx)

	// Тот же устойчивый к блокировке транспорт, что и у b.httpc (getMe/sendMessage):
	// иначе long-poll getUpdates дозванивался бы напрямую на заблокированный IP из DNS.
	// Отдельный клиент — из-за долгого таймаута long-poll (40с против 10с у b.httpc).
	client := telegramHTTPClient(40 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		body, _ := json.Marshal(map[string]any{
			"offset":          b.offset,
			"timeout":         30,
			"allowed_updates": []string{"message"},
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, b.apiURL("getUpdates"), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.logger.Error("telegram: polling error", "err", b.redact(err))
			time.Sleep(5 * time.Second)
			continue
		}

		var result tgUpdatesResponse
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		for _, upd := range result.Result {
			b.offset = upd.UpdateID + 1
			if upd.Message == nil {
				continue
			}
			text := strings.TrimSpace(upd.Message.Text)
			chatID := upd.Message.Chat.ID

			switch {
			case strings.HasPrefix(text, "/start "):
				token := strings.TrimPrefix(text, "/start ")
				b.handleStart(ctx, chatID, strings.TrimSpace(token))
			case text == "/start":
				b.send(chatID, "Привет! Отправьте <code>/start TOKEN</code>, где TOKEN — ваш токен привязки из панели RoutineOps (раздел Профиль).")
			}
		}
	}
}

func (b *Bot) handleStart(ctx context.Context, chatID int64, token string) {
	user, err := b.db.GetUserByLinkToken(ctx, token)
	if err != nil {
		b.logger.Error("telegram: lookup link token", "err", err)
		b.send(chatID, "Ошибка сервера. Попробуйте позже.")
		return
	}
	if user == nil {
		b.send(chatID, "❌ Токен не найден или уже использован. Сгенерируйте новый в панели RoutineOps.")
		return
	}
	if err := b.db.SetUserTelegramChatID(ctx, user.ID, strconv.FormatInt(chatID, 10)); err != nil {
		b.logger.Error("telegram: set chat_id", "err", err)
		b.send(chatID, "Ошибка сохранения. Попробуйте ещё раз.")
		return
	}
	// Invalidate the token so it can't be reused
	_ = b.db.SetUserLinkToken(ctx, user.ID, "")
	b.send(chatID, fmt.Sprintf("✅ Аккаунт <b>%s</b> успешно подключён.\nТеперь вы будете получать уведомления RoutineOps.", user.Email))
	b.logger.Info("telegram: account linked", "user_id", user.ID, "chat_id", chatID)
}
