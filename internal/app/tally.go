package app

import (
	"fmt"

	"github.com/botinok/temcshika/internal/store"
)

// tally is the merged result of a poll across VK-native and TG-mirror votes.
type tally struct {
	counts      map[int64]int // vk_answer_id -> number of (deduped) votes
	totalVoters int           // distinct voters across the poll; -1 if unknown (additive mode)
	deduped     bool          // true when VK voters were available and merged with dedup
}

// computeTally merges native VK votes with local TG votes.
//
// Dedup mode (non-anonymous poll + VK voters fetched): votes are counted as
// distinct identities, where a TG vote by a linked user is attributed to that
// user's VK id and OVERRIDES their native VK vote (so a person who voted in
// both VK and the TG mirror is counted once, using their TG choice).
//
// Additive mode (anonymous poll or voters unavailable): per option we simply add
// the native VK count and the local TG count. This may double-count a person who
// voted in both places, which we surface in the rendered note.
func computeTally(
	poll *store.Poll,
	options []store.Option,
	tgVotes []store.Vote,
	vkVoters []store.Vote,
	linkedVKByTG map[int64]int64,
) tally {
	counts := make(map[int64]int, len(options))
	for _, o := range options {
		counts[o.VKAnswerID] = 0
	}

	if poll.IsAnonymous || !poll.VotersAvailable {
		// Additive mode.
		tgByAnswer := make(map[int64]int)
		for _, v := range tgVotes {
			tgByAnswer[v.AnswerID]++
		}
		for _, o := range options {
			counts[o.VKAnswerID] = o.VKVotes + tgByAnswer[o.VKAnswerID]
		}
		return tally{counts: counts, totalVoters: -1, deduped: false}
	}

	// Dedup mode.
	// VK ids that a linked TG user has (re)voted on in TG override their native VK vote.
	claimedVK := make(map[int64]bool)
	for _, v := range tgVotes {
		if vk, ok := linkedVKByTG[v.TGID]; ok {
			claimedVK[vk] = true
		}
	}

	identityByAnswer := make(map[int64]map[string]bool, len(options))
	add := func(answerID int64, identity string) {
		set := identityByAnswer[answerID]
		if set == nil {
			set = make(map[string]bool)
			identityByAnswer[answerID] = set
		}
		set[identity] = true
	}

	for _, v := range tgVotes {
		if vk, ok := linkedVKByTG[v.TGID]; ok {
			add(v.AnswerID, fmt.Sprintf("vk:%d", vk))
		} else {
			add(v.AnswerID, fmt.Sprintf("tg:%d", v.TGID))
		}
	}
	for _, v := range vkVoters {
		if claimedVK[v.VKUserID] {
			continue // overridden by the user's TG vote
		}
		add(v.AnswerID, fmt.Sprintf("vk:%d", v.VKUserID))
	}

	allIdentities := make(map[string]bool)
	for _, o := range options {
		set := identityByAnswer[o.VKAnswerID]
		counts[o.VKAnswerID] = len(set)
		for id := range set {
			allIdentities[id] = true
		}
	}
	return tally{counts: counts, totalVoters: len(allIdentities), deduped: true}
}
