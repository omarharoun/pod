package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/push"
	"github.com/omarharoun/pod/backend/internal/store"
)

type recordingSender struct {
	messages []push.Message
	err      error
}

func (r *recordingSender) Send(_ context.Context, message push.Message) error {
	if r.err != nil {
		return r.err
	}
	r.messages = append(r.messages, message)
	return nil
}

type harness struct {
	t       *testing.T
	service *Service
	store   *store.Memory
	clock   *clock.Fixed
	pod     store.Pod
	week    store.Week
	members []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	memory := store.NewMemory()
	fixed := clock.NewFixed(time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))
	ctx := context.Background()

	var members []string
	for _, name := range []string{"sam", "alex"} {
		user, err := memory.UpsertUserByEmail(ctx, name+"@example.com", fixed.Now())
		if err != nil {
			t.Fatalf("UpsertUserByEmail: %v", err)
		}
		if err := memory.SetDisplayName(ctx, user.ID, strings.ToUpper(name[:1])+name[1:]); err != nil {
			t.Fatalf("SetDisplayName: %v", err)
		}
		members = append(members, user.ID)
	}
	if err := memory.CreatePod(ctx, store.Pod{
		ID: "pod-1", State: store.PodStateActive, GoalCategory: "home", Timezone: "UTC", CreatedAt: fixed.Now(),
	}, store.FreeLane(members...), fixed.Now()); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	pod, err := memory.GetActivePod(ctx, members[0], 0)
	if err != nil {
		t.Fatalf("GetActivePod: %v", err)
	}
	week, err := memory.EnsureWeek(ctx, store.Week{
		ID: "week-1", PodID: pod.ID, OpensAt: fixed.Now().Add(-24 * time.Hour),
		ClosesAt: fixed.Now().Add(5 * 24 * time.Hour),
	}, members)
	if err != nil {
		t.Fatalf("EnsureWeek: %v", err)
	}

	return &harness{
		t: t, store: memory, clock: fixed, pod: pod, week: week, members: members,
		service: NewService(memory, fixed, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
}

func (h *harness) registerDevice(userID string) {
	h.t.Helper()
	if err := h.store.RegisterDevice(context.Background(), store.Device{
		TokenHash: "hash-" + userID, UserID: userID, Platform: "ios", Locale: "en-US",
		RegisteredAt: h.clock.Now(), LastSeenAt: h.clock.Now(),
	}); err != nil {
		h.t.Fatalf("RegisterDevice: %v", err)
	}
}

func (h *harness) pendingFor(userID string) []store.Notification {
	h.t.Helper()
	all, err := h.store.PendingNotifications(context.Background(), 0)
	if err != nil {
		h.t.Fatalf("PendingNotifications: %v", err)
	}
	var out []store.Notification
	for _, notification := range all {
		if notification.UserID == userID {
			out = append(out, notification)
		}
	}
	return out
}

// The rule this whole package exists to keep: nothing is ever sent about somebody not
// having posted.
func TestNoNotificationIsAboutSomebodyNotHavingPosted(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.service.PodFormed(ctx, h.pod)
	h.service.WeekOpened(ctx, h.pod, h.week, "Clear one shelf")
	h.service.MemberPosted(ctx, h.pod, h.week, h.members[0], "Sam")
	h.service.WeekClosing(ctx, h.week)
	h.service.PodCompleted(ctx, h.week, 3)

	all, err := h.store.PendingNotifications(ctx, 0)
	if err != nil {
		t.Fatalf("PendingNotifications: %v", err)
	}
	for _, notification := range all {
		title, body, err := push.Resolve(copy.Key(notification.CopyKey), fullParams(notification.Params))
		if err != nil {
			t.Fatalf("resolve %q: %v", notification.CopyKey, err)
		}
		text := strings.ToLower(title + " " + body)
		for _, forbidden := range []string{
			"hasn't", "haven't", "has not", "have not", "still", "waiting on",
			"missed", "failed", "don't let", "remind", "chase", "0 of", "1 of",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s says %q: %s", notification.Kind, forbidden, text)
			}
		}
	}
}

// The week-closing notice goes to everyone, including people who have already posted.
// Sending it only to those still open would make it a message about not having posted,
// addressed to exactly the person who would feel it that way.
func TestTheClosingNoticeGoesToEveryone(t *testing.T) {
	h := newHarness(t)
	h.service.WeekClosing(context.Background(), h.week)

	for _, member := range h.members {
		if len(h.pendingFor(member)) != 1 {
			t.Fatalf("member %s got %d closing notices, want 1", member, len(h.pendingFor(member)))
		}
	}
}

func TestMemberPostedTellsTheOthersAndNotThePoster(t *testing.T) {
	h := newHarness(t)
	h.service.MemberPosted(context.Background(), h.pod, h.week, h.members[0], "Sam")

	if got := len(h.pendingFor(h.members[0])); got != 0 {
		t.Errorf("the poster was told about their own post %d times", got)
	}
	others := h.pendingFor(h.members[1])
	if len(others) != 1 {
		t.Fatalf("the other member got %d notifications, want 1", len(others))
	}
	if others[0].Params["name"] != "Sam" {
		t.Errorf("params: %+v", others[0].Params)
	}
}

