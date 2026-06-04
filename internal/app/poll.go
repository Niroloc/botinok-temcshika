package app

import (
	"time"

	"github.com/botinok/temcshika/internal/store"
	"github.com/botinok/temcshika/internal/vk"
)

// onNewPoll mirrors a freshly posted VK poll: persist it, snapshot voters, and
// deliver the interactive copy to all approved users. If we already know this
// poll, it is just re-synced.
func (a *App) onNewPoll(m vk.Message) {
	ps := m.Poll
	p := &store.Poll{
		VKPollID:    ps.PollID,
		VKOwnerID:   ps.OwnerID,
		VKPeerID:    m.PeerID,
		VKCMID:      m.CMID,
		Question:    ps.Question,
		IsMultiple:  ps.Multiple,
		IsAnonymous: ps.Anonymous,
		EndDate:     ps.EndDate,
	}
	id, created, err := a.st.UpsertPoll(p)
	if err != nil {
		a.log.Printf("upsert poll: %v", err)
		return
	}
	p.ID = id

	a.snapshotPoll(p, ps)

	if created {
		a.log.Printf("new poll id=%d vk_poll=%d:%d %q", id, ps.OwnerID, ps.PollID, ps.Question)
		a.deliverPollToAll(id)
	} else {
		a.refreshPollMessages(id)
	}
}

// snapshotPoll persists option counts and the native voter snapshot from a poll state.
func (a *App) snapshotPoll(p *store.Poll, ps *vk.PollState) {
	answerIDs := make([]int64, 0, len(ps.Answers))
	for i, ans := range ps.Answers {
		_ = a.st.UpsertOption(p.ID, store.Option{
			VKAnswerID: ans.ID,
			Text:       ans.Text,
			Position:   i,
			VKVotes:    ans.Votes,
		})
		answerIDs = append(answerIDs, ans.ID)
	}

	voters, ok := a.vk.GetVoters(p.VKOwnerID, p.VKPollID, answerIDs)
	p.VotersAvailable = ok && !ps.Anonymous
	if _, _, err := a.st.UpsertPoll(p); err != nil {
		a.log.Printf("update poll snapshot: %v", err)
	}
	if p.VotersAvailable {
		if err := a.st.ReplaceVKVoters(p.ID, voters); err != nil {
			a.log.Printf("replace vk voters: %v", err)
		}
	}
}

// syncPoll re-fetches a poll from VK, refreshes the mirror, and closes it if VK
// reports it finished or its end_date has passed.
func (a *App) syncPoll(pollID int64) {
	p, err := a.st.GetPoll(pollID)
	if err != nil || p == nil || p.Status != store.PollActive {
		return
	}
	ps, err := a.fetchPollState(p)
	if err != nil {
		// Neither path worked. Don't give up on finishing: fall back to the
		// end_date captured at creation and close on time with the last snapshot.
		a.log.Printf("sync poll %d (cached state, %v)", pollID, err)
		if p.EndDate > 0 && time.Now().Unix() >= p.EndDate {
			a.closePoll(p, "⏱ Голосование завершено.")
		}
		return
	}

	p.Question = ps.Question
	p.IsMultiple = ps.Multiple
	p.IsAnonymous = ps.Anonymous
	p.EndDate = ps.EndDate
	a.snapshotPoll(p, ps)

	ended := ps.Closed || (p.EndDate > 0 && time.Now().Unix() >= p.EndDate)
	if ended {
		a.closePoll(p, "⏱ Голосование завершено.")
		return
	}
	a.refreshPollMessages(pollID)
}

// fetchPollState gets the current poll state from VK, preferring a re-fetch of
// the chat message (works on conversation-message access alone) and falling back
// to polls.getById (which may be denied for a user-owned poll).
func (a *App) fetchPollState(p *store.Poll) (*vk.PollState, error) {
	if p.VKCMID > 0 {
		ps, err := a.vk.GetPollFromMessage(p.VKPeerID, p.VKCMID)
		if err == nil {
			return ps, nil
		}
		a.log.Printf("poll %d via message failed, trying polls.getById: %v", p.ID, err)
	}
	return a.vk.GetPoll(p.VKOwnerID, p.VKPollID)
}

