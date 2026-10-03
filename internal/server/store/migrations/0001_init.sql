-- Times are Unix milliseconds. Ids are prefixed random strings (usr_, dev_, tun_, enr_, dom_, pp_).

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE users (
    id             TEXT PRIMARY KEY,
    username       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash  TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    totp_secret    BLOB,            -- sealed with the secrets key; NULL means TOTP is off
    recovery_codes TEXT,            -- JSON array of HashToken(code); used codes are removed
    disabled_at    INTEGER,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE sessions (
    id_hash      TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE devices (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id),
    name           TEXT NOT NULL,
    public_key     BLOB NOT NULL UNIQUE,
    platform       TEXT NOT NULL DEFAULT '',
    client_version TEXT NOT NULL DEFAULT '',
    gui            INTEGER NOT NULL DEFAULT 0,
    config_rev     INTEGER NOT NULL DEFAULT 1,
    last_ip        TEXT NOT NULL DEFAULT '',
    last_seen_at   INTEGER,
    revoked_at     INTEGER,
    created_at     INTEGER NOT NULL
);
CREATE INDEX devices_user ON devices (user_id);

CREATE TABLE enrollments (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_by       TEXT NOT NULL,
    code_hash        TEXT NOT NULL UNIQUE,
    device_name_hint TEXT NOT NULL DEFAULT '',
    tunnel_ids       TEXT NOT NULL DEFAULT '[]', -- tunnels assigned to the device once it enrolls
    expires_at       INTEGER NOT NULL,
    used_at          INTEGER,
    device_id        TEXT,
    created_at       INTEGER NOT NULL
);

CREATE TABLE domains (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE COLLATE NOCASE, -- e.g. dev.example.com (tunnels get <sub>.dev.example.com)
    allow_users INTEGER NOT NULL DEFAULT 1,           -- whether non-admin users may put tunnels on it
    created_at  INTEGER NOT NULL
);

CREATE TABLE port_pools (
    id          TEXT PRIMARY KEY,
    proto       TEXT NOT NULL CHECK (proto IN ('tcp', 'udp')),
    range_start INTEGER NOT NULL CHECK (range_start BETWEEN 1 AND 65535),
    range_end   INTEGER NOT NULL CHECK (range_end BETWEEN range_start AND 65535),
    created_at  INTEGER NOT NULL
);

CREATE TABLE tunnels (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users (id),
    device_id             TEXT REFERENCES devices (id) ON DELETE SET NULL, -- NULL: waiting for an enrolling device
    name                  TEXT NOT NULL,
    type                  TEXT NOT NULL CHECK (type IN ('https', 'tcp', 'udp')),
    domain_id             TEXT REFERENCES domains (id),
    subdomain             TEXT,
    host                  TEXT UNIQUE COLLATE NOCASE, -- subdomain.domain for https tunnels
    remote_port           INTEGER,
    local_ip              TEXT NOT NULL,
    local_port            INTEGER NOT NULL CHECK (local_port BETWEEN 1 AND 65535),
    client_can_edit_local INTEGER NOT NULL DEFAULT 0,
    local_loopback_only   INTEGER NOT NULL DEFAULT 0,
    client_can_toggle     INTEGER NOT NULL DEFAULT 1,
    enabled               INTEGER NOT NULL DEFAULT 1,
    paused_by_client      INTEGER NOT NULL DEFAULT 0,
    note                  TEXT NOT NULL DEFAULT '',
    expires_at            INTEGER,
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL,
    UNIQUE (user_id, name),
    CHECK ((type = 'https' AND host IS NOT NULL AND remote_port IS NULL)
        OR (type IN ('tcp', 'udp') AND host IS NULL AND remote_port IS NOT NULL))
);
CREATE UNIQUE INDEX tunnels_port ON tunnels (type, remote_port) WHERE remote_port IS NOT NULL;
CREATE INDEX tunnels_device ON tunnels (device_id);

CREATE TABLE audit_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at         INTEGER NOT NULL,
    actor_type TEXT NOT NULL, -- user, device, system, anonymous
    actor_id   TEXT NOT NULL DEFAULT '',
    actor_name TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_logs_at ON audit_logs (at);
