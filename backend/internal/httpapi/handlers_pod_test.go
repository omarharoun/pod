package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/cycle"
)

func TestCurrentPodIsEmptyBeforeMatching(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	rec := h.do(http.MethodGet, "/v1/pods/current", nil, session.AccessToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204 — having no pod yet is a state, not a failure", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 should have no body, got %q", rec.Body.String())
	}
}

func TestCurrentPodAfterMatching(t *testing.T) {
	h := newHarness(t)
	mine := h.signIn("sam@example.com")
	theirs := h.signIn("alex@example.com")
	podID := h.seedPod(mine.User.ID, theirs.User.ID)

	var pod api.Pod
	decode(t, h.do(http.MethodGet, "/v1/pods/current", nil, mine.AccessToken), http.StatusOK, &pod)
	if pod.ID != podID {
		t.Fatalf("pod id = %q, want %q", pod.ID, podID)
	}
	if len(pod.Members) != 2 {
		t.Fatalf("got %d members", len(pod.Members))
	}
	if pod.StreakWeeks != 0 {
		t.Errorf("a new pod starts at zero weeks, got %d", pod.StreakWeeks)
	}

	you := 0
	for _, member := range pod.Members {
		if member.IsYou {
			you++
			if member.ID != mine.User.ID {
				t.Errorf("is_you is set on the wrong member")
			}
		}
	}
	if you != 1 {
		t.Fatalf("exactly one member should be marked as you, got %d", you)
	}
}

// Pod-only visibility, always (CLAUDE.md §2 rule 5). A pod someone else belongs to must
// answer exactly as a pod that does not exist, so ids cannot be probed.
func TestAnotherPersonsPodIsIndistinguishableFromAMissingOne(t *testing.T) {
	h := newHarness(t)
	insider := h.signIn("sam@example.com")
	outsider := h.signIn("stranger@example.com")
	podID := h.seedPod(insider.User.ID)

	theirs := h.do(http.MethodGet, "/v1/pods/"+podID, nil, outsider.AccessToken)
	missing := h.do(http.MethodGet, "/v1/pods/99999999-9999-4999-8999-999999999999", nil, outsider.AccessToken)

	if theirs.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("got %d and %d, want 404 for both", theirs.Code, missing.Code)
	}
	theirsBody := decodeError(t, theirs, http.StatusNotFound)
	missingBody := decodeError(t, missing, http.StatusNotFound)
	if theirsBody.Code != missingBody.Code || theirsBody.CopyKey != missingBody.CopyKey {
		t.Fatalf("the two answers differ (%+v vs %+v) — pod ids could be probed", theirsBody, missingBody)
	}
	if outsiderPod := h.do(http.MethodGet, "/v1/pods/current", nil, outsider.AccessToken); outsiderPod.Code != http.StatusNoContent {
		t.Fatalf("the outsider's current pod = %d, want 204", outsiderPod.Code)
	}
}

func TestPodByIDForAMember(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	podID := h.seedPod(session.User.ID)

	var pod api.Pod
	decode(t, h.do(http.MethodGet, "/v1/pods/"+podID, nil, session.AccessToken), http.StatusOK, &pod)
	if pod.ID != podID {
		t.Fatalf("pod id = %q", pod.ID)
	}
}

// The pod payload is the most likely place for a scoring leak, because it is the one
// screen that shows people to each other.
func TestPodResponseExposesNoScoreAndNoIndividualStreak(t *testing.T) {
	h := newHarness(t)
	mine := h.signIn("sam@example.com")
	theirs := h.signIn("alex@example.com")
	h.seedPod(mine.User.ID, theirs.User.ID)

	rec := h.do(http.MethodGet, "/v1/pods/current", nil, mine.AccessToken)
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"reliability", "band", "score", "completion", "rate", "follower"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("pod payload contains %q: %s", forbidden, rec.Body.String())
		}
	}
	// streak_weeks is expected, and it is the only streak there is.
	if strings.Count(body, "streak") != 1 || !strings.Contains(body, "streak_weeks") {
		t.Errorf("expected exactly one streak field, pod-level: %s", rec.Body.String())
	}
}

