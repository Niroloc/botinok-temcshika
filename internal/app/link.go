package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/botinok/temcshika/internal/vk"
)

const linkCodePrefix = "vklink-"

// newLinkCode generates a one-time link code like "vklink-a1b2c3".
func newLinkCode() string {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return linkCodePrefix + "000000"
	}
	return linkCodePrefix + hex.EncodeToString(buf)
}

// startLinking issues a fresh code to an approved TG user and explains the flow.
func (a *App) startLinking(tgID int64) {
	code := newLinkCode()
	if err := a.st.CreateLinkCode(code, tgID, a.cfg.LinkCodeTTL); err != nil {
		a.log.Printf("create link code: %v", err)
		_, _ = a.tg.SendText(tgID, "⚠️ Не удалось создать код привязки, попробуйте позже.")
		return
	}
	msg := fmt.Sprintf(
		"🔗 Привязка VK-аккаунта.\n\n"+
			"Отправьте в VK-беседу это сообщение целиком:\n\n%s\n\n"+
			"Код действует %s. Как только бот увидит его в беседе, привязка выполнится автоматически.",
		code, a.cfg.LinkCodeTTL.String())
	_, _ = a.tg.SendText(tgID, msg)
}

// tryConsumeLinkCode scans a VK message for a link code and, if found and valid,
// attaches the sender's VK id to the owning TG user.
func (a *App) tryConsumeLinkCode(m vk.Message) {
	for _, tok := range strings.Fields(strings.ToLower(m.Text)) {
		tok = strings.Trim(tok, ".,!?;:()[]")
		if !strings.HasPrefix(tok, linkCodePrefix) {
			continue
		}
		tgID, ok, err := a.st.ConsumeLinkCode(tok)
		if err != nil {
			a.log.Printf("consume link code: %v", err)
			return
		}
		if !ok {
			continue
		}
		if existing, _ := a.st.GetUserByVK(m.FromID); existing != nil && existing.TGID != tgID {
			_, _ = a.tg.SendText(tgID, "⚠️ Этот VK-аккаунт уже привязан к другому пользователю.")
			return
		}
		if err := a.st.SetVKLink(tgID, m.FromID); err != nil {
			a.log.Printf("set vk link: %v", err)
			_, _ = a.tg.SendText(tgID, "⚠️ Не удалось сохранить привязку (возможно, VK уже занят).")
			return
		}
		_, _ = a.tg.SendText(tgID, fmt.Sprintf("✅ VK-аккаунт привязан (id %d). Ваши голоса теперь дедуплицируются.", m.FromID))
		a.log.Printf("linked tg=%d <-> vk=%d", tgID, m.FromID)
		return
	}
}
