package store

import (
	"context"
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
