-- M3: self-service quotas, tunnel requests, custom domains, notification channels.

-- Self-service quota of a user (JSON, see store.Quota); '' means none (everything needs approval).
ALTER TABLE users ADD COLUMN quota TEXT NOT NULL DEFAULT '';

-- Root domains (tunnels get <sub>.<name>) and custom domains (one tunnel gets the whole name).
ALTER TABLE domains ADD COLUMN kind TEXT NOT NULL DEFAULT 'root' CHECK (kind IN ('root', 'custom'));
ALTER TABLE domains ADD COLUMN owner_user_id TEXT REFERENCES users (id);
-- custom domains: pending (waiting for an admin), dns (approved, waiting for DNS), active, disabled
ALTER TABLE domains ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('pending', 'dns', 'active', 'disabled'));
ALTER TABLE domains ADD COLUMN checked_at INTEGER;
ALTER TABLE domains ADD COLUMN check_error TEXT NOT NULL DEFAULT '';

CREATE TABLE tunnel_requests (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id),
    device_id   TEXT REFERENCES devices (id) ON DELETE SET NULL,
    payload     TEXT NOT NULL, -- JSON: type, subdomain, domainId, localIp, localPort, durationHours, customDomain
    reason      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_by TEXT,
    reviewed_at INTEGER,
    tunnel_id   TEXT,
    created_at  INTEGER NOT NULL
);
CREATE INDEX tunnel_requests_status ON tunnel_requests (status, created_at);

CREATE TABLE channels (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('webhook', 'telegram')),
    name       TEXT NOT NULL,
    config     BLOB NOT NULL,             -- sealed JSON (webhook URL / bot token and chat id)
    events     TEXT NOT NULL DEFAULT '[]', -- JSON array of event kinds; empty = all
    enabled    INTEGER NOT NULL DEFAULT 1,
    last_error TEXT NOT NULL DEFAULT '',
    last_sent  INTEGER,
    created_at INTEGER NOT NULL
);
