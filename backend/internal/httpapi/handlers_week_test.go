package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/store"
)

// weekHarness is a pod of two with an open week.
type weekHarness struct {
	*harness
	mine   api.AuthSession
	theirs api.AuthSession
	podID  string
	week   api.Week
}

func newWeekHarness(t *testing.T) *weekHarness {
	t.Helper()
	h := newHarness(t)
	mine := h.signIn("sam@example.com")
	theirs := h.signIn("alex@example.com")
	podID := h.seedPod(mine.User.ID, theirs.User.ID)

	w := &weekHarness{harness: h, mine: mine, theirs: theirs, podID: podID}
	decode(t, h.do(http.MethodGet, "/v1/pods/"+podID+"/weeks/current", nil, mine.AccessToken), http.StatusOK, &w.week)
	return w
}

func (w *weekHarness) currentWeek(token string) api.Week {
	w.t.Helper()
	var out api.Week
	decode(w.t, w.do(http.MethodGet, "/v1/pods/"+w.podID+"/weeks/current", nil, token), http.StatusOK, &out)
	return out
}

// post walks the whole two-step upload: slot, bytes, commit.
func (w *weekHarness) post(token string) api.Proof {
	w.t.Helper()
	var upload api.ProofUpload
	decode(w.t, w.do(http.MethodPost, w.proofPath(), api.ProofUploadRequest{
		Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 2048,
	}, token), http.StatusCreated, &upload)

	// The bytes go straight to storage, never through the API (ADR-008).
	if err := w.blobs.Put(blob.Key(w.week.ID, upload.ProofID), strings.NewReader("ten seconds of proof")); err != nil {
		w.t.Fatalf("upload: %v", err)
	}

	var proof api.Proof
	decode(w.t, w.do(http.MethodPost, w.proofPath()+"/"+upload.ProofID+"/commit", nil, token), http.StatusOK, &proof)
	return proof
}

func (w *weekHarness) proofPath() string {
	return "/v1/pods/" + w.podID + "/weeks/" + w.week.ID + "/proof"
}

func (w *weekHarness) actionPath() string {
	return "/v1/pods/" + w.podID + "/weeks/" + w.week.ID + "/action"
}

// ------------------------------------------------------------------ the week

// A pod matched at 09:15 should not stare at an empty screen until the job next runs.
func TestCurrentWeekOpensItselfOnFirstRead(t *testing.T) {
	w := newWeekHarness(t)
	if w.week.Index != 1 {
		t.Fatalf("first week index = %d, want 1", w.week.Index)
	}
	if !w.week.ClosesAt.After(w.week.OpensAt) {
		t.Fatalf("week closes %v, opens %v", w.week.ClosesAt, w.week.OpensAt)
	}
	if len(w.week.Members) != 2 {
		t.Fatalf("got %d member rows, want one per pod member", len(w.week.Members))
	}
	for _, member := range w.week.Members {
		if member.State != api.MemberWeekStateOpen {
			t.Errorf("a fresh week starts everyone open, got %q", member.State)
		}
	}
	if w.week.PodCompleted {
		t.Error("a fresh week is not completed")
	}

	// Reading twice must not open a second week.
	second := w.currentWeek(w.mine.AccessToken)
	if second.ID != w.week.ID {
		t.Fatalf("a second read opened a different week: %q then %q", w.week.ID, second.ID)
	}
}

