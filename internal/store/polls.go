package store

import "database/sql"

// Poll is a mirrored VK poll.
type Poll struct {
	ID              int64
	VKPollID        int64
	VKOwnerID       int64
	VKPeerID        int64
	VKCMID          int64
	Question        string
	IsMultiple      bool
	IsAnonymous     bool
	EndDate         int64
	VotersAvailable bool
	Status          string
}

// Option is a single answer option with the latest VK vote snapshot.
type Option struct {
	ID         int64
	VKAnswerID int64
	Text       string
	Position   int
	VKVotes    int
}

const (
	PollActive = "active"
	PollClosed = "closed"
)

// UpsertPoll inserts a poll mirror or updates its mutable snapshot fields,
// returning the local poll id and whether it was newly created.
func (s *Store) UpsertPoll(p *Poll) (id int64, created bool, err error) {
	row := s.db.QueryRow(`SELECT id FROM polls WHERE vk_owner_id=? AND vk_poll_id=?`,
		p.VKOwnerID, p.VKPollID)
	err = row.Scan(&id)
	if err == sql.ErrNoRows {
		res, e := s.db.Exec(`
			INSERT INTO polls (vk_poll_id, vk_owner_id, vk_peer_id, vk_cmid, question,
				is_multiple, is_anonymous, end_date, voters_available, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
			p.VKPollID, p.VKOwnerID, p.VKPeerID, p.VKCMID, p.Question,
			b2i(p.IsMultiple), b2i(p.IsAnonymous), p.EndDate, b2i(p.VotersAvailable), now())
		if e != nil {
			return 0, false, e
		}
		id, _ = res.LastInsertId()
		return id, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, err = s.db.Exec(`
		UPDATE polls SET question=?, is_multiple=?, is_anonymous=?, end_date=?, voters_available=?
		WHERE id=?`,
		p.Question, b2i(p.IsMultiple), b2i(p.IsAnonymous), p.EndDate, b2i(p.VotersAvailable), id)
	return id, false, err
}

// GetPoll loads a poll by local id.
func (s *Store) GetPoll(id int64) (*Poll, error) {
	p := &Poll{}
	var mult, anon, vavail int
	err := s.db.QueryRow(`
		SELECT id, vk_poll_id, vk_owner_id, vk_peer_id, vk_cmid, question,
			is_multiple, is_anonymous, end_date, voters_available, status
		FROM polls WHERE id=?`, id).
		Scan(&p.ID, &p.VKPollID, &p.VKOwnerID, &p.VKPeerID, &p.VKCMID, &p.Question,
			&mult, &anon, &p.EndDate, &vavail, &p.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsMultiple, p.IsAnonymous, p.VotersAvailable = mult != 0, anon != 0, vavail != 0
	return p, nil
}

// ActivePolls returns all polls still open.
func (s *Store) ActivePolls() ([]Poll, error) {
	rows, err := s.db.Query(`
		SELECT id, vk_poll_id, vk_owner_id, vk_peer_id, vk_cmid, question,
			is_multiple, is_anonymous, end_date, voters_available, status
		FROM polls WHERE status='active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Poll
	for rows.Next() {
		var p Poll
		var mult, anon, vavail int
		if err := rows.Scan(&p.ID, &p.VKPollID, &p.VKOwnerID, &p.VKPeerID, &p.VKCMID, &p.Question,
			&mult, &anon, &p.EndDate, &vavail, &p.Status); err != nil {
			return nil, err
		}
		p.IsMultiple, p.IsAnonymous, p.VotersAvailable = mult != 0, anon != 0, vavail != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClosePoll marks a poll closed.
func (s *Store) ClosePoll(id int64) error {
	_, err := s.db.Exec(`UPDATE polls SET status='closed', closed_at=? WHERE id=?`, now(), id)
	return err
}

// UpsertOption inserts or updates an answer option (matched by vk_answer_id).
func (s *Store) UpsertOption(pollID int64, o Option) error {
	_, err := s.db.Exec(`
		INSERT INTO poll_options (poll_id, vk_answer_id, text, position, vk_votes)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(poll_id, vk_answer_id)
		DO UPDATE SET text=excluded.text, position=excluded.position, vk_votes=excluded.vk_votes`,
		pollID, o.VKAnswerID, o.Text, o.Position, o.VKVotes)
	return err
}

// Options returns a poll's options ordered by position.
func (s *Store) Options(pollID int64) ([]Option, error) {
	rows, err := s.db.Query(`
		SELECT id, vk_answer_id, text, position, vk_votes
		FROM poll_options WHERE poll_id=? ORDER BY position`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Option
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.VKAnswerID, &o.Text, &o.Position, &o.VKVotes); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Per-user mirrored TG messages
// ---------------------------------------------------------------------------

// TGMessageRef ties a poll to a delivered TG message.
type TGMessageRef struct {
	TGID      int64
	MessageID int
}

// AddPollTGMessage records the TG message id created for a user.
func (s *Store) AddPollTGMessage(pollID, tgID int64, messageID int) error {
	_, err := s.db.Exec(`
		INSERT INTO poll_tg_messages (poll_id, tg_id, tg_message_id)
		VALUES (?, ?, ?)
		ON CONFLICT(poll_id, tg_id) DO UPDATE SET tg_message_id=excluded.tg_message_id`,
		pollID, tgID, messageID)
	return err
}

// PollTGMessages returns all delivered TG messages for a poll.
func (s *Store) PollTGMessages(pollID int64) ([]TGMessageRef, error) {
	rows, err := s.db.Query(`SELECT tg_id, tg_message_id FROM poll_tg_messages WHERE poll_id=?`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TGMessageRef
	for rows.Next() {
		var r TGMessageRef
		if err := rows.Scan(&r.TGID, &r.MessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Votes
// ---------------------------------------------------------------------------

// Vote pairs an answer with the voter identity.
type Vote struct {
	AnswerID int64
	TGID     int64 // for tg votes
	VKUserID int64 // for vk voters
}

// ToggleTGVote flips a user's TG vote for an answer. For single-choice polls it
// clears the user's other selections first. Returns the user's current selection set.
func (s *Store) ToggleTGVote(pollID, answerID, tgID int64, multiple bool) ([]int64, error) {
	var existing int
	_ = s.db.QueryRow(`SELECT 1 FROM tg_votes WHERE poll_id=? AND vk_answer_id=? AND tg_id=?`,
		pollID, answerID, tgID).Scan(&existing)

	if existing == 1 {
		if _, err := s.db.Exec(`DELETE FROM tg_votes WHERE poll_id=? AND vk_answer_id=? AND tg_id=?`,
			pollID, answerID, tgID); err != nil {
			return nil, err
		}
	} else {
		if !multiple {
			if _, err := s.db.Exec(`DELETE FROM tg_votes WHERE poll_id=? AND tg_id=?`, pollID, tgID); err != nil {
				return nil, err
			}
		}
		if _, err := s.db.Exec(`
			INSERT OR IGNORE INTO tg_votes (poll_id, vk_answer_id, tg_id, created_at)
			VALUES (?, ?, ?, ?)`, pollID, answerID, tgID, now()); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.Query(`SELECT vk_answer_id FROM tg_votes WHERE poll_id=? AND tg_id=?`, pollID, tgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sel []int64
	for rows.Next() {
		var a int64
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		sel = append(sel, a)
	}
	return sel, rows.Err()
}

// UserTGVotes returns the answer ids a user selected in a poll.
func (s *Store) UserTGVotes(pollID, tgID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT vk_answer_id FROM tg_votes WHERE poll_id=? AND tg_id=?`, pollID, tgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sel []int64
	for rows.Next() {
		var a int64
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		sel = append(sel, a)
	}
	return sel, rows.Err()
}

// AllTGVotes returns every TG vote in a poll (answer + voter tg id).
func (s *Store) AllTGVotes(pollID int64) ([]Vote, error) {
	rows, err := s.db.Query(`SELECT vk_answer_id, tg_id FROM tg_votes WHERE poll_id=?`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Vote
	for rows.Next() {
		var v Vote
		if err := rows.Scan(&v.AnswerID, &v.TGID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReplaceVKVoters swaps the snapshot of native VK voters for a poll.
// voters maps vk_answer_id -> list of vk_user_id.
func (s *Store) ReplaceVKVoters(pollID int64, voters map[int64][]int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM vk_voters WHERE poll_id=?`, pollID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO vk_voters (poll_id, vk_answer_id, vk_user_id) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for answerID, users := range voters {
		for _, uid := range users {
			if _, err := stmt.Exec(pollID, answerID, uid); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// AllVKVoters returns the native VK voter snapshot (answer + vk user id).
func (s *Store) AllVKVoters(pollID int64) ([]Vote, error) {
	rows, err := s.db.Query(`SELECT vk_answer_id, vk_user_id FROM vk_voters WHERE poll_id=?`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Vote
	for rows.Next() {
		var v Vote
		if err := rows.Scan(&v.AnswerID, &v.VKUserID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LinkedVKByTG returns a map of tg_id -> vk_user_id for all linked users
// (used by the tally merge to dedup TG votes against native VK voters).
func (s *Store) LinkedVKByTG() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT tg_id, vk_user_id FROM tg_users WHERE vk_user_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var tgID, vkID int64
		if err := rows.Scan(&tgID, &vkID); err != nil {
			return nil, err
		}
		out[tgID] = vkID
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
