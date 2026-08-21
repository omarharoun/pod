package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/auth"
	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/entitlement"
	"github.com/omarharoun/pod/backend/internal/httpapi"
	"github.com/omarharoun/pod/backend/internal/matching"
	"github.com/omarharoun/pod/backend/internal/migrate"
	"github.com/omarharoun/pod/backend/internal/notify"
	"github.com/omarharoun/pod/backend/internal/onboarding"
	"github.com/omarharoun/pod/backend/internal/push"
	"github.com/omarharoun/pod/backend/internal/store"
	"github.com/omarharoun/pod/backend/internal/week"
)

// The whole product, once, against a real database.
//
// Every other test in this package runs the API against store.Memory. That is fast and it
// is also exactly the arrangement that let ADR-024's bug ship: a rule enforced in the
// double and not in production, with every test saying it was held. This one wires the
// real server to Postgres and walks a person from signing in to graduating.
//
// It is deliberately one long test rather than several. The value is in the sequence —
// each step depends on the last having really happened — and splitting it would mean
// seeding the middle, which is where the interesting bugs are.
func TestAWholeCycleAgainstPostgres(t *testing.T) {
	j := newJourney(t)

	// ---- Two people sign in and say what they are putting off ----------------------

	sam := j.signIn("sam@example.com")
	alex := j.signIn("alex@example.com")

	for _, person := range []session{sam, alex} {
		j.do(t, http.MethodPut, "/v1/onboarding/intent", person, api.IntentRequest{
			GoalText:              "finally sort out the spare room",
			GoalCategory:          api.GoalCategoryHome,
			Intensity:             3,
			Timezone:              "Europe/Lisbon",
			SelfReportReliability: api.SelfReportReliabilitySometimes,
		}, http.StatusOK, nil)
		j.do(t, http.MethodPost, "/v1/pool", person, nil, http.StatusAccepted, nil)
	}

	// Nobody has a pod before a matching window runs. Matching is scheduled, not
	// on-request (CLAUDE.md §5), and this is what that means in practice.
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusNoContent, nil)

	// ---- A matching window forms the pod --------------------------------------------

	if _, err := j.matcher.RunWindow(context.Background()); err != nil {
		t.Fatalf("matching window: %v", err)
	}

	var pod api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &pod)
	if len(pod.Members) != 2 {
		t.Fatalf("the pod has %d members, want 2", len(pod.Members))
	}
	if pod.Cycle == nil || pod.Cycle.LengthWeeks != cycle.DefaultLengthWeeks {
		t.Fatalf("cycle: %+v", pod.Cycle)
	}
	// Pod-only visibility, and nothing per-member beyond a name (CLAUDE.md §2 rule 5).
	body := j.raw(t, http.MethodGet, "/v1/pods/current", sam)
	for _, forbidden := range []string{"reliability", "score", "band", "completion_rate", "email"} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Errorf("the pod response carries %q: %s", forbidden, body)
		}
	}

	// ---- Six weeks, everyone in ------------------------------------------------------

	for n := 0; n < cycle.DefaultLengthWeeks; n++ {
		var current api.Week
		j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", pod.ID), sam, nil, http.StatusOK, &current)

		for _, person := range []session{sam, alex} {
			j.post(t, pod.ID, current.ID, person)
		}

		// Past the close, and turn the week over.
		j.clock.Advance(current.ClosesAt.Sub(j.clock.Now()) + time.Hour)
		if _, err := j.weekly.Tick(context.Background()); err != nil {
			t.Fatalf("week %d tick: %v", n+1, err)
		}
		// Tokens are short-lived and we have just moved a week; sign back in.
		sam, alex = j.signIn("sam@example.com"), j.signIn("alex@example.com")
	}

	// ---- Graduation ------------------------------------------------------------------

	var graduated api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &graduated)
	if graduated.State != api.PodStateCompleted {
		t.Fatalf("after %d weeks the pod is %q, want completed", cycle.DefaultLengthWeeks, graduated.State)
	}
	if graduated.Cycle.WeeksEveryoneWasIn != cycle.DefaultLengthWeeks {
		t.Errorf("weeks everyone was in: got %d, want %d",
			graduated.Cycle.WeeksEveryoneWasIn, cycle.DefaultLengthWeeks)
	}
	if graduated.Continuation == nil || graduated.Continuation.YouAnswered {
		t.Fatalf("continuation: %+v", graduated.Continuation)
	}

	// ---- The proof is gone ------------------------------------------------------------

	// §2 rule 4, end to end: the bytes of every week are deleted, verifiably.
	remaining := 0
	err := filepath.WalkDir(j.blobRoot, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			remaining++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the blob store: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d proof file(s) survived the cycle — §2 rule 4 is a promise, not a cleanup job", remaining)
	}

	// ---- Continue, and the pod runs again ---------------------------------------------

	for _, person := range []session{sam, alex} {
		j.do(t, http.MethodPost, fmt.Sprintf("/v1/pods/%s/continue", graduated.ID), person,
			api.ContinueRequest{Continuing: true}, http.StatusOK, nil)
	}
	if _, err := j.weekly.Tick(context.Background()); err != nil {
		t.Fatalf("settling tick: %v", err)
	}

	var restarted api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &restarted)
	if restarted.State != api.PodStateActive {
		t.Fatalf("both members carried on and the pod is %q", restarted.State)
	}
	if restarted.StreakWeeks == 0 {
		t.Error("carrying on reset the shared streak — it belongs to the pod, and the pod did not stop")
	}
}

