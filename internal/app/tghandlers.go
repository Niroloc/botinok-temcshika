package app

import (
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/botinok/temcshika/internal/store"
)

func (a *App) handleTGUpdate(upd tgbotapi.Update) {
	switch {
	case upd.CallbackQuery != nil:
		a.handleCallback(upd.CallbackQuery)
	case upd.Message != nil:
		a.handleTGMessage(upd.Message)
	}
}

func (a *App) handleTGMessage(msg *tgbotapi.Message) {
	from := msg.From.ID
	isAdmin := from == a.cfg.TGAdminID

	created, err := a.st.UpsertTGUser(from, msg.From.UserName, msg.From.FirstName)
	if err != nil {
		a.log.Printf("upsert tg user: %v", err)
	}
	if created && isAdmin {
		_ = a.st.SetStatus(from, store.StatusApproved)
	}
	if created && !isAdmin {
		a.notifyAdminNewUser(msg.From)
		_, _ = a.tg.SendText(from, "👋 Заявка на доступ отправлена администратору. Ожидайте одобрения.")
		// fall through so /start etc. still get a reply
	}

	if !msg.IsCommand() {
		return
	}

	cmd := strings.ToLower(msg.Command())
	arg := strings.TrimSpace(msg.CommandArguments())

	if isAdmin && a.handleAdminCommand(from, cmd, arg) {
		return
	}

	user, _ := a.st.GetTGUser(from)
	approved := user != nil && user.Status == store.StatusApproved

	switch cmd {
	case "start", "help":
		a.sendHelp(from, approved, isAdmin)
	case "status":
		a.sendStatus(from, user)
	case "link":
		if !approved {
			_, _ = a.tg.SendText(from, "⏳ Доступ ещё не одобрен — привязка станет доступна после одобрения.")
			return
		}
		a.startLinking(from)
	default:
		if approved {
			a.sendHelp(from, approved, isAdmin)
		}
	}
}

func (a *App) sendHelp(tgID int64, approved, isAdmin bool) {
	var b strings.Builder
	b.WriteString("🤖 Бот-зеркало VK-беседы.\n\n")
	if approved {
		b.WriteString("/link — привязать VK-аккаунт (для дедупа голосов)\n")
		b.WriteString("/status — ваш статус и привязка\n")
	} else {
		b.WriteString("Доступ выдаётся администратором. /status — проверить статус.\n")
	}
	if isAdmin {
		b.WriteString("\nАдмин:\n")
		b.WriteString("/pending — заявки на доступ\n")
		b.WriteString("/approve <tg_id> — одобрить\n")
		b.WriteString("/block <tg_id> — заблокировать\n")
		b.WriteString("/polls — активные голосования\n")
		b.WriteString("/close <poll_id> — закрыть голосование досрочно\n")
	}
	_, _ = a.tg.SendText(tgID, b.String())
}

func (a *App) sendStatus(tgID int64, user *store.TGUser) {
	if user == nil {
		_, _ = a.tg.SendText(tgID, "Статус: неизвестен. Напишите /start.")
		return
	}
	link := "не привязан"
	if user.Linked() {
		link = fmt.Sprintf("VK id %d", user.VKUserID.Int64)
	}
	_, _ = a.tg.SendText(tgID, fmt.Sprintf("Статус: %s\nVK: %s", user.Status, link))
}

// ---------------------------------------------------------------------------
// Admin commands
// ---------------------------------------------------------------------------

func (a *App) handleAdminCommand(adminID int64, cmd, arg string) bool {
	switch cmd {
	case "pending":
		a.adminListPending(adminID)
	case "approve":
		a.adminSetStatus(adminID, arg, store.StatusApproved)
	case "block":
		a.adminSetStatus(adminID, arg, store.StatusBlocked)
	case "polls":
		a.adminListPolls(adminID)
	case "close":
		a.adminClosePoll(adminID, arg)
	default:
		return false
	}
	return true
}

func (a *App) adminListPending(adminID int64) {
	users, err := a.st.PendingUsers()
	if err != nil {
		_, _ = a.tg.SendText(adminID, "Ошибка: "+err.Error())
		return
	}
	if len(users) == 0 {
		_, _ = a.tg.SendText(adminID, "Заявок нет.")
		return
	}
	for _, u := range users {
		name := strings.TrimSpace(u.FirstName)
		if u.Username != "" {
			name += " (@" + u.Username + ")"
		}
		kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Одобрить", fmt.Sprintf("approve:%d", u.TGID)),
			tgbotapi.NewInlineKeyboardButtonData("⛔ Заблокировать", fmt.Sprintf("block:%d", u.TGID)),
		))
		_, _ = a.tg.SendKeyboard(adminID, fmt.Sprintf("Заявка: %s\nid %d", name, u.TGID), kb)
	}
}

func (a *App) adminSetStatus(adminID int64, arg, status string) {
	tgID, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		_, _ = a.tg.SendText(adminID, "Использование: /"+map[string]string{store.StatusApproved: "approve", store.StatusBlocked: "block"}[status]+" <tg_id>")
		return
	}
	a.applyStatus(adminID, tgID, status)
}

