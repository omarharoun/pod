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
		RETURNING id, email, display_name, created_at, onboarding_complete`,
		id, NormaliseEmail(email), now).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &u.OnboardingComplete)
	if err != nil {
		return User{}, wrap("upsert user", err)
	}
	return u, nil
}

func (p *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	err := p.pool.QueryRow(ctx, `
		SELECT id, email, display_name, created_at, onboarding_complete FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &u.OnboardingComplete)
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
