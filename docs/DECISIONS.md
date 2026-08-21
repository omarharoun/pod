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
---

## ADR-021 — A client can read its own intent back

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M5

**Context.** The contract had `putIntent` and no read. That was fine while the only writer
was onboarding, which holds the answers locally as it collects them. Recovery breaks the
assumption: someone whose pod ended answered these questions weeks ago, possibly on
another device, and the offer is to change exactly one field.

Without a read the client's options were to re-ask everything (a form the person already
filled in, which is what §2 rule 7 and the Variant B decision both refuse) or to send a
partial intent and let the server merge — which turns `PUT` into a `PATCH` that is not
documented as one.

**Decision.**
1. `GET /v1/onboarding/intent` returns the stored `Intent`, or 404 when there is none.
2. `self_report_reliability` leaves `IntentRequest`'s required set. Omitted, the stored
   answer is kept; for an account that has never answered, the cold-start default applies.
3. `Intent` does **not** carry the self-report back. It is an input to the reliability
   score and reflecting it would be the first step toward showing someone their own
   inputs. They can change it by answering again; they cannot read it.

**Consequences.**
* `PUT` is now "replace, except the one field you may omit". That asymmetry is documented
  on the field and is the price of not asking "how often do you keep commitments to
  yourself" a second time, of someone whose pod just ended.
* A 404 for "no intent yet" is a real 404 — the resource does not exist. It is
  distinguishable from an unrouted path because `GET /v1/onboarding/state` answers first
  and says which step they are on.

---


---

## ADR-023 — A cycle is six weeks, and graduating is not dissolving

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M6

**Context.** `CLAUDE.md` §7 names M6 "graduation, continue-or-close" and never says how long
a cycle is. Nothing in §2, §5 or §6 derives it. It is not on the §8 escalation list — not a
weight, not a band, not a §2 rule, not a parity divergence — so it is decided here and
recorded where it is cheap to change.

**Decision — six weeks.**

* Long enough that a shared streak means something. At four, a pod that loses two weeks to
  the dissolve threshold has half its cycle gone.
* Short enough to say yes to. Six weeks is a thing someone in a rut can agree to; three
  months is the commitment they have already failed at, which is why they are here.
* Under the 8-week reliability window, so one cycle can never fill the window on its own
  and pin someone's band.

`cycle.DefaultLengthWeeks` is the line to change. It is **copied onto each pod at
formation** (`pods.cycle_length_weeks`), so changing the default never moves the finish
line under a pod that is already running. The number is interpolated into copy
(`graduation.title` is "{count} weeks, done"), never spelled out, so changing it does not
leave a screen lying.

**Decision — `completed` is a state, and it is not `dissolved`.**

A pod that finished its cycle graduated. A pod that dissolved stopped early. They are
different events and the schema, the API and the copy all keep them apart:

* `CompleteCycle` touches neither `dissolved_pods` nor `recovery_offered_at`. Someone who
  finished six weeks and chose not to do another has nothing to recover from, and showing
  them "that pod has wound down" would be the product telling them something went wrong
  when nothing did.
* A check constraint forbids a row being both. A pod that is both has no correct screen.
* The job rules out dissolution *before* graduating, so a pod that went quiet for its last
  two weeks dissolves rather than graduating.

**Decision — the choice, and what it may not say.**

1. **Two continuing keeps the pod.** §6 says a working pair beats a forced trio about
   assembly; it is just as true of a second cycle. Whoever does not carry on re-enters the
   pool with no penalty of any kind.
2. **A "no" is private, structurally.** `Continuation` carries counts and the *caller's
   own* answer, and nothing else. There is no store method that returns who declined, no
   response field that could hold one, and a test asserts it against the serialised bytes
   for every member's view. "Alex is not continuing" is the most shaming sentence this
   product could show and it is one join away at all times.
3. **Silence is not a "no" until the deadline.** Two declines do not settle the choice
   while someone has not answered — they may still say yes, and reading their silence as
   agreement with the people who declined is what closes a pod out from under them. At the
   deadline, silence closes: holding someone in a pod they did not re-choose is the worse
   of the two mistakes.
