# Decisions

Append-only. Newest at the bottom. Re-read this before any architectural change
(`CLAUDE.md` §Workflow). If a decision here turns out to be wrong, add a new entry that
supersedes it — never edit the old one.

Each entry: **Context** (what forced a choice), **Decision** (what we did),
**Consequences** (what this costs us later).

---

## ADR-001 — Backend is Go

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1

**Context.** `CLAUDE.md` §Intro leaves the backend language open — "Go or TypeScript —
pick once, record in ADR-001" — and the choice has to be made before any endpoint exists.
The shape of this backend is: stateless HTTP services, Postgres as the source of truth, a
Redis-backed matching queue, and a scheduled matching job that does embedding maths over a
candidate pool.

**Decision.** Go.

Reasons, in the order they mattered:

1. The matching job is the only computationally interesting thing the backend does. It is
   a scheduled fan-out over a pool with per-candidate scoring — goroutines and a static
   binary suit it better than an event loop and a `node_modules` tree.
2. `net/http` with Go 1.22 pattern routing covers everything M1–M7 needs. No framework,
   so no framework upgrade treadmill.
3. A single static binary keeps deploy and the scheduled job identical: one image, two
   entrypoints.
4. `CLAUDE.md` §5 puts scoring and cycle maths on the backend precisely so it can't drift
   between clients. That code deserves a language with exhaustive-ish typing and a strong
   testing story out of the box.

TypeScript would have won if we expected to share types with a web client. We do not
have a web client, and §5 forbids sharing view code anyway.

**Consequences.**
* No type sharing with the clients — handled instead by generating all three model sets
  from `backend/openapi.yaml` (ADR-004).
* Embedding generation for the 0.40 similarity signal will call out to a hosted model
  rather than run in-process. That is a network dependency in the matching job; the job is
  scheduled and retryable, so it is an acceptable one.
* Minimum Go version is pinned in `backend/go.mod` and bumped deliberately, not by CI.

---

## ADR-002 — Sign-in is a 6-digit email code, not a password

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1

**Context.** `CLAUDE.md` §2 rule 7 caps the effort threshold: if a flow takes more than
about 15 seconds, it is wrong. That rule is written about check-in, but sign-in is the
first flow anyone meets, and a password field plus a password manager dance is the single
most expensive screen we could open with. The product also has nothing worth stealing
beyond ephemeral proof, and no social graph to hijack.

**Decision.** Passwordless. `POST /v1/auth/start` mails a 6-digit code;
`POST /v1/auth/verify` exchanges it for a short-lived access JWT plus a rotating,
single-use refresh token. Verifying an address that has no account creates one — there is
no separate sign-up flow, because there is nothing to fill in at that point.

Specifics that are part of the decision, not incidental:

* Codes are 6 digits, valid 10 minutes, at most 5 attempts, single-use.
* Codes are stored hashed. A leaked database row must not be a valid credential.
* `auth/start` answers the same way whether or not the address has an account, so it
  cannot be used to enumerate accounts.
* Refresh tokens rotate on every use and are stored hashed. Presenting an already-rotated
  token revokes the whole family — that is our reuse-detection path.
* Access tokens are HS256 JWTs, 15 minutes. Short enough that we do not need a revocation
  list for them; long enough not to thrash refresh.

**Consequences.**
* We depend on email deliverability for sign-in. A transactional provider is required
  before public launch, and a delivery outage is a total sign-in outage.
* Someone with access to a person's inbox has access to their pod. Acceptable given the
  data at stake (see §2 rule 4 — proof expires anyway), and no worse than password reset.
* The JWT signing key needs rotation support before launch. Deferred to M4, tracked in
  `backend/internal/auth`.

---

## ADR-003 — The API returns copy keys, never sentences

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1

**Context.** `CLAUDE.md` §2 rule 1 forbids shaming copy and requires every user-facing
string to live in `content/microcopy.json`. An API that returns `"message": "You failed to
verify"` breaks that rule from a place no copy review would ever look, and puts a second
source of truth for wording on the server.

**Decision.** Every error body is `{code, copy_key, params, request_id}`. `code` is for
branching, `copy_key` indexes `content/microcopy.json`, and `params` carries the
interpolation values. The API never returns a display sentence. Push notification bodies
are resolved server-side from the same file, so both clients and the push pipeline share
one catalogue.

