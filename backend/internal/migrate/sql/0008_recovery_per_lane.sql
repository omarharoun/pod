-- M7 follow-up: the recovery offer belongs to a lane, not to a person.
--
-- ADR-018's offer says "that pod has wound down" and proposes lowering *this* lane's
-- intensity. Stored per user, a pod ending in one lane put that offer in front of a lane
-- whose pod was running perfectly well — wrong, and unexplainable, because the copy is
-- deliberately identical for everyone and cannot say which pod it means.
--
-- The dissolved-pods count stays per user on purpose. Three pods ending under someone is
-- a fact about them whichever lanes it happened in, and it is what the alternate path
-- (CLAUDE.md §6) is keyed on.
CREATE TABLE recovery_offers (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slot       smallint    NOT NULL CHECK (slot BETWEEN 0 AND 4),
    offered_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, slot)
);

-- Carry across whatever is outstanding, onto the free lane: before this migration every
-- pod was a free-lane pod, so that is where any standing offer belongs.
INSERT INTO recovery_offers (user_id, slot, offered_at)
SELECT id, 0, recovery_offered_at FROM users WHERE recovery_offered_at IS NOT NULL;

ALTER TABLE users DROP COLUMN recovery_offered_at;
