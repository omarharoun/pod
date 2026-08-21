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
func newPostgres(t testing.TB) *store.Postgres {
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
func uniqueEmail(t testing.TB) string {
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
		// The two implementations must agree on *refusal*, not only on success. This
		// one diverged: Memory checked membership explicitly and Postgres left it to a
		// JOIN, which enforces nothing on a query that returns a list — so a stranger
		// got an empty list from Postgres and ErrNotFound from Memory, and every test
		// that ran against Memory said the rule was held (CLAUDE.md §2 rule 5).
		{"listing a pod you are not in", func(t *testing.T, s store.Store) {
			ctx := context.Background()
			owner, err := s.UpsertUserByEmail(ctx, uniqueEmail(t), time.Now().UTC())
			if err != nil {
				t.Fatalf("owner: %v", err)
			}
			stranger, err := s.UpsertUserByEmail(ctx, uniqueEmail(t), time.Now().UTC())
			if err != nil {
				t.Fatalf("stranger: %v", err)
			}
			podID, err := ids.NewUUID()
			if err != nil {
				t.Fatalf("ids.NewUUID: %v", err)
			}
			pod := store.Pod{
				ID: podID, State: store.PodStateActive, GoalCategory: "home",
				Timezone: "UTC", CreatedAt: time.Now().UTC(),
			}
			if err := s.CreatePod(ctx, pod, store.FreeLane(owner.ID), time.Now().UTC()); err != nil {
				t.Fatalf("CreatePod: %v", err)
			}
			if _, err := s.ListWeeksForMember(ctx, podID, stranger.ID, 0); err != store.ErrNotFound {
				t.Errorf("got %v, want ErrNotFound — an empty list is an existence oracle", err)
			}
			// And a real member of a pod with no weeks gets an empty list, not a 404.
			weeks, err := s.ListWeeksForMember(ctx, podID, owner.ID, 0)
			if err != nil {
				t.Errorf("a member of a pod with no weeks got %v", err)
			}
			if len(weeks) != 0 {
				t.Errorf("a pod with no weeks listed %d", len(weeks))
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

// ---------------------------------------------------------------- M2: onboarding

func seedUser(t testing.TB, db *store.Postgres) store.User {
	t.Helper()
	user, err := db.UpsertUserByEmail(context.Background(), uniqueEmail(t), time.Now().UTC().Add(-21*24*time.Hour))
	if err != nil {
		t.Fatalf("UpsertUserByEmail: %v", err)
	}
	return user
}

func seedIntent(t testing.TB, db *store.Postgres, userID string, mutate ...func(*store.Intent)) store.Intent {
	t.Helper()
	intent := store.Intent{
		UserID:       userID,
		GoalText:     "finally sort out the spare room",
		GoalCategory: "home",
		Intensity:    3,
		Timezone:     "Europe/Lisbon",
		SelfReport:   "sometimes",
		AvailabilityWindows: []store.AvailabilityWindow{
			{DayOfWeek: 5, StartMinute: 600, EndMinute: 720},
		},
		Embedding: []float32{0.1, 0.2, 0.3},
		UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	for _, m := range mutate {
		m(&intent)
	}
	if err := db.UpsertIntent(context.Background(), intent); err != nil {
		t.Fatalf("UpsertIntent: %v", err)
	}
	return intent
}

func TestPostgresIntentRoundTripAndReplace(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	user := seedUser(t, db)

	written := seedIntent(t, db, user.ID)
	got, err := db.GetIntent(ctx, user.ID, 0)
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	if got.GoalText != written.GoalText || got.Intensity != written.Intensity || got.Timezone != written.Timezone {
		t.Fatalf("got %+v", got)
	}
	if len(got.AvailabilityWindows) != 1 || got.AvailabilityWindows[0].StartMinute != 600 {
		t.Fatalf("availability round trip: %+v", got.AvailabilityWindows)
	}
	if len(got.Embedding) != 3 {
		t.Fatalf("embedding round trip: %+v", got.Embedding)
	}

	// Saving an intent completes onboarding.
	refreshed, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !refreshed.OnboardingComplete {
		t.Error("onboarding_complete should be true once an intent exists")
	}

	// A change of mind replaces the row rather than adding a second goal.
	seedIntent(t, db, user.ID, func(i *store.Intent) {
		i.GoalText = "learn to make bread"
		i.GoalCategory = "creative"
	})
	replaced, err := db.GetIntent(ctx, user.ID, 0)
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	if replaced.GoalText != "learn to make bread" || replaced.GoalCategory != "creative" {
		t.Fatalf("got %+v", replaced)
	}
}

func TestPostgresPoolLifecycle(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	user := seedUser(t, db)
	seedIntent(t, db, user.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := db.JoinPool(ctx, user.ID, 0, now); err != nil {
		t.Fatalf("JoinPool: %v", err)
	}
	// Re-joining loses, so the original place in the queue survives.
	if err := db.JoinPool(ctx, user.ID, 0, now.Add(time.Hour)); err != store.ErrConflict {
		t.Fatalf("second JoinPool: got %v, want ErrConflict", err)
	}
	ticket, err := db.GetPoolTicket(ctx, user.ID, 0)
	if err != nil {
		t.Fatalf("GetPoolTicket: %v", err)
	}
	if !ticket.QueuedAt.Equal(now) {
		t.Fatalf("queued_at moved to %v — re-joining jumped the queue", ticket.QueuedAt)
	}

	if err := db.LeavePool(ctx, user.ID, 0); err != nil {
		t.Fatalf("LeavePool: %v", err)
	}
	if err := db.LeavePool(ctx, user.ID, 0); err != nil {
		t.Fatalf("LeavePool should be idempotent, got %v", err)
	}
	if _, err := db.GetPoolTicket(ctx, user.ID, 0); err != store.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestPostgresPoolCandidatesCarryEverythingMatchingNeeds(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	user := seedUser(t, db)
	seedIntent(t, db, user.ID)
	now := time.Now().UTC()
	if err := db.JoinPool(ctx, user.ID, 0, now); err != nil {
		t.Fatalf("JoinPool: %v", err)
	}

	candidates, err := db.ListPoolCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("ListPoolCandidates: %v", err)
	}
	var mine *store.PoolCandidate
	for i := range candidates {
		if candidates[i].UserID == user.ID {
			mine = &candidates[i]
		}
	}
	if mine == nil {
		t.Fatal("the queued user is not in the candidate list")
	}
	if mine.GoalCategory != "home" || mine.Intensity != 3 || mine.Timezone != "Europe/Lisbon" || mine.SelfReport != "sometimes" {
		t.Fatalf("got %+v", *mine)
	}
	if len(mine.Availability) != 1 || len(mine.Embedding) != 3 {
		t.Fatalf("availability/embedding did not survive: %+v", *mine)
	}
	// Seeded three weeks ago.
	if mine.TenureWeeks != 3 {
		t.Errorf("tenure weeks = %d, want 3", mine.TenureWeeks)
	}
}

func TestPostgresCreatePodDequeuesAndScopesVisibility(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	first, second := seedUser(t, db), seedUser(t, db)
	outsider := seedUser(t, db)
	for _, u := range []store.User{first, second} {
		seedIntent(t, db, u.ID)
		if err := db.JoinPool(ctx, u.ID, 0, now); err != nil {
			t.Fatalf("JoinPool: %v", err)
		}
	}

	podID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	pod := store.Pod{ID: podID, State: store.PodStateActive, GoalCategory: "home", Timezone: "Europe/Lisbon", CreatedAt: now}
	if err := db.CreatePod(ctx, pod, store.FreeLane(first.ID, second.ID), now); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	for _, u := range []store.User{first, second} {
		if _, err := db.GetPoolTicket(ctx, u.ID, 0); err != store.ErrNotFound {
			t.Errorf("%s is still queued after being matched: %v", u.ID, err)
		}
		got, err := db.GetActivePod(ctx, u.ID, 0)
		if err != nil {
			t.Fatalf("GetActivePod: %v", err)
		}
		if got.ID != podID || len(got.Members) != 2 || got.StreakWeeks != 0 {
			t.Fatalf("got %+v", got)
		}
	}

	// Pod-only visibility, enforced in the query.
	if _, err := db.GetPodForMember(ctx, podID, outsider.ID); err != store.ErrNotFound {
		t.Fatalf("an outsider read the pod: %v", err)
	}
	if _, err := db.GetActivePod(ctx, outsider.ID, 0); err != store.ErrNotFound {
		t.Fatalf("an outsider has an active pod: %v", err)
	}
}

// One free pod, forever — and until the paywall exists in M7, exactly one, enforced by a
// partial unique index rather than trusted to a handler.
func TestPostgresRefusesASecondActivePod(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC()
	user, other := seedUser(t, db), seedUser(t, db)

	makePod := func() error {
		podID, err := ids.NewUUID()
		if err != nil {
			t.Fatalf("ids.NewUUID: %v", err)
		}
		pod := store.Pod{ID: podID, State: store.PodStateActive, GoalCategory: "home", Timezone: "UTC", CreatedAt: now}
		return db.CreatePod(ctx, pod, store.FreeLane(user.ID, other.ID), now)
	}
	if err := makePod(); err != nil {
		t.Fatalf("first pod: %v", err)
	}
	if err := makePod(); err != store.ErrConflict {
		t.Fatalf("second pod: got %v, want ErrConflict", err)
	}
	// And the failed attempt left nothing behind.
	pod, err := db.GetActivePod(ctx, user.ID, 0)
	if err != nil {
		t.Fatalf("GetActivePod: %v", err)
	}
	if len(pod.Members) != 2 {
		t.Fatalf("pod has %d members after a failed second insert", len(pod.Members))
	}
}

// -------------------------------------------------------------- M3: the week loop

func seedPodWith(t testing.TB, db *store.Postgres, members ...store.User) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	podID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ID)
	}
	pod := store.Pod{ID: podID, State: store.PodStateActive, GoalCategory: "home", Timezone: "Europe/Lisbon", CreatedAt: now}
	if err := db.CreatePod(ctx, pod, store.FreeLane(ids...), now); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	return podID
}

func seedWeek(t testing.TB, db *store.Postgres, podID string, members ...store.User) store.Week {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	weekID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	memberIDs := make([]string, 0, len(members))
	for _, member := range members {
		memberIDs = append(memberIDs, member.ID)
	}
	week, err := db.EnsureWeek(ctx, store.Week{
		ID: weekID, PodID: podID, ActionIsDefault: true,
		OpensAt: now.Add(-24 * time.Hour), ClosesAt: now.Add(5 * 24 * time.Hour),
	}, memberIDs)
	if err != nil {
		t.Fatalf("EnsureWeek: %v", err)
	}
	return week
}

func TestPostgresEnsureWeekIsIdempotent(t *testing.T) {
	db := newPostgres(t)
	first, second := seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, first, second)

	week := seedWeek(t, db, podID, first, second)
	if week.Index != 1 || len(week.Members) != 2 {
		t.Fatalf("got %+v", week)
	}
	for _, member := range week.Members {
		if member.State != store.MemberWeekOpen {
			t.Errorf("a fresh week starts open, got %q", member.State)
		}
	}

	// The same window again returns the same week rather than a second one.
	again, err := db.EnsureWeek(context.Background(), store.Week{
		ID: "99999999-9999-4999-8999-999999999999", PodID: podID,
		OpensAt: week.OpensAt, ClosesAt: week.ClosesAt,
	}, []string{first.ID, second.ID})
	if err != nil {
		t.Fatalf("EnsureWeek: %v", err)
	}
	if again.ID != week.ID {
		t.Fatalf("a second EnsureWeek made a new week: %q then %q", week.ID, again.ID)
	}
}

func TestPostgresProofLifecycle(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	mine, theirs := seedUser(t, db), seedUser(t, db)
	outsider := seedUser(t, db)
	podID := seedPodWith(t, db, mine, theirs)
	week := seedWeek(t, db, podID, mine, theirs)
	now := time.Now().UTC().Truncate(time.Microsecond)

	proofID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	if err := db.CreateProof(ctx, store.Proof{
		ID: proofID, WeekID: week.ID, UserID: mine.ID, Kind: "photo",
		ContentType: "image/jpeg", ByteSize: 2048, CreatedAt: now, ExpiresAt: week.ClosesAt,
	}); err != nil {
		t.Fatalf("CreateProof: %v", err)
	}

	// Uncommitted, so it is not a proof yet and the pod cannot see it.
	if _, err := db.GetProofForViewer(ctx, proofID, theirs.ID); err != nil {
		t.Fatalf("GetProofForViewer: %v", err)
	}
	if err := db.CommitProof(ctx, proofID, mine.ID, "proof/"+week.ID+"/"+proofID, now); err != nil {
		t.Fatalf("CommitProof: %v", err)
	}

	settled, err := db.GetWeekForMember(ctx, week.ID, mine.ID)
	if err != nil {
		t.Fatalf("GetWeekForMember: %v", err)
	}
	for _, member := range settled.Members {
		if member.UserID == mine.ID {
			if member.State != store.MemberWeekPosted || member.ProofID == nil || *member.ProofID != proofID {
				t.Fatalf("committing did not mark the member posted: %+v", member)
			}
		}
	}

	// One committed proof per person per week, enforced by the index.
	otherID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	if err := db.CreateProof(ctx, store.Proof{
		ID: otherID, WeekID: week.ID, UserID: mine.ID, Kind: "photo",
		ContentType: "image/jpeg", ByteSize: 2048, CreatedAt: now, ExpiresAt: week.ClosesAt,
	}); err != nil {
		t.Fatalf("CreateProof: %v", err)
	}
	if err := db.CommitProof(ctx, otherID, mine.ID, "proof/"+week.ID+"/"+otherID, now); err != store.ErrConflict {
		t.Fatalf("a second commit: got %v, want ErrConflict", err)
	}

	// Pod-only visibility.
	if _, err := db.GetProofForViewer(ctx, proofID, outsider.ID); err != store.ErrNotFound {
		t.Fatalf("an outsider read the proof: %v", err)
	}

	// Deletion takes the pointer with it.
	if err := db.MarkProofDeleted(ctx, proofID, now); err != nil {
		t.Fatalf("MarkProofDeleted: %v", err)
	}
	if _, err := db.GetProofForViewer(ctx, proofID, theirs.ID); err != store.ErrNotFound {
		t.Fatalf("a deleted proof is still readable: %v", err)
	}
	swept, err := db.GetWeekForMember(ctx, week.ID, mine.ID)
	if err != nil {
		t.Fatalf("GetWeekForMember: %v", err)
	}
	for _, member := range swept.Members {
		if member.UserID == mine.ID {
			if member.ProofID != nil {
				t.Fatalf("the week still points at a deleted proof: %v", *member.ProofID)
			}
			// The fact of posting survives; only the bytes are ephemeral.
			if member.State != store.MemberWeekPosted {
				t.Fatalf("the record of posting went with the proof: %q", member.State)
			}
		}
	}
}

func TestPostgresCloseWeekSettlesAndMovesTheSharedStreak(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	mine, theirs := seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, mine, theirs)
	week := seedWeek(t, db, podID, mine, theirs)
	now := time.Now().UTC().Truncate(time.Microsecond)

	proofID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	if err := db.CreateProof(ctx, store.Proof{
		ID: proofID, WeekID: week.ID, UserID: mine.ID, Kind: "photo",
		ContentType: "image/jpeg", ByteSize: 2048, CreatedAt: now, ExpiresAt: week.ClosesAt,
	}); err != nil {
		t.Fatalf("CreateProof: %v", err)
	}
	if err := db.CommitProof(ctx, proofID, mine.ID, "proof/k", now); err != nil {
		t.Fatalf("CommitProof: %v", err)
	}

	closed, err := db.CloseWeek(ctx, week.ID, now)
	if err != nil {
		t.Fatalf("CloseWeek: %v", err)
	}
	if closed.PodCompleted {
		t.Error("one of two is not a completed pod week")
	}
	states := map[string]store.MemberWeekState{}
	for _, member := range closed.Members {
		states[member.UserID] = member.State
	}
	if states[theirs.ID] != store.MemberWeekResting {
		t.Errorf("the non-poster is %q, want resting — never missed, never failed", states[theirs.ID])
	}

	pod, err := db.GetActivePod(ctx, mine.ID, 0)
	if err != nil {
		t.Fatalf("GetActivePod: %v", err)
	}
	if pod.StreakWeeks != 0 {
		t.Errorf("streak = %d, want 0 after an incomplete week", pod.StreakWeeks)
	}

	// Closing twice is a no-op, so a job that runs twice cannot double the streak.
	again, err := db.CloseWeek(ctx, week.ID, now)
	if err != nil {
		t.Fatalf("second CloseWeek: %v", err)
	}
	if again.ID != closed.ID {
		t.Fatal("closing twice produced a different week")
	}
}

func TestPostgresSetWeekActionLocksAfterAPost(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	mine := seedUser(t, db)
	podID := seedPodWith(t, db, mine)
	week := seedWeek(t, db, podID, mine)
	now := time.Now().UTC()

	if err := db.SetWeekAction(ctx, week.ID, "Clear one shelf"); err != nil {
		t.Fatalf("SetWeekAction: %v", err)
	}
	updated, err := db.GetWeekForMember(ctx, week.ID, mine.ID)
	if err != nil {
		t.Fatalf("GetWeekForMember: %v", err)
	}
	if updated.ActionText != "Clear one shelf" || updated.ActionIsDefault {
		t.Fatalf("got %+v", updated)
	}

	proofID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	if err := db.CreateProof(ctx, store.Proof{
		ID: proofID, WeekID: week.ID, UserID: mine.ID, Kind: "photo",
		ContentType: "image/jpeg", ByteSize: 1, CreatedAt: now, ExpiresAt: week.ClosesAt,
	}); err != nil {
		t.Fatalf("CreateProof: %v", err)
	}
	if err := db.CommitProof(ctx, proofID, mine.ID, "proof/k", now); err != nil {
		t.Fatalf("CommitProof: %v", err)
	}
	if err := db.SetWeekAction(ctx, week.ID, "Something else"); err != store.ErrConflict {
		t.Fatalf("got %v, want ErrConflict — editing after someone acted rewrites what they acted on", err)
	}
}

func TestPostgresListExpiredProofs(t *testing.T) {
	db := newPostgres(t)
	ctx := context.Background()
	mine := seedUser(t, db)
	podID := seedPodWith(t, db, mine)
	week := seedWeek(t, db, podID, mine)
	now := time.Now().UTC().Truncate(time.Microsecond)

	proofID, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("ids.NewUUID: %v", err)
	}
	if err := db.CreateProof(ctx, store.Proof{
		ID: proofID, WeekID: week.ID, UserID: mine.ID, Kind: "photo",
		ContentType: "image/jpeg", ByteSize: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateProof: %v", err)
	}

	expired, err := db.ListExpiredProofs(ctx, now, 0)
	if err != nil {
		t.Fatalf("ListExpiredProofs: %v", err)
	}
	for _, proof := range expired {
		if proof.ID == proofID {
			t.Fatal("a proof that has not expired is queued for deletion")
		}
	}

	expired, err = db.ListExpiredProofs(ctx, now.Add(2*time.Hour), 0)
	if err != nil {
		t.Fatalf("ListExpiredProofs: %v", err)
	}
	found := false
	for _, proof := range expired {
		if proof.ID == proofID {
			found = true
		}
	}
	if !found {
		t.Fatal("an expired proof was not queued for deletion — the sweep would never reach it")
	}
}

// A limit above the ceiling must be clamped down to it, not reset to the small default.
// The reliability fold asks for a decade of weeks (ADR-022); an implementation that
// answered 8 would break the §6 movement cap in production with every test still green.
func TestPostgresOutcomeLimitClampsDownNotBackToTheDefault(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	user := seedUser(t, db)
	podID := seedPodWith(t, db, user)
	for i := 0; i < 12; i++ {
		week := seedWeek(t, db, podID, user)
		if _, err := db.CloseWeek(ctx, week.ID, time.Now().UTC()); err != nil {
			t.Fatalf("CloseWeek: %v", err)
		}
	}

	all, err := db.RecentOutcomes(ctx, user.ID, 10_000)
	if err != nil {
		t.Fatalf("RecentOutcomes: %v", err)
	}
	if len(all) != 12 {
		t.Fatalf("asking for more history than exists returned %d weeks, want all 12", len(all))
	}

	capped, err := db.RecentOutcomes(ctx, user.ID, 5)
	if err != nil {
		t.Fatalf("RecentOutcomes: %v", err)
	}
	if len(capped) != 5 {
		t.Fatalf("an explicit limit of 5 returned %d weeks", len(capped))
	}
}

// ---- M6: cycles -------------------------------------------------------------------

// Graduating is not dissolving. The whole point of a separate method is that it touches
// neither dissolved_pods nor recovery_offered_at (ADR-023).
func TestPostgresCompleteCycleCostsNobodyAnything(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	sam, alex := seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, sam, alex)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := db.CompleteCycle(ctx, podID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}

	pod, err := db.GetPodForMember(ctx, podID, sam.ID)
	if err != nil {
		t.Fatalf("GetPodForMember: %v", err)
	}
	if pod.State != store.PodStateCompleted {
		t.Errorf("state: got %q, want completed", pod.State)
	}
	if pod.CompletedAt == nil || pod.DecideBy == nil {
		t.Errorf("completed_at %v, decide_by %v", pod.CompletedAt, pod.DecideBy)
	}
	if pod.DissolvedAt != nil {
		t.Error("graduating set dissolved_at")
	}
	// Membership survives: the pod may carry on, and ending it here would free everyone
	// from the one-active-pod index while the choice is still open.
	if len(pod.Members) != 2 {
		t.Errorf("a graduated pod has %d members, want 2", len(pod.Members))
	}
	for _, user := range []store.User{sam, alex} {
		recovery, err := db.RecoveryFor(ctx, user.ID)
		if err != nil {
			t.Fatalf("RecoveryFor: %v", err)
		}
		if recovery.OfferLowerIntensity || recovery.OfferAlternatePath {
			t.Errorf("finishing a cycle left %s with a recovery offer: %+v", user.ID, recovery)
		}
	}
}

// The counts come out of SQL as counts. There is no point in the call stack where a list
// of "who said no" exists to be logged, joined to a name, or serialised.
func TestPostgresContinuationCountsWithoutNamingAnyone(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	sam, alex, remi := seedUser(t, db), seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, sam, alex, remi)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.CompleteCycle(ctx, podID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}

	if err := db.AnswerContinuation(ctx, podID, sam.ID, true, now); err != nil {
		t.Fatalf("AnswerContinuation: %v", err)
	}
	if err := db.AnswerContinuation(ctx, podID, alex.ID, false, now); err != nil {
		t.Fatalf("AnswerContinuation: %v", err)
	}

	got, err := db.ContinuationFor(ctx, podID, sam.ID)
	if err != nil {
		t.Fatalf("ContinuationFor: %v", err)
	}
	if got.Continuing != 1 || got.Declined != 1 || got.Undecided != 1 {
		t.Errorf("counts: got %+v, want 1/1/1", got)
	}
	if !got.YouAnswered || !got.YouAreContinuing {
		t.Errorf("the caller's own answer: %+v", got)
	}

	// Read as the member who declined: their own answer, and still nobody else's.
	asAlex, err := db.ContinuationFor(ctx, podID, alex.ID)
	if err != nil {
		t.Fatalf("ContinuationFor: %v", err)
	}
	if !asAlex.YouAnswered || asAlex.YouAreContinuing {
		t.Errorf("the decliner's own answer: %+v", asAlex)
	}

	// Read as the member who has not answered.
	asRemi, err := db.ContinuationFor(ctx, podID, remi.ID)
	if err != nil {
		t.Fatalf("ContinuationFor: %v", err)
	}
	if asRemi.YouAnswered {
		t.Errorf("a member who has not answered was told they had: %+v", asRemi)
	}
}