`HealthCheck.detail` is the single exception, and is explicitly operator-facing — it is
never rendered to a user.

**Consequences.**
* A new server-side error needs a copy key added in the same change, or the client shows
  nothing. `tools/copylint` catches the reverse (a client referencing a missing key); the
  server side is covered by a handler test asserting every `copy_key` it can emit exists.
* Localisation later is a matter of adding locale files, with no server change.
* Clients cannot display server text verbatim as a fallback. That is the point.

---

## ADR-004 — Models are generated in-repo, not by a third-party OpenAPI generator

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1

**Context.** `CLAUDE.md` §Repo layout: "Never hand-write a client model — generate it."
The obvious route is `openapi-generator`, which needs a JVM, emits a large surface we do
not control, and produces Swift that fights strict concurrency.

**Decision.** `tools/codegen` — a small Go program that reads `backend/openapi.yaml`,
`content/microcopy.json` and `design/tokens.json` and writes:

| Output | Consumer |
|---|---|
| `backend/internal/api/models_gen.go` | the server's own wire types |
| `ios/PodKit/Sources/PodAPI/Generated/{Models,Copy,Tokens}.swift` | iOS |
| `android/core/api/.../generated/{Models,Copy,Tokens}.kt` | Android |
| `docs/API.md` | humans |

It deliberately supports only the subset of OpenAPI this spec uses (no `allOf`, `oneOf`,
or inline object schemas), and fails loudly on anything else rather than emitting
something plausible. `make spec` runs it in `--check` mode so CI fails if generated
output is stale.

It also enforces the §2 invariants that are checkable at the contract level: a field
named `*reliability_score*`, `*completion_rate*`, `*follower*`, a `streak` field outside
`Pod`, a shaming value in `MemberWeekState`, or a `Proof` without a required `expires_at`
all fail generation.

Type mapping, including the deliberate divergences:

| Spec | Go | Swift | Kotlin |
|---|---|---|---|
| `string` | `string` | `String` | `String` |
| `string(date-time)` | `time.Time` | `Date` | `Instant` |
| `string(uuid)` | `string` | `UUID` | `String` |
| `string(uri)` | `string` | `URL` | `String` |
| enum | `type X string` + consts | raw-value struct | `enum class` + `UNKNOWN` |

Swift gets `UUID`/`URL` because they are free and standard; Kotlin does not, because
`kotlin.uuid.Uuid` is still experimental and a URL type buys nothing at the Ktor boundary.
Both are *open* over enums — an unrecognised value from a newer server decodes rather than
throwing, so a server deploy can never brick an older install.

**Consequences.**
* Every spec construct we want to use has to be taught to the generator first. That is a
  feature: it keeps the contract small enough to hold in your head.
* Generated files are committed, so a client build never depends on running Go.
* If the spec ever needs polymorphism, this decision gets revisited in a new ADR.

---

## ADR-005 — Intensity is a 1–5 scale

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1 (spec) / M2 (matching)

**Context.** `CLAUDE.md` §6 hard-filters matches on "intensity level within ±1" but never
says how many levels there are. The answer changes what that filter *does*: on a 3-point
scale, ±1 means level 2 matches everyone and levels 1 and 3 only fail to match each other
— the filter barely filters.

**Decision.** Intensity is an integer 1–5, from "a few minutes a week" to "several hours a
week". ±1 then excludes roughly half the pool, which is what a hard filter is for.

**Consequences.**
* The onboarding question needs five distinguishable answers that a person can pick
  between in a couple of seconds. If the five options turn out not to be distinguishable
  in testing, collapse to three and *widen* the filter to ±1 of three — do not keep five
  labels people can't tell apart.
* This changes the *domain* of a hard filter, not the scoring weights or the reliability
  bands. Those are untouched and remain out of bounds without escalation (§8).

---

## ADR-006 — The week vocabulary is `posted` / `open` / `resting`

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1 (spec) / M3 (behaviour)

**Context.** The API needs a name for "this person did not post this week". Every natural
candidate — `missed`, `skipped`, `failed`, `incomplete` — is a verdict, and once it is in
the enum it leaks into log lines, analytics dashboards, and eventually a screen.

**Decision.** `MemberWeekState` is exactly three values:

* `posted` — a proof is in.
* `open` — the week is still running and they have not posted yet.
* `resting` — the week closed without a proof.

