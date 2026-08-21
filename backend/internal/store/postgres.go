package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the production Store. Postgres is the source of truth (CLAUDE.md §5).
type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func (p *Postgres) CreateAuthChallenge(ctx context.Context, c AuthChallenge) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO auth_challenges (id, email, code_hash, created_at, expires_at, attempts)
		VALUES ($1, $2, $3, $4, $5, 0)`,
		c.ID, c.Email, c.CodeHash, c.CreatedAt, c.ExpiresAt)
	return wrap("create auth challenge", err)
}

func (p *Postgres) GetAuthChallenge(ctx context.Context, id string) (AuthChallenge, error) {
	var c AuthChallenge
	err := p.pool.QueryRow(ctx, `
		SELECT id, email, code_hash, created_at, expires_at, attempts, consumed_at
		FROM auth_challenges WHERE id = $1`, id).
		Scan(&c.ID, &c.Email, &c.CodeHash, &c.CreatedAt, &c.ExpiresAt, &c.Attempts, &c.ConsumedAt)
	if err != nil {
		return AuthChallenge{}, wrap("get auth challenge", err)
	}
	return c, nil
}

func (p *Postgres) RecordChallengeAttempt(ctx context.Context, id string) (int, error) {
	var attempts int
	err := p.pool.QueryRow(ctx, `
		UPDATE auth_challenges SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts`, id).
		Scan(&attempts)
	if err != nil {
		return 0, wrap("record challenge attempt", err)
	}
	return attempts, nil
}

func (p *Postgres) ConsumeAuthChallenge(ctx context.Context, id string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE auth_challenges SET consumed_at = $2 WHERE id = $1 AND consumed_at IS NULL`, id, at)
	if err != nil {
		return wrap("consume auth challenge", err)
	}
	if tag.RowsAffected() == 0 {
		// Either it never existed or another verify got there first. From the caller's
		// point of view both mean the same thing: this code is spent.
		return ErrConflict
	}
	return nil
}

func (p *Postgres) CountRecentChallenges(ctx context.Context, email string, since time.Time) (int, error) {
	var count int
	err := p.pool.QueryRow(ctx, `
		SELECT count(*) FROM auth_challenges WHERE email = $1 AND created_at > $2`,
		NormaliseEmail(email), since).Scan(&count)
	return count, wrap("count recent challenges", err)
}

func (p *Postgres) UpsertUserByEmail(ctx context.Context, email string, now time.Time) (User, error) {
	id, err := newUUID()
	if err != nil {
		return User{}, err
	}
	var u User
	// DO UPDATE rather than DO NOTHING so that RETURNING yields the existing row on a
	// repeat sign-in; the assignment is a no-op by design.
	err = p.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id, email, display_name, created_at, onboarding_complete, dissolved_pods, recovery_offered_at`,
		id, NormaliseEmail(email), now).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &u.OnboardingComplete, &u.DissolvedPods, &u.RecoveryOfferedAt)
	if err != nil {
		return User{}, wrap("upsert user", err)
	}
	return u, nil
}

func (p *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	err := p.pool.QueryRow(ctx, `
		SELECT id, email, display_name, created_at, onboarding_complete, dissolved_pods, recovery_offered_at
		FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &u.OnboardingComplete, &u.DissolvedPods, &u.RecoveryOfferedAt)
	if err != nil {
		return User{}, wrap("get user", err)
	}
	return u, nil
}

func (p *Postgres) CreateRefreshToken(ctx context.Context, t RefreshToken) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, family_id, user_id, device_name, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.TokenHash, t.FamilyID, t.UserID, t.DeviceName, t.CreatedAt, t.ExpiresAt)
	return wrap("create refresh token", err)
}

func (p *Postgres) GetRefreshToken(ctx context.Context, hash string) (RefreshToken, error) {
	var t RefreshToken
	err := p.pool.QueryRow(ctx, `
		SELECT token_hash, family_id, user_id, device_name, created_at, expires_at, rotated_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, hash).
		Scan(&t.TokenHash, &t.FamilyID, &t.UserID, &t.DeviceName, &t.CreatedAt, &t.ExpiresAt, &t.RotatedAt, &t.RevokedAt)
	if err != nil {
		return RefreshToken{}, wrap("get refresh token", err)
	}
	return t, nil
}

// RotateRefreshToken marks the presented token rotated and inserts its successor in one
// transaction. The conditional UPDATE is what makes rotation single-use even under two
// simultaneous refreshes from the same device.
func (p *Postgres) RotateRefreshToken(ctx context.Context, oldHash string, next RefreshToken, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("begin rotation", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE refresh_tokens SET rotated_at = $2
		WHERE token_hash = $1 AND rotated_at IS NULL AND revoked_at IS NULL`, oldHash, at)
	if err != nil {
		return wrap("rotate refresh token", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM refresh_tokens WHERE token_hash = $1`, oldHash).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return wrap("check refresh token", err)
		}
		return ErrConflict
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, family_id, user_id, device_name, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		next.TokenHash, next.FamilyID, next.UserID, next.DeviceName, next.CreatedAt, next.ExpiresAt); err != nil {
		return wrap("insert rotated token", err)
	}
	return wrap("commit rotation", tx.Commit(ctx))
}

func (p *Postgres) RevokeFamily(ctx context.Context, familyID string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`, familyID, at)
	return wrap("revoke family", err)
}

func wrap(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return ErrConflict
	}
	return fmt.Errorf("store: %s: %w", what, err)
}

