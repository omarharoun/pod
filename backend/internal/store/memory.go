package store

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omarharoun/pod/backend/internal/cycle"
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
	// Keyed by lane: "<userID>/<slot>". A lane is a thing (ADR-026).
	intents map[string]Intent
	pool    map[string]PoolTicket
	// entitlements is user id -> which additional lanes they have opened. Lane 0 is
	// never in here and must never be read from here.
	entitlements map[string]map[int]bool
	// grants is "<source>/<externalID>" -> the lane it opened, so replaying a receipt
	// returns that lane rather than opening another.
	grants map[string]Membership

	// recoveryOffers is keyed by lane: a pod ending in one lane must not put "that pod
	// has wound down" in front of a lane whose pod is running.
	recoveryOffers map[string]bool

	// FailContinuationReads makes ContinuationFor fail, so a test can prove that a tick
	// where every graduated pod fails is a failed tick rather than a clean one.
	FailContinuationReads error

	// FailEntitlements makes ListEntitlements fail, so a test can prove the free lane
	// does not depend on it. Exported because that is the only way to set it, and the
	// property it proves is the one CLAUDE.md §2 rule 6 is about.
	FailEntitlements error

	pods       map[string]Pod
	podMembers map[string][]PodMember
	// continuations is pod id -> user id -> carrying on. Counted, never listed: no
	// method on this type returns who said no (CLAUDE.md §2 rule 1).
	continuations     map[string]map[string]bool
	weeks             map[string]Week
	memberWeeks       map[string][]MemberWeek
	proofs            map[string]Proof
	devices           map[string]Device
	notifications     map[string]Notification
	notificationKeys  map[string]string
	notificationsSent map[string]time.Time
	nextID            int

	// FailPing makes Ping report the datastore as unreachable, for health tests.
	FailPing error
}

func NewMemory() *Memory {
	return &Memory{
		users:             map[string]User{},
		usersByKey:        map[string]string{},
		challenges:        map[string]AuthChallenge{},
		tokens:            map[string]RefreshToken{},
		intents:           map[string]Intent{},
		pool:              map[string]PoolTicket{},
		pods:              map[string]Pod{},
		podMembers:        map[string][]PodMember{},
		weeks:             map[string]Week{},
		memberWeeks:       map[string][]MemberWeek{},
		proofs:            map[string]Proof{},
		devices:           map[string]Device{},
		notifications:     map[string]Notification{},
		notificationKeys:  map[string]string{},
		notificationsSent: map[string]time.Time{},
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

func laneKey(userID string, slot int) string {
	return userID + "/" + strconv.Itoa(slot)
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
	m.intents[laneKey(intent.UserID, intent.Slot)] = intent
	u := m.users[intent.UserID]
	u.OnboardingComplete = true
	m.users[intent.UserID] = u
	return nil
}

func (m *Memory) GetIntent(_ context.Context, userID string, slot int) (Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.intents[laneKey(userID, slot)]
	if !ok {
		return Intent{}, ErrNotFound
	}
	return intent, nil
}

func (m *Memory) JoinPool(_ context.Context, userID string, slot int, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.intents[laneKey(userID, slot)]; !ok {
		return ErrNotFound
	}
	// Scoped to the lane: being in a pod for one thing says nothing about another.
	if m.activePodOf(userID, slot) != "" {
		return ErrConflict
	}
	if m.pool == nil {
		m.pool = map[string]PoolTicket{}
	}
	if _, queued := m.pool[laneKey(userID, slot)]; queued {
		return ErrConflict
	}
	m.pool[laneKey(userID, slot)] = PoolTicket{UserID: userID, Slot: slot, QueuedAt: at}
	return nil
}

func (m *Memory) LeavePool(_ context.Context, userID string, slot int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pool, laneKey(userID, slot))
	return nil
}

func (m *Memory) GetPoolTicket(_ context.Context, userID string, slot int) (PoolTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ticket, ok := m.pool[laneKey(userID, slot)]
	if !ok {
		return PoolTicket{}, ErrNotFound
	}
	return ticket, nil
}

func (m *Memory) ListPoolCandidates(_ context.Context, limit int) ([]PoolCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]PoolCandidate, 0, len(m.pool))
	for key, ticket := range m.pool {
		intent, ok := m.intents[key]
		if !ok {
			continue
		}
		// Scoped to the lane, exactly as the Postgres query is: someone waiting for a
		// second thing is a candidate even while their first pod is running.
		if m.activePodOf(ticket.UserID, ticket.Slot) != "" {
			continue
		}
		out = append(out, PoolCandidate{
			UserID:       ticket.UserID,
			Slot:         ticket.Slot,
			GoalCategory: intent.GoalCategory,
			Intensity:    intent.Intensity,
			Timezone:     intent.Timezone,
			SelfReport:   intent.SelfReport,
			Availability: intent.AvailabilityWindows,
			Embedding:    intent.Embedding,
			TenureWeeks:  tenureWeeks(m.users[ticket.UserID].CreatedAt, ticket.QueuedAt),
			QueuedAt:     ticket.QueuedAt,
		})
	}
	// Oldest first, then by id, then by lane, so the pool order is stable and the job
	// reproducible.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].QueuedAt.Before(out[j].QueuedAt)
		}
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].Slot < out[j].Slot
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------------------------------------------------------------------- M7: slots

