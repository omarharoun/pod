# What is verified, and what is not

Everything in this repository has been written against `CLAUDE.md`. This file says which
parts of it have actually been *run*, by what, and what is left that no machine here can
answer.

Last updated after ADR-030.

---

## Where the checks run

Two places, and the difference matters.

**`make verify`** is the local gate. It runs the contract, the copy rules, the whole Go
suite against Postgres, the Android `:core:api` tests and `clientlint`. It is everything
that needs neither Xcode nor the Android SDK.

**`.github/workflows/verify.yml`** runs that same gate on Ubuntu, and then two more jobs
that `make verify` cannot: `ios` on a `macos-15` runner, and `android-app` with the Android
SDK. **A client change is not done until the run for that commit is green.** Read the job
log; do not reason about what the compiler would have said. See ADR-030 for why that
sentence is in this file.

## Verified, and by what

| Layer | How | Where |
|---|---|---|
| The contract | `make spec` — regenerates and diffs all 11 generated files | local + CI |
| Copy | `make copylint` — 202 strings, banned vocabulary, rung ordering, iOS plist drift | local + CI |
| Lanes | entitlement unit tests, plus the free lane surviving a broken entitlements store | local + CI |
| Backend | `go test ./...` with `TEST_DATABASE_URL` — unit + Postgres integration | local + CI |
| Migrations | applied to a throwaway empty database on every run, then re-applied | local + CI |
| Reliability | every one of the 4^8 histories × 3 self-reports, by enumeration | local + CI |
| Matching | unit tests including the shared-frame availability arithmetic | local + CI |
| End to end | five journeys against Postgres: a cycle, a dissolve, a carried pod, two lanes, a second cycle | local + CI |
| Android `:core:api` | plain Kotlin/JVM, no SDK needed (ADR-009) | local + CI |
| Client symbols | `make clientlint` — copy keys and initialiser labels against the generated models | local + CI |
| **iOS — PodKit** | `swift test --package-path ios/PodKit` — compiles under Swift 6 strict concurrency, 82 tests | **CI only** |
| **iOS — the app** | `xcodegen generate` + `xcodebuild build` for the simulator | **CI only** |
| **iOS — permissions** | the three usage descriptions asserted in the built app's `Info.plist` | **CI only** |
| **Android `:app`** | `./gradlew :app:assembleDebug test` — Compose, Hilt, every screen | **CI only** |

## Not verified, and why

| Layer | Blocked on | Risk |
|---|---|---|
| Previews | a human looking at them. `xcodebuild build` proves they compile, not that they read right. | Low |
| Proof capture | camera and microphone permissions on a real device | **Medium.** A simulator does not exercise TCC. |
| A notification refusal | Android 13+ on a real device | Medium |
| Anything behind a seam | credentials — see the last section | by design |
| Goals outside the lexicon's English | a hosted embedding model (ADR-031) | Low. Unknown vocabulary scores on words, as it did before. |

Everything else that used to be in this table is now in the one above it.

## What the gap produced while it lasted

Eight bugs, none catchable by anything running locally, all of which would have shipped.

Four found by review, before CI was read:

* iOS declared no `NSCameraUsageDescription` or `NSMicrophoneUsageDescription`, so the OS
  terminated the app the instant proof capture was tapped — M3's central action (ADR-025).
* Android declared `POST_NOTIFICATIONS` and never requested it, so push was dead on API
  33+ while a manifest comment claimed otherwise (ADR-025).
* Both clients decided the recovery phase on a flag that is never cleared, so anyone with
  three prior dissolves was shown "That pod has wound down" every time they were between
  pods — including straight after graduating (ADR-027).
* Both graduation screens bound their count to the trailing streak, handing a five-of-six
  pod the copy written for a pod that managed none (ADR-027).

Four found by finally reading the job log (ADR-030):

* PodKit did not compile at all: a shared `static let` of `ISO8601DateFormatter` is not
  concurrency-safe under Swift 6.
* `:app` did not compile: `androidx.compose.foundation.layout.weight` is not the modifier,
  imported in three files, plus a missing `Text`.