4. **Answering does not act.** The weekly job restarts or closes the pod, once the answer
   cannot change or the deadline arrives — so the last person to answer is never the one
   who appears to have ended it.
5. **There is no "your pod closed" notification.** Telling someone their pod wound down
   because others declined is a statement about those others, and a push notification is
   the worst place to make it. They find it when they open the app.

**Consequences.**
* The weekly job must not open a week that starts at or after the cycle's finish. It did,
  once — graduating and opening a seventh week in the same tick, leaving a pod holding an
  open week nobody could post to and nothing would close.
* `ListActivePods` now means `state = 'active'`, not `state <> 'dissolved'`. A graduated
  pod is neither, and under the old predicate it kept being handed new weeks.
* The shared streak survives into the next cycle, because the streak belongs to the pod and
  the pod did not stop (§2 rule 3).

---

## ADR-024 — What a 16-agent audit found, and what it says about the tests

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M6

**Context.** A background workflow audited the codebase against `CLAUDE.md` §2 and §6,
with every candidate finding put to independent agents told to refute it. 53 candidates,
18 survived. The backend ones are fixed here. What matters is not the count but that
**every one of them passed the existing test suite**.

**The four that were product-rule violations, and why nothing caught them.**

1. **`availabilityOverlap` added the timezone offset instead of subtracting it.**
   `Zone()` returns seconds *east* of UTC, so local → UTC subtracts. Adding it inverted the
   0.20-weight availability signal: two people whose local hours look identical but whose
   zones differ scored a *perfect* overlap, and two people genuinely free at the same
   instant scored zero. The existing test passed because its fixture was symmetric — both
   candidates shifted the same wrong way, and the error cancelled. The fix folds the day
   into a shared minutes-since-Monday-UTC frame, which also stops a window that crosses
   midnight once shifted from silently losing its minutes.

2. **The sweep skipped the bytes of an upload that was never committed** (§2 rule 4). It
   deleted only when `blob_key` was set, and `blob_key` is written by `commitProof` — so a
   client that PUT its bytes and then lost the network left a real photo in storage
   forever, while `MarkProofDeleted` marked the row gone and made it unlistable. Nothing
   else scans by prefix; there was no second chance. The key is derivable, so it is now
   used whether or not the proof was committed.

3. **The upload slot outlived its week** (§2 rule 4). `ViewProof` clamps its link to the
   proof's expiry; `StartProof` did not clamp to the week's close, so a slot signed a
   minute before close was writable for nine minutes after — long enough for bytes to land
   after the sweep had passed. The same clamp now applies to both.

4. **`ListWeeksForMember` failed open on Postgres** (§2 rule 5). Membership was enforced by
   a `JOIN`, which enforces nothing on a query that returns a list: a stranger matched no
   rows and got `200 {"weeks":[]}` — a working existence oracle for pod ids, and
   indistinguishable from a member of a pod with no weeks yet. `Memory` checked membership
   explicitly and returned `ErrNotFound`, so **every test that ran against `Memory` said
   the rule was held.**

**The lesson, which is the reason this ADR exists.**

Three of the four were invisible to the tests for the same reason: the test and the bug
shared an assumption. A symmetric fixture cancels a sign error. A fixture that always
commits never reaches the uncommitted branch. A test double that is stricter than
production proves the rule about the double.

The countermeasures now in the tree:

* `TestMemoryAndPostgresAgree` covers **refusal**, not only success. A store double that
  refuses where production permits is worse than no double, because it makes the suite
  lie. Every rule enforced in one implementation belongs in this table.
* The reliability invariants are checked by enumeration over all 4^8 histories (ADR-022),
  not by fixtures.
* The availability tests are now deliberately asymmetric: one candidate in a positive
  offset, one in a negative, different local hours, same instant.

**Also fixed.** A path id that is not a uuid returned 500 (Postgres `22P02` reaching the
default branch) where a well-formed unknown one returned 404 — an existence oracle again,
and a server error logged for a client typo. `wrap` maps it to `ErrNotFound`.

