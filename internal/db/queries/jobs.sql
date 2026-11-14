-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload, run_after, created_at, updated_at)
VALUES (@kind, @payload, @run_after, clock_timestamp(), clock_timestamp())
RETURNING id;

-- ClaimJob takes the oldest runnable job and marks it running in one
-- statement. SKIP LOCKED lets a second claimer pass over a row that is being
-- claimed instead of waiting for it. The attempt is counted when the job is
-- claimed, so a crash during the run still uses one attempt.
-- name: ClaimJob :one
UPDATE jobs SET status = 'running', attempts = attempts + 1, updated_at = @now
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.status = 'queued' AND j.run_after <= @now
    ORDER BY j.created_at, j.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteJob :execrows
UPDATE jobs SET status = 'succeeded', last_error = NULL, updated_at = @now
WHERE id = @id AND status = 'running';

-- name: RetryJob :execrows
UPDATE jobs SET status = 'queued', run_after = @run_after, last_error = @last_error, updated_at = @now
WHERE id = @id AND status = 'running';

-- RescheduleJob puts a job back without counting the attempt it just used:
-- the job did not fail, it was told to wait.
-- name: RescheduleJob :execrows
UPDATE jobs SET status = 'queued', run_after = @run_after, attempts = GREATEST(attempts - 1, 0), updated_at = @now
WHERE id = @id AND status = 'running';

-- name: FailJob :execrows
UPDATE jobs SET status = 'failed', last_error = @last_error, updated_at = @now
WHERE id = @id AND status = 'running';

-- RecoverRunningJobs requeues the jobs a previous process left running. It
-- runs at startup, before the worker claims anything, and assumes a single
-- worker process (ARCHITECTURE §1).
-- name: RecoverRunningJobs :execrows
UPDATE jobs SET status = 'queued', run_after = @now, updated_at = @now
WHERE status = 'running';

-- name: GetJob :one
SELECT * FROM jobs WHERE id = @id;

-- The work queue: enqueue, claim the oldest runnable job with
-- FOR UPDATE SKIP LOCKED, and record success, retry or failure.
--
-- Tables: jobs. The table belongs to the instance, not to a user, so these
-- queries take no user_id.
--
-- Rules: every query on a user-owned table filters by user_id; query names
-- are unique across all files; run `task generate` after editing.
--
-- Add queries above this description: sqlc copies the comment that precedes
-- a file's first query into that query's Go documentation.