// Answering twice replaces the answer. Someone who said no and changed their mind is
// exactly who this product is for.
func TestPostgresAnsweringTwiceReplaces(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	sam, alex := seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, sam, alex)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := db.AnswerContinuation(ctx, podID, sam.ID, false, now); err != nil {
		t.Fatalf("AnswerContinuation: %v", err)
	}
	if err := db.AnswerContinuation(ctx, podID, sam.ID, true, now.Add(time.Minute)); err != nil {
		t.Fatalf("AnswerContinuation again: %v", err)
	}

	got, err := db.ContinuationFor(ctx, podID, sam.ID)
	if err != nil {
		t.Fatalf("ContinuationFor: %v", err)
	}
	if got.Continuing != 1 || got.Declined != 0 {
		t.Errorf("changing your mind left %+v", got)
	}
}

// The next cycle keeps whoever said yes, keeps the shared streak, and clears the answers
// so the following cycle starts from nobody having answered.
func TestPostgresStartNextCycleKeepsTheYesesAndTheStreak(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	sam, alex, remi := seedUser(t, db), seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, sam, alex, remi)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.CompleteCycle(ctx, podID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}
	for _, user := range []store.User{sam, alex} {
		if err := db.AnswerContinuation(ctx, podID, user.ID, true, now); err != nil {
			t.Fatalf("AnswerContinuation: %v", err)
		}
	}
	if err := db.AnswerContinuation(ctx, podID, remi.ID, false, now); err != nil {
		t.Fatalf("AnswerContinuation: %v", err)
	}

	if err := db.StartNextCycle(ctx, podID, now.Add(time.Hour)); err != nil {
		t.Fatalf("StartNextCycle: %v", err)
	}

	pod, err := db.GetPodForMember(ctx, podID, sam.ID)
	if err != nil {
		t.Fatalf("GetPodForMember: %v", err)
	}
	if pod.State != store.PodStateActive {
		t.Errorf("state: got %q, want active", pod.State)
	}
	if pod.CompletedAt != nil || pod.DecideBy != nil {
		t.Error("the restarted pod still carries its completion")
	}
	if len(pod.Members) != 2 {
		t.Errorf("the next cycle has %d members, want the 2 who said yes", len(pod.Members))
	}
	// The one who stopped is free to be matched again, and carries nothing.
	if _, err := db.GetActivePod(ctx, remi.ID, 0); err == nil {
		t.Error("the member who stopped is still in the pod")
	}
	recovery, err := db.RecoveryFor(ctx, remi.ID)
	if err != nil {
		t.Fatalf("RecoveryFor: %v", err)
	}
	if recovery.OfferLowerIntensity {
		t.Error("choosing not to do another cycle was treated as a pod dissolving")
	}
	// Answers are cleared, so the next cycle's choice starts from silence.
	counts, err := db.ContinuationFor(ctx, podID, sam.ID)
	if err != nil {
		t.Fatalf("ContinuationFor: %v", err)
	}
	if counts.Continuing != 0 || counts.Undecided != 2 {
		t.Errorf("the next cycle inherited answers: %+v", counts)
	}
}