**Still open, on the clients** (they need a Mac and an Android SDK to verify, per the
standing decision to defer client builds): iOS declares no `NSCameraUsageDescription`,
`NSMicrophoneUsageDescription` or `NSPhotoLibraryUsageDescription`, so both proof-capture
paths terminate the app on a real device — M3's central action; Android never requests
`POST_NOTIFICATIONS`, so push is dead on Android 13+; the Android camera path leaves its
temp file on cancel; and several smaller parity gaps in auth and locale formatting.

---

## ADR-025 — The client fixes from the audit, and where copy lives when a plist reads it

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M6

**Context.** ADR-024 covers the backend half of the audit. This is the client half. Two of
these were blocking and neither was catchable on a machine without a device.

**iOS declared no usage descriptions.** No `NSCameraUsageDescription`, no
`NSMicrophoneUsageDescription`, no `NSPhotoLibraryUsageDescription`. iOS *terminates* a
process that touches the camera, the microphone or the library without them — there is no
exception to catch and no error to render, so both proof-capture paths killed the app the
instant they were tapped. That is M3's central action, and M3 is the product.

The strings are user-facing, so `content/microcopy.json` is their source of truth. But an
`Info.plist` cannot resolve a copy key: the OS reads the literal string out of the bundle.
So the rule is kept by **checking rather than resolving** — `tools/copylint` fails if
`ios/project.yml` is missing one of the three keys, or if its value has drifted from the
matching `permission.*` string. Verified by breaking it both ways.

This is the general answer for copy that a platform reads statically. Where a key can be
resolved, resolve it; where it cannot, the literal is checked against the key it came from
and the check is part of `make verify`.

**Android never requested `POST_NOTIFICATIONS`.** It was declared in the manifest, with a
comment saying it was asked for when a pod forms. Declaring it is not asking for it: on API
33+ it is denied until requested, so every notification the backend sent was dropped on the
floor. It is now requested at the moment a pod forms — the same moment iOS asks, and the
first moment "can we let you know when your pod does something" has a subject. A refusal
produces nothing: the week loop does not depend on push.

**Smaller things, each a real divergence.**

* The Android camera path deleted its temp file on success but not on cancel, and not on
  dismissal. A camera app that writes the frame before the user backs out left a real proof
  image in the app cache with no expiry and no sweep — against §2 rule 4 and against the
  manifest's own stated promise. It now deletes on both.
* `sendUnit` had no refresh-and-retry, so `leavePool`, `registerDevice` and
  `unregisterDevice` were the three calls that simply failed on a token rejected for clock
  skew — quietly, since none of them surfaces its failure. Sign-out is deliberately
  excluded: refreshing rotates the refresh token, so retrying it would mint a fresh session
  moments before the local tokens are discarded, leaving a live one nothing will revoke.
* An expired session at launch rendered a server-error notice on Android's sign-in screen.
  iOS distinguishes "could not be renewed" from "could not be reached"; Android did not.
* The Android code field used `NumberPassword`, masking the digits. The code arrives by
  email (ADR-002), so the person is reading it off one screen and typing it into another —
  masking hides the one thing they need to check. It also lost the typed code and restarted
  the resend countdown on rotation; both are `rememberSaveable` now.
* iOS's keyboard Go key went through `.onSubmit`, which the button's disabled state does
  not reach, so a second sign-in could fire while the first was in flight.
* iOS sent `en_US` where the contract specifies BCP 47. `Locale.identifier` is the ICU
  form; `.identifier(.bcp47)` is the contract's.
* The two clients reported different durations for the same voice note — iOS from a
  whole-second countdown, Android from real elapsed time. A 3.4-second note was 3000ms on
  one and 3400ms on the other, and they disagreed either side of the server's ten-second
  limit. iOS now reads the recorder's own position.

**What is still unverified.** These are reasoned changes to code no compiler in this
environment can check: `:app` needs the Android SDK and iOS needs Xcode. `:core:api`
compiles and its 52 tests pass, so the client changes inside it are verified; the Compose
and SwiftUI ones are not. That is the standing trade — the alternative was to leave a
known app-terminating bug in the tree until a Mac was available.

---

## ADR-026 — A paid slot is a second *thing*, not a second pod for the same goal

**Date:** 2026-08-21 · **Status:** accepted · **Escalated under §8** · **Milestone:** M7

