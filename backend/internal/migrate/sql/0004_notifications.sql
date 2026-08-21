-- M4: push registration and the outbox.

CREATE TABLE devices (
    -- The token is a credential for reaching somebody's phone, so it is stored hashed
    -- and never logged. The plaintext exists only in the request that registered it and
    -- in the push provider's call.
    token_hash text PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    platform   text        NOT NULL CHECK (platform IN ('ios', 'android')),
    locale     text        NOT NULL DEFAULT 'en-US',
    -- Kept so a provider rejection can retire a token that has gone stale.
    registered_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    retired_at    timestamptz
);

CREATE INDEX devices_user_id_idx ON devices (user_id) WHERE retired_at IS NULL;

-- The outbox. Its job is to make sending exactly-once-ish: the scheduled job runs hourly
-- and would otherwise re-send the same "new week" notification every hour for a week.
--
-- The unique key is (kind, subject, user_id), where subject is whatever the notification
-- is about — a week id, a pod id, a proof id.
CREATE TABLE notifications (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    -- Text rather than uuid because some kinds are about a pair of things — "this person
    -- posted in this week" — and a uuid column could only hold one of them. Keying it on
    -- the week alone would collapse two pod-mates' posts into one notification; keying it
    -- on the poster alone would suppress their post in every later week.
    subject_id text        NOT NULL,
    -- Copy key plus its parameters. Deliberately not the rendered sentence: a queued
    -- notification is resolved at send time from the catalogue, so fixing a wording
    -- mistake fixes what has not gone out yet (ADR-003).
    copy_key   text        NOT NULL,
    params     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL,
    sent_at    timestamptz,
    failed_at  timestamptz,
    UNIQUE (kind, subject_id, user_id)
);

CREATE INDEX notifications_unsent_idx ON notifications (created_at) WHERE sent_at IS NULL AND failed_at IS NULL;
