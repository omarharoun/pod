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
	"strings"
	"sync/atomic"
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

// matchingOptions turns the "pairs only" flag into a third-member threshold nothing can
// clear, so a pool of four produces two pairs rather than a trio and a leftover.
func matchingOptions(pairsOnly []bool) matching.Options {
	if len(pairsOnly) > 0 && pairsOnly[0] {
		return matching.Options{ThirdThreshold: 2}
	}
	return matching.Options{}
}

type capturingMailer struct{ code string }

func (m *capturingMailer) SendLoginCode(_ context.Context, _, code string, _ int) error {
	m.code = code
	return nil
}

// newJourney wires the real server to Postgres. `pairsOnly` stops the matcher adding a
// third member, which makes which-pod-gets-whom deterministic for the tests that care.
func newJourney(t *testing.T, pairsOnly ...bool) *journey {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the end-to-end journey")
	}
	ctx := context.Background()

	// A database of its own, created and dropped per test.
	//
	// The journey needs an empty database — it asserts that *no* proof survived a cycle,
	// which only means something if nothing else put one there. Truncating the shared one
	// is not an option: `go test ./...` runs packages concurrently, so internal/store
	// would be reading rows this test had just deleted. That is exactly what happened the
	// first time, and it passed alone and failed in the suite.
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	name := fmt.Sprintf("pod_journey_%d_%d", os.Getpid(), journeyCounter.Add(1))
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Skipf("cannot create databases with this connection: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize())
		_ = admin.Close(context.Background())
	})
	url = replaceDatabaseName(url, name)

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to the journey database: %v", err)
	}
	if _, err := migrate.Up(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
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
			onboarding.NewService(db, embed.Lexical{}, fixed, entitlement.DevVerifier{}),
			weekService, db, fixed, cfg, log, blobs,
		),
		blobRoot: blobRoot,
		store:    db,
		blobs:    blobs,
		clock:    fixed,
		matcher:  matching.NewJob(db, fixed, log, matchingOptions(pairsOnly), notifier, weekService),
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

// A pod that goes quiet dissolves, and its members are offered a gentler restart.
//
// This is the path the members-less week list broke, and it was invisible because every
// test of it ran against store.Memory. It runs against Postgres now.
func TestAQuietPodDissolvesAgainstPostgres(t *testing.T) {
	j := newJourney(t)

	sam := j.signIn("sam@example.com")
	alex := j.signIn("alex@example.com")
	for _, person := range []session{sam, alex} {
		j.do(t, http.MethodPut, "/v1/onboarding/intent", person, api.IntentRequest{
			GoalText: "finally sort out the spare room", GoalCategory: api.GoalCategoryHome,
			Intensity: 3, Timezone: "Europe/Lisbon",
			SelfReportReliability: api.SelfReportReliabilitySometimes,
		}, http.StatusOK, nil)
		j.do(t, http.MethodPost, "/v1/pool", person, nil, http.StatusAccepted, nil)
	}
	if _, err := j.matcher.RunWindow(context.Background()); err != nil {
		t.Fatalf("matching window: %v", err)
	}

	var pod api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &pod)

	// One quiet week is not enough — a pod is allowed a bad week.
	j.quietWeek(t, pod.ID, sam)
	sam, alex = j.signIn("sam@example.com"), j.signIn("alex@example.com")
	var still api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &still)
	if still.State != api.PodStateActive {
		t.Fatalf("one quiet week and the pod is %q", still.State)
	}

	// Two is.
	j.quietWeek(t, pod.ID, sam)
	sam, alex = j.signIn("sam@example.com"), j.signIn("alex@example.com")
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusNoContent, nil)

	// And both are offered the gentler restart, in the lane the pod occupied.
	for _, person := range []session{sam, alex} {
		var state api.OnboardingState
		j.do(t, http.MethodGet, "/v1/onboarding/state", person, nil, http.StatusOK, &state)
		if state.Recovery == nil || !state.Recovery.OfferLowerIntensity {
			t.Errorf("a pod dissolved and %s was offered nothing: %+v", person.userID, state.Recovery)
			continue
		}
		if state.Recovery.SuggestedIntensity == nil || *state.Recovery.SuggestedIntensity != 2 {
			t.Errorf("suggested intensity: %v, want 2", state.Recovery.SuggestedIntensity)
		}
		// Nothing about the pod that ended, or about anyone in it.
		body := j.raw(t, http.MethodGet, "/v1/onboarding/state", person)
		for _, forbidden := range []string{"inactive", "dissolved_reason", "dissolved_pods", "missed"} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Errorf("the state response says %q: %s", forbidden, body)
			}
		}
	}
}

