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