// A pod cannot be both graduated and dissolved. The constraint is in the schema because
// the two render completely differently and a row that is both has no correct screen.
func TestPostgresAPodCannotBeBothCompletedAndDissolved(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	sam, alex := seedUser(t, db), seedUser(t, db)
	podID := seedPodWith(t, db, sam, alex)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := db.CompleteCycle(ctx, podID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}
	// DissolvePod only acts on a pod that is not already dissolved; a graduated pod
	// reaching it would violate the check constraint, which is the point.
	err := db.DissolvePod(ctx, podID, "inactive", []string{sam.ID}, now.Add(time.Hour))
	if err == nil {
		pod, readErr := db.GetPodForMember(ctx, podID, sam.ID)
		if readErr == nil && pod.CompletedAt != nil && pod.DissolvedAt != nil {
			t.Fatal("a pod is marked both completed and dissolved")
		}
	}
}

// A path id that is not a uuid must answer as an id that is well-formed and unknown.
// Anything else is both an existence oracle and a 500 logged against the server for what
// is really a client typo.
func TestPostgresAMalformedIDIsNotFoundRatherThanAnError(t *testing.T) {
	ctx := context.Background()
	db := newPostgres(t)
	user := seedUser(t, db)

	if _, err := db.GetPodForMember(ctx, "not-a-uuid", user.ID); err != store.ErrNotFound {
		t.Errorf("GetPodForMember: got %v, want ErrNotFound", err)
	}
	if _, err := db.ListWeeksForMember(ctx, "not-a-uuid", user.ID, 0); err != store.ErrNotFound {
		t.Errorf("ListWeeksForMember: got %v, want ErrNotFound", err)
	}
}
