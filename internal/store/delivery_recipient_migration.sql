ALTER TABLE delivery_attempts RENAME TO delivery_attempts_v2;
ALTER TABLE deliveries RENAME TO deliveries_v2;
DROP INDEX delivery_ready;
DROP INDEX delivery_attempts_delivery;
CREATE TABLE deliveries (
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
    body_format TEXT NOT NULL DEFAULT 'text' CHECK (body_format IN ('text','html')),
    recipient_index INTEGER NOT NULL DEFAULT 0,
    UNIQUE(event_id, channel_id, policy_id, recipient_index)
);
CREATE INDEX delivery_ready ON deliveries(status, next_try);
INSERT INTO deliveries(id,event_id,channel_id,channel_name,channel_source,title,body,status,attempts,next_try,error,updated,claim,policy_id,body_format)
    SELECT id,event_id,channel_id,channel_name,channel_source,title,body,status,attempts,next_try,error,updated,claim,policy_id,body_format FROM deliveries_v2;
