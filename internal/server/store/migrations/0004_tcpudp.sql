-- Tunnel type "tcpudp": one remote port open for both TCP and UDP.
-- SQLite cannot change a CHECK constraint, so the table is rebuilt. Port uniqueness becomes per
-- protocol: a tcpudp tunnel occupies its port for TCP and for UDP.

CREATE TABLE tunnels_new (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users (id),
    device_id             TEXT REFERENCES devices (id) ON DELETE SET NULL,
    name                  TEXT NOT NULL,
    type                  TEXT NOT NULL CHECK (type IN ('https', 'tcp', 'udp', 'tcpudp')),
    domain_id             TEXT REFERENCES domains (id),
    subdomain             TEXT,
    host                  TEXT UNIQUE COLLATE NOCASE,
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
    access_policy         TEXT NOT NULL DEFAULT 'public' CHECK (access_policy IN ('public', 'password', 'basic', 'login')),
    access_password_hash  TEXT NOT NULL DEFAULT '',
    basic_username        TEXT NOT NULL DEFAULT '',
    policy_rev            INTEGER NOT NULL DEFAULT 1,
    ip_allowlist          TEXT NOT NULL DEFAULT '',
    interstitial          INTEGER NOT NULL DEFAULT 0,
    bandwidth_kbps        INTEGER NOT NULL DEFAULT 0,
    max_conns             INTEGER NOT NULL DEFAULT 0,
    monthly_quota_mb      INTEGER NOT NULL DEFAULT 0,
    quota_action          TEXT NOT NULL DEFAULT 'pause' CHECK (quota_action IN ('pause', 'alert')),
    host_rewrite          TEXT NOT NULL DEFAULT '',
    UNIQUE (user_id, name),
    CHECK ((type = 'https' AND host IS NOT NULL AND remote_port IS NULL)
        OR (type IN ('tcp', 'udp', 'tcpudp') AND host IS NULL AND remote_port IS NOT NULL))
);

INSERT INTO tunnels_new (
    id, user_id, device_id, name, type, domain_id, subdomain, host, remote_port, local_ip, local_port,
    client_can_edit_local, local_loopback_only, client_can_toggle, enabled, paused_by_client, note, expires_at,
    created_at, updated_at, access_policy, access_password_hash, basic_username, policy_rev, ip_allowlist,
    interstitial, bandwidth_kbps, max_conns, monthly_quota_mb, quota_action, host_rewrite)
SELECT
    id, user_id, device_id, name, type, domain_id, subdomain, host, remote_port, local_ip, local_port,
    client_can_edit_local, local_loopback_only, client_can_toggle, enabled, paused_by_client, note, expires_at,
    created_at, updated_at, access_policy, access_password_hash, basic_username, policy_rev, ip_allowlist,
    interstitial, bandwidth_kbps, max_conns, monthly_quota_mb, quota_action, host_rewrite
FROM tunnels;

DROP TABLE tunnels;
ALTER TABLE tunnels_new RENAME TO tunnels;

CREATE UNIQUE INDEX tunnels_tcp_port ON tunnels (remote_port) WHERE type IN ('tcp', 'tcpudp');
CREATE UNIQUE INDEX tunnels_udp_port ON tunnels (remote_port) WHERE type IN ('udp', 'tcpudp');
CREATE INDEX tunnels_device ON tunnels (device_id);