`resting` is a state, not a judgement, and it is symmetric with the recovery path in M5.
There is no fourth state, and no boolean anywhere that means "did they fail".
`tools/codegen` fails the build if a shaming value is added.

Pod-level completion stays a separate field, `Week.pod_completed`, because the pod is the
unit that succeeds (§2 rule 3).

**Consequences.**
* Analytics has to compute participation from state counts rather than reading a
  failure flag. Fine — that computation belongs in analytics, not in the contract.
* Copy for all three states lives under `state.member.*` in `content/microcopy.json` and
  is checked by `tools/copylint`.

---

## ADR-007 — Unbuilt endpoints answer `501`, not `404`

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1

**Context.** M1's deliverable is a contract covering onboarding, pod, week and proof, but
only auth and health are implemented. A route that is specified but unrouted returns `404`,
which is indistinguishable from a typo in a client's path.

**Decision.** Every specified route is registered. Routes whose milestone has not landed
answer `501` with `code: not_implemented_yet`. `x-milestone` on each operation is the
single place that says which is which, and it is a required field — generation fails
without it.

This does not license building client features against unimplemented endpoints; `CLAUDE.md`
§Build order still forbids that. It exists so that when a client *does* call one by
mistake, the error says so.

**Consequences.**
* The router has a small table of not-yet-implemented operations that shrinks each
  milestone. When it is empty, delete it.
* Monitoring should alert on `501`s: in production they mean a client shipped early.

---

## ADR-008 — Proof bytes never pass through the API

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1 (spec) / M3 (behaviour)

**Context.** `CLAUDE.md` §2 rule 4 makes ephemerality a promise: proof is deleted at cycle
end, server-side, verifiably. Anything that copies the bytes into a second place — an
application log, an API-tier temp file, a CDN cache with its own TTL — is a second place
to have to prove deletion from.

**Decision.** Two-step upload. `createProofUpload` returns a short-lived, single-use
signed URL; the client PUTs the bytes straight to object storage; `commitProof` publishes
it to the pod. Reads work the same way: a short-lived `view_url`, scoped to a member of
that pod, expiring well before the proof itself does.

Deletion is belt and braces: a sweep that deletes at `expires_at`, plus a bucket lifecycle
rule as a backstop, plus a test that asserts a swept proof is gone. An uncommitted upload
is swept too and never becomes visible.

**Consequences.**
* The API never sees the bytes, so it cannot moderate them. If moderation is ever needed
  it has to be an explicit, separate, disclosed decision — not a quiet consequence.
* `view_url` lifetime is a real leak window: anyone holding the URL can fetch it until it
  expires. Keep it to minutes and never log it.
* Storage credentials become a production dependency of the proof flow in M3.

---

## ADR-009 — `:core:api` is a plain Kotlin/JVM module, not an Android library

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M1

**Context.** The obvious shape for the Android side is one Android library module holding
the generated models, the HTTP client, the copy catalogue, the tokens and the sign-in
state machine. But none of those things are about Android, and making the module an
Android library means its tests need either an emulator or Robolectric — which is the
difference between a suite that runs in seconds on every save and one that nobody runs.

**Decision.** `:core:api` applies `org.jetbrains.kotlin.jvm`, not
`com.android.library`. It depends on kotlinx-serialization, kotlinx-datetime, coroutines
and `ktor-client-core` — nothing from the platform. Its tests are ordinary JUnit tests.

Two consequences fall out of that, and both are improvements:

* The one piece that genuinely needs a `Context` — `EncryptedTokenStore` — lives in
  `:app` and implements the `TokenStore` interface `:core:api` defines. The dependency
  points the right way.
* The Ktor engine is supplied by `:app` (OkHttp) rather than baked in, so tests inject
  `MockEngine` without an engine conflict.

The root `build.gradle.kts` deliberately declares no plugins, so `:core:api` can be
configured and tested on a machine with no Android SDK at all:

```bash
./gradlew :core:api:test --configure-on-demand
```

**Consequences.**
* Anything added to `:core:api` that reaches for `android.*` is in the wrong module. That
  is the constraint, and it is a useful one — it keeps platform concerns at the edges.
* Compose UI, Hilt wiring and the encrypted store are only covered by `:app`'s own tests,
  which do need the SDK.
