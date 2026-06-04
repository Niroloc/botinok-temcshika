package app

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/botinok/temcshika/internal/config"
	"github.com/botinok/temcshika/internal/store"
	"github.com/botinok/temcshika/internal/tg"
	"github.com/botinok/temcshika/internal/vk"
)

// App wires together the store, VK client and Telegram bot.
type App struct {
	cfg *config.Config
	st  *store.Store
	vk  *vk.Client
	tg  *tg.Bot
	log *log.Logger

	mu           sync.Mutex
	lastChatPeer int64 // most recent VK chat peer seen; reverse-forward target when VK_PEER_ID=0
}

// New constructs the application.
func New(cfg *config.Config, st *store.Store, vkc *vk.Client, tgb *tg.Bot, logger *log.Logger) *App {
	return &App{cfg: cfg, st: st, vk: vkc, tg: tgb, log: logger}
}

// Run starts all loops and blocks until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	a.vk.OnMessage(a.handleVKMessage)

	go func() {
		if err := a.vk.Run(ctx); err != nil && ctx.Err() == nil {
			a.log.Printf("vk long poll stopped: %v", err)
		}
	}()

	go a.pollSyncLoop(ctx)

	a.log.Printf("ready: tg=@%s vk_group=%d", a.tg.Self(), a.vk.GroupID())

	updates := a.tg.Updates(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case upd, ok := <-updates:
			if !ok {
				return nil
			}
			a.handleTGUpdate(upd)
		}
	}
}

// pollSyncLoop periodically re-syncs every active poll from VK (counts, voters,
// end_date/closed state).
func (a *App) pollSyncLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.PollRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			polls, err := a.st.ActivePolls()
			if err != nil {
				a.log.Printf("active polls: %v", err)
				continue
			}
			for i := range polls {
				a.syncPoll(polls[i].ID)
			}
		}
	}
}

// handleVKMessage dispatches an inbound VK chat message.
func (a *App) handleVKMessage(m vk.Message) {
	// Remember the chat peer so TG->VK forwarding has a target even when
	// VK_PEER_ID is not pinned. Chat (beseda) peers are >= 2000000000.
	if m.PeerID >= 2000000000 {
		a.mu.Lock()
		a.lastChatPeer = m.PeerID
		a.mu.Unlock()
	}

	// Optional peer filter.
	if a.cfg.VKPeerID != 0 && m.PeerID != int64(a.cfg.VKPeerID) {
		// Still allow link codes from DMs to the community.
		a.tryConsumeLinkCode(m)
		return
	}

	if m.HasPoll && m.Poll != nil {
		a.onNewPoll(m)
	}

	if a.containsMention(m.Text) {
		a.handleBroadcast(m)
	}

	a.tryConsumeLinkCode(m)
}

// targetVKPeer returns the VK chat peer for TG->VK forwarding: the pinned
// VK_PEER_ID if set, otherwise the most recently seen chat peer (0 if unknown).
func (a *App) targetVKPeer() int64 {
	if a.cfg.VKPeerID != 0 {
		return int64(a.cfg.VKPeerID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastChatPeer
}

func (a *App) containsMention(text string) bool {
	low := strings.ToLower(text)
	for _, tag := range a.cfg.MentionTags {
		if strings.Contains(low, tag) {
			return true
		}
	}
	return false
}
