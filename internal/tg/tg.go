package tg

import (
	"context"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Bot wraps the Telegram Bot API with the small set of operations the app needs.
type Bot struct {
	api     *tgbotapi.BotAPI
	adminID int64
}

// New connects to Telegram and returns a Bot. If client is non-nil it is used
// for all API calls (e.g. to route through a proxy); otherwise a direct
// connection is used.
func New(token string, adminID int64, client *http.Client) (*Bot, error) {
	var (
		api *tgbotapi.BotAPI
		err error
	)
	if client != nil {
		api, err = tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, client)
	} else {
		api, err = tgbotapi.NewBotAPI(token)
	}
	if err != nil {
		return nil, err
	}
	return &Bot{api: api, adminID: adminID}, nil
}

// Self returns the bot's own username.
func (b *Bot) Self() string { return b.api.Self.UserName }

// Updates returns the long-polling update channel.
func (b *Bot) Updates(ctx context.Context) tgbotapi.UpdatesChannel {
	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = 30
	ch := b.api.GetUpdatesChan(cfg)
	go func() {
		<-ctx.Done()
		b.api.StopReceivingUpdates()
	}()
	return ch
}

// SendText sends a plain message and returns the new message id.
func (b *Bot) SendText(chatID int64, text string) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true
	sent, err := b.api.Send(msg)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

// SendPhoto sends a photo by URL with an optional caption.
func (b *Bot) SendPhoto(chatID int64, photoURL, caption string) (int, error) {
	msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(photoURL))
	msg.Caption = caption
	sent, err := b.api.Send(msg)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

// SendKeyboard sends a message with an inline keyboard, returning the message id.
func (b *Bot) SendKeyboard(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = kb
	sent, err := b.api.Send(msg)
	if err != nil {
		return 0, err
	}
	return sent.MessageID, nil
}

// EditKeyboard replaces a message's text and inline keyboard.
func (b *Bot) EditKeyboard(chatID int64, messageID int, text string, kb tgbotapi.InlineKeyboardMarkup) error {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, text, kb)
	edit.DisableWebPagePreview = true
	_, err := b.api.Send(edit)
	return err
}

// EditTextStripKeyboard replaces a message's text and removes its inline keyboard.
func (b *Bot) EditTextStripKeyboard(chatID int64, messageID int, text string) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.DisableWebPagePreview = true
	empty := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	edit.ReplyMarkup = &empty
	_, err := b.api.Send(edit)
	return err
}

// AnswerCallback acknowledges a button press (optionally with a toast).
func (b *Bot) AnswerCallback(callbackID, text string) error {
	_, err := b.api.Request(tgbotapi.NewCallback(callbackID, text))
	return err
}
