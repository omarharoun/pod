# Screen mockups

Every screen in Pod as a Design Component artboard, drawn to `design/tokens.json`
and `content/microcopy.json`.

```sh
python3 design/mockups/build.py    # regenerate the artboards
python3 design/mockups/check.py    # prove they still match the tokens and the copy
```

`build.py` writes the `.dc.html` artboards and `canvas.json`. Seeding those into a
publishable canvas is a separate step (the `design` skill's helper); the ~2 MB
seeded `.html` is build output and is not committed.

## What is drawn, and what is invented

Nothing about the design is invented. Colours, spacing, radii, type steps and the
44px touch target come from `design/tokens.json`; the ring geometry, the choice
rows and the week screen's structure are read off `ios/App/Features/` rather than
imagined; every user-facing string is the literal value from
`content/microcopy.json`.

What *is* invented is sample data — the names somebody was matched with, the goal
they typed, a date, a count. `check.py` holds that list explicitly, so anything
else that appears on an artboard fails the check rather than passing as copy.

## Why this is checked at all

A mockup is a promise about what the app looks like, and the whole value of it is
that the promise is true. The failure mode is not that a mockup looks wrong — it
is that a mockup looks *right* while the app has moved, or that a plausible
sentence nobody wrote into `microcopy.json` gets drawn, reviewed, approved and
then built. Both are silent. `check.py` is what makes them loud:

* every visible string resolves to a `microcopy.json` entry
* every colour resolves to a `tokens.json` colour
* no interactive row is under the touch target the tokens set
* no shaming vocabulary, and nothing red anywhere on the canvas (§2 rule 1)

It is not wired into `make verify` — these are drawings, not shipped code, and the
gate is slow enough. Run it when you change the mockups, the tokens or the copy.

## The screens

| Artboard | |
|---|---|
| `SignIn`, `VerifyCode` | getting in |
| `Goal`, `Category`, `Intensity`, `Reliability` | naming the thing — one question per screen; selecting advances the flow, so there is no button to find |
| `Waiting`, `WeekNew` | the matching window, and landing in a pod |
| `Main`, `ProofSheet`, `WeekComplete`, `WeekResting`, `Lanes` | the week loop — this is the product |
| `Recovery`, `RecoveryAlternate` | a pod winding down |
| `Graduation`, `GraduationNone`, `GraduationWaiting` | the end of a six-week cycle |
| `SecondThing` | the only thing behind the paywall |

Three of those exist because they are where the product's rules are visible and
they are the screens least likely to get looked at: `WeekResting` (a week that
closed with somebody not in — blue-grey, "the pod carries on", never red),
`GraduationNone` (a cycle where not every week landed), and `Lanes` (the switcher
shows goals, never "1 of 2" — a lane number would turn two things somebody is
working on into a score).

Not drawn: the timezone question, which is the same shell as the others with a
picker; and the fourteen error states, which are all the same notice component.