// ---- harness -----------------------------------------------------------------------

type session struct {
	token  string
	userID string
}

type journey struct {
	server   *httpapi.Server
	blobRoot string
	store    *store.Postgres
	blobs    *blob.Local
	clock    *clock.Fixed
	matcher  *matching.Job
	weekly   *week.Job
	codes    *capturingMailer
}

type capturingMailer struct{ code string }

func (m *capturingMailer) SendLoginCode(_ context.Context, _, code string, _ int) error {
	m.code = code
	return nil
}

func newJourney(t *testing.T) *journey {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the end-to-end journey")
	}
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := migrate.Up(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// A clean slate, so a previous run's pods cannot be matched into this one's.
	for _, table := range []string{
		"proofs", "member_weeks", "weeks", "pod_continuations", "recovery_offers",
		"pod_members", "pods", "pool_tickets", "intents", "slot_entitlements",
		"notifications", "devices", "refresh_tokens", "auth_challenges", "users",
	} {
		if _, err := conn.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
	_ = conn.Close(ctx)

	db, err := store.NewPostgres(ctx, url)
	if err != nil {
		t.Fatalf("NewPostgres: %v", err)
	}
	t.Cleanup(db.Close)

	fixed := clock.NewFixed(time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))
	blobRoot := t.TempDir()
	blobs, err := blob.NewLocal(blobRoot, "http://test.invalid", "a-development-signing-secret-value")
	if err != nil {
		t.Fatalf("blob.NewLocal: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		Env: "test", Version: "journey",
		AuthSecret: "test-secret-that-is-at-least-32-bytes", AccessTokenTTL: 15 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour, LoginCodeTTL: 10 * time.Minute,
		MaxCodeAttempts: 5, MaxCodesPerHour: 50, ResendAfter: time.Second,
	}
	mailer := &capturingMailer{}
	notifier := notify.NewService(db, fixed, log)
	weekService := week.NewService(db, blobs, fixed, notifier)

	return &journey{
		server: httpapi.New(
			auth.NewService(db, mailer, fixed, cfg),
			onboarding.NewService(db, embed.Hashing{}, fixed, entitlement.DevVerifier{}),
			weekService, db, fixed, cfg, log, blobs,
		),
		blobRoot: blobRoot,
		store:    db,
		blobs:    blobs,
		clock:    fixed,
		matcher:  matching.NewJob(db, fixed, log, matching.Options{}, notifier, weekService),
		weekly:   week.NewJob(weekService, db, blobs, fixed, log, notifier, push.Logger{Log: log}),
		codes:    mailer,
	}
}

func (j *journey) signIn(email string) session {
	var start api.AuthStartResponse
	j.decode(j.call(http.MethodPost, "/v1/auth/start", "", api.AuthStartRequest{Email: email}), http.StatusOK, &start)
	var out api.AuthSession
	j.decode(j.call(http.MethodPost, "/v1/auth/verify", "", api.AuthVerifyRequest{
		ChallengeID: start.ChallengeID, Code: j.codes.code,
	}), http.StatusOK, &out)
	return session{token: out.AccessToken, userID: out.User.ID}
}

func (j *journey) do(t *testing.T, method, path string, s session, body any, want int, into any) {
	t.Helper()
	rec := j.call(method, path, s.token, body)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d, want %d — %s", method, path, rec.Code, want, rec.Body.String())
	}
	if into != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
}

func (j *journey) raw(t *testing.T, method, path string, s session) []byte {
	t.Helper()
	return j.call(method, path, s.token, nil).Body.Bytes()
}

// post puts one member in for the week, through the real three-step upload.
func (j *journey) post(t *testing.T, podID, weekID string, s session) {
	t.Helper()
	var slot api.ProofUpload
	j.do(t, http.MethodPost, fmt.Sprintf("/v1/pods/%s/weeks/%s/proof", podID, weekID), s,
		api.ProofUploadRequest{Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 20},
		http.StatusCreated, &slot)

	if err := j.blobs.Put(blob.Key(weekID, slot.ProofID), bytes.NewReader([]byte("ten seconds of it"))); err != nil {
		t.Fatalf("upload: %v", err)
	}
	j.do(t, http.MethodPost,
		fmt.Sprintf("/v1/pods/%s/weeks/%s/proof/%s/commit", podID, weekID, slot.ProofID),
		s, nil, http.StatusOK, nil)
}

func (j *journey) call(method, path, token string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	j.server.ServeHTTP(rec, req)
	return rec
}

func (j *journey) decode(rec *httptest.ResponseRecorder, want int, into any) {
	if rec.Code != want {
		panic(fmt.Sprintf("status %d, want %d: %s", rec.Code, want, rec.Body.String()))
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		panic(fmt.Sprintf("decode %s: %v", rec.Body.String(), err))
	}
}
