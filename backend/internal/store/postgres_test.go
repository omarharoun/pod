package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/omarharoun/pod/backend/internal/ids"
	"github.com/omarharoun/pod/backend/internal/migrate"
	"github.com/omarharoun/pod/backend/internal/store"
)

// newPostgres connects to TEST_DATABASE_URL, applies migrations, and hands back a store.
// Without that variable the whole file skips — `go test ./...` must stay runnable on a
// laptop with no database.
func newPostgres(t *testing.T) *store.Postgres {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the store integration tests")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := migrate.Up(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db, err := store.NewPostgres(ctx, url)
	if err != nil {
		t.Fatalf("NewPostgres: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// uniqueEmail keeps tests independent without truncating shared tables.
func uniqueEmail(t *testing.T) string {
	t.Helper()
	suffix, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	return "test-" + suffix + "@example.com"
}

func TestPostgresPing(t *testing.T) {
	db := newPostgres(t)
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPostgresUpsertUserIsIdempotent(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	email := uniqueEmail(t)

	first, err := db.UpsertUserByEmail(ctx, email, now)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Email != email || first.OnboardingComplete {
		t.Fatalf("unexpected user: %+v", first)
	}

	second, err := db.UpsertUserByEmail(ctx, "  "+email+"  ", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("a repeat sign-in created a second account: %q then %q", first.ID, second.ID)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("created_at moved: %v then %v", first.CreatedAt, second.CreatedAt)
	}

	fetched, err := db.GetUser(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if fetched.Email != email {
		t.Errorf("email: got %q", fetched.Email)
	}

	if _, err := db.GetUser(ctx, "11111111-1111-4111-8111-111111111111"); err != store.ErrNotFound {
		t.Errorf("GetUser(missing): got %v, want ErrNotFound", err)
	}
}

func TestPostgresChallengeLifecycle(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	id, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	email := uniqueEmail(t)
	challenge := store.AuthChallenge{
		ID: id, Email: email, CodeHash: "hash", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	if err := db.CreateAuthChallenge(ctx, challenge); err != nil {
		t.Fatalf("CreateAuthChallenge: %v", err)
	}

	loaded, err := db.GetAuthChallenge(ctx, id)
	if err != nil {
		t.Fatalf("GetAuthChallenge: %v", err)
	}
	if loaded.Attempts != 0 || loaded.ConsumedAt != nil {
		t.Fatalf("unexpected fresh challenge: %+v", loaded)
	}

	for want := 1; want <= 3; want++ {
		got, err := db.RecordChallengeAttempt(ctx, id)
		if err != nil {
			t.Fatalf("RecordChallengeAttempt: %v", err)
		}
		if got != want {
			t.Fatalf("attempts: got %d, want %d", got, want)
		}
	}

	count, err := db.CountRecentChallenges(ctx, email, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("CountRecentChallenges: %v", err)
	}
	if count != 1 {
		t.Errorf("recent challenges: got %d, want 1", count)
	}

	if err := db.ConsumeAuthChallenge(ctx, id, now); err != nil {
		t.Fatalf("ConsumeAuthChallenge: %v", err)
	}
	// Single-use: the second consume must lose.
	if err := db.ConsumeAuthChallenge(ctx, id, now); err != store.ErrConflict {
		t.Fatalf("second consume: got %v, want ErrConflict", err)
	}

	if _, err := db.GetAuthChallenge(ctx, "11111111-1111-4111-8111-111111111111"); err != store.ErrNotFound {
		t.Errorf("GetAuthChallenge(missing): got %v, want ErrNotFound", err)
	}
}

func TestPostgresRefreshTokenRotationIsSingleUse(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user, err := db.UpsertUserByEmail(ctx, uniqueEmail(t), now)
	if err != nil {
		t.Fatalf("UpsertUserByEmail: %v", err)
	}
	familyID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	// Token hashes are unique per run: the tests share a database and must not depend on
	// it being empty.
	token := func(suffix string) store.RefreshToken {
		return store.RefreshToken{
			TokenHash: familyID + "-" + suffix, FamilyID: familyID, UserID: user.ID, DeviceName: "phone",
			CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
		}
	}

	first := token("hash-1")
	if err := db.CreateRefreshToken(ctx, first); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if err := db.RotateRefreshToken(ctx, first.TokenHash, token("hash-2"), now); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}
	// Rotating the same token again must lose — this is the reuse-detection path.
	if err := db.RotateRefreshToken(ctx, first.TokenHash, token("hash-3"), now); err != store.ErrConflict {
		t.Fatalf("second rotation: got %v, want ErrConflict", err)
	}
	// And the loser's successor must not have been written.
	if _, err := db.GetRefreshToken(ctx, token("hash-3").TokenHash); err != store.ErrNotFound {
		t.Fatalf("the rolled-back successor should not exist: got %v", err)
	}

	rotated, err := db.GetRefreshToken(ctx, first.TokenHash)
	if err != nil {
		t.Fatalf("GetRefreshToken: %v", err)
	}
	if rotated.RotatedAt == nil {
		t.Fatal("the rotated token was not marked")
	}
	if rotated.Usable(now) {
		t.Fatal("a rotated token must not be usable")
	}

	if err := db.RotateRefreshToken(ctx, "never-existed", token("hash-4"), now); err != store.ErrNotFound {
		t.Fatalf("rotating an unknown token: got %v, want ErrNotFound", err)
	}
}

func TestPostgresRevokeFamilyTakesEverything(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	user, err := db.UpsertUserByEmail(ctx, uniqueEmail(t), now)
	if err != nil {
		t.Fatalf("UpsertUserByEmail: %v", err)
	}
	phoneFamily, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	tabletFamily, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}

	mk := func(hash, family string) store.RefreshToken {
		return store.RefreshToken{
			TokenHash: hash, FamilyID: family, UserID: user.ID,
			CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
		}
	}
	for _, tok := range []store.RefreshToken{
		mk(user.ID+"-phone-1", phoneFamily),
		mk(user.ID+"-phone-2", phoneFamily),
		mk(user.ID+"-tablet-1", tabletFamily),
	} {
		if err := db.CreateRefreshToken(ctx, tok); err != nil {
			t.Fatalf("CreateRefreshToken: %v", err)
		}
	}

	if err := db.RevokeFamily(ctx, phoneFamily, now); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	for _, hash := range []string{user.ID + "-phone-1", user.ID + "-phone-2"} {
		tok, err := db.GetRefreshToken(ctx, hash)
		if err != nil {
			t.Fatalf("GetRefreshToken(%s): %v", hash, err)
		}
		if tok.RevokedAt == nil || tok.Usable(now) {
			t.Errorf("%s should be revoked: %+v", hash, tok)
		}
	}
	// Revoking one device must leave the others signed in.
	tablet, err := db.GetRefreshToken(ctx, user.ID+"-tablet-1")
	if err != nil {
		t.Fatalf("GetRefreshToken(tablet): %v", err)
	}
	if !tablet.Usable(now) {
		t.Errorf("the other device was revoked too: %+v", tablet)
	}
}

// The two Store implementations have to agree, or the unit tests that run against Memory
// prove nothing about production.
func TestMemoryAndPostgresAgree(t *testing.T) {
	postgres := newPostgres(t)
	memory := store.NewMemory()

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, s store.Store)
	}{
		{"missing user", func(t *testing.T, s store.Store) {
			if _, err := s.GetUser(context.Background(), "11111111-1111-4111-8111-111111111111"); err != store.ErrNotFound {
				t.Errorf("got %v, want ErrNotFound", err)
			}
		}},
		{"missing challenge", func(t *testing.T, s store.Store) {
			if _, err := s.GetAuthChallenge(context.Background(), "11111111-1111-4111-8111-111111111111"); err != store.ErrNotFound {
				t.Errorf("got %v, want ErrNotFound", err)
			}
		}},
		{"missing refresh token", func(t *testing.T, s store.Store) {
			if _, err := s.GetRefreshToken(context.Background(), "nope"); err != store.ErrNotFound {
				t.Errorf("got %v, want ErrNotFound", err)
			}
		}},
		{"repeat upsert returns the same account", func(t *testing.T, s store.Store) {
			ctx, now := context.Background(), time.Now().UTC()
			email := uniqueEmail(t)
			first, err := s.UpsertUserByEmail(ctx, email, now)
			if err != nil {
				t.Fatalf("first: %v", err)
			}
			second, err := s.UpsertUserByEmail(ctx, email, now)
			if err != nil {
				t.Fatalf("second: %v", err)
			}
			if first.ID != second.ID {
				t.Errorf("ids differ: %q vs %q", first.ID, second.ID)
			}
		}},
	} {
		t.Run(tc.name+"/postgres", func(t *testing.T) { tc.run(t, postgres) })
		t.Run(tc.name+"/memory", func(t *testing.T) { tc.run(t, memory) })
	}
}