// ---- M6: cycles -------------------------------------------------------------------

// The assertion that matters most in M6, made against the bytes on the wire rather than
// the struct: no response may ever put a member id next to a decline. "Alex is not
// continuing" is the most shaming sentence this product could show.
func TestNoPodResponseEverAttributesADecline(t *testing.T) {
	h := newHarness(t)
	sam := h.signIn("sam@example.com")
	alex := h.signIn("alex@example.com")
	remi := h.signIn("remi@example.com")
	podID := h.seedPod(sam.User.ID, alex.User.ID, remi.User.ID)

	now := h.clock.Now()
	if err := h.store.CompleteCycle(t.Context(), podID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}
	decode(t, h.do(http.MethodPost, "/v1/pods/"+podID+"/continue",
		api.ContinueRequest{Continuing: true}, sam.AccessToken), http.StatusOK, nil)
	decode(t, h.do(http.MethodPost, "/v1/pods/"+podID+"/continue",
		api.ContinueRequest{Continuing: false}, alex.AccessToken), http.StatusOK, nil)

	// Every member's own view of every pod route.
	for _, viewer := range []api.AuthSession{sam, alex, remi} {
		for _, path := range []string{"/v1/pods/current", "/v1/pods/" + podID} {
			rec := h.do(http.MethodGet, path, nil, viewer.AccessToken)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s as %s: %d — %s", path, viewer.User.ID, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			// The member list legitimately carries ids. What must not appear is a
			// decline attached to one, so this checks the continuation object alone.
			continuation := continuationJSON(t, body)
			for _, member := range []string{sam.User.ID, alex.User.ID, remi.User.ID} {
				if member == viewer.User.ID {
					continue
				}
				if strings.Contains(continuation, member) {
					t.Errorf("%s as %s leaks member %s in the continuation: %s",
						path, viewer.User.ID, member, continuation)
				}
			}
		}
	}
}

// continuationJSON pulls the continuation object back out of a serialised pod.
func continuationJSON(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Continuation json.RawMessage `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode pod: %v", err)
	}
	if len(envelope.Continuation) == 0 {
		t.Fatal("a graduated pod answered without a continuation")
	}
	return string(envelope.Continuation)
}

func TestCurrentPodCarriesItsCycle(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	h.seedPod(session.User.ID)

	var pod api.Pod
	decode(t, h.do(http.MethodGet, "/v1/pods/current", nil, session.AccessToken), http.StatusOK, &pod)
	if pod.Cycle == nil {
		t.Fatal("a pod answered without its cycle")
	}
	if pod.Cycle.LengthWeeks != cycle.DefaultLengthWeeks {
		t.Errorf("length_weeks: got %d, want %d", pod.Cycle.LengthWeeks, cycle.DefaultLengthWeeks)
	}
	if pod.Cycle.EndsAt.Before(pod.CreatedAt) {
		t.Errorf("the cycle ends at %v, before the pod was created", pod.Cycle.EndsAt)
	}
	// An active pod is not being asked anything.
	if pod.Continuation != nil {
		t.Error("an active pod was asked whether it wants another cycle")
	}
}

func TestAnsweringAnActivePodIsAConflict(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	podID := h.seedPod(session.User.ID)

	body := decodeError(t, h.do(http.MethodPost, "/v1/pods/"+podID+"/continue",
		api.ContinueRequest{Continuing: true}, session.AccessToken), http.StatusConflict)
	if body.Code != api.ErrorCodeConflict {
		t.Errorf("code: got %q, want conflict", body.Code)
	}
}

// Pod-only visibility, always (CLAUDE.md §2 rule 5): answering for a pod you are not in
// is the same answer as a pod that does not exist.
func TestAnsweringSomebodyElsesPodIsNotFound(t *testing.T) {
	h := newHarness(t)
	sam := h.signIn("sam@example.com")
	stranger := h.signIn("stranger@example.com")
	podID := h.seedPod(sam.User.ID)

	decodeError(t, h.do(http.MethodPost, "/v1/pods/"+podID+"/continue",
		api.ContinueRequest{Continuing: true}, stranger.AccessToken), http.StatusNotFound)
}
