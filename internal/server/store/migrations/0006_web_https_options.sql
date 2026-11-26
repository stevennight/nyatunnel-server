-- Per-tunnel HTTPS controls for Web (type = 'https') tunnels.
-- Existing tunnels keep both enabled (HTTPS on + force redirect from HTTP to HTTPS).
ALTER TABLE tunnels ADD COLUMN enable_https INTEGER NOT NULL DEFAULT 1;
ALTER TABLE tunnels ADD COLUMN force_https  INTEGER NOT NULL DEFAULT 1;
