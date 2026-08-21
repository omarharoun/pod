// Package notify decides what a pod is told, and when.
//
// The rules here are the product's, not the delivery mechanism's. CLAUDE.md §2 rule 1
// governs every one of them, and the constraint it produces is sharper than it sounds:
// **there is no notification that names who has not posted.** Not "Alex hasn't posted
// yet", not "2 of 3 are in", not a reminder addressed only to the people still open. A
// notification that arrives only for the person who has not acted is a nudge with their
// name on it, and it is the exact mechanism this product was built to avoid.
//
// Everything sent is addressed to the whole pod, or is about something that happened
// rather than something that did not.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/ids"
	"github.com/omarharoun/pod/backend/internal/push"
	"github.com/omarharoun/pod/backend/internal/store"
)

// Kind is what a notification is about. The set is closed on purpose: adding one is a
// product decision, and the compiler should make you come here to make it.
type Kind string

const (
	// KindPodFormed — you have been matched.
	KindPodFormed Kind = "pod_formed"
	// KindWeekOpened — a new week, with its action.
	KindWeekOpened Kind = "week_opened"
	// KindMemberPosted — somebody in your pod is in. This is the product's core loop:
	// low-stakes social witness, arriving as it happens.
	KindMemberPosted Kind = "member_posted"
	// KindWeekClosing — the week closes tomorrow. Sent to everyone, including people who
	// have already posted, precisely so it is not a message about not having posted.
	KindWeekClosing Kind = "week_closing"
	// KindPodCompleted — everyone was in.
	KindPodCompleted Kind = "pod_completed"
	// KindCycleCompleted — the pod reached the end of its cycle. Sent to everyone,
	// whatever their week history: graduation is the pod's, not any member's
	// (CLAUDE.md §2 rule 3).
	KindCycleCompleted Kind = "cycle_completed"
	// KindCycleContinuing — enough people said yes, so the pod carries on. There is
	// deliberately no counterpart for "the pod closed": telling someone their pod
	// wound down because others declined is a statement about those others, and a
	// notification is the worst place to make it. They find it when they open the app.
	KindCycleContinuing Kind = "cycle_continuing"
)

// closingNotice is how far ahead of a week's close the whole pod is told.
const closingNotice = 24 * time.Hour

type Service struct {
	store store.Store
	clock clock.Clock
	log   *slog.Logger
}

func NewService(s store.Store, c clock.Clock, log *slog.Logger) *Service {
	return &Service{store: s, clock: c, log: log}
}

// PodFormed tells a new pod's members about each other.
func (s *Service) PodFormed(ctx context.Context, pod store.Pod) {
	for _, member := range pod.Members {
		others := otherNames(pod, member.UserID)
		if others == "" {
			continue
		}
		s.queue(ctx, KindPodFormed, pod.ID, member.UserID, copy.KeyPushPodFormedTitle,
			map[string]string{"names": others})
	}
}

// WeekOpened tells the pod what this week's one thing is.
func (s *Service) WeekOpened(ctx context.Context, pod store.Pod, week store.Week, actionText string) {
	for _, member := range week.Members {
		s.queue(ctx, KindWeekOpened, week.ID, member.UserID, copy.KeyPushWeekOpenedTitle,
			map[string]string{"action": actionText})
	}
}

// MemberPosted tells the *others* that somebody is in.
//
// Not the poster: they were there. And nothing about who has not posted.
func (s *Service) MemberPosted(ctx context.Context, pod store.Pod, week store.Week, posterID, posterName string) {
	for _, member := range week.Members {
		if member.UserID == posterID {
			continue
		}
		s.queue(ctx, KindMemberPosted, subjectOf(week.ID, posterID), member.UserID,
			copy.KeyPushMemberPostedTitle, map[string]string{"name": posterName})
	}
}

// WeekClosing tells the whole pod, a day out.
//
// Everyone, including people who have already posted. Sending only to those still open
// would make it a message about not having posted, addressed to exactly the person who
// would feel it that way.
func (s *Service) WeekClosing(ctx context.Context, week store.Week) {
	for _, member := range week.Members {
		s.queue(ctx, KindWeekClosing, week.ID, member.UserID, copy.KeyPushWeekClosingTitle, nil)
	}
}

// PodCompleted tells the pod it got there, with the shared count.
func (s *Service) PodCompleted(ctx context.Context, week store.Week, streakWeeks int) {
	for _, member := range week.Members {
		s.queue(ctx, KindPodCompleted, week.ID, member.UserID, copy.KeyPushPodCompletedTitle,
			map[string]string{"count": itoa(streakWeeks)})
	}
}

// CycleCompleted announces graduation to the whole pod.
//
// The same message to everyone, with no per-member number in it. Someone who posted twice
// in six weeks reached the end of the cycle exactly as much as someone who posted six
// times, and this notification is not the place to disagree.
func (s *Service) CycleCompleted(ctx context.Context, pod store.Pod) {
	for _, member := range pod.Members {
		s.queue(ctx, KindCycleCompleted, pod.ID, member.UserID, copy.KeyPushCycleCompletedTitle,
			map[string]string{"count": itoa(cycleLengthOf(pod))})
	}
}