// A week always has an action, so the loop never blocks on a decision nobody made.
func TestAFreshWeekCarriesItsDefaultAction(t *testing.T) {
	w := newWeekHarness(t)
	if !w.week.ActionIsDefault {
		t.Fatal("a fresh week should be carrying the default")
	}
	if w.week.ActionText == "" {
		t.Fatal("action_text is empty — the client would render a blank week")
	}
	expected, err := copy.Resolve(copy.KeyWeekDefaultAction, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if w.week.ActionText != expected {
		t.Fatalf("default action = %q, want the catalogue's %q", w.week.ActionText, expected)
	}
	if !w.week.ActionEditable {
		t.Fatal("a fresh week's action should be editable")
	}
}

func TestAnyMemberCanNameTheWeeksAction(t *testing.T) {
	w := newWeekHarness(t)

	var updated api.Week
	decode(t, w.do(http.MethodPut, w.actionPath(), api.WeekActionRequest{ActionText: "Clear one shelf"}, w.theirs.AccessToken), http.StatusOK, &updated)
	if updated.ActionText != "Clear one shelf" || updated.ActionIsDefault {
		t.Fatalf("got %+v", updated)
	}
	// And the other member sees it.
	if mine := w.currentWeek(w.mine.AccessToken); mine.ActionText != "Clear one shelf" {
		t.Fatalf("the other member sees %q", mine.ActionText)
	}
}

// Editing after somebody has acted would rewrite what they acted on.
func TestTheActionLocksOnceSomeoneHasPosted(t *testing.T) {
	w := newWeekHarness(t)
	decode(t, w.do(http.MethodPut, w.actionPath(), api.WeekActionRequest{ActionText: "Clear one shelf"}, w.mine.AccessToken), http.StatusOK, nil)
	w.post(w.mine.AccessToken)

	body := decodeError(t, w.do(http.MethodPut, w.actionPath(), api.WeekActionRequest{ActionText: "Something else"}, w.theirs.AccessToken), http.StatusConflict)
	if body.CopyKey != string(copy.KeyErrorActionLockedBody) {
		t.Errorf("copy_key = %q", body.CopyKey)
	}
	if after := w.currentWeek(w.mine.AccessToken); after.ActionText != "Clear one shelf" {
		t.Fatalf("the action changed to %q after someone had posted", after.ActionText)
	}
	if after := w.currentWeek(w.mine.AccessToken); after.ActionEditable {
		t.Error("action_editable should be false once someone has posted")
	}
}

func TestActionValidation(t *testing.T) {
	w := newWeekHarness(t)
	for name, text := range map[string]string{
		"too short": "no",
		"blank":     "   ",
		"too long":  strings.Repeat("a", 200),
	} {
		t.Run(name, func(t *testing.T) {
			decodeError(t, w.do(http.MethodPut, w.actionPath(), api.WeekActionRequest{ActionText: text}, w.mine.AccessToken), http.StatusBadRequest)
		})
	}
}

// ----------------------------------------------------------------- the proof

func TestPostingProofMovesOnlyYourOwnState(t *testing.T) {
	w := newWeekHarness(t)
	proof := w.post(w.mine.AccessToken)

	if proof.ViewURL == nil || *proof.ViewURL == "" {
		t.Fatal("a committed proof should come back with a view link")
	}
	if !proof.ExpiresAt.Equal(w.week.ClosesAt) {
		t.Fatalf("proof expires %v, week closes %v — proof dies with its week", proof.ExpiresAt, w.week.ClosesAt)
	}
	// The view link must expire well before the proof does: it is the real leak window.
	if proof.ViewURLExpiresAt == nil || !proof.ViewURLExpiresAt.Before(proof.ExpiresAt) {
		t.Fatalf("view link expiry %v is not before the proof's %v", proof.ViewURLExpiresAt, proof.ExpiresAt)
	}

	after := w.currentWeek(w.mine.AccessToken)
	for _, member := range after.Members {
		want := api.MemberWeekStateOpen
		if member.MemberID == w.mine.User.ID {
			want = api.MemberWeekStatePosted
		}
		if member.State != want {
			t.Errorf("member %s state = %q, want %q", member.MemberID, member.State, want)
		}
	}
	if after.PodCompleted {
		t.Error("one of two posting is not a completed pod week")
	}
}

func TestPostingTwiceInAWeekIsRefusedKindly(t *testing.T) {
	w := newWeekHarness(t)
	w.post(w.mine.AccessToken)

	rec := w.do(http.MethodPost, w.proofPath(), api.ProofUploadRequest{
		Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 2048,
	}, w.mine.AccessToken)
	body := decodeError(t, rec, http.StatusConflict)
	if body.CopyKey != string(copy.KeyErrorAlreadyPostedBody) {
		t.Errorf("copy_key = %q", body.CopyKey)
	}
}

// Committing a slot nothing was uploaded to would show the pod an empty tile.
func TestCommittingAnEmptySlotIsRefused(t *testing.T) {
	w := newWeekHarness(t)
	var upload api.ProofUpload
	decode(t, w.do(http.MethodPost, w.proofPath(), api.ProofUploadRequest{
		Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 2048,
	}, w.mine.AccessToken), http.StatusCreated, &upload)

	decodeError(t, w.do(http.MethodPost, w.proofPath()+"/"+upload.ProofID+"/commit", nil, w.mine.AccessToken), http.StatusConflict)

	// And an abandoned slot leaves the person open, free to try again.
	after := w.currentWeek(w.mine.AccessToken)
	for _, member := range after.Members {
		if member.MemberID == w.mine.User.ID && member.State != api.MemberWeekStateOpen {
			t.Fatalf("an abandoned upload moved the member to %q", member.State)
		}
	}
}

func TestProofUploadValidation(t *testing.T) {
	w := newWeekHarness(t)
	tooLong := 30000
	cases := map[string]struct {
		request api.ProofUploadRequest
		status  int
	}{
		"unknown kind":       {api.ProofUploadRequest{Kind: api.ProofKind("video"), ContentType: "video/mp4", ByteSize: 10}, http.StatusBadRequest},
		"wrong content type": {api.ProofUploadRequest{Kind: api.ProofKindPhoto, ContentType: "application/pdf", ByteSize: 10}, http.StatusBadRequest},
		"zero bytes":         {api.ProofUploadRequest{Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 0}, http.StatusBadRequest},
		"enormous":           {api.ProofUploadRequest{Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 100 << 20}, http.StatusBadRequest},
		"longer than ten seconds": {api.ProofUploadRequest{
			Kind: api.ProofKindVoice, ContentType: "audio/m4a", ByteSize: 1024, DurationMs: &tooLong,
		}, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			decodeError(t, w.do(http.MethodPost, w.proofPath(), tc.request, w.mine.AccessToken), tc.status)
		})
	}
}

