# CLAUDE.md — Pod

You are the build agent for **Pod**, a small-group accountability app for people stuck in a rut.
Three platforms, native each: **iOS (Swift/SwiftUI)**, **Android (Kotlin/Jetpack Compose)**, **backend (Go or TypeScript — pick once, record in ADR-001)**.

Read this file fully before your first task. Re-read `docs/DECISIONS.md` before any architectural change.

---

## 1. The product in one paragraph

A user names one thing they've been putting off, picks how much effort they can realistically give, and is matched into a **pod of 2–3 people** with the same goal category, same effort level, and overlapping timezone. Each week the pod has **one small action**. Each member posts a **10-second proof** (photo or voice note). The pod sees who's in and who isn't. Proof disappears at the end of the cycle. The streak is shared, not individual.

**The behavioral thesis:** ruts break through low-stakes social witness, not motivation. Every technical decision serves that.

---

## 2. Non-negotiable product rules

These are not preferences. Violating one is a bug, even if the code works.

1. **Never shame.** No "you failed," "streak broken," "0%," "don't let your pod down." Missing a week produces a neutral, recoverable state. Copy lives in `content/microcopy.json` — never hardcode user-facing strings.
2. **Recovery is cheaper than punishment.** Reward for completing must exceed penalty for missing in every scoring path.
3. **Streaks are pod-level, never individual.** Individual streaks reintroduce private guilt.
4. **Proof is ephemeral.** Auto-deleted at cycle end, server-side, verifiably. This is a promise to the user, not a cleanup job.
5. **No feed, no follower counts, no public profiles.** Pod-only visibility, always.
6. **One free pod, forever.** Paywall gates *additional* pods, never the core loop.
7. **Effort threshold stays tiny.** If a flow requires more than ~15 seconds to complete a check-in, it's wrong.

---

## 3. Repo layout

```
pod/
├── CLAUDE.md                  ← this file
├── docs/
│   ├── DECISIONS.md           ← ADR log, append-only
│   ├── API.md                 ← OpenAPI-derived, generated
│   └── DESIGN.md              ← tokens, exported from Claude Design
├── content/
│   └── microcopy.json         ← single source of truth for all UI strings
├── design/
│   └── tokens.json            ← colors, type, spacing (platform-agnostic)
├── backend/
│   ├── openapi.yaml           ← contract; changes here drive both clients
│   └── ...
├── ios/                       ← Swift 6, SwiftUI, no cross-platform frameworks
└── android/                   ← Kotlin, Compose, no cross-platform frameworks
```

**Rule:** `openapi.yaml` is the contract. Never hand-write a client model — generate it. If a client needs a field the spec doesn't have, change the spec first, in its own commit.

---

## 4. Workflow — how you work each task

Follow this loop. Do not skip steps, do not batch multiple features into one pass.

### Step 1 — Orient
- Read the task.
- Read `docs/DECISIONS.md` for anything that constrains it.
- If the task touches UI, read `design/tokens.json` and `content/microcopy.json` first.
- **If the task is ambiguous, ask one question and stop.** Do not guess at product behavior.

### Step 2 — Plan
Write the plan into the task thread before writing code:
- What changes in the spec (if anything)
- What changes on backend
- What changes on iOS
- What changes on Android
- What tests prove it works

Wait for approval on any plan that changes `openapi.yaml`, the scoring algorithm, or a product rule from §2.

### Step 3 — Design (only if new UI)
Use **Claude Design** to produce the screen before implementing it.
- Input: the relevant tokens from `design/tokens.json` + the microcopy keys + the platform.
- Produce **two variants**, not one — pick with a stated reason.
- Export the resulting spacing/type/color decisions back into `design/tokens.json` if anything new was introduced. Tokens never live only in a screen.
- **iOS and Android are not the same design.** Same tokens, same information architecture, native idiom each: iOS gets SF Symbols, native navigation, haptics, sheets; Android gets Material 3 surfaces, its own nav patterns, predictive back.

### Step 4 — Build in this order, always
1. **Spec** — update `openapi.yaml`, regenerate `docs/API.md`
2. **Backend** — endpoint + tests + migration
3. **Regenerate clients** — models from spec, both platforms
4. **iOS** — feature + tests
5. **Android** — feature + tests

Never build a client feature against an endpoint that doesn't exist yet. Never mock the contract — implement it.

### Step 5 — Verify
Before declaring done, run and report:
- Backend: unit + integration tests green, migration applied cleanly
- iOS: builds for simulator, previews render, unit tests green
- Android: builds, previews render, unit tests green
- **Copy check:** every new user-facing string exists in `microcopy.json` and passes §2 rule 1
- **Parity check:** the feature does the same thing on both clients — list any deliberate divergence