**Context.** `CLAUDE.md` §2 rule 6 says "one free pod, forever; the paywall gates
*additional* pods, never the core loop". §1 says a user names **one** thing. Those two
sentences do not by themselves say what an additional pod is *for*, and the answer changes
the schema, the matching filter and the effort budget. This was escalated rather than
decided, and the decision below is the answer that came back.

**Decision — a slot is a lane, and each lane is its own thing.**

`slot 0` is the free thing. `slot N` is an additional thing with its own goal text, its own
category, its own intensity, its own queue ticket and its own pod.

The rejected alternative was a second pod drawn from the *same* intent — more witness for
the one thing you named. It loses on three counts:

1. **Effort.** Two check-ins a week against one goal spends §2 rule 7's budget twice for
   one outcome. The rule says a flow over ~15 seconds is wrong; two flows for one goal is
   the same mistake in a different shape.
2. **Matching.** Both pods would be drawn from the same candidate set under the same hard
   filters, so the second pod is systematically the *worse* half of the first pod's
   shortlist — and the person paid for it.
3. **It is not what a rut looks like.** Someone who has broken one thing loose and wants to
   start another is the case the paywall is actually for.

**What this widens.** §1 becomes "one **free** thing". That is a real change to the
product's first paragraph and it is named here so nobody has to infer it from a migration.
The core loop is untouched: slot 0 needs no entitlement, ever, and no code path may make
the free pod's existence depend on a payment record.

**Consequences for the schema.**

* `intents` is re-keyed from `(user_id)` to `(user_id, slot)`. So is `pool_tickets`.
* `pod_members` gains `slot`, and the invariant narrows rather than disappearing:
  ```sql
  DROP INDEX pod_members_one_active_per_user;
  CREATE UNIQUE INDEX pod_members_one_active_per_slot
      ON pod_members (user_id, slot) WHERE left_at IS NULL;
  ```
  Dropping it outright would delete the ADR-013 guarantee — that index is what makes two
  concurrent matching windows lose the race cleanly instead of producing someone in two
  pods, and it is cited by name in `CreatePod`. The replacement keeps the same failure mode
  at a finer grain.
* **`slot` belongs to the membership, not to the pod.** Your slot-1 pod is somebody else's
  slot-0 pod. There is no `slot` column on `pods`, and on the wire `Pod.slot` is the
  *caller's* slot in it, computed per viewer exactly like `PodMember.is_you`.
* **The index says nothing about entitlement and must never be made to.** It caps pods per
  slot at one; it does not cap slots. A constraint of the shape "you may have as many
  active memberships as you have grant rows" would make the free pod's existence depend on
  a payment record, and one missing or badly-migrated row would take the core loop away
  from someone who never paid for it and never needed to.

**Two more decisions, recorded here so they are not silent.**

* **`MaxSlots = 5`.** A structural ceiling *above* the paywall and unrelated to what anyone
  paid: someone running six small things a week is not doing the product in §1. A
  judgement, not a weight or a band, so not itself a §8 item — but said out loud.
* **Data minimisation (§8, fourth bullet).** An entitlement row stores `source` and an
  opaque provider transaction id, and nothing else. No name, no card, no address, no price,
  no country. Prices come from StoreKit and Play at render time and never from this API.
  The server needs to know *that* a slot is paid for, not who paid or what it cost.

---

## ADR-027 — Three ways a finished cycle was reported back as a failure

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M7

**Context.** A second adversarial audit ran over the M5/M6 code and the fixes from ADR-024.
It died partway through on a usage limit — 11 of 51 agents finished — but two findings had
already survived refutation, and a third turned up alongside them. All three are the same
shape: a thing that went *fine* being shown to someone as a thing that went wrong. That is
§2 rule 1, and it is the failure mode this product exists to avoid.

**1. The alternate path summoned the recovery screen.**

Both clients decided the recovery phase with `offerLowerIntensity || offerAlternatePath`.
The second flag is derived from `users.dissolved_pods >= 3`, a count that only ever
increments and is never cleared. So anyone who had had three pods dissolve saw *"That pod
has wound down"* and the one-to-one card **every time they were between pods** — including
immediately after graduating a cycle and choosing to stop.