func (m *Memory) ListEntitlements(_ context.Context, userID string) (map[int]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailEntitlements != nil {
		return nil, m.FailEntitlements
	}
	opened := map[int]bool{}
	for slot, granted := range m.entitlements[userID] {
		if granted {
			opened[slot] = true
		}
	}
	return opened, nil
}

func (m *Memory) GrantSlot(_ context.Context, userID string, slot int, source, externalID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entitlements == nil {
		m.entitlements = map[string]map[int]bool{}
		m.grants = map[string]Membership{}
	}
	key := source + "/" + externalID
	if existing, ok := m.grants[key]; ok && existing.UserID != userID {
		// The same transaction opening a lane for a second account is a replay attack,
		// not a retry.
		return ErrConflict
	}
	if m.entitlements[userID] == nil {
		m.entitlements[userID] = map[int]bool{}
	}
	m.entitlements[userID][slot] = true
	m.grants[key] = Membership{UserID: userID, Slot: slot}
	return nil
}

func (m *Memory) SlotForExternalID(_ context.Context, source, externalID string) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	granted, ok := m.grants[source+"/"+externalID]
	if !ok {
		return "", 0, ErrNotFound
	}
	return granted.UserID, granted.Slot, nil
}

func (m *Memory) CreatePod(_ context.Context, pod Pod, memberships []Membership, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// One active pod per *lane*, matching the partial unique index exactly.
	for _, member := range memberships {
		if m.activePodOf(member.UserID, member.Slot) != "" {
			return ErrConflict
		}
	}
	if m.pods == nil {
		m.pods = map[string]Pod{}
		m.podMembers = map[string][]PodMember{}
	}
	members := make([]PodMember, 0, len(memberships))
	for _, member := range memberships {
		members = append(members, PodMember{
			UserID:      member.UserID,
			Slot:        member.Slot,
			DisplayName: m.users[member.UserID].DisplayName,
			JoinedAt:    at,
		})
		// Only this lane's ticket. Someone waiting for a second thing stays waiting.
		delete(m.pool, laneKey(member.UserID, member.Slot))
	}
	pod.Members = members
	if pod.CycleLengthWeeks <= 0 {
		pod.CycleLengthWeeks = cycle.DefaultLengthWeeks
	}
	if pod.CycleStartedAt.IsZero() {
		pod.CycleStartedAt = pod.CreatedAt
	}
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

func (m *Memory) GetActivePod(_ context.Context, userID string, slot int) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	podID := m.activePodOf(userID, slot)
	if podID == "" {
		return Pod{}, ErrNotFound
	}
	pod := m.pods[podID]
	pod.Members = m.podMembers[podID]
	return pod, nil
}

// CountWeeksEveryoneWasIn is how many of a pod's closed weeks the pod completed.
func (m *Memory) CountWeeksEveryoneWasIn(_ context.Context, podID string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, week := range m.weeks {
		if week.PodID != podID || week.ClosedAt == nil || !week.PodCompleted {
			continue
		}
		if week.OpensAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

// ListActivePodsForUser is every lane the caller currently has a pod in.
func (m *Memory) ListActivePodsForUser(_ context.Context, userID string) ([]Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type found struct {
		slot int
		pod  Pod
	}
	var hits []found
	for podID, members := range m.podMembers {
		pod := m.pods[podID]
		if pod.State == PodStateDissolved {
			continue
		}
		for _, member := range members {
			if member.UserID != userID {
				continue
			}
			pod.Members = members
			hits = append(hits, found{slot: member.Slot, pod: pod})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].slot < hits[j].slot })
	out := make([]Pod, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.pod)
	}
	return out, nil
}