// applyStatus changes a user's status and notifies both sides.
func (a *App) applyStatus(adminID, tgID int64, status string) {
	if err := a.st.SetStatus(tgID, status); err != nil {
		_, _ = a.tg.SendText(adminID, "Ошибка: "+err.Error())
		return
	}
	switch status {
	case store.StatusApproved:
		_, _ = a.tg.SendText(adminID, fmt.Sprintf("✅ Пользователь %d одобрен.", tgID))
		_, _ = a.tg.SendText(tgID, "🎉 Доступ одобрен! Сообщения с @all и голосования из беседы будут приходить сюда.\nПривяжите VK через /link.")
		a.deliverActivePollsToUser(tgID)
	case store.StatusBlocked:
		_, _ = a.tg.SendText(adminID, fmt.Sprintf("⛔ Пользователь %d заблокирован.", tgID))
	}
}

func (a *App) adminListPolls(adminID int64) {
	polls, err := a.st.ActivePolls()
	if err != nil {
		_, _ = a.tg.SendText(adminID, "Ошибка: "+err.Error())
		return
	}
	if len(polls) == 0 {
		_, _ = a.tg.SendText(adminID, "Активных голосований нет.")
		return
	}
	var b strings.Builder
	b.WriteString("Активные голосования:\n")
	for i := range polls {
		fmt.Fprintf(&b, "• id %d — %s\n", polls[i].ID, strings.TrimSpace(polls[i].Question))
	}
	b.WriteString("\nЗакрыть: /close <poll_id>")
	_, _ = a.tg.SendText(adminID, b.String())
}

func (a *App) adminClosePoll(adminID int64, arg string) {
	pollID, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		_, _ = a.tg.SendText(adminID, "Использование: /close <poll_id>")
		return
	}
	p, err := a.st.GetPoll(pollID)
	if err != nil || p == nil {
		_, _ = a.tg.SendText(adminID, "Голосование не найдено.")
		return
	}
	if p.Status != store.PollActive {
		_, _ = a.tg.SendText(adminID, "Голосование уже закрыто.")
		return
	}
	a.closePoll(p, "Голосование закрыто администратором.")
	_, _ = a.tg.SendText(adminID, fmt.Sprintf("🏁 Голосование %d закрыто.", pollID))
}

func (a *App) notifyAdminNewUser(u *tgbotapi.User) {
	name := strings.TrimSpace(u.FirstName)
	if u.UserName != "" {
		name += " (@" + u.UserName + ")"
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("✅ Одобрить", fmt.Sprintf("approve:%d", u.ID)),
		tgbotapi.NewInlineKeyboardButtonData("⛔ Заблокировать", fmt.Sprintf("block:%d", u.ID)),
	))
	_, _ = a.tg.SendKeyboard(a.cfg.TGAdminID, fmt.Sprintf("🔔 Новая заявка на доступ:\n%s\nid %d", name, u.ID), kb)
}

// ---------------------------------------------------------------------------
// Callback queries (inline buttons)
// ---------------------------------------------------------------------------

func (a *App) handleCallback(cb *tgbotapi.CallbackQuery) {
	parts := strings.Split(cb.Data, ":")
	switch parts[0] {
	case "v":
		a.handleVoteCallback(cb, parts)
	case "approve", "block":
		a.handleAdminCallback(cb, parts)
	default:
		_ = a.tg.AnswerCallback(cb.ID, "")
	}
}

func (a *App) handleVoteCallback(cb *tgbotapi.CallbackQuery, parts []string) {
	from := cb.From.ID
	if len(parts) != 3 {
		_ = a.tg.AnswerCallback(cb.ID, "")
		return
	}
	pollID, _ := strconv.ParseInt(parts[1], 10, 64)
	answerID, _ := strconv.ParseInt(parts[2], 10, 64)

	user, _ := a.st.GetTGUser(from)
	if user == nil || user.Status != store.StatusApproved {
		_ = a.tg.AnswerCallback(cb.ID, "Нет доступа")
		return
	}
	p, err := a.st.GetPoll(pollID)
	if err != nil || p == nil {
		_ = a.tg.AnswerCallback(cb.ID, "Голосование не найдено")
		return
	}
	if p.Status != store.PollActive {
		_ = a.tg.AnswerCallback(cb.ID, "Голосование завершено")
		return
	}

	if _, err := a.st.ToggleTGVote(pollID, answerID, from, p.IsMultiple); err != nil {
		a.log.Printf("toggle vote: %v", err)
		_ = a.tg.AnswerCallback(cb.ID, "Ошибка")
		return
	}
	_ = a.tg.AnswerCallback(cb.ID, "Голос учтён")
	a.refreshPollMessages(pollID)
}

func (a *App) handleAdminCallback(cb *tgbotapi.CallbackQuery, parts []string) {
	if cb.From.ID != a.cfg.TGAdminID {
		_ = a.tg.AnswerCallback(cb.ID, "Только для админа")
		return
	}
	if len(parts) != 2 {
		_ = a.tg.AnswerCallback(cb.ID, "")
		return
	}
	tgID, _ := strconv.ParseInt(parts[1], 10, 64)
	status := store.StatusApproved
	if parts[0] == "block" {
		status = store.StatusBlocked
	}
	a.applyStatus(a.cfg.TGAdminID, tgID, status)
	_ = a.tg.AnswerCallback(cb.ID, "Готово")
}
