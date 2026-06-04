package app

import (
	"fmt"
	"strings"

	"github.com/botinok/temcshika/internal/vk"
)

// handleBroadcast forwards an @all VK message (text + photos) to every approved TG user.
func (a *App) handleBroadcast(m vk.Message) {
	fresh, err := a.st.RecordBroadcast(m.PeerID, m.FromID, m.CMID, m.Text)
	if err != nil {
		a.log.Printf("record broadcast: %v", err)
		return
	}
	if !fresh {
		return // already forwarded (restart / duplicate event)
	}

	users, err := a.st.ApprovedUsers()
	if err != nil {
		a.log.Printf("approved users: %v", err)
		return
	}
	if len(users) == 0 {
		return
	}

	name := a.vk.UserName(m.FromID)
	body := fmt.Sprintf("📣 %s (VK-беседа):", name)
	if text := strings.TrimSpace(m.Text); text != "" {
		body += "\n\n" + text
	}

	for _, u := range users {
		if _, err := a.tg.SendText(u.TGID, body); err != nil {
			a.log.Printf("broadcast text to %d: %v", u.TGID, err)
		}
		for _, url := range m.Photos {
			if _, err := a.tg.SendPhoto(u.TGID, url, ""); err != nil {
				a.log.Printf("broadcast photo to %d: %v", u.TGID, err)
			}
		}
	}
	a.log.Printf("broadcast cmid=%d -> %d users (%d photos)", m.CMID, len(users), len(m.Photos))
}