* If a second consumer ever appears — a desktop tool, a test harness — it can use
  `:core:api` unchanged.

---

## ADR-010 — The five intensity rungs, and a correction to ADR-005

**Date:** 2026-08-21 · **Status:** accepted · **Supersedes part of ADR-005** · **Milestone:** M2

**Context.** ADR-005 chose a 1–5 intensity scale and then never said what the five rungs
*were*. The spec carried `minimum: 1, maximum: 5` and a parenthetical, `microcopy.json`
had nothing, and so `intensity: 3` meant nothing to a user, to a reviewer, or to anyone
reasoning about whether the hard filter was doing useful work.

**Decision.** The rungs are time per week, because that is the only axis someone can
answer honestly in two seconds about a week they haven't had yet:

| | | filter admits |
|---|---|---|
| 1 | A few minutes | 1, 2 |
| 2 | Half an hour | 1, 2, 3 |
| 3 | An hour or two | 2, 3, 4 |
| 4 | Half a day | 3, 4, 5 |
| 5 | Several hours | 4, 5 |

They are named in `content/microcopy.json` under `onboarding.intensity.*`. The integer is
the matching filter's business and is never displayed — a person picks a phrase, not a
number, and never sees where they sit on a scale relative to anyone else.

The onboarding question is worded "what's realistic on a normal week, not a good one."
Optimism here produces a pod where someone quietly can't keep up, which is the exact
failure §2 rule 1 exists to prevent.

**Correction to ADR-005.** Its fallback clause said that if five rungs don't test as
distinguishable, collapse to three and keep the ±1 filter. That is wrong: on a 3-point
scale ±1 means level 2 matches everyone and levels 1 and 3 fail to exclude anything but
each other — the filter stops filtering. **The correct fallback is three rungs with exact
match.** Everything else in ADR-005 stands.

**Consequences.**
* Changing the number of rungs now means changing the filter arithmetic with it. The two
  are one decision, not two, and ADR-005 is the proof of how easily they come apart.
* The rungs are copy, so they are covered by `tools/copylint` and by both clients'
  catalogue tests.

---

## ADR-011 — The embedder is hashed bag-of-words until it isn't

**Date:** 2026-08-21 · **Status:** accepted, with a known expiry · **Milestone:** M2

**Context.** The 0.40 weight in `CLAUDE.md` §6 is embedding cosine over goal text. A real
embedding means a hosted model, which means a network dependency, an API key, a cost per
call and a vendor decision — none of which M2 needs in order to prove the pipeline.

**Decision.** `internal/embed.Hashing`: hashed word and word-pair features projected into
256 dimensions and normalised. Deterministic, dependency-free, and it makes the matching
job run end to end today.

It is a real implementation, not a stub — but it matches on shared vocabulary, not on
meaning. "Run every morning" and "start jogging daily" score **-0.07** against each other,
which is to say the 0.40 signal is close to noise for anyone who does not happen to use
the same words as their future pod-mate. There is a test that prints exactly that number
so nobody discovers it by accident.

Embedding happens at intent-save time, not in the matching job, so a matching window never
waits on a model call and a model outage delays nobody's onboarding.

**Consequences.**
* **This must be replaced before real users are matched.** Matching people on vocabulary
  overlap is not the product described in §1.
* Swapping it is one line at the two `embed.Hashing{}` call sites plus a backfill of the
  `intents.embedding` column, which is why `Dimensions` is a constant in the package
  rather than inferred from the stored data.
* Until then, the other three signals are carrying the match, and the category hard filter
  is doing more work than §6 intended.

---

## ADR-012 — The third-member threshold is 0.55

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M2

**Context.** `CLAUDE.md` §6 says to pair the highest scores, "then attempt a third above
threshold", and never names the threshold. Without one there is no assembly step.

**Decision.** `matching.DefaultThirdThreshold = 0.55`, and a third is judged on its
**weaker** pairing, not the average of the two.

The weaker pairing is the substantive half of this decision. A third who fits one member
brilliantly and the other not at all produces a pod of two plus a spectator, which is the
shape §2 rule 1 is most likely to be violated in — the spectator is the one who quietly
stops posting.

0.55 sits above what two people with nothing in common but the category can score, and
below what a genuinely good third scores. Unlike the four weights, it is a parameter
(`matching.Options`) rather than a constant, because it is the one number here that
genuinely wants tuning against real pools.