// activePodOf must be called with the lock held. It is scoped to one lane.
// Your slot-1 pod says nothing about your slot-0 lane.
func (m *Memory) activePodOf(userID string, slot int) string {
	for podID, members := range m.podMembers {
		if m.pods[podID].State == PodStateDissolved {
			continue
		}
		for _, member := range members {
			if member.UserID == userID && member.Slot == slot {
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

// -------------------------------------------------------------- M3: the week loop

func (m *Memory) ListActivePods(_ context.Context) ([]Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Pod
	for id, pod := range m.pods {
		// Only running pods. A graduated pod is not dissolved, but it must not be
		// handed another week either.
		if pod.State != PodStateActive {
			continue
		}
		pod.Members = m.podMembers[id]
		out = append(out, pod)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) EnsureWeek(_ context.Context, week Week, memberIDs []string) (Week, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.weeks {
		if existing.PodID == week.PodID && existing.OpensAt.Equal(week.OpensAt) {
			return m.hydrateWeek(existing), nil
		}
	}
	week.Index = 1
	for _, existing := range m.weeks {
		if existing.PodID == week.PodID && existing.Index >= week.Index {
			week.Index = existing.Index + 1
		}
	}
	m.weeks[week.ID] = week
	for _, userID := range memberIDs {
		m.memberWeeks[week.ID] = append(m.memberWeeks[week.ID], MemberWeek{UserID: userID, State: MemberWeekOpen})
	}
	return m.hydrateWeek(week), nil
}

func (m *Memory) GetWeekForMember(_ context.Context, weekID, userID string) (Week, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	week, ok := m.weeks[weekID]
	if !ok || !m.isMemberOf(week.PodID, userID) {
		return Week{}, ErrNotFound
	}
	return m.hydrateWeek(week), nil
}

func (m *Memory) GetCurrentWeek(_ context.Context, podID, userID string, at time.Time) (Week, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isMemberOf(podID, userID) {
		return Week{}, ErrNotFound
	}
	for _, week := range m.weeks {
		if week.PodID == podID && !at.Before(week.OpensAt) && at.Before(week.ClosesAt) {
			return m.hydrateWeek(week), nil
		}
	}
	return Week{}, ErrNotFound
}

func (m *Memory) ListWeeksForMember(_ context.Context, podID, userID string, limit int) ([]Week, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isMemberOf(podID, userID) {
		return nil, ErrNotFound
	}
	var out []Week
	for _, week := range m.weeks {
		if week.PodID == podID {
			out = append(out, m.hydrateWeek(week))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index > out[j].Index })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) ListWeeksForMemberless(_ context.Context, podID string, limit int) ([]Week, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Week
	for _, week := range m.weeks {
		if week.PodID == podID {
			out = append(out, m.hydrateWeek(week))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index > out[j].Index })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) SetWeekAction(_ context.Context, weekID, actionText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	week, ok := m.weeks[weekID]
	if !ok {
		return ErrNotFound
	}
	if week.ClosedAt != nil || m.anyoneHasPosted(weekID) {
		return ErrConflict
	}
	week.ActionText = actionText
	week.ActionIsDefault = false
	m.weeks[weekID] = week
	return nil
}

func (m *Memory) CloseWeek(_ context.Context, weekID string, at time.Time) (Week, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	week, ok := m.weeks[weekID]
	if !ok {
		return Week{}, ErrNotFound
	}
	if week.ClosedAt != nil {
		return m.hydrateWeek(week), nil
	}

	members := m.memberWeeks[weekID]
	posted := 0
	for i, member := range members {
		if member.State == MemberWeekOpen {
			members[i].State = MemberWeekResting
		}
		if members[i].State == MemberWeekPosted {
			posted++
		}
	}
	m.memberWeeks[weekID] = members

	// The pod completed when everyone in it posted. It is the pod that succeeds, so a
	// partial week is not a partial score — it is simply not a completed week.
	week.PodCompleted = len(members) > 0 && posted == len(members)
	closedAt := at
	week.ClosedAt = &closedAt
	m.weeks[weekID] = week

	pod := m.pods[week.PodID]
	if week.PodCompleted {
		pod.StreakWeeks++
	} else {
		// A missed week resets the shared streak and nothing else. No score moves, no
		// member is named, and next week opens exactly like any other.
		pod.StreakWeeks = 0
	}
	m.pods[week.PodID] = pod

	return m.hydrateWeek(week), nil
}

func (m *Memory) CreateProof(_ context.Context, proof Proof) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.weeks[proof.WeekID]; !ok {
		return ErrNotFound
	}
	m.proofs[proof.ID] = proof
	return nil
}

func (m *Memory) GetProofForViewer(_ context.Context, proofID, viewerID string) (Proof, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proof, ok := m.proofs[proofID]
	if !ok || proof.DeletedAt != nil {
		return Proof{}, ErrNotFound
	}
	week := m.weeks[proof.WeekID]
	if !m.isMemberOf(week.PodID, viewerID) {
		return Proof{}, ErrNotFound
	}
	return proof, nil
}

func (m *Memory) CommitProof(_ context.Context, proofID, userID, blobKey string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	proof, ok := m.proofs[proofID]
	if !ok || proof.UserID != userID || proof.DeletedAt != nil {
		return ErrNotFound
	}
	if proof.CommittedAt != nil {
		return ErrConflict
	}
	for _, other := range m.proofs {
		if other.WeekID == proof.WeekID && other.UserID == userID && other.CommittedAt != nil && other.DeletedAt == nil {
			return ErrConflict
		}
	}
	committedAt := at
	proof.CommittedAt = &committedAt
	proof.BlobKey = &blobKey
	m.proofs[proofID] = proof

	members := m.memberWeeks[proof.WeekID]
	for i, member := range members {
		if member.UserID == userID {
			postedAt := at
			members[i].State = MemberWeekPosted
			members[i].PostedAt = &postedAt
			id := proofID
			members[i].ProofID = &id
		}
	}
	m.memberWeeks[proof.WeekID] = members
	return nil
}

func (m *Memory) ListExpiredProofs(_ context.Context, at time.Time, limit int) ([]Proof, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Proof
	for _, proof := range m.proofs {
		if proof.DeletedAt == nil && !at.Before(proof.ExpiresAt) {
			out = append(out, proof)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) MarkProofDeleted(_ context.Context, proofID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	proof, ok := m.proofs[proofID]
	if !ok {
		return ErrNotFound
	}
	deletedAt := at
	proof.DeletedAt = &deletedAt
	proof.BlobKey = nil
	m.proofs[proofID] = proof

	// The proof id comes off the member's week with the bytes: a pod-mate should not be
	// left holding a handle to something that no longer exists.
	members := m.memberWeeks[proof.WeekID]
	for i, member := range members {
		if member.ProofID != nil && *member.ProofID == proofID {
			members[i].ProofID = nil
		}
	}
	m.memberWeeks[proof.WeekID] = members
	return nil
}

// hydrateWeek must be called with the lock held.
func (m *Memory) hydrateWeek(week Week) Week {
	members := make([]MemberWeek, len(m.memberWeeks[week.ID]))
	copy(members, m.memberWeeks[week.ID])
	sort.Slice(members, func(i, j int) bool { return members[i].UserID < members[j].UserID })
	week.Members = members
	return week
}

// anyoneHasPosted must be called with the lock held.
func (m *Memory) anyoneHasPosted(weekID string) bool {
	for _, member := range m.memberWeeks[weekID] {
		if member.State == MemberWeekPosted {
			return true
		}
	}
	return false
}

// isMemberOf must be called with the lock held.
func (m *Memory) isMemberOf(podID, userID string) bool {
	for _, member := range m.podMembers[podID] {
		if member.UserID == userID {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------ M4: notifications

func (m *Memory) RegisterDevice(_ context.Context, device Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.devices[device.TokenHash]; ok {
		// Re-registering keeps the device's place rather than making a second one.
		existing.Platform = device.Platform
		existing.Locale = device.Locale
		existing.UserID = device.UserID
		existing.LastSeenAt = device.LastSeenAt
		m.devices[device.TokenHash] = existing
		return nil
	}
	m.devices[device.TokenHash] = device
	return nil
}

func (m *Memory) RetireDevice(_ context.Context, tokenHash string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.devices, tokenHash)
	return nil
}

func (m *Memory) DevicesFor(_ context.Context, userID string) ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Device
	for _, device := range m.devices {
		if device.UserID == userID {
			out = append(out, device)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TokenHash < out[j].TokenHash })
	return out, nil
}

func (m *Memory) QueueNotification(_ context.Context, notification Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := notification.Kind + "|" + notification.SubjectID + "|" + notification.UserID
	if _, ok := m.notificationKeys[key]; ok {
		return ErrConflict
	}
	m.notificationKeys[key] = notification.ID
	m.notifications[notification.ID] = notification
	return nil
}

func (m *Memory) PendingNotifications(_ context.Context, limit int) ([]Notification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Notification
	for id, notification := range m.notifications {
		if _, done := m.notificationsSent[id]; !done {
			out = append(out, notification)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) MarkNotificationSent(_ context.Context, id string, at time.Time, failed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.notifications[id]; !ok {
		return ErrNotFound
	}
	m.notificationsSent[id] = at
	return nil
}

// ---------------------------------------------------------------- M5: recovery

func (m *Memory) RecentOutcomes(_ context.Context, userID string, limit int) ([]WeekOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// One entry per CALENDAR week, not per member-week — a person can be in two pods at
	// once since M7, and reliability.Compute folds one capped step per entry, so two rows
	// a week made a two-lane person's score move twice as fast for the same behaviour.
	// Keyed on the ISO week of the close, because two pods in zones up to three hours
	// apart close the same week at different moments.
	byWeek := map[string]WeekOutcome{}
	for weekID, members := range m.memberWeeks {
		week := m.weeks[weekID]
		if week.ClosedAt == nil {
			// An open week is not history yet.
			continue
		}
		for _, member := range members {
			if member.UserID != userID {
				continue
			}
			year, isoWeek := week.ClosesAt.ISOWeek()
			key := strconv.Itoa(year) + "/" + strconv.Itoa(isoWeek)
			existing, seen := byWeek[key]
			if !seen {
				byWeek[key] = WeekOutcome{
					WeekID:       weekID,
					Posted:       member.State == MemberWeekPosted,
					PodCompleted: week.PodCompleted,
					ClosesAt:     week.ClosesAt,
				}
				continue
			}
			// The gentler reading of both, matching the SQL: showing up in any lane is
			// showing up, and a pod completing is a pod completing.
			existing.Posted = existing.Posted || member.State == MemberWeekPosted
			existing.PodCompleted = existing.PodCompleted || week.PodCompleted
			if week.ClosesAt.After(existing.ClosesAt) {
				existing.ClosesAt = week.ClosesAt
			}
			if weekID < existing.WeekID {
				existing.WeekID = weekID
			}
			byWeek[key] = existing
		}
	}
	out := make([]WeekOutcome, 0, len(byWeek))
	for _, outcome := range byWeek {
		out = append(out, outcome)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ClosesAt.Equal(out[j].ClosesAt) {
			return out[i].ClosesAt.After(out[j].ClosesAt)
		}
		return out[i].WeekID > out[j].WeekID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CompleteCycle graduates a pod. Not DissolvePod with a different word: no member
// carries a count and nobody is offered a gentler restart (ADR-023).
func (m *Memory) CompleteCycle(_ context.Context, podID string, at, decideBy time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pod, ok := m.pods[podID]
	if !ok {
		return ErrNotFound
	}
	if pod.State != PodStateActive {
		return nil
	}
	completed, deadline := at, decideBy
	pod.State = PodStateCompleted
	pod.CompletedAt = &completed
	pod.DecideBy = &deadline
	m.pods[podID] = pod
	return nil
}

func (m *Memory) AnswerContinuation(_ context.Context, podID, userID string, continuing bool, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.continuations == nil {
		m.continuations = map[string]map[string]bool{}
	}
	if m.continuations[podID] == nil {
		m.continuations[podID] = map[string]bool{}
	}
	m.continuations[podID][userID] = continuing
	return nil
}

func (m *Memory) ContinuationFor(_ context.Context, podID, userID string) (Continuation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailContinuationReads != nil {
		return Continuation{}, m.FailContinuationReads
	}
	if _, ok := m.pods[podID]; !ok {
		return Continuation{}, ErrNotFound
	}
	var out Continuation
	answers := m.continuations[podID]
	for _, member := range m.podMembers[podID] {
		answer, answered := answers[member.UserID]
		switch {
		case !answered:
			out.Undecided++
		case answer:
			out.Continuing++
		default:
			out.Declined++
		}
		if member.UserID == userID && answered {
			out.YouAnswered = true
			out.YouAreContinuing = answer
		}
	}
	return out, nil
}

func (m *Memory) ListPodsAwaitingContinuation(_ context.Context) ([]Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Pod
	for id, pod := range m.pods {
		if pod.State != PodStateCompleted {
			continue
		}
		pod.Members = m.podMembers[id]
		out = append(out, pod)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) CloseCompletedPod(_ context.Context, podID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pod, ok := m.pods[podID]
	if !ok {
		return ErrNotFound
	}
	if pod.State != PodStateCompleted {
		return nil
	}
	// Memberships end; nobody's dissolved_pods moves and nobody is offered recovery.
	m.podMembers[podID] = nil
	return nil
}

func (m *Memory) StartNextCycle(_ context.Context, podID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pod, ok := m.pods[podID]
	if !ok {
		return ErrNotFound
	}
	if pod.State != PodStateCompleted {
		return nil
	}
	answers := m.continuations[podID]
	staying := make([]PodMember, 0, len(m.podMembers[podID]))
	for _, member := range m.podMembers[podID] {
		if answers[member.UserID] {
			staying = append(staying, member)
		}
	}
	m.podMembers[podID] = staying
	pod.State = PodStateActive
	pod.CompletedAt = nil
	pod.DecideBy = nil
	// The next cycle starts now. Leaving this at CreatedAt is what made a pod that
	// carried on re-graduate on the following tick.
	pod.CycleStartedAt = at
	pod.Members = staying
	m.pods[podID] = pod
	delete(m.continuations, podID)
	return nil
}

func (m *Memory) DissolvePod(_ context.Context, podID, reason string, inactiveMemberIDs []string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pod, ok := m.pods[podID]
	if !ok {
		return ErrNotFound
	}
	if pod.State == PodStateDissolved {
		return nil
	}
	pod.State = PodStateDissolved
	dissolvedAt := at
	pod.DissolvedAt = &dissolvedAt
	m.pods[podID] = pod

	inactive := map[string]bool{}
	for _, id := range inactiveMemberIDs {
		inactive[id] = true
	}
	for _, member := range m.podMembers[podID] {
		user := m.users[member.UserID]
		if inactive[member.UserID] {
			// Only inactive members carry the count (CLAUDE.md §6).
			user.DissolvedPods++
		}
		m.users[member.UserID] = user
		// The offer belongs to the lane the pod occupied for *this* member, which
		// differs per member because a slot belongs to the membership (ADR-026).
		if m.recoveryOffers == nil {
			m.recoveryOffers = map[string]bool{}
		}
		m.recoveryOffers[laneKey(member.UserID, member.Slot)] = true
	}
	// Membership ends with the pod, so everyone is free to be matched again.
	m.podMembers[podID] = nil
	return nil
}

func (m *Memory) RecoveryFor(_ context.Context, userID string, slot int) (Recovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[userID]
	if !ok {
		return Recovery{}, ErrNotFound
	}
	return Recovery{
		// Per lane: the offer is about the pod that ended, and it ended in one lane.
		OfferLowerIntensity: m.recoveryOffers[laneKey(userID, slot)],
		// Not per lane: three pods ending under someone is a fact about them whichever
		// lanes it happened in, and that is what §6 keys the one-to-one path on.
		OfferAlternatePath: user.DissolvedPods >= AlternatePathAfter,
	}, nil
}

func (m *Memory) ClearRecoveryOffer(_ context.Context, userID string, slot int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return ErrNotFound
	}
	delete(m.recoveryOffers, laneKey(userID, slot))
	return nil
}