// ---------------------------------------------------------------- M2: onboarding

func (p *Postgres) SetDisplayName(ctx context.Context, userID, displayName string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE users SET display_name = $2 WHERE id = $1`, userID, displayName)
	if err != nil {
		return wrap("set display name", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) UpsertIntent(ctx context.Context, intent Intent) error {
	windows, err := json.Marshal(intent.AvailabilityWindows)
	if err != nil {
		return fmt.Errorf("store: encode availability: %w", err)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("begin intent upsert", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO intents (user_id, goal_text, goal_category, intensity, timezone,
		                     self_report_reliability, availability_windows, embedding, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE SET
			goal_text = EXCLUDED.goal_text,
			goal_category = EXCLUDED.goal_category,
			intensity = EXCLUDED.intensity,
			timezone = EXCLUDED.timezone,
			self_report_reliability = EXCLUDED.self_report_reliability,
			availability_windows = EXCLUDED.availability_windows,
			embedding = EXCLUDED.embedding,
			updated_at = EXCLUDED.updated_at`,
		intent.UserID, intent.GoalText, intent.GoalCategory, intent.Intensity, intent.Timezone,
		intent.SelfReport, windows, intent.Embedding, intent.UpdatedAt); err != nil {
		return wrap("upsert intent", err)
	}

	// Finishing the intent is finishing onboarding. Waiting for a pod is not onboarding.
	if _, err := tx.Exec(ctx, `UPDATE users SET onboarding_complete = true WHERE id = $1`, intent.UserID); err != nil {
		return wrap("mark onboarding complete", err)
	}
	return wrap("commit intent upsert", tx.Commit(ctx))
}

func (p *Postgres) GetIntent(ctx context.Context, userID string) (Intent, error) {
	var intent Intent
	var windows []byte
	err := p.pool.QueryRow(ctx, `
		SELECT user_id, goal_text, goal_category, intensity, timezone,
		       self_report_reliability, availability_windows, embedding, updated_at
		FROM intents WHERE user_id = $1`, userID).
		Scan(&intent.UserID, &intent.GoalText, &intent.GoalCategory, &intent.Intensity, &intent.Timezone,
			&intent.SelfReport, &windows, &intent.Embedding, &intent.UpdatedAt)
	if err != nil {
		return Intent{}, wrap("get intent", err)
	}
	if err := json.Unmarshal(windows, &intent.AvailabilityWindows); err != nil {
		return Intent{}, fmt.Errorf("store: decode availability: %w", err)
	}
	return intent, nil
}

func (p *Postgres) JoinPool(ctx context.Context, userID string, at time.Time) error {
	var hasIntent bool
	if err := p.pool.QueryRow(ctx, `SELECT true FROM intents WHERE user_id = $1`, userID).Scan(&hasIntent); err != nil {
		return wrap("check intent", err)
	}

	var inPod bool
	if err := p.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pod_members WHERE user_id = $1 AND left_at IS NULL)`, userID).Scan(&inPod); err != nil {
		return wrap("check active pod", err)
	}
	if inPod {
		return ErrConflict
	}

	tag, err := p.pool.Exec(ctx, `
		INSERT INTO pool_tickets (user_id, queued_at) VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING`, userID, at)
	if err != nil {
		return wrap("join pool", err)
	}
	if tag.RowsAffected() == 0 {
		// Already queued. Reporting a conflict rather than silently resetting the wait
		// keeps the queue order honest — re-joining must not jump anyone.
		return ErrConflict
	}
	return nil
}

func (p *Postgres) LeavePool(ctx context.Context, userID string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM pool_tickets WHERE user_id = $1`, userID)
	return wrap("leave pool", err)
}