**Consequences.**
* Raising it produces more pairs, lowering it produces more trios. `CLAUDE.md` §6 is clear
  which way to err: **if no third clears it, ship a pod of two.**
* Tuning it is not a §8 escalation — it is not a weight and not a band. Changing any of
  the four weights still is.

---

## ADR-013 — The matching queue is a Postgres table, not Redis

**Date:** 2026-08-21 · **Status:** accepted · **Deviates from CLAUDE.md §5** · **Milestone:** M2

**Context.** `CLAUDE.md` §5 nominates "Redis for the matching queue". This is the one place
M2 does not follow a platform standard, so it needs to be visible rather than discovered.

**Decision.** The pool is `pool_tickets` in Postgres.

The reasoning is about what the matching job actually does. It is an hourly batch that
reads *every* waiting person's category, intensity, timezone, availability and embedding —
all of which are already Postgres rows — and then writes pods and dequeues their members
**in one transaction**. Redis holding the queue would mean the job reads ids from Redis,
joins them against Postgres, and then has two systems to keep consistent across a failure
between the dequeue and the pod insert. The failure mode is someone in a pod they cannot
see, or queued for a second one.

The transactional dequeue is not a nicety: `pod_members` has a partial unique index that
allows one active pod per person, so a concurrent window losing the race fails cleanly
rather than producing a duplicate. That guarantee needs the queue and the pods in the same
database.

Redis earns its place when the queue is hot — high write rate, low latency, pub/sub. An
hourly batch over a table is none of those things.

**Consequences.**
* One fewer production dependency for M2, and the matching job is testable against a plain
  Postgres.
* `Store` exposes the queue behind `JoinPool`/`LeavePool`/`ListPoolCandidates`, so moving
  to Redis later is contained to the store, not spread through the job.
* If matching ever needs to be closer to real time than hourly — which §5 says it should
  not — this decision gets revisited.
* **Overturn this if the Redis line in §5 was load-bearing for a reason not visible here.**
  It is one table and one implementation.

---

## ADR-014 — Any member names the week's action, and it locks on the first proof

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M3

**Context.** `CLAUDE.md` §1 says "each week the pod has one small action" and never says who
writes it. Three answers were available: derive it from the members' goal texts, have the
pod negotiate one, or let any member set it.

Deriving it means the machine tells two or three adults what their week is, from a sentence
they typed once at onboarding. Negotiating it means building a proposal-and-agree flow —
screens, notifications, a tie-break — before the loop the product exists for has run once.

**Decision.** Any member may name it. It stays editable until the first proof lands, and
locks after that.

Until somebody names one, the week carries a neutral default — "One small thing towards it
this week" — so the loop never blocks on a decision nobody made. `action_is_default` tells
the client to invite someone to name it; it is an invitation, never a nag.

The lock is the substantive half. Once a person has posted against what the week said,
changing the words rewrites what they did. That is a small betrayal, and exactly the kind
that erodes a low-stakes promise.

**Where the default text lives.** In `content/microcopy.json`, resolved by the server at
response time — not stored in the `weeks` row. An English sentence in a database column
would be a second source of truth for copy, which ADR-003 exists to prevent. The column is
empty while `action_is_default` is true.

**Consequences.**
* A pod where nobody names anything still works, week after week, on the default. That is a
  real outcome and an acceptable one.
* Two members editing in the same minute: last write wins, silently. Acceptable for two or
  three people; revisit if it ever bites.
* A negotiation flow is still possible later. This does not preclude one — it declines to
  build one before the week loop has been used.

---

## ADR-015 — The development blob store, and what it is not

**Date:** 2026-08-21 · **Status:** accepted, development only · **Milestone:** M3

**Context.** ADR-008 says proof bytes never pass through the API: a signed URL, a direct PUT
to object storage, then a commit. M3 has to make that flow real without an S3 account.

**Decision.** `blob.Store` is the seam. `blob.Local` implements it against the local disk,
signing URLs that point at `/blobs/{key}` on the service itself, and `config` refuses to
select it outside development — the same stance as the logging mailer in ADR-002. Both
`cmd/api` and `cmd/weekly` fail to start in any other environment rather than quietly
serving proof from the API tier.