ADR-023 forbids exactly that in writing, and the backend honours it: `CloseCompletedPod`
and `StartNextCycle` touch neither `dissolved_pods` nor the recovery offer. The client
phase check broke it anyway. Worse than a one-off: such a person could effectively never
reach the ordinary questions screen again.

Gated on `offerLowerIntensity` alone now — the flag that actually means "a pod dissolved
under you". The alternate path rides along on that screen; it does not summon it.

The test that was supposed to guard this missed it by construction: it asserted both flags
false after `CompleteCycle`, but its fixture users had `dissolved_pods = 0`, so the flag
was false for a reason unrelated to graduating.

**2. The graduation count was the trailing streak.**

`streak_weeks` is reset to zero by any miss, so it is *consecutive weeks to date*, not
*weeks everyone was in*. Bound to the graduation copy it produced two wrong screens:

* fully in for weeks 1–5, missed week 6 → streak 0 → the consolation line, handed to a pod
  that landed five of six because of *when* the miss happened;
* fully in for 1, 2, 3, 5, 6, missed 4 → *"2 weeks where everyone was in"*, quietly
  deleting three weeks the pod did complete and that everyone there remembers.

`Cycle` gains `weeks_everyone_was_in`, counted from `weeks.pod_completed`, and both screens
bind to that. `Cycle.weeks_completed` stays what it says it is — the position in the cycle,
not a measure of anybody — and the two are now documented against each other.

The zero-case copy was wrong too: *"Some weeks landed, some didn't"* is false for a pod
where none did. It is *"Not every week landed. Getting to the end still counts."*

**3. A pod ending in one lane followed you into another.**

Found while wiring M7. `recovery_offered_at` was a column on `users`, so a lane-1 pod
dissolving put *"that pod has wound down"* — and an offer to lower the intensity — in front
of lane 0, whose pod was running perfectly well. Unexplainable as well as wrong, because
ADR-018 makes the copy deliberately identical for everyone and it cannot say which pod it
means.

Migration 0008 moves the offer to a `recovery_offers` table keyed by `(user_id, slot)`. The
dissolved-pods **count** stays per user on purpose: three pods ending under someone is a
fact about them whichever lanes it happened in, and that is what §6 keys the one-to-one
path on.

**The pattern, again.** ADR-024's lesson was that a test and a bug can share an assumption.
All three of these are the same lesson from the other side: the *backend* was right in every
case, and the mistake was in what the client decided to show. Rules kept in the store and
broken in the view are still broken. Both clients now assert their phase decisions, and the
graduation copy is checked against a count with a defined meaning rather than the nearest
number that looked like one.

---

## ADR-028 — The API had never run against Postgres, and two bugs were waiting there

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M7

**Context.** Every test in `internal/httpapi` ran the server against `store.Memory`. That
is fast, and it is also precisely the arrangement that let ADR-024's bug ship: a rule
enforced in the double and not in production, with the whole suite reporting it held.

So the API had been exercised thousands of times and **never once against a real
database**. `TestAWholeCycleAgainstPostgres` wires the real server to Postgres and walks
two people from signing in through six weeks to graduation and a second cycle. It found two
bugs in its first run, and neither was subtle once seen.

**1. Every pod dissolved after two weeks, whoever showed up.**

`ListWeeksForMemberless` returned weeks *without their members* on Postgres.

"Memberless" means **not scoped to a member** — it is the weekly job's view, and it
deliberately skips the membership check the caller-facing list makes. It does not mean the
weeks come back stripped of who was in them. `dissolveIfInactive` asks each week whether
anyone posted; a week with no members answers no; so a pod where everybody posted every
week looked exactly like a pod nobody had touched.

`Memory` loaded the members. Every dissolve test — including the one specifically about a
pod where one person keeps showing up — ran against `Memory` and passed. In production this
would have dissolved every pod in the product on its second week.

**2. No graduated pod ever restarted or closed.**

The weekly job asks for continuation counts with no caller, so it passed `""` as the user
id. Postgres rejects an empty string as a `uuid` — `22P02` — and ADR-024's own fix maps
that to `ErrNotFound` so a malformed path id answers 404 rather than 500. The job caught
the error, logged, and skipped. Forever. `NULL` is the honest way to say "no caller", and
that is what it passes now.

