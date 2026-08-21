-- The cycle's arithmetic must be anchored to when the CYCLE started, not to when the pod
-- was created.
--
-- Everything about a cycle — where it ends, how many of its weeks have closed, how many
-- of them the pod completed — was computed from pods.created_at. That is right for the
-- first cycle and wrong for every one after it: a pod that carried on was already past
-- its finish line, so the next tick graduated it again. Its second cycle lasted an hour.
ALTER TABLE pods ADD COLUMN cycle_started_at timestamptz;

-- Existing pods are in their first cycle by construction: nothing has ever started a
-- second one that survived a tick.
UPDATE pods SET cycle_started_at = created_at WHERE cycle_started_at IS NULL;

ALTER TABLE pods ALTER COLUMN cycle_started_at SET NOT NULL;