func (p *Postgres) GetPoolTicket(ctx context.Context, userID string) (PoolTicket, error) {
	var ticket PoolTicket
	err := p.pool.QueryRow(ctx, `
		SELECT user_id, queued_at FROM pool_tickets WHERE user_id = $1`, userID).
		Scan(&ticket.UserID, &ticket.QueuedAt)
	if err != nil {
		return PoolTicket{}, wrap("get pool ticket", err)
	}
	return ticket, nil
}

func (p *Postgres) ListPoolCandidates(ctx context.Context, limit int) ([]PoolCandidate, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT t.user_id, i.goal_category, i.intensity, i.timezone, i.self_report_reliability,
		       i.availability_windows, i.embedding, u.created_at, t.queued_at
		FROM pool_tickets t
		JOIN intents i ON i.user_id = t.user_id
		JOIN users u ON u.id = t.user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM pod_members m WHERE m.user_id = t.user_id AND m.left_at IS NULL
		)
		ORDER BY t.queued_at, t.user_id
		LIMIT $1`, poolLimit(limit))
	if err != nil {
		return nil, wrap("list pool candidates", err)
	}
	defer rows.Close()

	var out []PoolCandidate
	for rows.Next() {
		var c PoolCandidate
		var windows []byte
		var createdAt time.Time
		if err := rows.Scan(&c.UserID, &c.GoalCategory, &c.Intensity, &c.Timezone, &c.SelfReport,
			&windows, &c.Embedding, &createdAt, &c.QueuedAt); err != nil {
			return nil, wrap("scan pool candidate", err)
		}
		if err := json.Unmarshal(windows, &c.Availability); err != nil {
			return nil, fmt.Errorf("store: decode availability for %s: %w", c.UserID, err)
		}
		c.TenureWeeks = tenureWeeks(createdAt, c.QueuedAt)
		out = append(out, c)
	}
	return out, wrap("list pool candidates", rows.Err())
}

// defaultPoolLimit is a safety valve, not a page size — see the Store interface comment.
const defaultPoolLimit = 10000

func poolLimit(limit int) int {
	if limit <= 0 || limit > defaultPoolLimit {
		return defaultPoolLimit
	}
	return limit
}

func (p *Postgres) CreatePod(ctx context.Context, pod Pod, memberIDs []string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("begin pod creation", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO pods (id, state, goal_category, timezone, streak_weeks, created_at)
		VALUES ($1, $2, $3, $4, 0, $5)`,
		pod.ID, string(pod.State), pod.GoalCategory, pod.Timezone, pod.CreatedAt); err != nil {
		return wrap("insert pod", err)
	}

	for _, userID := range memberIDs {
		// The partial unique index on pod_members enforces one active pod per person, so
		// a race with another matching window loses here rather than producing someone
		// in two pods.
		if _, err := tx.Exec(ctx, `
			INSERT INTO pod_members (pod_id, user_id, joined_at) VALUES ($1, $2, $3)`,
			pod.ID, userID, at); err != nil {
			return wrap("insert pod member", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM pool_tickets WHERE user_id = $1`, userID); err != nil {
			return wrap("dequeue pod member", err)
		}
	}
	return wrap("commit pod creation", tx.Commit(ctx))
}

func (p *Postgres) GetPodForMember(ctx context.Context, podID, userID string) (Pod, error) {
	return p.loadPod(ctx, `
		SELECT p.id, p.state, p.goal_category, p.timezone, p.streak_weeks, p.created_at, p.dissolved_at
		FROM pods p
		JOIN pod_members m ON m.pod_id = p.id AND m.user_id = $2 AND m.left_at IS NULL
		WHERE p.id = $1`, podID, userID)
}

func (p *Postgres) GetActivePod(ctx context.Context, userID string) (Pod, error) {
	return p.loadPod(ctx, `
		SELECT p.id, p.state, p.goal_category, p.timezone, p.streak_weeks, p.created_at, p.dissolved_at
		FROM pods p
		JOIN pod_members m ON m.pod_id = p.id
		WHERE m.user_id = $1 AND m.left_at IS NULL AND p.state <> 'dissolved'`, userID)
}

func (p *Postgres) loadPod(ctx context.Context, query string, args ...any) (Pod, error) {
	var pod Pod
	var state string
	err := p.pool.QueryRow(ctx, query, args...).
		Scan(&pod.ID, &state, &pod.GoalCategory, &pod.Timezone, &pod.StreakWeeks, &pod.CreatedAt, &pod.DissolvedAt)
	if err != nil {
		return Pod{}, wrap("load pod", err)
	}
	pod.State = PodState(state)

	rows, err := p.pool.Query(ctx, `
		SELECT m.user_id, u.display_name, m.joined_at
		FROM pod_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.pod_id = $1 AND m.left_at IS NULL
		ORDER BY m.joined_at, m.user_id`, pod.ID)
	if err != nil {
		return Pod{}, wrap("load pod members", err)
	}
	defer rows.Close()
	for rows.Next() {
		var member PodMember
		if err := rows.Scan(&member.UserID, &member.DisplayName, &member.JoinedAt); err != nil {
			return Pod{}, wrap("scan pod member", err)
		}
		pod.Members = append(pod.Members, member)
	}
	return pod, wrap("load pod members", rows.Err())
}

// -------------------------------------------------------------- M3: the week loop

func (p *Postgres) ListActivePods(ctx context.Context) ([]Pod, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, state, goal_category, timezone, streak_weeks, created_at, dissolved_at
		FROM pods WHERE state <> 'dissolved' ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list active pods", err)
	}
	defer rows.Close()

	var pods []Pod
	for rows.Next() {
		var pod Pod
		var state string
		if err := rows.Scan(&pod.ID, &state, &pod.GoalCategory, &pod.Timezone, &pod.StreakWeeks, &pod.CreatedAt, &pod.DissolvedAt); err != nil {
			return nil, wrap("scan pod", err)
		}
		pod.State = PodState(state)
		pods = append(pods, pod)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list active pods", err)
	}

	for i := range pods {
		members, err := p.podMembers(ctx, pods[i].ID)
		if err != nil {
			return nil, err
		}
		pods[i].Members = members
	}
	return pods, nil
}

func (p *Postgres) podMembers(ctx context.Context, podID string) ([]PodMember, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT m.user_id, u.display_name, m.joined_at
		FROM pod_members m JOIN users u ON u.id = m.user_id
		WHERE m.pod_id = $1 AND m.left_at IS NULL
		ORDER BY m.joined_at, m.user_id`, podID)
	if err != nil {
		return nil, wrap("load pod members", err)
	}
	defer rows.Close()
	var members []PodMember
	for rows.Next() {
		var member PodMember
		if err := rows.Scan(&member.UserID, &member.DisplayName, &member.JoinedAt); err != nil {
			return nil, wrap("scan pod member", err)
		}
		members = append(members, member)
	}
	return members, wrap("load pod members", rows.Err())
}

func (p *Postgres) EnsureWeek(ctx context.Context, week Week, memberIDs []string) (Week, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Week{}, wrap("begin ensure week", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existingID string
	err = tx.QueryRow(ctx, `SELECT id FROM weeks WHERE pod_id = $1 AND opens_at = $2`, week.PodID, week.OpensAt).Scan(&existingID)
	switch {
	case err == nil:
		// Already opened, by an earlier run of the job or a concurrent one.
		_ = tx.Rollback(ctx)
		return p.GetWeek(ctx, existingID)
	case !errors.Is(err, pgx.ErrNoRows):
		return Week{}, wrap("check for existing week", err)
	}

	var index int
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(max(index), 0) + 1 FROM weeks WHERE pod_id = $1`, week.PodID).Scan(&index); err != nil {
		return Week{}, wrap("next week index", err)
	}
	week.Index = index

	if _, err := tx.Exec(ctx, `
		INSERT INTO weeks (id, pod_id, index, action_text, action_is_default, opens_at, closes_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		week.ID, week.PodID, week.Index, week.ActionText, week.ActionIsDefault, week.OpensAt, week.ClosesAt); err != nil {
		return Week{}, wrap("insert week", err)
	}
	for _, userID := range memberIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO member_weeks (week_id, user_id, state) VALUES ($1, $2, 'open')`,
			week.ID, userID); err != nil {
			return Week{}, wrap("insert member week", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Week{}, wrap("commit ensure week", err)
	}
	return p.GetWeek(ctx, week.ID)
}

// GetWeek loads a week with no visibility check. Callers that serve a request must use
// GetWeekForMember instead.
func (p *Postgres) GetWeek(ctx context.Context, weekID string) (Week, error) {
	week, err := p.scanWeek(ctx, `
		SELECT id, pod_id, index, action_text, action_is_default, opens_at, closes_at, pod_completed, closed_at
		FROM weeks WHERE id = $1`, weekID)
	if err != nil {
		return Week{}, err
	}
	return p.withMembers(ctx, week)
}

func (p *Postgres) GetWeekForMember(ctx context.Context, weekID, userID string) (Week, error) {
	week, err := p.scanWeek(ctx, `
		SELECT w.id, w.pod_id, w.index, w.action_text, w.action_is_default, w.opens_at, w.closes_at, w.pod_completed, w.closed_at
		FROM weeks w
		JOIN pod_members m ON m.pod_id = w.pod_id AND m.user_id = $2 AND m.left_at IS NULL
		WHERE w.id = $1`, weekID, userID)
	if err != nil {
		return Week{}, err
	}
	return p.withMembers(ctx, week)
}

func (p *Postgres) GetCurrentWeek(ctx context.Context, podID, userID string, at time.Time) (Week, error) {
	week, err := p.scanWeek(ctx, `
		SELECT w.id, w.pod_id, w.index, w.action_text, w.action_is_default, w.opens_at, w.closes_at, w.pod_completed, w.closed_at
		FROM weeks w
		JOIN pod_members m ON m.pod_id = w.pod_id AND m.user_id = $2 AND m.left_at IS NULL
		WHERE w.pod_id = $1 AND w.opens_at <= $3 AND w.closes_at > $3`, podID, userID, at)
	if err != nil {
		return Week{}, err
	}
	return p.withMembers(ctx, week)
}

func (p *Postgres) ListWeeksForMember(ctx context.Context, podID, userID string, limit int) ([]Week, error) {
	if limit <= 0 || limit > 200 {
		limit = 52
	}
	rows, err := p.pool.Query(ctx, `
		SELECT w.id, w.pod_id, w.index, w.action_text, w.action_is_default, w.opens_at, w.closes_at, w.pod_completed, w.closed_at
		FROM weeks w
		JOIN pod_members m ON m.pod_id = w.pod_id AND m.user_id = $2 AND m.left_at IS NULL
		WHERE w.pod_id = $1
		ORDER BY w.index DESC
		LIMIT $3`, podID, userID, limit)
	if err != nil {
		return nil, wrap("list weeks", err)
	}
	defer rows.Close()

	var weeks []Week
	for rows.Next() {
		week, err := scanWeekRow(rows)
		if err != nil {
			return nil, err
		}
		weeks = append(weeks, week)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list weeks", err)
	}
	for i := range weeks {
		hydrated, err := p.withMembers(ctx, weeks[i])
		if err != nil {
			return nil, err
		}
		weeks[i] = hydrated
	}
	return weeks, nil
}

func (p *Postgres) ListWeeksForMemberless(ctx context.Context, podID string, limit int) ([]Week, error) {
	if limit <= 0 || limit > 200 {
		limit = 52
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, pod_id, index, action_text, action_is_default, opens_at, closes_at, pod_completed, closed_at
		FROM weeks WHERE pod_id = $1 ORDER BY index DESC LIMIT $2`, podID, limit)
	if err != nil {
		return nil, wrap("list weeks", err)
	}
	defer rows.Close()

	var weeks []Week
	for rows.Next() {
		week, err := scanWeekRow(rows)
		if err != nil {
			return nil, err
		}
		weeks = append(weeks, week)
	}
	return weeks, wrap("list weeks", rows.Err())
}

func (p *Postgres) SetWeekAction(ctx context.Context, weekID, actionText string) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE weeks SET action_text = $2, action_is_default = false
		WHERE id = $1
		  AND closed_at IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM member_weeks WHERE week_id = $1 AND state = 'posted'
		  )`, weekID, actionText)
	if err != nil {
		return wrap("set week action", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := p.pool.QueryRow(ctx, `SELECT true FROM weeks WHERE id = $1`, weekID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return wrap("check week", err)
		}
		// The week exists but is closed, or somebody has already acted on what it said.
		return ErrConflict
	}
	return nil
}

func (p *Postgres) CloseWeek(ctx context.Context, weekID string, at time.Time) (Week, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Week{}, wrap("begin close week", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var alreadyClosed *time.Time
	if err := tx.QueryRow(ctx, `SELECT closed_at FROM weeks WHERE id = $1 FOR UPDATE`, weekID).Scan(&alreadyClosed); err != nil {
		return Week{}, wrap("lock week", err)
	}
	if alreadyClosed != nil {
		_ = tx.Rollback(ctx)
		return p.GetWeek(ctx, weekID)
	}

	// Everyone still open at the boundary is resting. Not missed, not failed — the week
	// simply closed without them (ADR-006).
	if _, err := tx.Exec(ctx, `
		UPDATE member_weeks SET state = 'resting' WHERE week_id = $1 AND state = 'open'`, weekID); err != nil {
		return Week{}, wrap("settle member weeks", err)
	}

	var total, posted int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE state = 'posted')
		FROM member_weeks WHERE week_id = $1`, weekID).Scan(&total, &posted); err != nil {
		return Week{}, wrap("count member weeks", err)
	}
	completed := total > 0 && posted == total

	if _, err := tx.Exec(ctx, `
		UPDATE weeks SET pod_completed = $2, closed_at = $3 WHERE id = $1`, weekID, completed, at); err != nil {
		return Week{}, wrap("close week", err)
	}

	// The streak is the pod's. A week that did not complete resets it and nothing else:
	// no score moves here, and no member is named.
	if completed {
		if _, err := tx.Exec(ctx, `
			UPDATE pods SET streak_weeks = streak_weeks + 1
			WHERE id = (SELECT pod_id FROM weeks WHERE id = $1)`, weekID); err != nil {
			return Week{}, wrap("advance streak", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE pods SET streak_weeks = 0
			WHERE id = (SELECT pod_id FROM weeks WHERE id = $1)`, weekID); err != nil {
			return Week{}, wrap("reset streak", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Week{}, wrap("commit close week", err)
	}
	return p.GetWeek(ctx, weekID)
}

func (p *Postgres) CreateProof(ctx context.Context, proof Proof) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO proofs (id, week_id, user_id, kind, content_type, byte_size, duration_ms, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		proof.ID, proof.WeekID, proof.UserID, proof.Kind, proof.ContentType,
		proof.ByteSize, proof.DurationMs, proof.CreatedAt, proof.ExpiresAt)
	return wrap("create proof", err)
}

func (p *Postgres) GetProofForViewer(ctx context.Context, proofID, viewerID string) (Proof, error) {
	var proof Proof
	err := p.pool.QueryRow(ctx, `
		SELECT p.id, p.week_id, p.user_id, p.kind, p.content_type, p.byte_size, p.duration_ms,
		       p.blob_key, p.created_at, p.committed_at, p.expires_at, p.deleted_at
		FROM proofs p
		JOIN weeks w ON w.id = p.week_id
		JOIN pod_members m ON m.pod_id = w.pod_id AND m.user_id = $2 AND m.left_at IS NULL
		WHERE p.id = $1 AND p.deleted_at IS NULL`, proofID, viewerID).
		Scan(&proof.ID, &proof.WeekID, &proof.UserID, &proof.Kind, &proof.ContentType, &proof.ByteSize,
			&proof.DurationMs, &proof.BlobKey, &proof.CreatedAt, &proof.CommittedAt, &proof.ExpiresAt, &proof.DeletedAt)
	if err != nil {
		return Proof{}, wrap("get proof", err)
	}
	return proof, nil
}

func (p *Postgres) CommitProof(ctx context.Context, proofID, userID, blobKey string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("begin commit proof", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var weekID string
	err = tx.QueryRow(ctx, `
		SELECT week_id FROM proofs
		WHERE id = $1 AND user_id = $2 AND committed_at IS NULL AND deleted_at IS NULL
		FOR UPDATE`, proofID, userID).Scan(&weekID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return wrap("lock proof", err)
	}

	// The partial unique index makes a second committed proof for the same person and
	// week impossible; this turns that into a clean conflict.
	if _, err := tx.Exec(ctx, `
		UPDATE proofs SET committed_at = $2, blob_key = $3 WHERE id = $1`, proofID, at, blobKey); err != nil {
		if errors.Is(wrap("", err), ErrConflict) {
			return ErrConflict
		}
		return wrap("commit proof", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE member_weeks SET state = 'posted', posted_at = $3
		WHERE week_id = $1 AND user_id = $2`, weekID, userID, at); err != nil {
		return wrap("mark member posted", err)
	}
	return wrap("commit proof transaction", tx.Commit(ctx))
}

func (p *Postgres) ListExpiredProofs(ctx context.Context, at time.Time, limit int) ([]Proof, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, week_id, user_id, kind, content_type, byte_size, duration_ms,
		       blob_key, created_at, committed_at, expires_at, deleted_at
		FROM proofs
		WHERE deleted_at IS NULL AND expires_at <= $1
		ORDER BY expires_at
		LIMIT $2`, at, limit)
	if err != nil {
		return nil, wrap("list expired proofs", err)
	}
	defer rows.Close()

	var proofs []Proof
	for rows.Next() {
		var proof Proof
		if err := rows.Scan(&proof.ID, &proof.WeekID, &proof.UserID, &proof.Kind, &proof.ContentType,
			&proof.ByteSize, &proof.DurationMs, &proof.BlobKey, &proof.CreatedAt, &proof.CommittedAt,
			&proof.ExpiresAt, &proof.DeletedAt); err != nil {
			return nil, wrap("scan expired proof", err)
		}
		proofs = append(proofs, proof)
	}
	return proofs, wrap("list expired proofs", rows.Err())
}

func (p *Postgres) MarkProofDeleted(ctx context.Context, proofID string, at time.Time) error {
	// blob_key is cleared with the deletion: nothing should be left holding a pointer to
	// bytes that are gone (CLAUDE.md §2 rule 4).
	_, err := p.pool.Exec(ctx, `
		UPDATE proofs SET deleted_at = $2, blob_key = NULL WHERE id = $1 AND deleted_at IS NULL`, proofID, at)
	return wrap("mark proof deleted", err)
}

func (p *Postgres) scanWeek(ctx context.Context, query string, args ...any) (Week, error) {
	var week Week
	err := p.pool.QueryRow(ctx, query, args...).
		Scan(&week.ID, &week.PodID, &week.Index, &week.ActionText, &week.ActionIsDefault,
			&week.OpensAt, &week.ClosesAt, &week.PodCompleted, &week.ClosedAt)
	if err != nil {
		return Week{}, wrap("load week", err)
	}
	return week, nil
}

func scanWeekRow(rows pgx.Rows) (Week, error) {
	var week Week
	if err := rows.Scan(&week.ID, &week.PodID, &week.Index, &week.ActionText, &week.ActionIsDefault,
		&week.OpensAt, &week.ClosesAt, &week.PodCompleted, &week.ClosedAt); err != nil {
		return Week{}, wrap("scan week", err)
	}
	return week, nil
}

func (p *Postgres) withMembers(ctx context.Context, week Week) (Week, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT mw.user_id, mw.state, mw.posted_at,
		       (SELECT pr.id FROM proofs pr
		         WHERE pr.week_id = mw.week_id AND pr.user_id = mw.user_id
		           AND pr.committed_at IS NOT NULL AND pr.deleted_at IS NULL
		         LIMIT 1)
		FROM member_weeks mw
		WHERE mw.week_id = $1
		ORDER BY mw.user_id`, week.ID)
	if err != nil {
		return Week{}, wrap("load member weeks", err)
	}
	defer rows.Close()
	for rows.Next() {
		var member MemberWeek
		var state string
		if err := rows.Scan(&member.UserID, &state, &member.PostedAt, &member.ProofID); err != nil {
			return Week{}, wrap("scan member week", err)
		}
		member.State = MemberWeekState(state)
		week.Members = append(week.Members, member)
	}
	return week, wrap("load member weeks", rows.Err())
}