// The job runs hourly; the same week must not produce a notification every hour.
func TestQueueingTheSameThingTwiceIsANoOp(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		h.service.WeekOpened(ctx, h.pod, h.week, "Clear one shelf")
	}
	if got := len(h.pendingFor(h.members[0])); got != 1 {
		t.Fatalf("got %d notifications after five passes, want 1", got)
	}
}

// Two pod-mates posting in the same week are two notifications, and the same person
// posting next week is another one.
func TestPostNotificationsAreKeyedByWeekAndPoster(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.service.MemberPosted(ctx, h.pod, h.week, h.members[0], "Sam")
	h.service.MemberPosted(ctx, h.pod, h.week, h.members[1], "Alex")
	if got := len(h.pendingFor(h.members[0])) + len(h.pendingFor(h.members[1])); got != 2 {
		t.Fatalf("two posts produced %d notifications, want 2", got)
	}

	nextWeek := h.week
	nextWeek.ID = "week-2"
	h.service.MemberPosted(ctx, h.pod, nextWeek, h.members[0], "Sam")
	if got := len(h.pendingFor(h.members[1])); got != 2 {
		t.Fatalf("the same person posting next week produced %d notifications for their pod-mate, want 2", got)
	}
}

func TestSendResolvesFromTheCatalogue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.registerDevice(h.members[1])
	h.service.MemberPosted(ctx, h.pod, h.week, h.members[0], "Sam")

	sender := &recordingSender{}
	result, err := h.service.Send(ctx, sender, 0)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Sent != 1 {
		t.Fatalf("got %+v", result)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("got %d messages", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0].Title, "Sam") {
		t.Errorf("title = %q", sender.messages[0].Title)
	}
	if sender.messages[0].Body == "" {
		t.Error("empty body")
	}

	// And nothing is sent twice.
	second, err := h.service.Send(ctx, sender, 0)
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if second.Sent != 0 || len(sender.messages) != 1 {
		t.Fatalf("a second pass re-sent: %+v", second)
	}
}

// Plenty of people will never grant push. The product has to work for them, and their
// notifications must not jam the outbox forever.
func TestAPersonWithNoDeviceIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.service.WeekOpened(ctx, h.pod, h.week, "Clear one shelf")

	sender := &recordingSender{}
	result, err := h.service.Send(ctx, sender, 0)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.NoDevices != 2 || result.Failed != 0 || result.Sent != 0 {
		t.Fatalf("got %+v", result)
	}
	if remaining, _ := h.store.PendingNotifications(ctx, 0); len(remaining) != 0 {
		t.Fatalf("%d notifications are still queued for people with no phone", len(remaining))
	}
}

// A token the provider has rejected is retired rather than retried forever.
func TestARetiredTokenIsDropped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.registerDevice(h.members[1])
	h.service.MemberPosted(ctx, h.pod, h.week, h.members[0], "Sam")

	if _, err := h.service.Send(ctx, &recordingSender{err: push.ErrTokenRetired}, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	devices, err := h.store.DevicesFor(ctx, h.members[1])
	if err != nil {
		t.Fatalf("DevicesFor: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("a rejected token survived: %+v", devices)
	}
}

func TestTheClosingNoticeIsDueOnlyInTheLastDay(t *testing.T) {
	h := newHarness(t)
	if h.service.DueForClosingNotice(h.week) {
		t.Error("due five days out")
	}
	h.clock.Advance(h.week.ClosesAt.Sub(h.clock.Now()) - 2*time.Hour)
	if !h.service.DueForClosingNotice(h.week) {
		t.Error("not due two hours out")
	}
	h.clock.Advance(3 * time.Hour)
	if h.service.DueForClosingNotice(h.week) {
		t.Error("still due after the week closed")
	}
}

// A key that will not resolve will not resolve on a retry either.
func TestAnUnresolvableNotificationIsFailedRatherThanRetriedForever(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.registerDevice(h.members[0])
	if err := h.store.QueueNotification(ctx, store.Notification{
		ID: "n-1", UserID: h.members[0], Kind: "bogus", SubjectID: "s-1",
		CopyKey: "push.notAThing", CreatedAt: h.clock.Now(),
	}); err != nil {
		t.Fatalf("QueueNotification: %v", err)
	}

	result, err := h.service.Send(ctx, &recordingSender{}, 0)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("got %+v", result)
	}
	if remaining, _ := h.store.PendingNotifications(ctx, 0); len(remaining) != 0 {
		t.Fatal("the unresolvable notification is still jamming the outbox")
	}
}

func fullParams(params map[string]string) map[string]string {
	out := map[string]string{"name": "Sam", "names": "Alex", "action": "Clear one shelf", "count": "3"}
	for k, v := range params {
		out[k] = v
	}
	return out
}

var _ = errors.Is