The signing scheme is written by hand rather than borrowed, because the development store
has to verify its own signatures. It covers key, operation and expiry, so a leaked link
cannot be extended, retargeted at another proof, or upgraded from read to write. A forged or
expired link answers 404, not 403: there is nothing here to confirm the existence of.

The `/blobs` routes are deliberately outside `/v1`. They take no bearer token, they are not
in `openapi.yaml`, and they must not look like an API anyone should build against — the
signature in the query is the entire authorisation.

**What this is not.** In development the bytes *do* pass through the service, which is the
letter of ADR-008 broken. The interface is identical, so the week loop cannot tell; but
production must point at object storage, and the code refuses to start otherwise rather than
relying on anyone remembering.

**Consequences.**
* Proof bytes in development sit on a developer's disk until the sweep runs.
  `POD_BLOB_ROOT` should not be a directory anyone backs up.
* The production implementation is one type satisfying four methods. The signing helpers
  are reusable against S3 or GCS signed URLs with no change to callers.
* An upload is bounded twice — by the size the slot was signed for and by a hard cap on the
  route — so a client that signs for a small photo cannot then send a large one.

---

## ADR-016 — There is no notification about not having posted

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M4

**Context.** Every accountability product sends the reminder. "Alex hasn't posted yet."
"2 of 3 are in — don't be the one." "Your streak ends tonight." It is the most obvious
lever available and it is the single most effective way to violate `CLAUDE.md` §2 rule 1
while technically shipping the feature.

The temptation is strongest here because push is the one surface with no screen around it:
no context, no way to soften it, and it arrives while someone is doing something else.

**Decision.** The set of notification kinds is closed, and every one of them is either
addressed to the whole pod or is about something that **happened**:

| kind | who gets it |
|---|---|
| `pod_formed` | everyone |
| `week_opened` | everyone |
| `member_posted` | the others — someone is in |
| `week_closing` | **everyone**, including people who have already posted |
| `pod_completed` | everyone |

There is no kind for "you have not posted", and adding one means editing a closed `Kind`
set in `internal/notify`, which is where this ADR is quoted.

Three specifics that carry the weight:

1. **`week_closing` goes to everyone.** Sending it only to those still open would make it
   a message *about not having posted*, delivered to exactly the person who would read it
   that way. Its body — "Whatever happens, next week opens on Monday" — is §2 rule 2 said
   out loud, and it is true for every recipient.
2. **A week that closes incomplete sends nothing at all.** Nothing went wrong, so nothing
   reports that it did.
3. **The last person to post triggers `pod_completed`, not `member_posted`.** The pod
   finishing is the more interesting fact, and it belongs to everyone rather than being an
   announcement about the person who happened to go last.

**Why the server sends sentences here at all.** ADR-003 says the API returns copy keys and
never display text. A push payload has nowhere to put a key, because there is no client
running to resolve it. So this is the one exception, and it is bounded: bodies are resolved
from `content/microcopy.json` at send time, the outbox stores a key and parameters rather
than a rendered sentence, and `tools/copylint` covers those strings exactly as it covers
every other. A wording fix reaches everything not yet sent.

**Consequences.**
* Engagement will be lower than a product that nags. That is the trade the whole brief is
  making.
* A person with no display name still produces a notification, using `pod.someone` —
  silence would cost the pod its witness over a missing optional field.
* The outbox's `subject_id` is text, not a uuid, because `member_posted` is about a pair
  of things. Keying on the week alone would collapse two pod-mates' posts into one; keying
  on the poster alone would suppress their post in every week after the first.

---

## ADR-017 — What `pod_impact` means

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M5

**Context.** `CLAUDE.md` §6 weights the reliability score at
`0.15 × pod_impact // did their presence correlate with pod completion`. The weight is
fixed and out of bounds under §8. The *definition* is a comment.

**Decision.** Among the rolling eight weeks:

```
pod_impact = ( P(pod completed | they posted) − P(pod completed | they did not) + 1 ) / 2
```

mapped from [−1, 1] onto [0, 1]. Someone with no weeks on one side of that comparison —
they never missed, or they never posted — scores the neutral **0.5**. There is nothing to
compare and guessing would be worse than abstaining.

Why a difference of conditional rates rather than, say, the share of completed weeks they
were present for: the latter rewards being in a pod that completes, which is mostly a fact
about the other people. The difference measures what the brief actually asks — whether
their presence *made a difference* — and it lands at 0.5 for someone whose pod completes
regardless of them, which is the honest answer.

