-- Who may pass a tunnel's login gate: only its owner (default), the owner plus listed users, or
-- every account of the site.
ALTER TABLE tunnels ADD COLUMN login_access TEXT NOT NULL DEFAULT 'owner' CHECK (login_access IN ('owner', 'users', 'all'));
ALTER TABLE tunnels ADD COLUMN login_users TEXT NOT NULL DEFAULT '[]'; -- JSON array of user ids
