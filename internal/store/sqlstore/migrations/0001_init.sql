-- burp initial schema. Portable across sqlite and postgres: timestamps are
-- stored as unix milliseconds in BIGINT columns.

CREATE TABLE IF NOT EXISTS users (
    id          BIGINT PRIMARY KEY,
    login       TEXT NOT NULL,
    avatar_url  TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);

-- One credential per user. Deleting a user removes their tokens.
CREATE TABLE IF NOT EXISTS credentials (
    user_id             BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token        TEXT NOT NULL,
    refresh_token       TEXT NOT NULL,
    access_expires_at   BIGINT NOT NULL,
    refresh_expires_at  BIGINT NOT NULL,
    updated_at          BIGINT NOT NULL
);

-- Browser sessions; deleting a user signs them out everywhere.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash  TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS oauth_states (
    state       TEXT PRIMARY KEY,
    return_to   TEXT NOT NULL DEFAULT '',
    created_at  BIGINT NOT NULL,
    expires_at  BIGINT NOT NULL
);

-- Per-file reviewed marks; one row per (user, PR, path).
CREATE TABLE IF NOT EXISTS file_marks (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    owner       TEXT NOT NULL,
    repo        TEXT NOT NULL,
    number      INTEGER NOT NULL,
    path        TEXT NOT NULL,
    head_sha    TEXT NOT NULL,
    marked_at   BIGINT NOT NULL,
    PRIMARY KEY (user_id, owner, repo, number, path)
);

-- Draft review comments held until the review is submitted.
CREATE TABLE IF NOT EXISTS drafts (
    id          TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    owner       TEXT NOT NULL,
    repo        TEXT NOT NULL,
    number      INTEGER NOT NULL,
    path        TEXT NOT NULL,
    side        TEXT NOT NULL,
    line        INTEGER NOT NULL,
    start_line  INTEGER NOT NULL DEFAULT 0,
    commit_sha  TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL,
    created_at  BIGINT NOT NULL,
    updated_at  BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS drafts_pr_idx ON drafts(user_id, owner, repo, number);
