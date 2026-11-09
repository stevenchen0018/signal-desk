CREATE TABLE IF NOT EXISTS job_traces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_id INTEGER NOT NULL,
    kind TEXT NOT NULL,
    source TEXT NOT NULL,
    trigger TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'running',
    note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS job_traces_resource ON job_traces(resource_id,id);
CREATE TABLE IF NOT EXISTS trace_steps (
    trace_id INTEGER NOT NULL REFERENCES job_traces(id),
    ordinal INTEGER NOT NULL,
    at INTEGER NOT NULL,
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    PRIMARY KEY(trace_id,ordinal)
);
CREATE TABLE IF NOT EXISTS trace_events (
    trace_id INTEGER NOT NULL REFERENCES job_traces(id),
    event_id INTEGER NOT NULL UNIQUE REFERENCES events(id),
    PRIMARY KEY(trace_id,event_id)
);
CREATE TABLE IF NOT EXISTS delivery_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id INTEGER NOT NULL REFERENCES deliveries(id),
    at INTEGER NOT NULL,
    status TEXT NOT NULL,
    note TEXT NOT NULL,
    next_try INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    steps TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS delivery_attempts_delivery ON delivery_attempts(delivery_id,id);