// Pod-only visibility, always (CLAUDE.md §2 rule 5).
func TestAnOutsiderCanSeeNothingOfTheWeek(t *testing.T) {
	w := newWeekHarness(t)
	proof := w.post(w.mine.AccessToken)
	outsider := w.signIn("stranger@example.com")

	for name, path := range map[string]string{
		"the week":    "/v1/pods/" + w.podID + "/weeks/current",
		"the history": "/v1/pods/" + w.podID + "/weeks",
		"the proof":   w.proofPath() + "/" + proof.ID,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := w.do(http.MethodGet, path, nil, outsider.AccessToken); rec.Code != http.StatusNotFound {
				t.Fatalf("got %d, want 404 — body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A pod-mate can see your proof. That is the entire product.
func TestAPodMateCanSeeYourProof(t *testing.T) {
	w := newWeekHarness(t)
	proof := w.post(w.mine.AccessToken)

	var seen api.Proof
	decode(t, w.do(http.MethodGet, w.proofPath()+"/"+proof.ID, nil, w.theirs.AccessToken), http.StatusOK, &seen)
	if seen.ID != proof.ID || seen.ViewURL == nil {
		t.Fatalf("got %+v", seen)
	}
}

// -------------------------------------------------------- the promises in §2

// Nothing about a week may carry a per-member verdict or a score.
func TestWeekPayloadCarriesNoVerdictAndNoScore(t *testing.T) {
	w := newWeekHarness(t)
	w.post(w.mine.AccessToken)

	rec := w.do(http.MethodGet, "/v1/pods/"+w.podID+"/weeks/current", nil, w.mine.AccessToken)
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"missed", "failed", "failure", "broken", "reliability", "score", "band", "completion_rate", "streak"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("week payload contains %q: %s", forbidden, rec.Body.String())
		}
	}
}

// A member who did not post is resting when the week closes. Not missed, not failed.
func TestClosingAWeekRestsWhoeverDidNotPost(t *testing.T) {
	w := newWeekHarness(t)
	w.post(w.mine.AccessToken)

	closed, err := w.store.CloseWeek(context.Background(), w.week.ID, w.clock.Now())
	if err != nil {
		t.Fatalf("CloseWeek: %v", err)
	}
	states := map[string]store.MemberWeekState{}
	for _, member := range closed.Members {
		states[member.UserID] = member.State
	}
	if states[w.mine.User.ID] != store.MemberWeekPosted {
		t.Errorf("the poster is %q", states[w.mine.User.ID])
	}
	if states[w.theirs.User.ID] != store.MemberWeekResting {
		t.Errorf("the non-poster is %q, want resting", states[w.theirs.User.ID])
	}
	if closed.PodCompleted {
		t.Error("one of two is not a completed pod week")
	}
}

// The streak belongs to the pod, and it moves only when everyone was in.
func TestTheStreakIsPodLevelAndMovesOnlyOnAFullWeek(t *testing.T) {
	w := newWeekHarness(t)
	w.post(w.mine.AccessToken)
	w.post(w.theirs.AccessToken)

	full := w.currentWeek(w.mine.AccessToken)
	if !full.PodCompleted {
		t.Fatal("everyone posted but pod_completed is false")
	}

	if _, err := w.store.CloseWeek(context.Background(), w.week.ID, w.clock.Now()); err != nil {
		t.Fatalf("CloseWeek: %v", err)
	}
	var pod api.Pod
	decode(t, w.do(http.MethodGet, "/v1/pods/current", nil, w.mine.AccessToken), http.StatusOK, &pod)
	if pod.StreakWeeks != 1 {
		t.Fatalf("streak_weeks = %d, want 1", pod.StreakWeeks)
	}
}

// Ephemerality is a promise, not a cleanup job (CLAUDE.md §2 rule 4).
func TestAProofIsGoneOnceItsWeekHasClosed(t *testing.T) {
	w := newWeekHarness(t)
	proof := w.post(w.mine.AccessToken)

	// Past the week's close, the proof is gone whether or not the sweep has run.
	// Crossing a week boundary also outruns the access token, so both sign in again.
	w.clock.Advance(w.week.ClosesAt.Sub(w.clock.Now()) + time.Minute)
	mine := w.signIn("sam@example.com")
	theirs := w.signIn("alex@example.com")

	if rec := w.do(http.MethodGet, w.proofPath()+"/"+proof.ID, nil, mine.AccessToken); rec.Code != http.StatusNotFound {
		t.Fatalf("the proof survived its week: %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.do(http.MethodGet, w.proofPath()+"/"+proof.ID, nil, theirs.AccessToken); rec.Code != http.StatusNotFound {
		t.Fatalf("a pod-mate could still read it: %d", rec.Code)
	}
}

func TestPostingToAClosedWeekIsRefused(t *testing.T) {
	w := newWeekHarness(t)
	w.clock.Advance(w.week.ClosesAt.Sub(w.clock.Now()) + time.Minute)
	mine := w.signIn("sam@example.com")

	body := decodeError(t, w.do(http.MethodPost, w.proofPath(), api.ProofUploadRequest{
		Kind: api.ProofKindPhoto, ContentType: "image/jpeg", ByteSize: 2048,
	}, mine.AccessToken), http.StatusConflict)
	if body.CopyKey != string(copy.KeyErrorWeekClosedBody) {
		t.Errorf("copy_key = %q", body.CopyKey)
	}
}

// Pod-only visibility, always (CLAUDE.md §2 rule 5). A pod id belonging to someone else
// must answer exactly as a pod id that does not exist.
//
// The week list used to fail open: its membership check was a JOIN, and a stranger simply
// matched no rows, so they got 200 with an empty list — a working existence oracle for pod
// ids, and the one endpoint where the check was structural rather than explicit.
func TestListingSomebodyElsesWeeksIsNotFound(t *testing.T) {
	h := newHarness(t)
	sam := h.signIn("sam@example.com")
	stranger := h.signIn("stranger@example.com")
	ids := h.seedWeekWithProof(sam.User.ID)

	rec := h.do(http.MethodGet, "/v1/pods/"+ids.pod+"/weeks", nil, stranger.AccessToken)
	if rec.Code == http.StatusOK {
		t.Fatalf("a stranger listed a pod's weeks: %s", rec.Body.String())
	}
	decodeError(t, rec, http.StatusNotFound)
}

// And a member of a pod that has no weeks yet gets an empty list, not a 404. The two
// cases are different and were previously the same.
func TestAMemberOfAPodWithNoWeeksGetsAnEmptyList(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	podID := h.seedPod(session.User.ID)

	var body api.WeekList
	decode(t, h.do(http.MethodGet, "/v1/pods/"+podID+"/weeks", nil, session.AccessToken), http.StatusOK, &body)
	if len(body.Weeks) != 0 {
		t.Errorf("a pod with no weeks listed %d", len(body.Weeks))
	}
}