// A pod where one person keeps showing up is not inactive. It is a pod carrying somebody,
// which is the product working (ADR-019).
func TestAPodOnePersonCarriesDoesNotDissolveAgainstPostgres(t *testing.T) {
	j := newJourney(t)

	sam := j.signIn("sam@example.com")
	alex := j.signIn("alex@example.com")
	for _, person := range []session{sam, alex} {
		j.do(t, http.MethodPut, "/v1/onboarding/intent", person, api.IntentRequest{
			GoalText: "finally sort out the spare room", GoalCategory: api.GoalCategoryHome,
			Intensity: 3, Timezone: "Europe/Lisbon",
			SelfReportReliability: api.SelfReportReliabilitySometimes,
		}, http.StatusOK, nil)
		j.do(t, http.MethodPost, "/v1/pool", person, nil, http.StatusAccepted, nil)
	}
	if _, err := j.matcher.RunWindow(context.Background()); err != nil {
		t.Fatalf("matching window: %v", err)
	}
	var pod api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &pod)

	// Three weeks where only Sam posts.
	for n := 0; n < 3; n++ {
		var current api.Week
		j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", pod.ID), sam, nil, http.StatusOK, &current)
		j.post(t, pod.ID, current.ID, sam)
		j.clock.Advance(current.ClosesAt.Sub(j.clock.Now()) + time.Hour)
		if _, err := j.weekly.Tick(context.Background()); err != nil {
			t.Fatalf("tick: %v", err)
		}
		sam = j.signIn("sam@example.com")
	}

	var carried api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &carried)
	if carried.State != api.PodStateActive {
		t.Fatalf("a pod one person carried for three weeks is %q", carried.State)
	}
	// No week completed, so the shared streak is zero — and that is all it is. Nothing
	// anywhere says who was missing.
	if carried.StreakWeeks != 0 {
		t.Errorf("streak: got %d, want 0", carried.StreakWeeks)
	}
}

