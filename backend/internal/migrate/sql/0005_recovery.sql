-- M5: what happens when a pod stops, and what is offered afterwards.

ALTER TABLE pods
    -- Why a pod ended. `inactive` is the only value the system writes today; the column
    -- exists so a future reason is a value rather than a schema change.
    ADD COLUMN dissolved_reason text;

ALTER TABLE users
    -- How many pods have dissolved under this person. CLAUDE.md §6: after three, they
    -- are offered the alternate path — "some people need less social pressure, not
    -- more". It is a count of circumstances, not a mark against anyone, and it is never
    -- returned by the API.
    ADD COLUMN dissolved_pods integer NOT NULL DEFAULT 0 CHECK (dissolved_pods >= 0),
    -- Set when a pod they were in dissolved, cleared when they join a new one. Drives the
    -- offer to lower the bar.
    ADD COLUMN recovery_offered_at timestamptz;

-- Still no reliability score anywhere in this schema. It is computed from week history at
-- matching time (internal/reliability), so there is no stored number to drift, to
-- backfill, or to be dragged into a response by a future join (CLAUDE.md §6).