// buildTally loads the current merged result for a poll.
func (a *App) buildTally(pollID int64) (*store.Poll, []store.Option, tally, error) {
	p, err := a.st.GetPoll(pollID)
	if err != nil || p == nil {
		return nil, nil, tally{}, err
	}
	options, err := a.st.Options(pollID)
	if err != nil {
		return nil, nil, tally{}, err
	}
	tgVotes, err := a.st.AllTGVotes(pollID)
	if err != nil {
		return nil, nil, tally{}, err
	}
	vkVoters, err := a.st.AllVKVoters(pollID)
	if err != nil {
		return nil, nil, tally{}, err
	}
	linked, err := a.st.LinkedVKByTG()
	if err != nil {
		return nil, nil, tally{}, err
	}
	return p, options, computeTally(p, options, tgVotes, vkVoters, linked), nil
}

// deliverPollToAll sends the interactive poll copy to every approved user.
func (a *App) deliverPollToAll(pollID int64) {
	users, err := a.st.ApprovedUsers()
	if err != nil {
		a.log.Printf("approved users: %v", err)
		return
	}
	p, options, t, err := a.buildTally(pollID)
	if err != nil || p == nil {
		a.log.Printf("build tally: %v", err)
		return
	}
	for _, u := range users {
		a.sendPollToUser(p, options, t, u.TGID)
	}
}

// deliverActivePollsToUser sends every active poll to a single (newly approved) user.
func (a *App) deliverActivePollsToUser(tgID int64) {
	polls, err := a.st.ActivePolls()
	if err != nil {
		a.log.Printf("active polls: %v", err)
		return
	}
	for i := range polls {
		p, options, t, err := a.buildTally(polls[i].ID)
		if err != nil || p == nil {
			continue
		}
		a.sendPollToUser(p, options, t, tgID)
	}
}

func (a *App) sendPollToUser(p *store.Poll, options []store.Option, t tally, tgID int64) {
	sel, _ := a.st.UserTGVotes(p.ID, tgID)
	text, kb := renderPoll(p, options, t, asSet(sel))
	msgID, err := a.tg.SendKeyboard(tgID, text, kb)
	if err != nil {
		a.log.Printf("send poll to %d: %v", tgID, err)
		return
	}
	if err := a.st.AddPollTGMessage(p.ID, tgID, msgID); err != nil {
		a.log.Printf("save poll msg: %v", err)
	}
}

// refreshPollMessages re-renders every delivered TG message for a poll.
func (a *App) refreshPollMessages(pollID int64) {
	p, options, t, err := a.buildTally(pollID)
	if err != nil || p == nil {
		return
	}
	refs, err := a.st.PollTGMessages(pollID)
	if err != nil {
		a.log.Printf("poll tg messages: %v", err)
		return
	}
	for _, ref := range refs {
		sel, _ := a.st.UserTGVotes(pollID, ref.TGID)
		text, kb := renderPoll(p, options, t, asSet(sel))
		if err := a.tg.EditKeyboard(ref.TGID, ref.MessageID, text, kb); err != nil {
			// "message is not modified" is benign and expected when nothing changed.
			a.log.Printf("edit poll msg for %d: %v", ref.TGID, err)
		}
	}
}

// closePoll finalizes a poll: strips buttons from every mirror, posts a results
// summary to each user, and reports the outcome back to the VK chat.
func (a *App) closePoll(p *store.Poll, reason string) {
	if err := a.st.ClosePoll(p.ID); err != nil {
		a.log.Printf("close poll %d: %v", p.ID, err)
		return
	}
	_, options, t, err := a.buildTally(p.ID)
	if err != nil {
		a.log.Printf("final tally: %v", err)
		return
	}
	results := renderResults(p, options, t)

	refs, _ := a.st.PollTGMessages(p.ID)
	for _, ref := range refs {
		if err := a.tg.EditTextStripKeyboard(ref.TGID, ref.MessageID, "🔒 "+reason); err != nil {
			a.log.Printf("strip keyboard for %d: %v", ref.TGID, err)
		}
		if _, err := a.tg.SendText(ref.TGID, results); err != nil {
			a.log.Printf("send results to %d: %v", ref.TGID, err)
		}
	}

	if err := a.vk.SendMessage(p.VKPeerID, results); err != nil {
		a.log.Printf("post results to vk peer %d: %v", p.VKPeerID, err)
	}
	a.log.Printf("closed poll id=%d", p.ID)
}

func asSet(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
