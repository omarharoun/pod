-- M1: accounts and passwordless sign-in (ADR-002).
--
-- Everything here is UTC (CLAUDE.md §5). Pod cycles anchor to a pod's shared timezone,
-- which is a boundary calculation on top of UTC, never a substitute for it.

CREATE TABLE users (
    id                  uuid PRIMARY KEY,
    -- Stored already normalised (trimmed, lower-cased) by the application, so the
    -- uniqueness constraint means what it looks like it means.
    email               text        NOT NULL UNIQUE,
    display_name        text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL,
    onboarding_complete boolean     NOT NULL DEFAULT false
);

-- One outstanding login code. The code itself is never stored — only a hash peppered
-- with the service secret and bound to this row's id.
CREATE TABLE auth_challenges (
    id          uuid PRIMARY KEY,
    email       text        NOT NULL,
    code_hash   text        NOT NULL,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    attempts    integer     NOT NULL DEFAULT 0,
    consumed_at timestamptz
);

-- Supports the per-address send limit, which is a count over a rolling hour.
CREATE INDEX auth_challenges_email_created_at_idx ON auth_challenges (email, created_at DESC);

-- Lets the expiry sweep find spent rows without a full scan. Challenges are credentials;
-- they should not outlive their usefulness in the table any more than in memory.
CREATE INDEX auth_challenges_expires_at_idx ON auth_challenges (expires_at);

-- A rotating chain of refresh tokens, one chain per signed-in device. family_id ties a
-- chain together so that a replayed token can revoke every descendant of it.
CREATE TABLE refresh_tokens (
    token_hash  text PRIMARY KEY,
    family_id   uuid        NOT NULL,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_name text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    rotated_at  timestamptz,
    revoked_at  timestamptz
);

CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
