package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-process Store for tests. It is deliberately strict about the same
// invariants Postgres enforces — atomic attempt counting, single-use rotation — so that a
// test passing here means something.
type Memory struct {
	mu         sync.Mutex
	users      map[string]User
	usersByKey map[string]string // normalised email -> user id
	challenges map[string]AuthChallenge
	tokens     map[string]RefreshToken
	intents    map[string]Intent
	pool       map[string]PoolTicket
	pods       map[string]Pod
	podMembers map[string][]PodMember
	nextID     int

	// FailPing makes Ping report the datastore as unreachable, for health tests.
	FailPing error
}

func NewMemory() *Memory {
	return &Memory{
		users:      map[string]User{},
		usersByKey: map[string]string{},
		challenges: map[string]AuthChallenge{},
		tokens:     map[string]RefreshToken{},
		intents:    map[string]Intent{},
		pool:       map[string]PoolTicket{},
		pods:       map[string]Pod{},
		podMembers: map[string][]PodMember{},
	}
}

// NewID hands out deterministic identifiers so test failures are readable.
func (m *Memory) NewID(prefix string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return prefix + "-" + itoa(m.nextID)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func (m *Memory) Ping(context.Context) error { return m.FailPing }

func (m *Memory) CreateAuthChallenge(_ context.Context, c AuthChallenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.challenges[c.ID] = c
	return nil
}

func (m *Memory) GetAuthChallenge(_ context.Context, id string) (AuthChallenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok {
		return AuthChallenge{}, ErrNotFound
	}
	return c, nil
}

func (m *Memory) RecordChallengeAttempt(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok {
		return 0, ErrNotFound
	}
	c.Attempts++
	m.challenges[id] = c
	return c.Attempts, nil
}

func (m *Memory) ConsumeAuthChallenge(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok {
		return ErrNotFound
	}
	if c.ConsumedAt != nil {
		return ErrConflict
	}
	c.ConsumedAt = &at
	m.challenges[id] = c
	return nil
}

func (m *Memory) CountRecentChallenges(_ context.Context, email string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := NormaliseEmail(email)
	n := 0
	for _, c := range m.challenges {
		if NormaliseEmail(c.Email) == key && c.CreatedAt.After(since) {
			n++
		}
	}
	return n, nil
}

func (m *Memory) UpsertUserByEmail(_ context.Context, email string, now time.Time) (User, error) {
	key := NormaliseEmail(email)
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.usersByKey[key]; ok {
		return m.users[id], nil
	}
	m.nextID++
	u := User{ID: "user-" + itoa(m.nextID), Email: key, CreatedAt: now}
	m.users[u.ID] = u
	m.usersByKey[key] = u.ID
	return u, nil
}

func (m *Memory) GetUser(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Memory) CreateRefreshToken(_ context.Context, t RefreshToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.TokenHash] = t
	return nil
}

func (m *Memory) GetRefreshToken(_ context.Context, hash string) (RefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[hash]
	if !ok {
		return RefreshToken{}, ErrNotFound
	}
	return t, nil
}

func (m *Memory) RotateRefreshToken(_ context.Context, oldHash string, next RefreshToken, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.tokens[oldHash]
	if !ok {
		return ErrNotFound
	}
	if old.RotatedAt != nil || old.RevokedAt != nil {
		return ErrConflict
	}
	old.RotatedAt = &at
	m.tokens[oldHash] = old
	m.tokens[next.TokenHash] = next
	return nil
}

func (m *Memory) RevokeFamily(_ context.Context, familyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, t := range m.tokens {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &at
			m.tokens[hash] = t
		}
	}
	return nil
}

// NormaliseEmail lower-cases and trims. It deliberately does not strip Gmail-style dots
// or plus-addressing: those are the user's addresses to distinguish, not ours.
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ---------------------------------------------------------------- M2: onboarding

func (m *Memory) SetDisplayName(_ context.Context, userID, displayName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.DisplayName = displayName
	m.users[userID] = u
	return nil
}

