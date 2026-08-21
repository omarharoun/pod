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

## M6 — Graduation, and the choice

The pod reached the end of its six weeks. This is the only moment in the product where
celebration is the right register, and it is still not a ranking.

**Variant A — a modal over the week screen.** The pod's ring, filled, with the choice as
two buttons underneath. Dismissible; the week screen stays behind it.

**Variant B — the screen replaces the week.** There is no week any more, so the week screen
is showing something that no longer exists.

**Picked: B.** A is tempting because it keeps the ring, and the ring is the best thing on
the week screen. But a modal is something you dismiss, and what is behind this one is a
week that has ended — a proof grid whose proof was deleted, an action nobody can post
against. Dismissing the modal would land someone on a dead screen and leave the choice
somewhere they have to go looking for it.

**The ring does not come with it, and that is deliberate.** `PodRing` means *who is in this
week*, and at graduation there is no week. Drawing it closed would say everyone posted,
which is false for a pod that completed one week of six; drawing it open would end six
weeks on a picture of a gap. Either is a statement the screen is not entitled to make. The
count line below carries the pod-level truth instead, in words, where it can be phrased
honestly for both cases.

### What it says

> **Six weeks, done**
> That's the cycle. Your pod got to the end of it together.

Then one line about the streak, and this is the fork that matters:

> *{count} weeks where everyone was in.*
> — or, when that number is zero —
> *Some weeks landed, some didn't. You still got here.*

The second is the harder one to write and the more important. A pod that completed one week
out of six still finished the cycle, and the screen may not imply otherwise. "You still got
here" is doing the work: it refuses to make the count the point. What it may **not** do is
show a per-member figure, or a best week, or who posted most. The cycle belongs to the pod
(`CLAUDE.md` §2 rule 3).

### The choice

> **Another six?**
> Same pod, same goal, a fresh cycle. Everyone decides for themselves.
> [ I'm in for another ]  [ I'll stop here ]

"Everyone decides for themselves" is there to pre-empt the thought the product most wants
to avoid — *if I say no, am I letting them down*. Both buttons are equal weight, as on the
M5 recovery screen and for the same reason.

Once answered, the screen becomes a waiting state:

> **You're in**
> Waiting to hear from the others. We'll let you know.
> *2 of you are carrying on so far.*

A **count**, never a list. "Alex is not continuing" is the sentence this entire design
exists to make impossible, and a member id beside a `false` is one join away from it at all
times — which is why the API carries counts and the store has no method that returns who
declined (ADR-023).

The person who said no gets a different, equally final line:

> **That's the cycle, then**
> Nothing more to do. If you want another pod later, it's here.

Both states offer **Change my mind** while the deadline is open, and the deadline is stated
rather than counted down: *You can decide any time before {date}*. A countdown on this
screen would be pressure, and pressure is the thing the product is built not to apply.

### Divergence

None intended. iOS gets two `PodPrimaryButton`/`PodSecondaryButton` peers; Android gets
the `Button` + `FilledTonalButton` pair, which is Material 3's way of saying "equal weight,
different emphasis". Same information, same order, native idiom each.

---

## M7 — A second thing

The paywall. `CLAUDE.md` §2 rule 6 is the whole brief: one free pod forever, and the
paywall gates *additional* pods, never the core loop. A paid lane is a second **thing**,
with its own goal (ADR-026).

**Variant A — a "+" on the week screen.** Always visible, opens the store sheet. Puts the
paid thing next to the free thing at all times.

**Variant B — nowhere until you finish something.** The offer appears on the graduation
screen, and in one line on the pod screen after a cycle has completed.

**Picked: B.** A is what every app does and it is the wrong instinct here. A permanent "+"
is a permanent advertisement in the middle of a product about not feeling bad, shown most
often to the person who has not managed their first thing yet — the person §2 rule 6 exists
to protect. It also implies that more lanes are the way to get better at this, which is the
opposite of §1.

B puts the offer exactly where it is true: you just finished six weeks of something, so
starting another thing is a real thought you might be having. Someone who never finishes a
cycle never sees a paywall, which is correct.

### What it says

On the graduation screen, below the continue-or-close choice and never above it:

> **Something else you've been putting off?**
> You can run a second thing alongside this one. Same shape: one small action a week,
> a different pod.
> [ Start a second thing ]

It does not say "unlock", "upgrade", "pro", or "premium". Nothing about the free pod is
locked, and calling it that would be a lie about the product's own shape. It also does not
name a price — StoreKit and Play own that, and the button opens the platform sheet where
the real price lives.

### The lane list

Once someone has more than one lane, the app needs a way between them. A scrolling row
above everything else, showing each lane's **goal text**, never its number:

> [ sort out the spare room ] [ learn to make bread ]

Numbers would put someone on a scale — the same reason intensity rungs are phrases
(ADR-010). A lane with no goal yet reads "Something new".

It appears **only once there is more than one lane**. A switcher over a single thing is a
control that does nothing, sitting on the screen that should be the simplest in the
product.

Each lane keeps its own draft. Moving between them mid-answer must not carry a
half-finished goal across — they are different things, which is the whole point.

And buying a lane moves you to it. Someone who has just decided to start a second thing
wants to name it, not to be returned to the one they finished.

### What the paywall may not do

* It may not appear before a cycle has completed.
* It may not interrupt a check-in. Nothing may stand between someone and their ten seconds.
* It may not count what somebody has not bought — no "1 of 5 slots used".
* A failed purchase says nothing was charged, and leaves the free lane untouched. The
  server-side test for that is `TestTheFreeLaneSurvivesABrokenEntitlementsStore`; the
  client's job is to never render a state where the free pod looks unavailable.

### Divergence

The purchase sheet is the platform's, so the two clients genuinely differ below the button:
iOS gets StoreKit 2's `Product.purchase()`, Android gets Play Billing. Everything above the
button is the same. Neither is implemented here — both need store credentials and a real
app record, which is the same seam the server has (`selectReceiptVerifier`).

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