// ------------------------------------------------------------ M4: notifications

func (p *Postgres) RegisterDevice(ctx context.Context, device Device) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO devices (token_hash, user_id, platform, locale, registered_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (token_hash) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			platform = EXCLUDED.platform,
			locale = EXCLUDED.locale,
			last_seen_at = EXCLUDED.last_seen_at,
			retired_at = NULL`,
		device.TokenHash, device.UserID, device.Platform, device.Locale, device.RegisteredAt, device.LastSeenAt)
	return wrap("register device", err)
}

func (p *Postgres) RetireDevice(ctx context.Context, tokenHash string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE devices SET retired_at = $2 WHERE token_hash = $1 AND retired_at IS NULL`, tokenHash, at)
	return wrap("retire device", err)
}

func (p *Postgres) DevicesFor(ctx context.Context, userID string) ([]Device, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT token_hash, user_id, platform, locale, registered_at, last_seen_at
		FROM devices WHERE user_id = $1 AND retired_at IS NULL ORDER BY token_hash`, userID)
	if err != nil {
		return nil, wrap("list devices", err)
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		var device Device
		if err := rows.Scan(&device.TokenHash, &device.UserID, &device.Platform, &device.Locale,
			&device.RegisteredAt, &device.LastSeenAt); err != nil {
			return nil, wrap("scan device", err)
		}
		devices = append(devices, device)
	}
	return devices, wrap("list devices", rows.Err())
}

func (p *Postgres) QueueNotification(ctx context.Context, notification Notification) error {
	params, err := json.Marshal(notification.Params)
	if err != nil {
		return fmt.Errorf("store: encode notification params: %w", err)
	}
	tag, err := p.pool.Exec(ctx, `
		INSERT INTO notifications (id, user_id, kind, subject_id, copy_key, params, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (kind, subject_id, user_id) DO NOTHING`,
		notification.ID, notification.UserID, notification.Kind, notification.SubjectID,
		notification.CopyKey, params, notification.CreatedAt)
	if err != nil {
		return wrap("queue notification", err)
	}
	if tag.RowsAffected() == 0 {
		// Already queued. This is what stops an hourly job sending the same "new week"
		// push every hour for a week.
		return ErrConflict
	}
	return nil
}

func (p *Postgres) PendingNotifications(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, user_id, kind, subject_id, copy_key, params, created_at
		FROM notifications
		WHERE sent_at IS NULL AND failed_at IS NULL
		ORDER BY created_at, id
		LIMIT $1`, limit)
	if err != nil {
		return nil, wrap("list pending notifications", err)
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var notification Notification
		var params []byte
		if err := rows.Scan(&notification.ID, &notification.UserID, &notification.Kind,
			&notification.SubjectID, &notification.CopyKey, &params, &notification.CreatedAt); err != nil {
			return nil, wrap("scan notification", err)
		}
		if err := json.Unmarshal(params, &notification.Params); err != nil {
			return nil, fmt.Errorf("store: decode notification params: %w", err)
		}
		out = append(out, notification)
	}
	return out, wrap("list pending notifications", rows.Err())
}