func (m *Memory) UpsertIntent(_ context.Context, intent Intent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[intent.UserID]; !ok {
		return ErrNotFound
	}
	if m.intents == nil {
		m.intents = map[string]Intent{}
	}
	m.intents[intent.UserID] = intent
	u := m.users[intent.UserID]
	u.OnboardingComplete = true
	m.users[intent.UserID] = u
	return nil
}

func (m *Memory) GetIntent(_ context.Context, userID string) (Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.intents[userID]
	if !ok {
		return Intent{}, ErrNotFound
	}
	return intent, nil
}

func (m *Memory) JoinPool(_ context.Context, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.intents[userID]; !ok {
		return ErrNotFound
	}
	if m.activePodOf(userID) != "" {
		return ErrConflict
	}
	if m.pool == nil {
		m.pool = map[string]PoolTicket{}
	}
	if _, queued := m.pool[userID]; queued {
		return ErrConflict
	}
	m.pool[userID] = PoolTicket{UserID: userID, QueuedAt: at}
	return nil
}

func (m *Memory) LeavePool(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pool, userID)
	return nil
}

func (m *Memory) GetPoolTicket(_ context.Context, userID string) (PoolTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, ok := m.pool[userID]
	if !ok {
		return PoolTicket{}, ErrNotFound
	}
	return ticket, nil
}

func (m *Memory) ListPoolCandidates(_ context.Context, limit int) ([]PoolCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]PoolCandidate, 0, len(m.pool))
	for userID, ticket := range m.pool {
		intent, ok := m.intents[userID]
		if !ok {
			continue
		}
		out = append(out, PoolCandidate{
			UserID:       userID,
			GoalCategory: intent.GoalCategory,
			Intensity:    intent.Intensity,
			Timezone:     intent.Timezone,
			SelfReport:   intent.SelfReport,
			Availability: intent.AvailabilityWindows,
			Embedding:    intent.Embedding,
			TenureWeeks:  tenureWeeks(m.users[userID].CreatedAt, ticket.QueuedAt),
			QueuedAt:     ticket.QueuedAt,
		})
	}
	// Oldest first, then by id, so the pool order is stable and the job reproducible.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].QueuedAt.Before(out[j].QueuedAt)
		}
		return out[i].UserID < out[j].UserID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) CreatePod(_ context.Context, pod Pod, memberIDs []string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range memberIDs {
		if m.activePodOf(id) != "" {
			return ErrConflict
		}
	}
	if m.pods == nil {
		m.pods = map[string]Pod{}
		m.podMembers = map[string][]PodMember{}
	}
	members := make([]PodMember, 0, len(memberIDs))
	for _, id := range memberIDs {
		members = append(members, PodMember{UserID: id, DisplayName: m.users[id].DisplayName, JoinedAt: at})
		delete(m.pool, id)
	}
	pod.Members = members
	m.pods[pod.ID] = pod
	m.podMembers[pod.ID] = members
	return nil
}

func (m *Memory) GetPodForMember(_ context.Context, podID, userID string) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pod, ok := m.pods[podID]
	if !ok {
		return Pod{}, ErrNotFound
	}
	for _, member := range m.podMembers[podID] {
		if member.UserID == userID {
			pod.Members = m.podMembers[podID]
			return pod, nil
		}
	}
	// Not a member: the same answer as no such pod, so pod ids cannot be probed.
	return Pod{}, ErrNotFound
}

func (m *Memory) GetActivePod(_ context.Context, userID string) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	podID := m.activePodOf(userID)
	if podID == "" {
		return Pod{}, ErrNotFound
	}
	pod := m.pods[podID]
	pod.Members = m.podMembers[podID]
	return pod, nil
}

// activePodOf must be called with the lock held.
func (m *Memory) activePodOf(userID string) string {
	for podID, members := range m.podMembers {
		if m.pods[podID].State == PodStateDissolved {
			continue
		}
		for _, member := range members {
			if member.UserID == userID {
				return podID
			}
		}
	}
	return ""
}

// tenureWeeks is whole weeks between two instants, never negative.
func tenureWeeks(since, now time.Time) int {
	if since.IsZero() || now.Before(since) {
		return 0
	}
	return int(now.Sub(since).Hours() / (24 * 7))
}