* Every request in a bought lane went to `/v1/pool%3Fslot=2` and 404'd, because
  `appendingPathComponent` escapes what it is handed. The free lane was unaffected, so
  nothing about the product looked broken — only the thing somebody had paid for.
* `UIDevice.current` is main-actor isolated in the iOS 18 SDK and was read from a
  nonisolated property.

Note what the recovery flag, the graduation count and the lane 404 have in common: the
*backend was correct in all three*. The rule was kept in the store and broken on the way to
the screen. No amount of server-side testing would have found them.

And note the third of the second group especially. It had a test — asserting exactly the
right thing, written the same day — that had never run, because the package it lives in did
not compile. Android had the identical test, in a module that does build locally, and it
was green from the start. **A test that cannot run is documentation, and nothing in the
repository can tell the two apart.** That is why the CI jobs exist and why this file leads
with them.

`make clientlint` closes a narrow slice of the remaining gap: it checks that every copy key
and every initialiser label the hand-written client code uses actually exists in the
generated models. It is not a type checker and does not pretend to be one — it is what runs
in the seconds before a push, not what decides whether the push was right.

---

## On a Mac, if you have one

CI does all of this on every push, so none of it is a prerequisite to contributing. It is
faster locally when you are iterating.

```sh
brew install go postgres@16 openjdk@17 xcodegen
createdb podtest
export TEST_DATABASE_URL="postgres://localhost:5432/podtest?sslmode=disable"
make verify

swift test --package-path ios/PodKit
cd ios && xcodegen generate && xcodebuild -scheme Pod -destination 'platform=iOS Simulator,name=iPhone 16' build
cd ../android && ./gradlew :app:assembleDebug test
```

Then open the previews — the part CI cannot do. Every screen has at least two, including
its awkward states: a graduation with a zero streak, a recovery offer on the smallest rung,
a pod of two.

## On a device — the two things a simulator will not tell you

1. **Tap proof capture, both photo and voice.** This is what the missing usage descriptions
   would have crashed. It is the product's central action and it needs a real device.
2. **Grant, then deny, notifications on Android 13+.** Confirm a refusal produces nothing —
   no nag, no error, no blocked screen. The week loop does not depend on push and must
   behave that way.

## The two flows worth walking end to end

* **A full cycle.** Six weeks, graduation, continue-or-close. Check the zero-streak line
  reads right for a pod that mostly missed — that is the copy this product exists to get
  right, and it is the one a reviewer is least likely to see.
* **A pod that dissolves.** Two quiet weeks, then the recovery screen. Check it never says
  why, and reads identically for the member who posted every week.

---

## What has no implementation at all

Six seams are deliberately empty, and each refuses to start rather than pretending:

| Seam | Where | What it needs |
|---|---|---|
| Transactional email | `cmd/api` `selectMailer` (ADR-002) | a provider; in development the code goes to the log |
| Blob storage | `cmd/api` `selectBlobStore` (ADR-015) | a bucket; in development, local disk |
| Receipt verification | `cmd/api` `selectReceiptVerifier` (ADR-026) | StoreKit and Play credentials |
| Purchase sheet | `PurchaseBroker` on both clients | the same, client-side |
| Push delivery | `push.Sender`, `PushTokenSource` | APNs and FCM credentials |
| Embeddings | `internal/embed` (ADR-011, ADR-031) | a hosted model would still be better than the lexicon — but the lexicon is no longer *wrong* |

Every one of these is wired so that the product works without it. An app with no billing
behaves like someone who cancelled; an account with no push behaves like someone who
declined. None of them can take away the free pod.

**The embedder used to be the one to fix first, and it is fixed.** ADR-011's hashed
bag-of-words scored the synonym pair "run every morning" / "start jogging daily" at −0.07 —
worse than random on the signal carrying 0.40 of the match. `embed.Lexical` scores it 0.801,
and a strict ordering over 55 goals is asserted on every run (ADR-031). A hosted model would
still be better; the difference is that the seam now degrades rather than misleads.

**Any change to `internal/embed` is a backfill.** Run `cmd/reembed` after one. Until it has
run, a stale vector scores 0 and the signal drops out — no wrong answers, just three
signals instead of four.
