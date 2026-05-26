-- Singleton key/value settings persisted across restarts. Currently holds only
-- the admin password hash, which is set once via the first-run /admin/setup flow
-- (there is no ADMIN_PASSWORD env). Kept as a generic table so future operator
-- settings can reuse it without another migration.
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