// CycleContinuing tells the pod it is carrying on.
func (s *Service) CycleContinuing(ctx context.Context, pod store.Pod) {
	for _, member := range pod.Members {
		s.queue(ctx, KindCycleContinuing, pod.ID, member.UserID, copy.KeyPushCycleContinuingTitle, nil)
	}
}

func cycleLengthOf(pod store.Pod) int {
	if pod.CycleLengthWeeks > 0 {
		return pod.CycleLengthWeeks
	}
	return cycle.DefaultLengthWeeks
}

// DueForClosingNotice reports whether a week has crossed into its last day.
func (s *Service) DueForClosingNotice(week store.Week) bool {
	now := s.clock.Now()
	return week.ClosedAt == nil &&
		now.Before(week.ClosesAt) &&
		!now.Before(week.ClosesAt.Add(-closingNotice))
}

// queue writes one notification to the outbox. A duplicate is the normal case for an
// hourly job and is not worth a log line.
func (s *Service) queue(ctx context.Context, kind Kind, subjectID, userID string, key copy.Key, params map[string]string) {
	id, err := ids.NewUUID()
	if err != nil {
		s.log.ErrorContext(ctx, "could not mint a notification id", slog.Any("error", err))
		return
	}
	// The stored key is the base; push.Resolve appends .title and .body.
	base := strings.TrimSuffix(string(key), ".title")

	err = s.store.QueueNotification(ctx, store.Notification{
		ID:        id,
		UserID:    userID,
		Kind:      string(kind),
		SubjectID: subjectID,
		CopyKey:   base,
		Params:    params,
		CreatedAt: s.clock.Now(),
	})
	if err != nil && !errors.Is(err, store.ErrConflict) {
		s.log.ErrorContext(ctx, "could not queue a notification",
			slog.String("kind", string(kind)), slog.Any("error", err))
	}
}

// SendResult is what one delivery pass did.
type SendResult struct {
	Sent      int
	NoDevices int
	Failed    int
}

// Send delivers everything queued.
//
// A person with no registered device is not a failure: plenty of people will never grant
// push, and the product has to work for them. Their notification is marked done and
// nothing is retried forever.
func (s *Service) Send(ctx context.Context, sender push.Sender, limit int) (SendResult, error) {
	pending, err := s.store.PendingNotifications(ctx, limit)
	if err != nil {
		return SendResult{}, fmt.Errorf("notify: read outbox: %w", err)
	}

	result := SendResult{}
	now := s.clock.Now()
	for _, notification := range pending {
		devices, err := s.store.DevicesFor(ctx, notification.UserID)
		if err != nil {
			return result, fmt.Errorf("notify: read devices: %w", err)
		}
		if len(devices) == 0 {
			result.NoDevices++
			_ = s.store.MarkNotificationSent(ctx, notification.ID, now, false)
			continue
		}

		title, body, err := push.Resolve(copy.Key(notification.CopyKey), notification.Params)
		if err != nil {
			// A key that will not resolve will not resolve on a retry either. Mark it
			// failed rather than jamming the outbox behind it.
			s.log.ErrorContext(ctx, "notification copy did not resolve",
				slog.String("copy_key", notification.CopyKey), slog.Any("error", err))
			result.Failed++
			_ = s.store.MarkNotificationSent(ctx, notification.ID, now, true)
			continue
		}

		delivered := false
		for _, device := range devices {
			err := sender.Send(ctx, push.Message{
				Token:    device.TokenHash,
				Platform: push.Platform(device.Platform),
				Title:    title,
				Body:     body,
				Thread:   notification.SubjectID,
			})
			switch {
			case errors.Is(err, push.ErrTokenRetired):
				if err := s.store.RetireDevice(ctx, device.TokenHash, now); err != nil {
					s.log.ErrorContext(ctx, "could not retire a token", slog.Any("error", err))
				}
			case err != nil:
				s.log.ErrorContext(ctx, "push failed", slog.Any("error", err))
			default:
				delivered = true
			}
		}

		if delivered {
			result.Sent++
		} else {
			result.Failed++
		}
		_ = s.store.MarkNotificationSent(ctx, notification.ID, now, !delivered)
	}
	return result, nil
}

// subjectOf keys a per-poster, per-week notification.
//
// Both halves are needed. The week alone would collapse two pod-mates' posts into one
// notification; the poster alone would suppress their post in every week after the first.
func subjectOf(weekID, posterID string) string {
	return weekID + ":" + posterID
}

func otherNames(pod store.Pod, exclude string) string {
	var names []string
	for _, member := range pod.Members {
		if member.UserID == exclude || member.DisplayName == "" {
			continue
		}
		names = append(names, member.DisplayName)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return names[0] + " and " + names[1]
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