Note the shape of this one: a correct fix (`22P02` → not found) turned a loud failure into
a silent one somewhere else. Mapping an error class is never local.

**Decision.** One end-to-end test, against Postgres, covering the whole journey. It is
deliberately a single long test rather than several: the value is in the sequence, each
step depending on the last having really happened, and splitting it would mean seeding the
middle — which is exactly where these two were hiding.

Both bugs are also pinned in `TestMemoryAndPostgresAgree`, which is where they should have
been caught. That table is now the standing answer to this class: **every rule enforced in
one store implementation belongs in it.**

**Consequences.**
* `week.Service.Open` is exported and the matching job opens a new pod's first week in the
  same breath as forming it. Found by the same test: a pod had no week until the weekly
  job's next tick, so its members were told "you've got a pod" and then opened the app to a
  404 — which the client renders as *"Something's off on our end"*, on the best moment in
  the product.
* `cmd/matcher` therefore needs a week service, which needs a blob store it must never use.
  It gets `blob.Unavailable`, whose every method fails loudly: a process that has no
  business touching proof bytes should not be handed something that quietly can.
* The journey test clears the database it runs against. It is the only test that does, and
  it must stay that way — it is why it can assert that *no* proof survived the cycle.

---

## ADR-029 — The second audit, and the cycle that lasted an hour

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M7

**Context.** A second workflow audited M7 and the ADR-027 fixes. Twelve findings, verified
adversarially, none refuted. Most were mine from the same day. They are recorded together
because they share a cause worth naming, and because one of them uncovered something worse
than itself.

**What it found, and what was true.**

* **`:app` did not compile.** `GraduationScreen.kt` called `Modifier.fillMaxWidth()` without
  importing it. `clientlint` cannot catch this — it checks generated symbols, not the
  standard library — and no compiler here can either.
* **iOS's graduation screen read a stale pod.** It took `pod` as an argument and never read
  `controller.pod`, so answering continue-or-close changed nothing on screen. Android read
  the returned pod from the start. The controller's own test passed: it asserted the
  controller, and the view was what was broken. That is ADR-027's pattern for the third
  time in a day.
* **The paywall's network call disabled the continue-or-close choice** on Android, because
  `working` merged the cycle's state with the slots request's. An entitlements query
  decided whether a core-loop control was tappable — §2 rule 6, exactly.
* **A failed slots request rendered an error on the free lane's screen.** Both clients. The
  offer already hides itself when slots are unavailable; the notice was pure noise on the
  one screen that should feel like an ending.
* **`podTimezone` matched on user id alone**, so one lane's ticket could set another lane's
  pod's shared timezone.
* **`PodMember.Slot` was always 0 from Postgres** — neither member query selected it, while
  Memory returned the real value. The divergence class again.
* **`DissolvePod` wrote a recovery offer for every membership row**, including ones a
  previous graduation had already ended. Someone who finished a cycle and chose to stop
  would be told "that pod has wound down" about the pod they had finished.
* **`weeks_everyone_was_in` counted the pod's whole life**, not the cycle being graduated.

**And the one underneath.** Fixing that last one meant anchoring the count to the cycle's
start — at which point it became obvious that *everything* about a cycle was anchored to
`pods.created_at`: where it ends, how many weeks have closed, whether it is over. That is
right for a first cycle and wrong for every one after it. **A pod that carried on was
already past its finish line, so the next tick graduated it again. Its second cycle lasted
one hour.**

M6 shipped with that. Nothing caught it because no test had ever run a pod past its second
cycle's first tick — the M6 tests asserted the pod restarted and stopped there.

Migration 0009 adds `pods.cycle_started_at`, set at creation and reset by `StartNextCycle`,
and every piece of cycle arithmetic now measures from it.

**Two smaller things found while fixing it**, both worth recording because both were
silent:

* `ListPodsAwaitingContinuation` selected eleven columns and scanned ten. The weekly job
  logs and continues on a store error, so no graduated pod ever restarted or closed — and
  nothing said so. That is the second time this session a swallowed error in that job hid
  a total failure of a feature (ADR-028 was the first). **The job's log-and-continue is
  right for one pod failing and wrong for a query that cannot succeed for any pod.**
