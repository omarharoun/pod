-- M3: the week loop. One small action, ten seconds of proof, and who is in.

CREATE TABLE weeks (
    id          uuid PRIMARY KEY,
    pod_id      uuid        NOT NULL REFERENCES pods (id) ON DELETE CASCADE,
    -- 1-based within the pod, assigned on creation.
    index       integer     NOT NULL CHECK (index >= 1),
    action_text text        NOT NULL,
    -- True while the week is carrying its category default because nobody has named an
    -- action. The client uses it to invite, never to nag.
    action_is_default boolean NOT NULL DEFAULT true,
    -- Anchored to the pod's timezone, stored UTC (CLAUDE.md §5).
    opens_at    timestamptz NOT NULL,
    closes_at   timestamptz NOT NULL,
    -- Set when the week is closed out. Whether the POD completed, never a per-member
    -- verdict (CLAUDE.md §2 rule 3).
    pod_completed boolean   NOT NULL DEFAULT false,
    closed_at   timestamptz,
    CHECK (closes_at > opens_at),
    UNIQUE (pod_id, index)
);

-- One open week per pod. A second one would mean two places to post the same week.
CREATE UNIQUE INDEX weeks_one_per_pod_per_window ON weeks (pod_id, opens_at);
CREATE INDEX weeks_closes_at_idx ON weeks (closes_at);

-- A member's standing in a week.
--
-- There is no `missed` and no `failed` here, and there will not be (ADR-006). A row is
-- created open when the week opens and moves to posted, or is left to close as resting.
CREATE TABLE member_weeks (
    week_id   uuid        NOT NULL REFERENCES weeks (id) ON DELETE CASCADE,
    user_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    state     text        NOT NULL DEFAULT 'open' CHECK (state IN ('posted', 'open', 'resting')),
    posted_at timestamptz,
    PRIMARY KEY (week_id, user_id)
);

CREATE INDEX member_weeks_user_id_idx ON member_weeks (user_id);

-- Ten seconds of evidence, then it is gone.
--
-- The bytes live in object storage, never in this table and never in the API tier
-- (ADR-008). What is here is the pointer and the expiry, and the expiry is a promise
-- (CLAUDE.md §2 rule 4).
CREATE TABLE proofs (
    id           uuid PRIMARY KEY,
    week_id      uuid        NOT NULL REFERENCES weeks (id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         text        NOT NULL CHECK (kind IN ('photo', 'voice')),
    content_type text        NOT NULL,
    byte_size    bigint      NOT NULL CHECK (byte_size > 0),
    duration_ms  integer,
    -- The key in object storage. Null until the upload is committed.
    blob_key     text,
    created_at   timestamptz NOT NULL,
    committed_at timestamptz,
    -- When the bytes are deleted, server-side. Set at creation, never extended.
    expires_at   timestamptz NOT NULL,
    deleted_at   timestamptz
);

-- One committed proof per person per week. An uncommitted slot does not count, so a
-- retry after a failed upload works without the person having to do anything.
CREATE UNIQUE INDEX proofs_one_committed_per_member_per_week
    ON proofs (week_id, user_id)
    WHERE committed_at IS NOT NULL AND deleted_at IS NULL;

-- The sweep's index: find what is due for deletion without scanning.
CREATE INDEX proofs_expires_at_idx ON proofs (expires_at) WHERE deleted_at IS NULL;
