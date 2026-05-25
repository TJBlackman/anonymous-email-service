-- Mailbox login sessions. A session ties an opaque token (stored in the
-- session cookie) to an inbox/account, with a sliding expiry refreshed on every
-- authenticated request.
CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    inbox_id    INTEGER NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at  INTEGER NOT NULL,
    FOREIGN KEY(inbox_id) REFERENCES inboxes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_inbox_id ON sessions(inbox_id);