* Counting from the cycle's start instant dropped the cycle's *first* week, because a pod
  formed on a Thursday joins the week already in progress and that week opens before the
  pod exists. The count runs from the first week's opening now.

**Consequences.**
* Five end-to-end journeys against Postgres now: a whole cycle, a quiet pod dissolving, a
  pod one person carries, two lanes getting two pods, and a second cycle that is a whole
  cycle. The last one exists solely because of this ADR.
* Two findings are deliberately **not** fixed: no client can address a lane other than 0,
  so a purchased lane has no screen. That is a real gap and it is recorded in
  `docs/VERIFYING.md` rather than papered over — see ADR-030.

---

## ADR-030 — The Mac was in CI the whole time

**Date:** 2026-08-21 · **Status:** accepted · **Milestone:** M7

**Context.** `docs/VERIFYING.md` said nothing in `ios/` had ever been compiled, and laid out
what to do on a rented Mac. The user pointed out that GitHub Actions has `macos-15` runners.

They were right, and it was worse than a missed opportunity: the `ios` and `android-app`
jobs went into `.github/workflows/verify.yml` back at M3, so **twenty-eight pushes had each
compiled both clients and reported the result**, and the answer was sitting in a job log
every time. I had gone on describing the clients as unverifiable while a machine verified
them, and failed, on every commit.

**Decision.** CI is the client compiler. `make verify` remains the local gate — it is what
can run without Xcode and the Android SDK — but a client change is not done until the run
for that commit is green. Read the job log rather than reasoning about what Swift will say.

**What the log said, once read.** Four rounds, each one error class:

* **PodKit did not compile.** A `static let` array of `ISO8601DateFormatter` is not
  concurrency-safe under Swift 6. Now `Date.ISO8601FormatStyle`, which is a Sendable value
  type — the safe fix rather than `nonisolated(unsafe)`, which would have asserted a
  thread-safety guarantee Foundation does not make.
* **`:app` did not compile.** `androidx.compose.foundation.layout.weight`, imported in
  three files, is an internal extension on layout parent data, not the modifier;
  `Modifier.weight` is a `ColumnScope` / `RowScope` member needing no import. Plus a
  missing `Text` import.
* **Every request in a bought lane 404'd.** `URL.appendingPathComponent` escapes what it is
  handed, so `"/v1/pool?slot=2"` went out as `/v1/pool%3Fslot=2`. Path and query are built
  through `URLComponents` now.
* **Two main-actor properties.** `UIDevice.current` is isolated in the iOS 18 SDK.
* **The plist check was reading the wrong plist.** With the app target finally building,
  the step that guards ADR-025's usage descriptions ran for the first time — and looked at
  `Pod.app/PodKit_PodAPI.bundle/Info.plist`, because `-path '*Pod.app*'` matches a resource
  bundle nested inside the app and `head -1` picked it. It now matches
  `*/Pod.app/Info.plist` exactly. A check that has never executed is not a check, whatever
  it says it does.

**The one worth keeping.** The lane 404 had a test — written the same day, asserting exactly
the right thing — that had never run once, because the package it lives in did not compile.
Android had the identical test, in a module that *does* build here, and it was green from
the start.

That is the ADR-024 pattern with the roles swapped. There, a rule was enforced in the test
double and not in production, and the suite reported that it held. Here a rule was checked
on the platform that could run its tests and merely *written down* on the platform that
could not — and the write-up read exactly like the check. **A test that cannot run is
documentation, and the repository has no way to tell the two apart.** The countermeasure is
not another test; it is that every test must be somewhere that runs it.

**Consequences.**
* `docs/VERIFYING.md` is rewritten. Its "not verified" table was true when written and
  false by the next commit, which is its own lesson: a status file that is not derived from
  something is a claim, and it goes stale silently.
* The remaining unverified surface is genuinely device-only: proof capture against real
  camera and microphone permissions, and a notification refusal on Android 13+.
* ADR-029 closes by pointing at this ADR for the two findings it deferred. Both are fixed:
  the lane switcher landed before this, and the lane's requests work as of this.
