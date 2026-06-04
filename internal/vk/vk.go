package vk

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/SevereCloud/vksdk/v2/api"
	"github.com/SevereCloud/vksdk/v2/events"
	longpoll "github.com/SevereCloud/vksdk/v2/longpoll-bot"
	"github.com/SevereCloud/vksdk/v2/object"
)

// Message is a simplified inbound VK chat message.
type Message struct {
	PeerID  int64
	FromID  int64
	CMID    int64 // conversation message id
	Text    string
	Photos  []string // largest-size URLs
	Poll    *PollState
	HasPoll bool
}

// PollState is a snapshot of a VK poll.
type PollState struct {
	PollID    int64
	OwnerID   int64
	Question  string
	Multiple  bool
	Anonymous bool
	Closed    bool
	EndDate   int64
	Answers   []Answer
}

// Answer is one poll option with its native vote count.
type Answer struct {
	ID    int64
	Text  string
	Votes int
}

// Client wraps the VK API and Bots Long Poll.
type Client struct {
	api     *api.VK
	lp      *longpoll.LongPoll
	groupID int
	onMsg   func(Message)
}

// New builds a VK client. If groupID is 0 it is auto-detected from the token.
func New(token string, groupID int) (*Client, error) {
	vk := api.NewVK(token)

	if groupID == 0 {
		groups, err := vk.GroupsGetByID(api.Params{})
		if err != nil {
			return nil, fmt.Errorf("groups.getById (set VK_GROUP_ID to skip): %w", err)
		}
		if len(groups) == 0 {
			return nil, fmt.Errorf("could not resolve group id from token")
		}
		groupID = groups[0].ID
	}

	lp, err := longpoll.NewLongPoll(vk, groupID)
	if err != nil {
		return nil, fmt.Errorf("init long poll: %w", err)
	}

	c := &Client{api: vk, lp: lp, groupID: groupID}
	lp.MessageNew(func(_ context.Context, obj events.MessageNewObject) {
		if c.onMsg != nil {
			c.onMsg(convert(obj.Message))
		}
	})
	return c, nil
}

// GroupID returns the resolved community id.
func (c *Client) GroupID() int { return c.groupID }

// OnMessage registers the inbound message handler.
func (c *Client) OnMessage(fn func(Message)) { c.onMsg = fn }

// Run blocks processing the long poll until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		c.lp.Shutdown()
	}()
	return c.lp.Run()
}

func convert(m object.MessagesMessage) Message {
	out := Message{
		PeerID: int64(m.PeerID),
		FromID: int64(m.FromID),
		CMID:   int64(m.ConversationMessageID),
		Text:   m.Text,
	}
	for _, att := range m.Attachments {
		switch att.Type {
		case "photo":
			if u := maxPhotoURL(att.Photo); u != "" {
				out.Photos = append(out.Photos, u)
			}
		case "poll":
			out.HasPoll = true
			out.Poll = convertPoll(att.Poll)
		}
	}
	return out
}

func convertPoll(p object.PollsPoll) *PollState {
	ps := &PollState{
		PollID:    int64(p.ID),
		OwnerID:   int64(p.OwnerID),
		Question:  p.Question,
		Multiple:  bool(p.Multiple),
		Anonymous: bool(p.Anonymous),
		Closed:    bool(p.Closed),
		EndDate:   int64(p.EndDate),
	}
	for _, a := range p.Answers {
		ps.Answers = append(ps.Answers, Answer{ID: int64(a.ID), Text: a.Text, Votes: a.Votes})
	}
	return ps
}

func maxPhotoURL(p object.PhotosPhoto) string {
	best := ""
	bestArea := float64(-1)
	for _, s := range p.Sizes {
		area := s.Width * s.Height
		if area > bestArea {
			bestArea = area
			best = s.URL
		}
	}
	return best
}

// GetPoll fetches the current state of a poll.
func (c *Client) GetPoll(ownerID, pollID int64) (*PollState, error) {
	resp, err := c.api.PollsGetByID(api.Params{
		"owner_id": ownerID,
		"poll_id":  pollID,
	})
	if err != nil {
		return nil, err
	}
	return convertPoll(object.PollsPoll(resp)), nil
}

// GetPollFromMessage re-fetches the chat message holding the poll and returns
// the poll's current state from its attachment. This relies only on access to
// the conversation's messages (which a bot added to the chat has), so it works
// even when polls.getById is denied for a user-owned poll. Returns an error if
// the message can't be fetched or no longer carries a poll attachment.
func (c *Client) GetPollFromMessage(peerID, cmid int64) (*PollState, error) {
	resp, err := c.api.MessagesGetByConversationMessageID(api.Params{
		"peer_id":                  peerID,
		"conversation_message_ids": cmid,
	})
	if err != nil {
		return nil, err
	}
	for _, m := range resp.Items {
		for _, att := range m.Attachments {
			if att.Type == "poll" {
				return convertPoll(att.Poll), nil
			}
		}
	}
	return nil, fmt.Errorf("poll attachment not found in peer=%d cmid=%d", peerID, cmid)
}

// GetVoters returns vk_answer_id -> []vk_user_id for the given answers.
// ok=false means voters are not accessible (anonymous poll or no permission);
// the caller should fall back to additive (non-deduplicated) counting.
func (c *Client) GetVoters(ownerID, pollID int64, answerIDs []int64) (map[int64][]int64, bool) {
	if len(answerIDs) == 0 {
		return nil, false
	}
	ids := make([]string, len(answerIDs))
	for i, a := range answerIDs {
		ids[i] = fmt.Sprintf("%d", a)
	}
	resp, err := c.api.PollsGetVoters(api.Params{
		"owner_id":   ownerID,
		"poll_id":    pollID,
		"answer_ids": strings.Join(ids, ","),
	})
	if err != nil {
		return nil, false
	}
	out := make(map[int64][]int64)
	for _, v := range resp {
		for _, uid := range v.Users.Items {
			out[int64(v.AnswerID)] = append(out[int64(v.AnswerID)], int64(uid))
		}
	}
	return out, true
}

// SendMessage posts a text message into a VK peer (e.g. the chat) using the community token.
func (c *Client) SendMessage(peerID int64, text string) error {
	_, err := c.api.MessagesSend(api.Params{
		"peer_id":   peerID,
		"message":   text,
		"random_id": randomID(),
	})
	return err
}

// UserName best-effort resolves "First Last" for a positive user id; falls back to "id<n>".
func (c *Client) UserName(userID int64) string {
	if userID <= 0 {
		return fmt.Sprintf("club%d", -userID)
	}
	users, err := c.api.UsersGet(api.Params{"user_ids": fmt.Sprintf("%d", userID)})
	if err != nil || len(users) == 0 {
		return fmt.Sprintf("id%d", userID)
	}
	return strings.TrimSpace(users[0].FirstName + " " + users[0].LastName)
}

func randomID() int {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<31))
	if err != nil {
		return 1
	}
	return int(n.Int64())
}
