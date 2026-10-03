-- M2: access policies, limits and traffic accounting.

ALTER TABLE tunnels ADD COLUMN access_policy TEXT NOT NULL DEFAULT 'public'
    CHECK (access_policy IN ('public', 'password', 'basic', 'login'));
ALTER TABLE tunnels ADD COLUMN access_password_hash TEXT NOT NULL DEFAULT ''; -- password and basic
ALTER TABLE tunnels ADD COLUMN basic_username TEXT NOT NULL DEFAULT '';
ALTER TABLE tunnels ADD COLUMN policy_rev INTEGER NOT NULL DEFAULT 1;          -- bumps invalidate gate cookies
ALTER TABLE tunnels ADD COLUMN ip_allowlist TEXT NOT NULL DEFAULT '';           -- comma separated CIDRs / addresses
ALTER TABLE tunnels ADD COLUMN interstitial INTEGER NOT NULL DEFAULT 0;         -- first-visit warning page
ALTER TABLE tunnels ADD COLUMN bandwidth_kbps INTEGER NOT NULL DEFAULT 0;       -- 0 = unlimited, per direction
ALTER TABLE tunnels ADD COLUMN max_conns INTEGER NOT NULL DEFAULT 0;            -- 0 = unlimited
ALTER TABLE tunnels ADD COLUMN monthly_quota_mb INTEGER NOT NULL DEFAULT 0;     -- 0 = unlimited
ALTER TABLE tunnels ADD COLUMN quota_action TEXT NOT NULL DEFAULT 'pause' CHECK (quota_action IN ('pause', 'alert'));
ALTER TABLE tunnels ADD COLUMN host_rewrite TEXT NOT NULL DEFAULT '';

-- Hourly traffic per tunnel, as metered by the server edge.
CREATE TABLE traffic_stats (
    tunnel_id   TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    bucket_hour INTEGER NOT NULL, -- Unix ms of the hour's start (UTC)
    bytes_in    INTEGER NOT NULL DEFAULT 0, -- visitor -> device
    bytes_out   INTEGER NOT NULL DEFAULT 0, -- device -> visitor
    conns       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tunnel_id, bucket_hour)
);
CREATE INDEX traffic_stats_hour ON traffic_stats (bucket_hour);
CREATE INDEX traffic_stats_user ON traffic_stats (user_id, bucket_hour);
