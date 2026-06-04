package app

import (
	"fmt"
	"sort"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/botinok/temcshika/internal/store"
)

const maxButtonLen = 48

// renderPoll builds the live mirror text and inline keyboard for one user.
// selected holds the vk_answer_ids the user picked in TG.
func renderPoll(poll *store.Poll, options []store.Option, t tally, selected map[int64]bool) (string, tgbotapi.InlineKeyboardMarkup) {
	var b strings.Builder
	fmt.Fprintf(&b, "🗳 %s\n\n", strings.TrimSpace(poll.Question))

	max := maxCount(options, t)
	for _, o := range options {
		mark := "▫️"
		if selected[o.VKAnswerID] {
			mark = "✅"
		}
		n := t.counts[o.VKAnswerID]
		fmt.Fprintf(&b, "%s %s\n   %s %d\n", mark, strings.TrimSpace(o.Text), bar(n, max), n)
	}

	b.WriteString("\n")
	b.WriteString(footer(poll, t))

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(options))
	for _, o := range options {
		label := truncate(strings.TrimSpace(o.Text), maxButtonLen)
		if selected[o.VKAnswerID] {
			label = "✅ " + truncate(strings.TrimSpace(o.Text), maxButtonLen-2)
		}
		data := fmt.Sprintf("v:%d:%d", poll.ID, o.VKAnswerID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, data)))
	}
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// renderResults builds the final, button-less summary of a finished poll.
func renderResults(poll *store.Poll, options []store.Option, t tally) string {
	type row struct {
		text string
		n    int
	}
	rs := make([]row, 0, len(options))
	for _, o := range options {
		rs = append(rs, row{text: strings.TrimSpace(o.Text), n: t.counts[o.VKAnswerID]})
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].n > rs[j].n })

	var b strings.Builder
	fmt.Fprintf(&b, "🏁 Итоги голосования\n\n🗳 %s\n\n", strings.TrimSpace(poll.Question))
	max := 0
	for _, r := range rs {
		if r.n > max {
			max = r.n
		}
	}
	for i, r := range rs {
		medal := fmt.Sprintf("%d.", i+1)
		if i == 0 && r.n > 0 {
			medal = "🥇"
		}
		fmt.Fprintf(&b, "%s %s\n   %s %d\n", medal, r.text, bar(r.n, max), r.n)
	}
	b.WriteString("\n")
	b.WriteString(footer(poll, t))
	return b.String()
}

func footer(poll *store.Poll, t tally) string {
	var parts []string
	if t.deduped {
		parts = append(parts, fmt.Sprintf("👥 Проголосовало: %d", t.totalVoters))
	} else {
		parts = append(parts, "ℹ️ Суммарные голоса VK+TG (дедуп недоступен)")
	}
	if poll.IsMultiple {
		parts = append(parts, "можно выбрать несколько вариантов")
	}
	if poll.IsAnonymous {
		parts = append(parts, "анонимное")
	}
	return strings.Join(parts, " · ")
}

func maxCount(options []store.Option, t tally) int {
	max := 0
	for _, o := range options {
		if n := t.counts[o.VKAnswerID]; n > max {
			max = n
		}
	}
	return max
}

// bar renders a proportional unicode bar (0..10 blocks) relative to the leader.
func bar(n, max int) string {
	const width = 10
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	filled := n * width / max
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
