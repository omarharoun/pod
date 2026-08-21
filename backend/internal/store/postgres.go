package store

import (
	"context"
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