// quietWeek advances past the current week's close with nobody posting.
func (j *journey) quietWeek(t *testing.T, podID string, s session) {
	t.Helper()
	var current api.Week
	j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", podID), s, nil, http.StatusOK, &current)
	j.clock.Advance(current.ClosesAt.Sub(j.clock.Now()) + time.Hour)
	if _, err := j.weekly.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// Two lanes, two goals, two pods, against a real database.
//
// This is what migration 0007 is for: the one-active-pod index narrowed to per-lane
// (ADR-026). If it were still per-user, the second pod's creation would lose the race with
// the first and this test would find a person stuck with one pod they paid to have two of.
func TestTwoLanesGetTwoPodsAgainstPostgres(t *testing.T) {
	j := newJourney(t, true)

	sam := j.signIn("sam@example.com")

	// The free lane, and three other people so both lanes can be matched.
	j.do(t, http.MethodPut, "/v1/onboarding/intent", sam, api.IntentRequest{
		GoalText: "finally sort out the spare room", GoalCategory: api.GoalCategoryHome,
		Intensity: 3, Timezone: "Europe/Lisbon", SelfReportReliability: api.SelfReportReliabilitySometimes,
	}, http.StatusOK, nil)
	j.do(t, http.MethodPost, "/v1/pool", sam, nil, http.StatusAccepted, nil)

	// A paid lane, with a different thing in it.
	var lane api.Slot
	j.do(t, http.MethodPost, "/v1/slots", sam, api.ClaimSlotRequest{
		Source: api.PurchaseSourceAppStore, Receipt: "dev:order-1",
	}, http.StatusOK, &lane)
	if lane.Slot != 1 || !lane.Entitled {
		t.Fatalf("claimed lane: %+v", lane)
	}
	j.do(t, http.MethodPut, "/v1/onboarding/intent?slot=1", sam, api.IntentRequest{
		GoalText: "learn to make bread properly", GoalCategory: api.GoalCategoryHome,
		Intensity: 3, Timezone: "Europe/Lisbon", SelfReportReliability: api.SelfReportReliabilitySometimes,
	}, http.StatusOK, nil)
	j.do(t, http.MethodPost, "/v1/pool?slot=1", sam, nil, http.StatusAccepted, nil)

	// Everyone else queues after Sam, a minute apart.
	//
	// The pool is ordered by queued_at, then user id. On a fixed clock every candidate
	// queues at the same instant, so the order falls to random UUIDs — and when the two
	// other people happen to sort first, greedy pairs them with each other and BOTH of
	// Sam's lanes are left over. That is correct matcher behaviour on that pool and it
	// made this test pass or fail by coin flip.
	for _, email := range []string{"alex@example.com", "remi@example.com"} {
		j.clock.Advance(time.Minute)
		other := j.signIn(email)
		j.do(t, http.MethodPut, "/v1/onboarding/intent", other, api.IntentRequest{
			GoalText: "finally sort out the spare room", GoalCategory: api.GoalCategoryHome,
			Intensity: 3, Timezone: "Europe/Lisbon", SelfReportReliability: api.SelfReportReliabilitySometimes,
		}, http.StatusOK, nil)
		j.do(t, http.MethodPost, "/v1/pool", other, nil, http.StatusAccepted, nil)
	}

	if _, err := j.matcher.RunWindow(context.Background()); err != nil {
		t.Fatalf("matching window: %v", err)
	}

	// Both lanes have a pod, and they are different pods.
	var free, paid api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &free)
	j.do(t, http.MethodGet, "/v1/pods/current?slot=1", sam, nil, http.StatusOK, &paid)
	if free.ID == paid.ID {
		t.Fatal("both lanes point at the same pod")
	}
	// And Sam is not in a pod with themselves — a mirror is not accountability.
	for _, member := range paid.Members {
		if member.IsYou {
			continue
		}
		for _, other := range free.Members {
			if other.ID == member.ID && other.IsYou {
				t.Error("sam is podded with their own other lane")
			}
		}
	}

	// Each lane holds its own goal, and writing one does not touch the other.
	var goal, second api.Intent
	j.do(t, http.MethodGet, "/v1/onboarding/intent", sam, nil, http.StatusOK, &goal)
	j.do(t, http.MethodGet, "/v1/onboarding/intent?slot=1", sam, nil, http.StatusOK, &second)
	if goal.GoalText == second.GoalText {
		t.Errorf("both lanes hold %q", goal.GoalText)
	}

	// The lane list agrees with all of it.
	var slots api.SlotList
	j.do(t, http.MethodGet, "/v1/slots", sam, nil, http.StatusOK, &slots)
	for _, slot := range slots.Slots[:2] {
		if !slot.Entitled || !slot.HasIntent || !slot.HasPod {
			t.Errorf("lane %d: %+v", slot.Slot, slot)
		}
	}
	for _, slot := range slots.Slots[2:] {
		if slot.Entitled {
			t.Errorf("lane %d is entitled without a purchase", slot.Slot)
		}
	}

	// One lane's week is not the other's.
	var freeWeek, paidWeek api.Week
	j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", free.ID), sam, nil, http.StatusOK, &freeWeek)
	j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", paid.ID), sam, nil, http.StatusOK, &paidWeek)
	if freeWeek.ID == paidWeek.ID {
		t.Fatal("both pods share a week")
	}
	j.post(t, free.ID, freeWeek.ID, sam)

	// Posting in one lane says nothing about the other.
	var stillOpen api.Week
	j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", paid.ID), sam, nil, http.StatusOK, &stillOpen)
	for _, member := range stillOpen.Members {
		if member.MemberID == sam.userID && member.State == api.MemberWeekStatePosted {
			t.Error("posting in the free lane marked the paid lane's week as posted")
		}
	}
}

