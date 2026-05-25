-- Enable foreign keys and WAL mode for performance
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS inboxes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT UNIQUE NOT NULL COLLATE NOCASE,
    local_part TEXT NOT NULL COLLATE NOCASE,
    token TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_accessed_at INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_inboxes_token ON inboxes(token);
CREATE INDEX IF NOT EXISTS idx_inboxes_address ON inboxes(address);
CREATE INDEX IF NOT EXISTS idx_inboxes_expires_at ON inboxes(expires_at);

CREATE TABLE IF NOT EXISTS emails (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id INTEGER NOT NULL,
    message_id TEXT,
    sender TEXT NOT NULL,
    sender_name TEXT,
    recipient TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '(no subject)',
    body_html TEXT,
    body_text TEXT,
    raw_headers TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    received_at INTEGER NOT NULL DEFAULT (unixepoch()),
    is_read INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(inbox_id) REFERENCES inboxes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_emails_inbox_id ON emails(inbox_id);
CREATE INDEX IF NOT EXISTS idx_emails_received_at ON emails(received_at);

CREATE TABLE IF NOT EXISTS attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email_id INTEGER NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    content BLOB NOT NULL,
    FOREIGN KEY(email_id) REFERENCES emails(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_attachments_email_id ON attachments(email_id);
