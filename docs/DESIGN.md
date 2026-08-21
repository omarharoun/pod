# Design

Tokens live in [`design/tokens.json`](../design/tokens.json) and are generated into both
clients by `make generate`. This file records the screens: the variants considered, and
why one won. Per `CLAUDE.md` §Step 3, every new screen gets two variants and a stated
reason before it is built.

Same tokens, same information architecture, native idiom each. **iOS and Android are not
the same design.**

---

## The direction

Pod is for someone who has been avoiding a thing for a while and feels a bit bad about it.
The interface should feel like a kitchen table, not a gym.

* **Warm paper, not white.** `surface.canvas` is `#FBF7F2`. A pure-white app in this
  category reads as clinical or as productivity software, and both are the wrong promise.
* **One accent, used sparingly.** A muted terracotta. It marks the one thing to do on a
  screen and nothing else.
* **No red anywhere near a person's week.** `design/tokens.json` deliberately has no
  `error` or `danger` role. The three member states are a soft green (`state.posted`), a
  warm grey (`state.open`) and a cool grey (`state.resting`). Missing a week must not be
  rendered in the colour of a mistake (`CLAUDE.md` §2 rule 1).
* **Nothing celebratory outlasts `motion.settle` (420ms).** No confetti. The reward is the
  other people, not the animation.
* **Type is a baseline, never a size.** Everything scales with Dynamic Type and Android's
  font scale.

---

## M1 — Sign in (email)

**Variant A — "welcome first."** App name and tagline stacked over an illustration, the
email field below the fold, a Continue button beneath it.

**Variant B — "field first."** Title, one line of explanation, the field, the button. The
field takes focus on appear; the keyboard's Go key submits.

**Picked: B.** `CLAUDE.md` §2 rule 7 caps the effort threshold at about fifteen seconds
for a check-in, and sign-in is the first thing anyone measures the app by. Variant A costs
a scroll and a tap before anything can be typed, and buys a tagline that means nothing yet
to someone who has not used the product. B is reachable without moving a thumb to the
button at all.

What B gives up: there is no brand moment on first launch. That is the right trade —
the brand moment is the first week the pod completes, and it cannot be faked here.

* iOS: `.textContentType(.emailAddress)` so the QuickType bar offers the address,
  `.submitLabel(.go)`, focus on appear.
* Android: `KeyboardType.Email` with `ImeAction.Go`, autofill hint, focus requested on
  first composition.

## M1 — Verify code

**Variant A — six separate boxes.** The familiar one-box-per-digit treatment, with focus
advancing as you type.

**Variant B — one field, auto-submitting.** A single large field, digits only, that fires
the moment the sixth digit lands.

**Picked: B.** Six-box inputs look precise and behave badly: paste lands in the first box
on several keyboards, backspace across a boundary is inconsistent, and every extra
`UITextField` is another thing for VoiceOver to walk through. One field with
`.oneTimeCode` lets iOS fill it from the notification without the person reading the code
at all, which is as close to zero effort as this screen gets. Android does the same
through SMS Retriever's email-equivalent: an autofill hint plus a single field.

Auto-submit is the point of the variant: nobody should type a code they just read and then
have to find a button.

* The countdown before "send it again" comes from the server
  (`resend_after_seconds`), not a hardcoded 30 — the limit is the server's to set.
* "Use a different email" is present and quiet. Getting the address wrong is common and
  must never feel like a wrong turn.

## M1 — Home (placeholder)

**Variant A — an empty pod ring.** Show the M3 shared ring greyed out, with "your pod is
coming".

**Variant B — a plain line of text.** "You're in. Nothing to do yet — we'll set up your
pod next."

**Picked: B.** Variant A shows a person the shape of a thing that does not exist yet and
implies a pod is being assembled when M2's matching has not been built. Drawing the ring
now also means drawing it twice, because M3 will change it. B is honest and disposable.

This screen is explicitly temporary. The real home screen is M3's week loop, and it is the
product — `CLAUDE.md` §7 says nothing after it starts until it feels finished.

---

## Errors

Failures render as a calm block under the content: a title, a line of explanation, and a
retry only when retrying is a sensible thing to do. Never an alert, never red, never a
toast that disappears before it is read.

The server sends a `copy_key` and parameters, never a sentence (ADR-003). The client picks
the title locally from the error code — the server cannot see the screen and should not be
choosing how a failure is framed on it.

---

## Platform divergences so far

| | iOS | Android |
|---|---|---|
| Type scaling | `@ScaledMetric` relative to `.body` | `sp` units, system font scale |
| Code autofill | `.oneTimeCode` content type | autofill hint on a single field |
| Colour source | `PodTokens.color(role, scheme)` | same, as `0xAARRGGBB` |
| Back behaviour | swipe-back from the code screen returns to email | predictive back does the same |

Nothing here changes what the app does — only how it feels native. Any divergence that
changes behaviour is an escalation (`CLAUDE.md` §8).