**Consequences.**
* It is noisy at small n. That is why it carries 0.15 and not more, and why the neutral
  case is common early on.
* It can be gamed by not posting in weeks the pod will fail anyway. Nobody has that
  information — a week's outcome is not knowable until it closes — so the attack requires
  predicting your pod-mates.

---

## ADR-018 — Recovery is an offer with no reason attached

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M5

**Context.** When a pod dissolves, the product has to say something. Everything true about
*why* it dissolved is a statement about people not showing up, and there is no wording of
that which does not land as a verdict on somebody.

**Decision.** `Recovery` carries two booleans and nothing else: `offer_lower_intensity` and
`offer_alternate_path`, plus a `suggested_intensity` when there is a gentler rung.

The copy never says why. "That pod has wound down. It happens. We'll find you another one
whenever you're ready." — no cause, no count, no reference to anyone's behaviour, and it
reads identically to the person who posted every week and the person who never did.

Accepting the lower-intensity offer is an ordinary `putIntent` with a smaller number. There
is no dedicated endpoint, because it is not a special kind of change — it is the same
change anyone can make at any time, surfaced at the moment it is most useful.

**What is deliberately absent from the wire:** the reliability score, the count of pods
that have ended, which members were inactive, and the reason string on the pod. The reason
is stored (`pods.dissolved_reason`) for operators and never returned.

**Consequences.**
* Somebody who was carrying a quiet pod gets the same message as the person who went
  quiet. That is a real cost, and it is the correct one: the alternative is telling
  somebody the machine noticed.
* The alternate-path offer appears after three (§6) and currently opens nothing — the
  one-to-one product does not exist. The client shows the offer and says it will be in
  touch, which is honest.

---

## ADR-019 — "Inactive" means two different things, and the gentler reading wins twice

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M5

**Context.** `CLAUDE.md` §6: *"pod inactive 2 consecutive weeks → dissolve → members
re-enter pool. Only inactive members take the small reliability penalty."*

Two words are doing a lot of work and they are not the same word twice.

**An inactive pod** could mean (a) no member posted, or (b) the pod did not complete.
**An inactive member** could mean (a) they did not post in the weeks that triggered it, or
(b) they never posted in that pod at all.

The readings interact. Under pod-(a) + member-(a), the penalty clause is vacuous: if nobody
posted for two weeks then everybody is inactive by definition, and "only inactive members"
excludes nobody. A sentence in the brief that excludes nobody is a sentence being read
wrong.

**Decision.** Pod-(a) and member-(b).

* **An inactive pod is one where nobody posted for two consecutive weeks.** A pod where one
  person keeps showing up is not inactive — it is a pod carrying someone, which is what
  pods are for. Dissolving that would end it *because* somebody kept turning up.
* **An inactive member is one who posted in none of that pod's weeks, ever.** Somebody who
  posted in week one and then went quiet is not the same as somebody who was never there,
  and the score should not treat them the same.

Each choice is the gentler of the two available, and together they make the penalty clause
mean something: the pod that dissolves is one nobody was posting to recently, and the
people who carry it are the ones who never posted at all.

**Consequences.**
* A half-active pod — one member posting every week, the other never — is never dissolved
  by this rule. The active member stays in a pod that does not complete, which is arguably
  worse for them than being re-matched. If that turns out to be the common failure, the
  fix is a *separate* rule about half-active pods, not a re-reading of this one.
* `dissolveIfInactive` reads the pod's whole history to decide who carries the count, not
  just the trigger weeks. That is a longer query for a rarer event, which is the right way
  round.

---

## ADR-020 — The intensity rungs must name increasing amounts of time

**Date:** 2026-08-21 · **Status:** accepted · **Amends ADR-010** · **Milestone:** M5

**Context.** ADR-010 fixed the five rungs. Its table named rung 4 *Half a day* and rung 5
*Several hours* — so the scale went up in the integer and down in the English. Nothing
caught it: the integer is what the hard filter compares, and no test reads the phrases as
durations.

It surfaced building the M5 recovery screen, whose entire premise is that rung N−1 is
*smaller* than rung N. For someone on rung 5 the offer would have read "you picked several
hours; half a day is also a real week" — a bigger commitment, presented as a smaller one,
to the person in the product least able to take it.

**Decision.** The rungs are:

