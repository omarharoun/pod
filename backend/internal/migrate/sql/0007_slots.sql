-- M7: additional lanes. Each is a second *thing* with its own goal (ADR-026), never a
-- second pod for the goal you already named.

-- Slot 0 is the free one and needs no row here, ever. CLAUDE.md §2 rule 6 gives everyone
-- one free pod forever, and the free loop must not depend on the presence of a payment
-- record — one missing or badly-migrated row would otherwise take the core product away
-- from someone who never paid for it and never needed to.
CREATE TABLE slot_entitlements (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slot       smallint    NOT NULL CHECK (slot BETWEEN 1 AND 4),
    -- app_store | play_store.
    source     text        NOT NULL,
    -- The store's opaque transaction id. Deliberately the only thing kept from a
    -- receipt: no name, no card, no address, no price, no country (CLAUDE.md §8).
    -- Prices come from StoreKit and Play at render time and never from this API.
    external_id text       NOT NULL,
    granted_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, slot)
);

-- Replaying a receipt returns the slot it already opened rather than opening another.
CREATE UNIQUE INDEX slot_entitlements_external_id ON slot_entitlements (source, external_id);

-- An intent is per lane, because a lane is a thing.
ALTER TABLE intents ADD COLUMN slot smallint NOT NULL DEFAULT 0 CHECK (slot BETWEEN 0 AND 4);
ALTER TABLE intents DROP CONSTRAINT intents_pkey;
ALTER TABLE intents ADD PRIMARY KEY (user_id, slot);

-- So is a place in the queue: waiting for a pod for one thing says nothing about another.
ALTER TABLE pool_tickets ADD COLUMN slot smallint NOT NULL DEFAULT 0 CHECK (slot BETWEEN 0 AND 4);
ALTER TABLE pool_tickets DROP CONSTRAINT pool_tickets_pkey;
ALTER TABLE pool_tickets ADD PRIMARY KEY (user_id, slot);

-- The slot belongs to the MEMBERSHIP, not to the pod: your slot-1 pod is somebody else's
-- slot-0 pod. There is deliberately no slot column on pods.
ALTER TABLE pod_members ADD COLUMN slot smallint NOT NULL DEFAULT 0 CHECK (slot BETWEEN 0 AND 4);

-- The one-active-pod invariant narrows; it does not disappear.
--
-- Dropping it outright would quietly delete the ADR-013 guarantee: this index is what makes
-- two concurrent matching windows lose the race cleanly instead of producing someone in two
-- pods, and CreatePod cites it by name. The replacement keeps the same failure mode at a
-- finer grain.
--
-- Note what it does NOT do: it caps pods per lane at one, and says nothing about how many
-- lanes anyone may have. A constraint of the shape "as many memberships as you have grant
-- rows" would make the free pod conditional on a payment record, which is the one thing
-- §2 rule 6 forbids.
DROP INDEX pod_members_one_active_per_user;
CREATE UNIQUE INDEX pod_members_one_active_per_slot
    ON pod_members (user_id, slot)
    WHERE left_at IS NULL;

-- Matching draws candidates per lane, so the pool index carries the slot too.
DROP INDEX IF EXISTS intents_category_intensity_idx;
CREATE INDEX intents_category_intensity_idx ON intents (goal_category, intensity, slot);
