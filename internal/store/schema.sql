CREATE TABLE IF NOT EXISTS admin (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL,
    salt BLOB NOT NULL,
    password BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
    token_hash BLOB PRIMARY KEY,
    expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS resources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK (kind IN ('mailboxes','rules','models','channels','smtp','templates','policies')),
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    config BLOB NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1,
    interval_seconds INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT '',
    next_run INTEGER NOT NULL DEFAULT 0,
    requested INTEGER NOT NULL DEFAULT 0,
    lease_until INTEGER NOT NULL DEFAULT 0,
    run_token TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS singleton_resources ON resources(kind) WHERE kind IN ('smtp','models');
CREATE INDEX IF NOT EXISTS resource_schedule ON resources(kind, enabled, next_run);
CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    at INTEGER NOT NULL,
    success INTEGER NOT NULL,
    message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    at INTEGER NOT NULL,
    kind TEXT NOT NULL,
    source TEXT NOT NULL,
    title TEXT NOT NULL,
    detail TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES events(id),
    channel_id INTEGER NOT NULL,
    channel_name TEXT NOT NULL,
    channel_source TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_try INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    updated INTEGER NOT NULL,
    claim TEXT NOT NULL DEFAULT '',
    policy_id INTEGER NOT NULL DEFAULT 0,
    UNIQUE(event_id, channel_id, policy_id)
);
CREATE INDEX IF NOT EXISTS delivery_ready ON deliveries(status, next_try);

CREATE TABLE IF NOT EXISTS model_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_token TEXT NOT NULL UNIQUE,
    started_at INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0,
    trigger TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    http_status INTEGER NOT NULL DEFAULT 0,
    model_count INTEGER,
    added TEXT NOT NULL DEFAULT '[]',
    removed TEXT NOT NULL DEFAULT '[]',
    note TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS interface_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    at INTEGER NOT NULL,
    direction TEXT NOT NULL,
    operation TEXT NOT NULL,
    target TEXT NOT NULL,
    reference TEXT NOT NULL,
    status INTEGER NOT NULL,
    success INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    note TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS interface_logs_direction ON interface_logs(direction, id);