| | | filter admits |
|---|---|---|
| 1 | A few minutes | 1, 2 |
| 2 | Half an hour | 1, 2, 3 |
| 3 | An hour or two | 2, 3, 4 |
| 4 | A few hours | 3, 4, 5 |
| 5 | Half a day | 4, 5 |

Rung 4 becomes *A few hours* and rung 5 becomes *Half a day*. The filter arithmetic is
unchanged — this is a copy correction, not a scale change, so it is not a §8 escalation.

`tools/copylint` now holds a phrase → minutes table and fails if the five rungs are not
strictly increasing. Renaming a rung means stating how long it is, which is the step that
was skipped.

**Consequences.**
* The rungs are ordinal in three places — the hard filter, the ±1 admission table, and the
  recovery offer. Only the third one reads the words, which is why the third one found it.
* Anyone who answered before this lands keeps their integer. Rungs 4 and 5 now describe
  slightly different amounts of time than the person picked. That is a real but small
  wrong-by-one-notch, and it is the smaller harm than leaving the scale inverted.

---

---

## ADR-022 — The reliability score is a fold, replayed from a fixed seed

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M5

**Context.** `Compute` implemented the two §6 movement rules — increment larger than
decrement, cap per-week movement so a single bad week can't drop a band — by comparing
this week against `raw(older weeks)` and damping the difference. Its own doc comment
called that "the score their history had before the most recent week".

It was not. The score they had before this week was itself damped. Measuring against the
undamped formula re-opened the whole gap every week, so both guarantees were computed
against a number the person never had. Checked exhaustively over every history inside the
window:

* a single week could move a score by **0.47**, against a stated cap of 0.10
* **56,388** histories where one bad week dropped a band
* histories where **posting a proof lowered the score by 0.30** — §2 rule 2 backwards

Every fixture test in the package passed throughout. The bug was invisible to examples
and obvious to enumeration.

**Decision.** `Compute` replays the history forward from a seed, one week at a time,
applying the asymmetry and the cap at every step.

1. **`raw` keeps the §6 window.** The formula sees a rolling 8 weeks, exactly as stated.
2. **The fold does not.** A cap on per-week movement is a statement about a trajectory,
   and a trajectory cannot be a function of an 8-week slice. `Window` (8, the formula) and
   `Replay` (520, the fold) are now separate constants.
3. **The seed must sit at a fixed point.** A fold seeded at "eight weeks ago" re-seeds
   itself every week, and the discontinuity is not small: measured, one week could move a
   score by **0.24** and a first bad week dropped a band in ~1.3% of full-window cases.
   Lengthening the replay shrinks it (0.13 at 12 weeks, 0.10 at 16, 0.025 at 52) but never
   removes it. Only a fixed anchor does, so the fold starts at the first week on record.
4. **The band floor holds for one week and is cleared by posting.** Keying it off position
   instead — "you may not cross unless you are already on the floor" — meant someone who
   reached a boundary by doing well lost a band to their first bad week. It is now
   explicit state in the fold, and a week someone posted never spends it.

**What is asserted now.** Over all 4^8 histories × 3 self-reports, and over the sequences
someone actually lives:

* no first bad week ever drops a band
* posting is never worse than not posting — ties only, where both are held at a floor
* no week moves the score by more than the cap
* out of the low band in **5** posted weeks; out of the high band in **6** missed ones

That last pair is §2 rule 2 in the form a person feels: getting back is cheaper than
falling.

**Consequences.**
* `raw` is still the §6 formula and is still tested as such. What changed is that it is
  the fold's *target*, not the score.
* Old weeks influence the score, through where they left it. They cannot not — that is
  what a movement cap means. `TestOnlyTheRollingWindowCounts` asserted the opposite and
  was wrong to; it is now `TestTheFormulaSeesOnlyTheRollingWindow`, plus a test that a bad
  patch washes out.
* `Postgres.RecentOutcomes` used to reset an over-large limit to 8. With `Replay` at 520
  that would have silently returned the old behaviour and no test would have failed. It
  clamps down to a ceiling now, and there is a test for the clamp specifically.
* The weights and the bands are untouched. This is a fix to the arithmetic that was
  supposed to implement §6, not a change to §6, so it is not an escalation under §8.
* The invariants are checked by enumeration, not by example. Fixtures could not have found
  this and did not.