// journeyCounter keeps each journey's database name distinct within a process.
var journeyCounter atomic.Int64

// replaceDatabaseName swaps the database in a postgres URL, keeping everything else.
func replaceDatabaseName(url, name string) string {
	base, query, hasQuery := strings.Cut(url, "?")
	slash := strings.LastIndex(base, "/")
	if slash < 0 {
		return url
	}
	out := base[:slash+1] + name
	if hasQuery {
		out += "?" + query
	}
	return out
}

// A pod that carries on gets a whole second cycle, not an immediate second graduation.
//
// The cycle's arithmetic is anchored to when the cycle started. Anchoring it to when the
// *pod* was created means a pod that continues is already past its finish line, so the
// very next tick graduates it again — a second cycle that lasts one hour.
func TestASecondCycleIsAWholeCycleAgainstPostgres(t *testing.T) {
	j := newJourney(t)

	sam := j.signIn("sam@example.com")
	alex := j.signIn("alex@example.com")
	for _, person := range []session{sam, alex} {
		j.do(t, http.MethodPut, "/v1/onboarding/intent", person, api.IntentRequest{
			GoalText: "finally sort out the spare room", GoalCategory: api.GoalCategoryHome,
			Intensity: 3, Timezone: "Europe/Lisbon",
			SelfReportReliability: api.SelfReportReliabilitySometimes,
		}, http.StatusOK, nil)
		j.do(t, http.MethodPost, "/v1/pool", person, nil, http.StatusAccepted, nil)
	}
	if _, err := j.matcher.RunWindow(context.Background()); err != nil {
		t.Fatalf("matching window: %v", err)
	}
	var pod api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &pod)

	for n := 0; n < cycle.DefaultLengthWeeks; n++ {
		var current api.Week
		j.do(t, http.MethodGet, fmt.Sprintf("/v1/pods/%s/weeks/current", pod.ID), sam, nil, http.StatusOK, &current)
		for _, person := range []session{sam, alex} {
			j.post(t, pod.ID, current.ID, person)
		}
		j.clock.Advance(current.ClosesAt.Sub(j.clock.Now()) + time.Hour)
		if _, err := j.weekly.Tick(context.Background()); err != nil {
			t.Fatalf("tick: %v", err)
		}
		sam, alex = j.signIn("sam@example.com"), j.signIn("alex@example.com")
	}

	var graduated api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &graduated)
	for _, person := range []session{sam, alex} {
		j.do(t, http.MethodPost, fmt.Sprintf("/v1/pods/%s/continue", graduated.ID), person,
			api.ContinueRequest{Continuing: true}, http.StatusOK, nil)
	}
	if _, err := j.weekly.Tick(context.Background()); err != nil {
		t.Fatalf("settling tick: %v", err)
	}

	// One more tick, an hour later. The second cycle has six weeks to run.
	j.clock.Advance(time.Hour)
	if _, err := j.weekly.Tick(context.Background()); err != nil {
		t.Fatalf("next tick: %v", err)
	}

	sam = j.signIn("sam@example.com")
	var second api.Pod
	j.do(t, http.MethodGet, "/v1/pods/current", sam, nil, http.StatusOK, &second)
	if second.State != api.PodStateActive {
		t.Fatalf("an hour into its second cycle the pod is %q", second.State)
	}
	if second.Cycle.WeeksCompleted != 0 {
		t.Errorf("the second cycle is already %d weeks in", second.Cycle.WeeksCompleted)
	}
	if second.Cycle.WeeksEveryoneWasIn != 0 {
		t.Errorf("the second cycle reports %d weeks everyone was in, before it has had one",
			second.Cycle.WeeksEveryoneWasIn)
	}
	if !second.Cycle.EndsAt.After(graduated.Cycle.EndsAt) {
		t.Errorf("the second cycle ends at %v, not after the first's %v",
			second.Cycle.EndsAt, graduated.Cycle.EndsAt)
	}
}
