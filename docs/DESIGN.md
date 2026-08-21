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

## M2 — Onboarding

**Variant A — "one page."** Goal, category, intensity and the self-report question on a
single scrolling form with one submit button at the bottom.

**Variant B — "one question at a time."** Four screens, each with one question, advancing
on selection. A progress row of four dots. Back is always available.

**Picked: B.** A is fewer taps and looks shorter, and that is the trap: a single screen
with four unanswered questions is a screen someone has to *read* before they can start,
and the goal field is the hardest question of the four sitting at the top of it. B asks
one thing at a time, each answerable in a couple of seconds, and the category, intensity
and reliability screens advance on tap with no button to find.

What B gives up: you cannot see your earlier answers while making a later one, and it is
four navigation transitions instead of none. Back covers the first; the transitions are
`motion.quick`, so they cost about 160ms each.

The goal screen keeps its Continue button — it is the one answer that is typed rather than
tapped, and auto-advancing out of a text field the moment someone pauses would be hostile.

**On the intensity screen**, the five rungs (ADR-010) are shown as phrases only. No
numbers, no scale, no position indicator. A person picks "half an hour", never "3 of 5" —
seeing where you sit relative to others is the beginning of the comparison this product
exists to avoid.

**On the self-report screen**, the question is `CLAUDE.md` §6's own wording, softened by
the line beneath it: "there's no right answer — it only helps us pick who you'd get on
with." The three answers are phrased as circumstances rather than character
("Not as often as I'd like", not "Rarely"). This screen is the single most likely place in
the whole product to make someone feel judged before they have done anything, and it is
answered before they have any evidence to the contrary.

## M2 — Waiting for a pod

**Variant A — a spinner and "finding your pod…".** Indeterminate, no end in sight.

**Variant B — the next window, stated.** "We match every hour, so you're not waiting long"
plus the actual time from `pool_ticket.next_window_at`, and a quiet way to leave the queue.

**Picked: B.** Matching is a scheduled job by design (`CLAUDE.md` §5, ADR-013), so the wait
is a known quantity and pretending otherwise with a spinner would be a lie that also
invites pull-to-refresh. Telling someone the time lets them close the app, which is the
correct behaviour here.

The server sends the window time; the client never computes it. An hour is the server's
policy, not a client constant.

## M2 — The pod

Deliberately thin for now: who is in it, and that is all. The week loop is M3 and it owns
this screen; anything richer built here is built to be replaced.

What is **not** on it, and will not be: any per-member figure, any ordering that implies
rank, any "joined first" precedence. Members render in join order because that is stable,
not because it means anything. `streak_weeks` is pod-level and reads as "N weeks
together", never as a number next to a person.

---

## M3 — The week

This is the product (`CLAUDE.md` §7). Everything else exists to get someone here.

### The shared ring

**Variant A — one ring each.** A small ring per member, side by side, each filling when
that person posts.

**Variant B — one ring, segmented.** A single ring divided into two or three arcs, one per
member. It closes only when everyone is in.

**Picked: B, and it is not a close call.** Variant A is three individual progress
indicators arranged in a row, which is three individual scores however carefully they are
drawn — and the first thing anyone does with three of anything is compare them.
`CLAUDE.md` §2 rule 3 puts the streak on the pod because individual streaks reintroduce
private guilt; a per-member ring reintroduces it visually, one layer below where the rule
was written.

One ring made of everyone's segments is the visual form of "the streak is shared". It is
incomplete until the pod is complete, and it is nobody's ring in particular.

What B gives up: you cannot tell at a glance *which* segment is yours without reading the
names beneath. That is the right trade — the names are right there, and a ring that
highlights your own arc is a ring that invites you to compare it with the others'.

**Segment colours** come from `state.*` in `design/tokens.json`, and the palette has no
error role by construction:

* `posted` — soft green. In.
* `open` — warm grey. Not yet, and the week is still running. Reads as unfilled, not as
  absent.
* `resting` — cool grey. The week closed without them. Visibly different from `open`
  because it is a different fact, and deliberately *not* louder than it.

The gap between `open` and `resting` is the whole design problem of this screen. Resting
has to be legible — the pod can see who is in, that is the point — without being a
verdict. Cool grey against warm grey says "this is settled" without saying "this is bad",
and neither is anywhere near the red the tokens do not contain.

### Posting

**Variant A — a sheet with two big choices**, photo or voice, then capture.

**Variant B — one primary button that opens the camera immediately**, with voice behind a
smaller secondary.

**Picked: A.** B is fewer taps for the photo case, and it is wrong for this product: a
camera that opens itself is a camera pointed at whatever is in front of you, and the
person who chose a voice note did so because they are not somewhere they want to
photograph. Making one of the two feel like the real option and the other like a fallback
is the same mistake as making one member's ring bigger.

Both paths cap at ten seconds and say so before recording, not after.

Under the capture options, always: *"It disappears when the week closes. Nobody outside
your pod ever sees it."* That sentence is the product's central promise and this is the
one moment a person is deciding whether to believe it.

### What is deliberately absent

* No count of how many weeks anyone has personally posted.
* No ordering of members by anything.
* No "X hasn't posted yet" notification copy, here or in M4.
* No streak number rendered next to a person — `state.pod.weeksTogether` reads as "6 weeks
  together" and is attached to the ring, not to a face.
* No progress bar toward a goal. There is no goal to be a percentage of.

### Closing

When a week closes complete, the ring closes and the count moves. It lasts
`motion.settle` — 420ms — and nothing else happens. No confetti (`design/tokens.json`
says so explicitly). The reward is the other people.

When a week closes incomplete, nothing happens at all. The next week opens with everyone
open again, and `state.pod.restartingNote` — "The pod picks back up this week" — is the
entire acknowledgement. There is no screen that reports what went wrong, because nothing
did.

---

## M5 — After a pod ends

**Variant A — a banner on the goal screen.** "That pod wound down" above the questions,
with the intensity question pre-set one rung lower.

**Variant B — its own screen, before anything else.** One beat: what happened, the offer,
and two ways forward.

**Picked: B.** A is fewer screens and it buries the moment that matters most to exactly the
person who needs it. Someone whose pod just ended is the likeliest person in the product to
quietly stop opening it; putting the offer inside a form they have already filled in once
makes it easy to skim past.

More importantly, A implies a correction. A banner over a pre-lowered answer says *we have
adjusted you*. B asks.

### What the screen may not say

It may not say why the pod ended, because everything true about that is a statement about
somebody not showing up. The copy is identical for the person who posted every week and the
person who never did:

> **That pod has wound down**
> It happens. We'll find you another one whenever you're ready.

No count of pods that have ended. No reference to anyone's behaviour. No "unfortunately".
No "again".

### The offer

> **Want to make it smaller?**
> You picked *an hour or two*. *Half an hour* is also a real week.

"Is also a real week" is the whole sentence doing the work. It refuses the framing that a
smaller commitment is a reduced one — which is the framing the person is already applying
to themselves, and the one `CLAUDE.md` §2 rule 2 exists to interrupt.

Both buttons are equal weight. **Keep it as it is** is not a secondary action, because for
plenty of people it is the right answer and the pod simply did not work out.

### The alternate path

After three (`CLAUDE.md` §6), a second card offers the one-to-one path. It does not exist
yet, and the copy says so — "we can set that up when it's ready" — rather than pretending.
Offering something that is not there and going quiet is worse than not offering it.

It appears *below* the lower-the-bar offer and is never the only thing on screen: the
message is "here is another way", never "the group thing is not for you".

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