### Step 6 — Record
- Append to `docs/DECISIONS.md` if you made a call that constrains future work
- One commit per layer, conventional commits (`feat(ios):`, `fix(backend):`)

---

## 5. Platform standards

### Backend
- Stateless services; Postgres as source of truth; Redis for the matching queue.
- Matching runs as a **scheduled job**, not on-request — a user entering the pool waits for the next matching window (target: hourly). This is intentional: instant matching produces bad pods.
- Every scoring change ships behind a flag and is backfill-safe.
- All timestamps UTC; pod cycles are anchored to the pod's shared timezone, not each member's device.

### iOS
- Swift 6, strict concurrency. SwiftUI. Minimum iOS 17.
- Architecture: feature modules, `@Observable` view models, no singletons except a single injected environment container.
- Networking: `URLSession` + generated models. No third-party networking library.
- Local persistence: SwiftData for cached pod state, Keychain for tokens.
- Push: APNs. Notification copy comes from `microcopy.json`, resolved server-side.

### Android
- Kotlin, Compose, Material 3. Minimum API 26.
- Architecture: feature modules, ViewModel + `StateFlow`, unidirectional data flow.
- Networking: Ktor client + generated models. DI with Hilt.
- Local persistence: Room for cached pod state, EncryptedSharedPreferences for tokens.
- Push: FCM. Same copy source as iOS.

**Shared rule:** no shared UI layer, no KMP for view code. Business logic that must not drift (scoring, cycle math) lives on the **backend**, not duplicated in two clients.

---

## 6. The two algorithms — implement exactly

### Matching
Hard filters, then scoring, then assembly.

**Hard filters (must all pass):**
- same goal category
- timezone within ±3 hours
- intensity level within ±1

**Score remaining candidates:**
| Signal | Weight |
|---|---|
| goal-text semantic similarity (embedding cosine) | 0.40 |
| reliability-band compatibility | 0.30 |
| availability window overlap | 0.20 |
| tenure mix (new + experienced preferred) | 0.10 |

**Assembly:** greedily pair highest scores, then attempt a third above threshold. **If no third clears the threshold, ship a pod of two.** A working pair beats a forced trio.

**Cold start:** no history → reliability 0.5, adjusted ±0.1 by the self-report question ("how often do you keep commitments to yourself").

**Failure:** pod inactive 2 consecutive weeks → dissolve → members re-enter pool. Only inactive members take the small reliability penalty. After 3 failed pods, flag the user for the alternate path (1-on-1) — some people need less social pressure, not more.

### Reliability score
```
score = 0.60 × completion_rate      // rolling 8 weeks
      + 0.25 × recency_weight       // last 2 weeks counted 2×
      + 0.15 × pod_impact           // did their presence correlate with pod completion
```
- Bands: low 0–0.3 / medium 0.3–0.7 / high 0.7–1.0. Match within band, ±1 band max.
- Increment on completion **must be larger** than decrement on miss. Cap per-week movement so a single bad week can't drop a band.
- Never expose the raw score to users. Never.

---

## 7. Build order (milestones)

1. **M1 — Contract + auth.** `openapi.yaml` covering onboarding, pod, week, proof. Auth. Both clients can sign in and hit a health endpoint.
2. **M2 — Onboarding + matching.** Goal, intensity, timezone. Matching job with a seeded pool. Pod assignment lands on both clients.
3. **M3 — The week loop.** Weekly action, post proof, pod status, the shared ring. This is the product — it must feel finished before anything else is built.
4. **M4 — Ephemerality + notifications.** Server-side proof deletion at cycle end. Push copy from microcopy.
5. **M5 — Recovery + dissolution.** Missed-week state, lower-the-bar flow, pod dissolve and re-match.
6. **M6 — Cycle completion.** Graduation, continue-or-close.
7. **M7 — Paywall.** Additional pods only.

Do not start M4 until M3 is genuinely good on both platforms.

---

## 8. What to escalate rather than decide

Stop and ask when:
- A change would touch any rule in §2
- The scoring weights or bands would change
- A platform can't achieve parity and you'd need a divergent behavior
- Anything involves storing more personal data than the feature strictly needs
- A dependency wants background location, contacts, or health data

---

## 9. Definition of done

A task is done when: spec updated, backend tested, both clients built and tested, copy sourced from `microcopy.json`, parity stated, decisions recorded. Not before.