func (p *Postgres) MarkNotificationSent(ctx context.Context, id string, at time.Time, failed bool) error {
	column := "sent_at"
	if failed {
		column = "failed_at"
	}
	_, err := p.pool.Exec(ctx,
		fmt.Sprintf(`UPDATE notifications SET %s = $2 WHERE id = $1`, column), id, at)
	return wrap("mark notification sent", err)
}

// ---------------------------------------------------------------- M5: recovery

func (p *Postgres) RecentOutcomes(ctx context.Context, userID string, limit int) ([]WeekOutcome, error) {
	if limit <= 0 || limit > 52 {
		limit = 8
	}
	rows, err := p.pool.Query(ctx, `
		SELECT w.id, mw.state = 'posted', w.pod_completed, w.closes_at
		FROM member_weeks mw
		JOIN weeks w ON w.id = mw.week_id
		WHERE mw.user_id = $1 AND w.closed_at IS NOT NULL
		ORDER BY w.closes_at DESC, w.id DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, wrap("recent outcomes", err)
	}
	defer rows.Close()

	var out []WeekOutcome
	for rows.Next() {
		var outcome WeekOutcome
		if err := rows.Scan(&outcome.WeekID, &outcome.Posted, &outcome.PodCompleted, &outcome.ClosesAt); err != nil {
			return nil, wrap("scan outcome", err)
		}
		out = append(out, outcome)
	}
	return out, wrap("recent outcomes", rows.Err())
}

func (p *Postgres) DissolvePod(ctx context.Context, podID, reason string, inactiveMemberIDs []string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap("begin dissolve", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM pods WHERE id = $1 FOR UPDATE`, podID).Scan(&state); err != nil {
		return wrap("lock pod", err)
	}
	if state == string(PodStateDissolved) {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE pods SET state = 'dissolved', dissolved_at = $2, dissolved_reason = $3 WHERE id = $1`,
		podID, at, reason); err != nil {
		return wrap("dissolve pod", err)
	}

	// Membership ends with the pod, which is also what frees everyone from the partial
	// unique index so they can be matched again.
	if _, err := tx.Exec(ctx, `
		UPDATE pod_members SET left_at = $2 WHERE pod_id = $1 AND left_at IS NULL`, podID, at); err != nil {
		return wrap("end memberships", err)
	}

	// Everyone gets the offer to lower the bar; only inactive members carry the count
	// (CLAUDE.md §6).
	if _, err := tx.Exec(ctx, `
		UPDATE users SET recovery_offered_at = $2
		WHERE id IN (SELECT user_id FROM pod_members WHERE pod_id = $1)`, podID, at); err != nil {
		return wrap("offer recovery", err)
	}
	if len(inactiveMemberIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET dissolved_pods = dissolved_pods + 1 WHERE id = ANY($1)`,
			inactiveMemberIDs); err != nil {
			return wrap("count dissolved pod", err)
		}
	}
	return wrap("commit dissolve", tx.Commit(ctx))
}

func (p *Postgres) RecoveryFor(ctx context.Context, userID string) (Recovery, error) {
	var dissolved int
	var offeredAt *time.Time
	err := p.pool.QueryRow(ctx, `
		SELECT dissolved_pods, recovery_offered_at FROM users WHERE id = $1`, userID).
		Scan(&dissolved, &offeredAt)
	if err != nil {
		return Recovery{}, wrap("recovery for", err)
	}
	return Recovery{
		OfferLowerIntensity: offeredAt != nil,
		OfferAlternatePath:  dissolved >= AlternatePathAfter,
	}, nil
}

func (p *Postgres) ClearRecoveryOffer(ctx context.Context, userID string) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET recovery_offered_at = NULL WHERE id = $1`, userID)
	return wrap("clear recovery offer", err)
}
