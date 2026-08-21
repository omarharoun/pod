-- M2: the thing you've been putting off, the queue, and the pod you land in.

-- One intent per person. Replacing it replaces the row: a person has one thing they are
-- working on, and changing it is a change of mind, not a second goal (CLAUDE.md §1).
CREATE TABLE intents (
    user_id                 uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    goal_text               text        NOT NULL,
    goal_category           text        NOT NULL,
    -- 1..5, see ADR-010. Constrained here as well as in the handler because the matching
    -- filter's arithmetic assumes the range.
    intensity               smallint    NOT NULL CHECK (intensity BETWEEN 1 AND 5),
    timezone                text        NOT NULL,
    self_report_reliability text        NOT NULL,
    -- Windows in the person's own local time, as [{day_of_week,start_minute,end_minute}].
    availability_windows    jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- The 0.40 similarity signal. Regenerating these is a backfill, which is why the
    -- dimension is fixed in internal/embed rather than inferred from the column.
    embedding               real[]      NOT NULL,
    updated_at              timestamptz NOT NULL
);

CREATE INDEX intents_category_intensity_idx ON intents (goal_category, intensity);

-- Who is waiting for the next matching window.
--
-- CLAUDE.md §5 nominates Redis for this queue; it lives in Postgres instead, because the
-- matching job is an hourly batch that reads every waiting person's attributes — which
-- are already here — and writes pods transactionally with the dequeue. See ADR-013.
CREATE TABLE pool_tickets (
    user_id   uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    queued_at timestamptz NOT NULL
);

CREATE INDEX pool_tickets_queued_at_idx ON pool_tickets (queued_at);

CREATE TABLE pods (
    id            uuid PRIMARY KEY,
    -- forming | active | dissolved
    state         text        NOT NULL,
    goal_category text        NOT NULL,
    -- The pod's shared zone. Cycles anchor here, not to each member's device
    -- (CLAUDE.md §5).
    timezone      text        NOT NULL,
    -- Consecutive weeks the POD completed. There is deliberately no per-member
    -- equivalent, here or anywhere (CLAUDE.md §2 rule 3).
    streak_weeks  integer     NOT NULL DEFAULT 0 CHECK (streak_weeks >= 0),
    created_at    timestamptz NOT NULL,
    dissolved_at  timestamptz
);

CREATE INDEX pods_state_idx ON pods (state);

CREATE TABLE pod_members (
    pod_id    uuid        NOT NULL REFERENCES pods (id) ON DELETE CASCADE,
    user_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    joined_at timestamptz NOT NULL,
    left_at   timestamptz,
    PRIMARY KEY (pod_id, user_id)
);

CREATE INDEX pod_members_user_id_idx ON pod_members (user_id);

-- One active pod per person. CLAUDE.md §2 rule 6 gives everyone one free pod forever and
-- gates additional ones behind the paywall in M7; until that exists, the invariant is
-- enforced here rather than trusted to the handler.
CREATE UNIQUE INDEX pod_members_one_active_per_user
    ON pod_members (user_id)
    WHERE left_at IS NULL;

-- Nothing in this migration stores a reliability score. It is derived at matching time
-- from history plus the cold-start self-report, and it is never persisted next to a user
-- where a future join could drag it into a response (CLAUDE.md §6).
