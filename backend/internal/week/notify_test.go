package week

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/notify"
	"github.com/omarharoun/pod/backend/internal/push"
	"github.com/omarharoun/pod/backend/internal/store"
)

func (h *harness) pending() []store.Notification {
	h.t.Helper()
	out, err := h.store.PendingNotifications(context.Background(), 0)
	if err != nil {
		h.t.Fatalf("PendingNotifications: %v", err)
	}
	return out
}

func (h *harness) kindsFor(userID string) []string {
	h.t.Helper()
	var kinds []string
	for _, notification := range h.pending() {
		if notification.UserID == userID {
			kinds = append(kinds, notification.Kind)
		}
	}
	return kinds
}

func TestPostingTellsTheOtherMemberAndNotThePoster(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	week := h.currentWeek()
	h.post(week.ID, h.members[0])

	if kinds := h.kindsFor(h.members[0]); contains(kinds, string(notify.KindMemberPosted)) {
		t.Errorf("the poster was told about their own post: %v", kinds)
	}
	if kinds := h.kindsFor(h.members[1]); !contains(kinds, string(notify.KindMemberPosted)) {
		t.Errorf("the other member was not told: %v", kinds)
	}
}

// The last person to post completes the pod, and that is the more interesting fact — so
// everyone gets the completion, including the person who just finished it.
func TestTheLastPostAnnouncesTheWholePodRatherThanOnePerson(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	week := h.currentWeek()
	h.post(week.ID, h.members[0])
	h.post(week.ID, h.members[1])

	for _, member := range h.members {
		if !contains(h.kindsFor(member), string(notify.KindPodCompleted)) {
			t.Errorf("member %s was not told the pod completed: %v", member, h.kindsFor(member))
		}
	}
}

// Opening a week tells the pod what the one thing is, once — not once an hour.
func TestOpeningAWeekAnnouncesItExactlyOnce(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := h.job.Tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	// Ticks drain the outbox, so count what was sent rather than what is queued.
	opened := 0
	for _, notification := range h.pending() {
		if notification.Kind == string(notify.KindWeekOpened) {
			opened++
		}
	}
	if opened != 0 {
		t.Fatalf("%d week-opened notifications are still queued after three ticks", opened)
	}
}

// A week that did not complete produces no notification at all. Nothing went wrong, so
// nothing reports that it did (CLAUDE.md §2 rule 1).
func TestAnIncompleteWeekClosesSilently(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	week := h.currentWeek()
	h.post(week.ID, h.members[0])

	// Drain everything the post queued, so what is left is what closing produced.
	if _, err := h.service.notifier.Send(ctx, push.Logger{Log: discardLogger()}, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	if _, err := h.job.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, notification := range h.pending() {
		if notification.Kind == string(notify.KindPodCompleted) {
			t.Fatalf("an incomplete week announced a completion")
		}
	}
}

// Nothing the server can send may be about somebody not having posted.
func TestNothingTheWeekLoopSendsIsAboutNotPosting(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	week := h.currentWeek()
	h.post(week.ID, h.members[0])
	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) - 2*time.Hour)
	if _, err := h.job.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, notification := range h.pending() {
		title, body, err := push.Resolve(copy.Key(notification.CopyKey), map[string]string{
			"name": "Sam", "names": "Alex", "action": "Clear one shelf", "count": "1",
		})
		if err != nil {
			t.Fatalf("resolve %q: %v", notification.CopyKey, err)
		}
		text := strings.ToLower(title + " " + body)
		for _, forbidden := range []string{"hasn't", "haven't", "has not", "still waiting", "missed", "remind"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s says %q: %s", notification.Kind, forbidden, text)
			}
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
