-- M6: a pod runs for a fixed number of weeks, then finishes, and its members choose
-- whether to do another one.

ALTER TABLE pods
    -- How many weeks this pod's cycle runs for, fixed when the pod forms. Stored per pod
    -- rather than read from config at render time, so changing the default never moves
    -- the finish line under a pod that is already running (ADR-023).
    ADD COLUMN cycle_length_weeks integer NOT NULL DEFAULT 6
        CHECK (cycle_length_weeks >= 1),
    -- When the cycle finished. Graduation, not an ending: `completed` and `dissolved` are
    -- different events and only the second one has a reliability consequence.
    ADD COLUMN completed_at timestamptz,
    -- After this, an unanswered pod closes. Closing is the default because holding
    -- someone in a pod they did not re-choose is the worse mistake.
    ADD COLUMN decide_by timestamptz;

-- A pod cannot be both graduated and dissolved.
ALTER TABLE pods
    ADD CONSTRAINT pods_completed_xor_dissolved
        CHECK (completed_at IS NULL OR dissolved_at IS NULL);

-- One row per member per cycle: are they carrying on?
--
-- There is deliberately no index or view that joins this to a name, and no API shape that
-- returns a member id beside a `false`. "Alex is not continuing" is the most shaming
-- sentence this product could show, and the schema should not make it one join away
-- (CLAUDE.md §2 rule 1). What crosses the wire is a count.
CREATE TABLE pod_continuations (
    pod_id      uuid        NOT NULL REFERENCES pods (id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    continuing  boolean     NOT NULL,
    answered_at timestamptz NOT NULL,
    PRIMARY KEY (pod_id, user_id)
);
