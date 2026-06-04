-- Telegram users: admin approval + optional VK link.
CREATE TABLE IF NOT EXISTS tg_users (
    tg_id       INTEGER PRIMARY KEY,
    username    TEXT    NOT NULL DEFAULT '',
    first_name  TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL DEFAULT 'pending', -- pending | approved | blocked
    vk_user_id  INTEGER UNIQUE,                     -- linked VK user id, NULL if not linked
    created_at  INTEGER NOT NULL,
    approved_at INTEGER,
    linked_at   INTEGER
);

-- One-time codes the user posts into the VK chat to prove ownership of a VK account.
CREATE TABLE IF NOT EXISTS link_codes (
    code       TEXT    PRIMARY KEY,
    tg_id      INTEGER NOT NULL REFERENCES tg_users(tg_id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_link_codes_tg ON link_codes(tg_id);

-- Mirror of a VK poll.
CREATE TABLE IF NOT EXISTS polls (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    vk_poll_id       INTEGER NOT NULL,
    vk_owner_id      INTEGER NOT NULL,
    vk_peer_id       INTEGER NOT NULL, -- chat where the poll was posted (for result post-back)
    vk_cmid          INTEGER NOT NULL DEFAULT 0,
    question         TEXT    NOT NULL DEFAULT '',
    is_multiple      INTEGER NOT NULL DEFAULT 0,
    is_anonymous     INTEGER NOT NULL DEFAULT 0,
    end_date         INTEGER NOT NULL DEFAULT 0, -- unix ts, 0 = no end
    voters_available INTEGER NOT NULL DEFAULT 0, -- 1 if last polls.getVoters succeeded
    status           TEXT    NOT NULL DEFAULT 'active', -- active | closed
    created_at       INTEGER NOT NULL,
    closed_at        INTEGER,
    UNIQUE(vk_owner_id, vk_poll_id)
);

-- Answer options + latest snapshot of native VK vote counts.
CREATE TABLE IF NOT EXISTS poll_options (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    poll_id      INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    vk_answer_id INTEGER NOT NULL,
    text         TEXT    NOT NULL DEFAULT '',
    position     INTEGER NOT NULL DEFAULT 0,
    vk_votes     INTEGER NOT NULL DEFAULT 0,
    UNIQUE(poll_id, vk_answer_id)
);

-- Per-user mirrored TG message (so we can edit it / strip buttons / append results).
CREATE TABLE IF NOT EXISTS poll_tg_messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    poll_id       INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    tg_id         INTEGER NOT NULL REFERENCES tg_users(tg_id) ON DELETE CASCADE,
    tg_message_id INTEGER NOT NULL,
    UNIQUE(poll_id, tg_id)
);

-- Local votes cast through TG inline buttons.
CREATE TABLE IF NOT EXISTS tg_votes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    poll_id      INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    vk_answer_id INTEGER NOT NULL,
    tg_id        INTEGER NOT NULL REFERENCES tg_users(tg_id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    UNIQUE(poll_id, vk_answer_id, tg_id)
);

-- Snapshot of native VK voters per answer (only when the poll is not anonymous
-- and polls.getVoters is accessible). Used to dedup against TG votes.
CREATE TABLE IF NOT EXISTS vk_voters (
    poll_id      INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    vk_answer_id INTEGER NOT NULL,
    vk_user_id   INTEGER NOT NULL,
    PRIMARY KEY (poll_id, vk_answer_id, vk_user_id)
);

-- Idempotency log for @all broadcasts (avoid re-forwarding on restart/retries).
CREATE TABLE IF NOT EXISTS broadcasts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    vk_peer_id INTEGER NOT NULL,
    vk_from_id INTEGER NOT NULL,
    vk_cmid    INTEGER NOT NULL,
    text       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(vk_peer_id, vk_cmid)
);
