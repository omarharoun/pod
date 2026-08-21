# What is verified, and what is not

Everything in this repository has been written against `CLAUDE.md`, but not everything has
been *run*. This file says exactly which is which, so nobody has to guess — and so the
first person with a Mac knows what to do and what to expect.

Last updated after ADR-027.

---

## Verified, and by what

| Layer | How | Status |
|---|---|---|
| The contract | `make spec` — regenerates and diffs all 11 generated files | green |
| Copy | `make copylint` — 202 strings, banned vocabulary, rung ordering, iOS plist drift | green |
| Lanes | entitlement unit tests, plus the free lane surviving a broken entitlements store | green |
| Backend | `go test ./...` with `TEST_DATABASE_URL` set — unit + Postgres integration | green |
| Migrations | applied to a throwaway empty database on every run, then re-applied | green |
| Reliability | every one of the 4^8 histories × 3 self-reports, by enumeration | green |
| Matching | unit tests including the shared-frame availability arithmetic | green |
| Android `:core:api` | `make android-core-test` — plain Kotlin/JVM, no SDK needed (ADR-009) | green, 73 tests |
| Client symbols | `make clientlint` — copy keys and initialiser labels against the generated models | green |
| End to end | five journeys against Postgres: a cycle, a dissolve, a carried pod, two lanes, a second cycle | green |

`make verify` runs all of the above. It is what CI runs, and it passes.

## Not verified, and why

| Layer | Blocked on | Risk |
|---|---|---|
| iOS — everything | Xcode. No Mac in the build environment. | **High.** Nothing in `ios/` has ever been compiled. |
| Android `:app` | The Android SDK, and network access to the Gradle plugin. | **Medium.** Compose, Hilt wiring, and every screen. |

`:core:api` is deliberately a plain Kotlin/JVM module (ADR-009) precisely so that the
repositories, the API client, the copy catalogue and their tests can be verified without
the SDK. Everything above that line — the Compose screens, the view models, Hilt — cannot.

**This gap has already produced four bugs**, none caught by a compiler and all of which
would have shipped:

* iOS declared no `NSCameraUsageDescription` or `NSMicrophoneUsageDescription`, so the OS
  terminated the app the instant proof capture was tapped — M3's central action (ADR-025).
* Android declared `POST_NOTIFICATIONS` and never requested it, so push was dead on API
  33+ while a manifest comment claimed otherwise (ADR-025).
* Both clients decided the recovery phase on a flag that is never cleared, so anyone with
  three prior dissolves was shown "That pod has wound down" every time they were between
  pods — including straight after graduating (ADR-027).
* Both graduation screens bound their count to the trailing streak, handing a five-of-six
  pod the copy written for a pod that managed none (ADR-027).

Note what the last two have in common with each other and not with the first two: the
*backend was correct in both cases*. The rule was kept in the store and broken in the view.
No amount of server-side testing would have found them.

Assume there are more.

`make clientlint` closes a narrow slice of this. It is not a type checker and does not
pretend to be one: it checks that every copy key and every initialiser label the
hand-written client code uses actually exists in the generated models. That is the mistake
writing against generated code by hand actually produces, and it is the only compiler
substitute available without a Mac. It will not catch a concurrency error, a missing
`@OptIn`, or a view that reads state outside composition.

---

## On a Mac, in this order

### 1. The gate that already passes

```sh
brew install go postgres@16 openjdk@17
createdb podtest
export TEST_DATABASE_URL="postgres://localhost:5432/podtest?sslmode=disable"
make verify
```

If this fails, something is wrong with the environment, not the code.

### 2. iOS — the first compile this code has ever had

```sh
brew install xcodegen
swift test --package-path ios/PodKit       # PodKit alone, no app target
cd ios && xcodegen generate && xcodebuild -scheme Pod -destination 'platform=iOS Simulator,name=iPhone 15' build
```

**Expect failures on the first run.** Every Swift file here was written without a
compiler. What to look for, in rough order of likelihood:

* missing or wrong imports
* `@Observable` / `@MainActor` / `Sendable` mismatches under Swift 6 strict concurrency
* result-builder problems inside `#Preview` and `@ViewBuilder` bodies
* generated symbols that do not exist — check against
  `ios/PodKit/Sources/PodAPI/Generated/`

Then open the previews. Every screen has at least two, including its awkward states: a
graduation with a zero streak, a recovery offer on the smallest rung, a pod of two.

### 3. Android `:app`

```sh
cd android && ./gradlew :app:assembleDebug :app:test
```

Same expectation. Look for missing `@OptIn` on experimental Material 3 and coroutines
APIs, Hilt bindings that do not resolve, and state read outside composition.

### 4. On a device — the two things a simulator will not tell you

1. **Tap proof capture, both photo and voice.** This is what the missing usage descriptions
   would have crashed. It is the product's central action and it needs a real device.
2. **Grant, then deny, notifications on Android 13+.** Confirm a refusal produces nothing —
   no nag, no error, no blocked screen. The week loop does not depend on push and must
   behave that way.

### 5. The two flows worth walking end to end

* **A full cycle.** Six weeks, graduation, continue-or-close. Check the zero-streak line
  reads right for a pod that mostly missed — that is the copy this product exists to get
  right, and it is the one a reviewer is least likely to see.
* **A pod that dissolves.** Two quiet weeks, then the recovery screen. Check it never says
  why, and reads identically for the member who posted every week.

---

## What has no implementation at all

Three seams are deliberately empty, and each refuses to start rather than pretending:

| Seam | Where | What it needs |
|---|---|---|
| Transactional email | `cmd/api` `selectMailer` (ADR-002) | a provider; in development the code goes to the log |
| Blob storage | `cmd/api` `selectBlobStore` (ADR-015) | a bucket; in development, local disk |
| Receipt verification | `cmd/api` `selectReceiptVerifier` (ADR-026) | StoreKit and Play credentials |
| Purchase sheet | `PurchaseBroker` on both clients | the same, client-side |
| Push delivery | `push.Sender`, `PushTokenSource` | APNs and FCM credentials |
| Embeddings | `internal/embed` (ADR-011) | a real model — the hashing stand-in **scores a synonym pair at −0.07**, which is worse than random, and ADR-011 says so |

Every one of these is wired so that the product works without it. An app with no billing
behaves like someone who cancelled; an account with no push behaves like someone who
declined. None of them can take away the free pod.

**The embedder is the one to fix first.** It is not a seam that degrades gracefully — it
feeds the 0.40-weight similarity signal with numbers that are actively wrong, so matching
is currently running on three signals and noise.
