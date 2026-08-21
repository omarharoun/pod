"""Regenerates the Pod screen mockups as Design Component artboards.

    python3 design/mockups/build.py

Every colour, size, radius and type step below is lifted from design/tokens.json;
every user-facing string is the literal value from content/microcopy.json. Neither
is read at build time on purpose — these are drawings, and a mockup that silently
re-rendered itself when a token moved would hide the drift rather than show it.
`check.py` is what catches that, and it reads both files.

The .dc.html artboards and canvas.json this writes are the source of the canvas.
The seeded ~2 MB .html is build output and is not committed.
"""

import math, pathlib

OUT = pathlib.Path(__file__).resolve().parent
OUT.mkdir(parents=True, exist_ok=True)

# ---- tokens, lifted from design/tokens.json (light scheme) ----------------
C = dict(
    canvas="#FBF7F2", raised="#FFFFFF", sunken="#F1EAE1",
    primary="#1A1714", secondary="#5C544B", tertiary="#8C8177", onAccent="#FFFBF6",
    accent="#C2603C", accentPressed="#A24E2F", accentSubtle="#F6E2D8",
    posted="#4F7A5B", postedSubtle="#DFEAE0",
    open="#B9AFA3", openSubtle="#EDE6DD",
    resting="#8C93A8", restingSubtle="#E4E6EC",
    hairline="rgba(26,23,20,0.10)", strong="rgba(26,23,20,0.22)",
)
S = dict(xs=4, sm=8, md=12, lg=16, xl=24, xxl=32, xxxl=48, gutter=20, section=32)
R = dict(sm=8, md=14, lg=22, pill=999)
Z = dict(touch=44, ringStroke=10, ringDia=176, btnH=52)

FONT = '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", system-ui, sans-serif'

def t(style):
    """Type token -> css."""
    m = dict(
        display=(34, 40, 600, -0.4), title=(24, 30, 600, -0.2), heading=(19, 25, 600, 0),
        body=(16, 23, 400, 0), bodyStrong=(16, 23, 600, 0),
        caption=(13, 18, 400, 0.1), micro=(11, 15, 500, 0.4),
    )[style]
    return f"font-size:{m[0]}px;line-height:{m[1]}px;font-weight:{m[2]};letter-spacing:{m[3]}px"

W, H = 390, 844

# ---- fragments -----------------------------------------------------------
def screen(body, bg=None, pad_top=None):
    bg = bg or C["canvas"]
    pt = S["xxxl"] if pad_top is None else pad_top
    return (f'<div style="width:{W}px;height:{H}px;background:{bg};font-family:{FONT};'
            f'color:{C["primary"]};box-sizing:border-box;padding:{pt}px {S["gutter"]}px {S["xl"]}px;'
            f'display:flex;flex-direction:column;overflow:hidden">\n{body}\n</div>')

def primary_btn(label, enabled=True, subtle=False):
    bg = C["accentSubtle"] if subtle else C["accent"]
    fg = C["accent"] if subtle else C["onAccent"]
    op = "1" if enabled else "0.38"
    return (f'<div style="display:flex;align-items:center;justify-content:center;'
            f'min-height:{Z["btnH"]}px;background:{bg};color:{fg};border-radius:{R["md"]}px;'
            f'opacity:{op};{t("bodyStrong")}">{label}</div>')

def text_link(label, color=None):
    return f'<div style="{t("caption")};color:{color or C["accent"]}">{label}</div>'

def field(label, value, placeholder=False):
    fg = C["tertiary"] if placeholder else C["primary"]
    return (f'<div style="display:flex;flex-direction:column;gap:{S["sm"]}px">'
            f'<div style="{t("micro")};color:{C["tertiary"]};text-transform:uppercase">{label}</div>'
            f'<div style="background:{C["sunken"]};border-radius:{R["md"]}px;padding:{S["lg"]}px;'
            f'{t("body")};color:{fg};min-height:{Z["touch"]}px;box-sizing:border-box;'
            f'display:flex;align-items:center">{value}</div></div>')

def choice(label, selected=False):
    bg = C["accentSubtle"] if selected else C["sunken"]
    tick = (f'<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="{C["accent"]}" '
            f'stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">'
            f'<path d="M20 6 9 17l-5-5"/></svg>') if selected else ""
    return (f'<div style="display:flex;align-items:center;gap:{S["md"]}px;background:{bg};'
            f'border-radius:{R["md"]}px;padding:{S["md"]}px {S["lg"]}px;min-height:{Z["touch"]}px;'
            f'box-sizing:border-box">'
            f'<div style="{t("body")};flex-grow:1">{label}</div>{tick}</div>')

def stack(children, gap):
    return (f'<div style="display:flex;flex-direction:column;gap:{gap}px">'
            + "".join(children) + "</div>")

def spacer():
    return '<div style="flex-grow:1"></div>'

def eyebrow(text):
    return f'<div style="{t("micro")};color:{C["tertiary"]};text-transform:uppercase">{text}</div>'

def h_title(text):
    return f'<div style="{t("title")};text-wrap:pretty">{text}</div>'

def body_text(text, color=None):
    return f'<div style="{t("body")};color:{color or C["secondary"]};text-wrap:pretty">{text}</div>'

def caption(text, color=None):
    return f'<div style="{t("caption")};color:{color or C["secondary"]};text-wrap:pretty">{text}</div>'

# ---- the pod ring --------------------------------------------------------
def ring(states, streak_line=None, dia=Z["ringDia"]):
    """states: list of 'posted'|'open'|'resting'. One arc per member, one ring."""
    stroke = Z["ringStroke"]
    r = (dia - stroke) / 2
    cx = cy = dia / 2
    circ = 2 * math.pi * r
    gap = 0.02
    n = len(states)
    seg = 1.0 / n - gap
    arcs = []
    for i, st in enumerate(states):
        start = i / n + gap / 2
        length = seg * circ
        offset = -start * circ
        arcs.append(
            f'<circle cx="{cx}" cy="{cy}" r="{r:.2f}" fill="none" stroke="{C[st]}" '
            f'stroke-width="{stroke}" stroke-linecap="round" '
            f'stroke-dasharray="{length:.2f} {circ - length:.2f}" '
            f'stroke-dashoffset="{offset:.2f}" transform="rotate(-90 {cx} {cy})"/>')
    centre = ""
    if streak_line:
        centre = (f'<div style="position:absolute;inset:0;display:flex;align-items:center;'
                  f'justify-content:center;padding:0 {S["xl"]}px;text-align:center;'
                  f'{t("caption")};color:{C["secondary"]};text-wrap:pretty">{streak_line}</div>')
    return (f'<div style="position:relative;width:{dia}px;height:{dia}px;margin:0 auto">'
            f'<svg width="{dia}" height="{dia}" viewBox="0 0 {dia} {dia}">'
            f'<circle cx="{cx}" cy="{cy}" r="{r:.2f}" fill="none" stroke="{C["sunken"]}" '
            f'stroke-width="{stroke}"/>{"".join(arcs)}</svg>{centre}</div>')

def member_row(name, state, detail):
    dot = C[state]
    fill = C[state + "Subtle"] if state + "Subtle" in C else C["sunken"]
    short = dict(posted="In", open="Not yet", resting="Resting")[state]
    return (f'<div style="display:flex;align-items:center;gap:{S["md"]}px;padding:{S["md"]}px 0">'
            f'<div style="width:{Z["touch"]}px;height:{Z["touch"]}px;border-radius:{R["pill"]}px;'
            f'background:{fill};display:flex;align-items:center;justify-content:center;flex-shrink:0">'
            f'<div style="width:12px;height:12px;border-radius:999px;background:{dot}"></div></div>'
            f'<div style="display:flex;flex-direction:column;gap:2px;flex-grow:1">'
            f'<div style="{t("bodyStrong")}">{name}</div>'
            f'<div style="{t("caption")};color:{C["secondary"]}">{detail}</div></div>'
            f'<div style="{t("micro")};color:{C[state]};text-transform:uppercase">{short}</div></div>')

def divider():
    return f'<div style="height:1px;background:{C["hairline"]}"></div>'

def progress_dots(index, total=5):
    dots = []
    for i in range(total):
        on = i <= index
        dots.append(f'<div style="width:6px;height:6px;border-radius:999px;'
                    f'background:{C["accent"] if on else C["strong"]}"></div>')
    return (f'<div style="display:flex;gap:{S["sm"]}px;align-items:center;justify-content:center">'
            + "".join(dots) + "</div>")

def back_chevron():
    return (f'<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="{C["primary"]}" '
            f'stroke-width="2" stroke-linecap="round" stroke-linejoin="round">'
            f'<path d="m15 18-6-6 6-6"/></svg>')

def onboarding_header(index, back=True):
    left = (f'<div style="width:{Z["touch"]}px;height:{Z["touch"]}px;display:flex;align-items:center">'
            + (back_chevron() if back else "") + "</div>")
    return (f'<div style="display:flex;align-items:center;gap:{S["md"]}px;margin-bottom:{S["xl"]}px">'
            f'{left}<div style="flex-grow:1"></div>{progress_dots(index)}'
            f'<div style="flex-grow:1"></div>'
            f'<div style="width:{Z["touch"]}px;height:{Z["touch"]}px"></div></div>')

DOC = """<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
  <style>
    body {{ margin: 0; }}
    a {{ color: {accent}; }} a:hover {{ color: {pressed}; }}
  </style>
</helmet>
{body}
</x-dc>
</body>
</html>
"""

def write(name, body):
    (OUT / f"{name}.dc.html").write_text(
        DOC.format(body=body, accent=C["accent"], pressed=C["accentPressed"]))


# ============================ THE SCREENS =================================
# Every string below is the literal value from content/microcopy.json.

# 1. Sign in — auth.start.*
write("SignIn", screen(stack([
    stack([
        h_title("Let&rsquo;s get you in"),
        body_text("We&rsquo;ll email you a 6-digit code. No password to remember."),
    ], S["md"]),
    field("Email", "you@example.com", placeholder=True),
    primary_btn("Send code", enabled=False),
    spacer(),
    f'<div style="{t("micro")};color:{C["tertiary"]};text-align:center;text-wrap:pretty">'
    f'By continuing you agree to our terms and privacy notice.</div>',
], S["xl"])))

# 2. Verify code — auth.verify.*
write("VerifyCode", screen(stack([
    stack([
        h_title("Check your email"),
        body_text("We sent a 6-digit code to <span style=\"color:%s\">sam@example.com</span>. "
                  "It&rsquo;s good for 10 minutes." % C["primary"]),
    ], S["md"]),
    f'<div style="display:flex;flex-direction:column;gap:{S["sm"]}px">'
    f'<div style="{t("micro")};color:{C["tertiary"]};text-transform:uppercase">6-digit code</div>'
    f'<div style="background:{C["sunken"]};border-radius:{R["md"]}px;padding:{S["lg"]}px;'
    f'min-height:{Z["touch"]}px;box-sizing:border-box;display:flex;align-items:center;'
    f'font-size:28px;line-height:34px;font-weight:600;letter-spacing:10px">418<span '
    f'style="color:{C["tertiary"]};font-weight:400">···</span></div></div>',
    primary_btn("Continue", enabled=False),
    f'<div style="display:flex;align-items:center;justify-content:space-between">'
    f'{text_link("You can resend in 24s", C["tertiary"])}'
    f'{text_link("Use a different email", C["secondary"])}</div>',
    spacer(),
], S["xl"])))

# 3. Onboarding — the goal, in their words (onboarding.intent.* / goal.*)
write("Goal", screen(
    onboarding_header(0, back=False) + stack([
        stack([
            h_title("What have you been putting off?"),
            body_text("One thing. The one that&rsquo;s been sitting there."),
        ], S["md"]),
        f'<div style="display:flex;flex-direction:column;gap:{S["sm"]}px">'
        f'<div style="{t("micro")};color:{C["tertiary"]};text-transform:uppercase">In your words</div>'
        f'<div style="background:{C["sunken"]};border-radius:{R["md"]}px;padding:{S["lg"]}px;'
        f'min-height:96px;box-sizing:border-box;{t("body")};color:{C["tertiary"]}">'
        f'Finally sort out the spare room</div>'
        f'{caption("Only your pod will see this.", C["tertiary"])}</div>',
        primary_btn("Continue", enabled=False),
        spacer(),
    ], S["xl"]), pad_top=S["xl"]))

# 4. Onboarding — category (onboarding.category.*)
write("Category", screen(
    onboarding_header(1) + stack([
        h_title("What kind of thing is it?"),
        stack([choice(c, selected=(c == "Home and space")) for c in [
            "Moving my body", "Making something", "Admin and paperwork",
            "Learning something", "Work and career", "Home and space",
            "Health and looking after myself", "People I&rsquo;ve lost touch with",
            "Money things",
        ]], S["sm"]),
    ], S["xl"]), pad_top=S["xl"]))

# 5. Onboarding — intensity (onboarding.intensity.*)
write("Intensity", screen(
    onboarding_header(2) + stack([
        stack([
            h_title("How much can you give it in a week?"),
            body_text("Pick what&rsquo;s realistic on a normal week, not a good one. "
                      "You can change it later."),
        ], S["md"]),
        stack([choice(c, selected=(c == "Half an hour")) for c in [
            "A few minutes", "Half an hour", "An hour or two", "A few hours", "Half a day",
        ]], S["sm"]),
        spacer(),
    ], S["xl"]), pad_top=S["xl"]))

# 6. Onboarding — the self-report (onboarding.reliability.*)
write("Reliability", screen(
    onboarding_header(4) + stack([
        stack([
            h_title("One last thing"),
            body_text("How often do you keep commitments you make to yourself? "
                      "There&rsquo;s no right answer &mdash; it only helps us pick who "
                      "you&rsquo;d get on with."),
        ], S["md"]),
        stack([choice(c) for c in [
            "Not as often as I&rsquo;d like", "Some of the time", "Most of the time",
        ]], S["sm"]),
        spacer(),
        primary_btn("Find me a pod"),
    ], S["xl"]), pad_top=S["xl"]))

# 7. Waiting for the matching window (pool.waiting.*)
write("Waiting", screen(stack([
    spacer(),
    stack([
        h_title("Looking for your pod"),
        body_text("We match every hour, so you&rsquo;re not waiting long. "
                  "We&rsquo;ll let you know."),
        f'<div style="{t("caption")};color:{C["accent"]}">Next match at 15:00</div>',
    ], S["md"]),
    spacer(),
    f'<div style="text-align:center">{text_link("Leave the queue", C["secondary"])}</div>',
], S["lg"])))


# 8. The week, first one — the newly-formed banner (pod.newlyFormed.*)
write("WeekNew", screen(stack([
    f'<div style="background:{C["accentSubtle"]};border-radius:{R["md"]}px;padding:{S["lg"]}px;'
    f'display:flex;flex-direction:column;gap:{S["xs"]}px">'
    f'<div style="{t("heading")}">You&rsquo;re matched</div>'
    f'<div style="{t("caption")};color:{C["secondary"]};text-wrap:pretty">'
    f'Same goal, same kind of week. Your first action lands soon.</div></div>',
    stack([
        eyebrow("This week"),
        f'<div style="{t("title")};text-wrap:pretty">One small thing towards it this week.</div>',
        caption("Nobody&rsquo;s named this week&rsquo;s thing yet. You can."),
        text_link("Name it"),
        f'<div style="{t("caption")};color:{C["tertiary"]}">Closes in 6 days</div>',
    ], S["sm"]),
    ring(["open", "open", "open"]),
    stack([
        member_row("You", "open", "Your 10 seconds is still open."),
        divider(),
        member_row("Priya", "open", "Priya hasn&rsquo;t posted yet."),
        divider(),
        member_row("Marcus", "open", "Marcus hasn&rsquo;t posted yet."),
    ], 0),
    primary_btn("Post your 10 seconds"),
], S["xl"]), pad_top=S["xxl"]))

# 9. The week, mid-flight — two in, you still open. The product.
write("Week", screen(stack([
    stack([
        eyebrow("This week"),
        f'<div style="{t("title")};text-wrap:pretty">Clear one shelf</div>',
        text_link("Change it"),
        f'<div style="{t("caption")};color:{C["tertiary"]}">Closes in 2 days</div>',
    ], S["sm"]),
    ring(["posted", "posted", "open"], streak_line="3 weeks together"),
    stack([
        member_row("You", "open", "Your 10 seconds is still open."),
        divider(),
        member_row("Priya", "posted", "Priya is in this week."),
        divider(),
        member_row("Marcus", "posted", "Marcus is in this week."),
    ], 0),
    primary_btn("Post your 10 seconds"),
    spacer(),
    f'<div style="text-align:center">{text_link("Sign out", C["tertiary"])}</div>',
], S["xl"]), pad_top=S["xxl"]))

# 10. The week, closed, everyone in (week.podCompleted)
write("WeekComplete", screen(stack([
    stack([
        eyebrow("This week"),
        f'<div style="{t("title")};text-wrap:pretty">Clear one shelf</div>',
        f'<div style="{t("micro")};color:{C["tertiary"]}">'
        f'Someone&rsquo;s already posted, so this week&rsquo;s thing stays as it is.</div>',
        f'<div style="{t("caption")};color:{C["tertiary"]}">This week is done</div>',
    ], S["sm"]),
    stack([
        ring(["posted", "posted", "posted"], streak_line="4 weeks together"),
        f'<div style="{t("bodyStrong")};color:{C["posted"]};text-align:center">'
        f'Everyone was in this week.</div>',
    ], S["md"]),
    stack([
        member_row("You", "posted", "You&rsquo;re in this week."),
        divider(),
        member_row("Priya", "posted", "Priya is in this week."),
        divider(),
        member_row("Marcus", "posted", "Marcus is in this week."),
    ], 0),
    spacer(),
], S["xl"]), pad_top=S["xxl"]))

# 11. A week that closed with somebody resting — the never-shame case.
write("WeekResting", screen(stack([
    stack([
        eyebrow("This week"),
        f'<div style="{t("title")};text-wrap:pretty">Clear one shelf</div>',
        f'<div style="{t("caption")};color:{C["tertiary"]}">This week is done</div>',
    ], S["sm"]),
    stack([
        ring(["posted", "posted", "resting"], streak_line="4 weeks together"),
        f'<div style="{t("caption")};color:{C["secondary"]};text-align:center">'
        f'The pod picks back up this week.</div>',
    ], S["md"]),
    stack([
        member_row("You", "posted", "You&rsquo;re in this week."),
        divider(),
        member_row("Priya", "posted", "Priya is in this week."),
        divider(),
        member_row("Marcus", "resting", "Marcus sat this one out. The pod carries on."),
    ], 0),
    spacer(),
], S["xl"]), pad_top=S["xxl"]))

# 12. The proof sheet (week.post.*) — a sheet over the week screen.
sheet_body = stack([
    f'<div style="width:36px;height:4px;border-radius:999px;background:{C["strong"]};'
    f'margin:0 auto"></div>',
    stack([
        f'<div style="{t("heading")}">Post your 10 seconds</div>',
        caption("It disappears when the week closes. Nobody outside your pod ever sees it."),
    ], S["sm"]),
    stack([
        f'<div style="display:flex;align-items:center;gap:{S["md"]}px;background:{C["sunken"]};'
        f'border-radius:{R["md"]}px;padding:{S["lg"]}px;min-height:{Z["touch"]}px;box-sizing:border-box">'
        f'<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="{C["primary"]}" '
        f'stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">'
        f'<path d="M14.5 4h-5L8 6H4a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-4z"/>'
        f'<circle cx="12" cy="13" r="3.5"/></svg>'
        f'<div style="{t("body")}">Take a photo</div></div>',
        f'<div style="display:flex;align-items:center;gap:{S["md"]}px;background:{C["sunken"]};'
        f'border-radius:{R["md"]}px;padding:{S["lg"]}px;min-height:{Z["touch"]}px;box-sizing:border-box">'
        f'<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="{C["primary"]}" '
        f'stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">'
        f'<rect x="9" y="2" width="6" height="12" rx="3"/><path d="M5 11a7 7 0 0 0 14 0"/>'
        f'<path d="M12 18v4"/></svg>'
        f'<div style="{t("body")}">Record a voice note</div></div>',
    ], S["sm"]),
    f'<div style="text-align:center;padding-top:{S["xs"]}px">'
    f'{text_link("Not now", C["secondary"])}</div>',
], S["xl"])

write("ProofSheet",
    f'<div style="position:relative;width:{W}px;height:{H}px;font-family:{FONT};'
    f'color:{C["primary"]};overflow:hidden;background:{C["canvas"]}">'
    # the week screen behind it
    f'<div style="position:absolute;inset:0;padding:{S["xxl"]}px {S["gutter"]}px;'
    f'display:flex;flex-direction:column;gap:{S["xl"]}px">'
    + eyebrow("This week")
    + f'<div style="{t("title")}">Clear one shelf</div>'
    + ring(["posted", "posted", "open"], streak_line="3 weeks together")
    + '</div>'
    # scrim
    f'<div style="position:absolute;inset:0;background:rgba(26,23,20,0.36)"></div>'
    # the sheet
    f'<div style="position:absolute;left:0;right:0;bottom:0;background:{C["raised"]};'
    f'border-radius:{R["lg"]}px {R["lg"]}px 0 0;padding:{S["md"]}px {S["gutter"]}px {S["xxl"]}px;'
    f'box-sizing:border-box">{sheet_body}</div></div>')


# 13. Recovery — the pod wound down, and the smaller rung (recovery.*)
write("Recovery", screen(stack([
    spacer(),
    stack([
        h_title("That pod has wound down"),
        body_text("It happens. We&rsquo;ll find you another one whenever you&rsquo;re ready."),
    ], S["md"]),
    divider(),
    stack([
        f'<div style="{t("heading")}">Want to make it smaller?</div>',
        body_text("You picked An hour or two. Half an hour is also a real week."),
    ], S["sm"]),
    stack([
        primary_btn("Make it Half an hour"),
        primary_btn("Keep it as it is", subtle=True),
    ], S["sm"]),
    spacer(),
    f'<div style="text-align:center">{text_link("Find me another pod", C["secondary"])}</div>',
], S["xl"])))

# 14. Recovery, the alternate path — offered after three dissolves (recovery.alternate.*)
write("RecoveryAlternate", screen(stack([
    spacer(),
    stack([
        h_title("There&rsquo;s another way to do this"),
        body_text("Some people do better one-to-one than in a group. "
                  "We can set that up when it&rsquo;s ready."),
    ], S["md"]),
    stack([
        primary_btn("We&rsquo;ll let you know"),
        primary_btn("Find me another pod", subtle=True),
    ], S["sm"]),
    spacer(),
], S["xl"])))

# 15. Graduation — end of the six-week cycle (graduation.*)
write("Graduation", screen(stack([
    spacer(),
    stack([
        f'<div style="{t("display")};text-wrap:pretty">6 weeks, done</div>',
        body_text("That&rsquo;s the cycle. Your pod got to the end of it together."),
    ], S["md"]),
    ring(["posted", "posted", "posted"], streak_line="4 weeks where everyone was in."),
    divider(),
    stack([
        f'<div style="{t("heading")}">Another 6?</div>',
        body_text("Same pod, same goal, a fresh cycle. Everyone decides for themselves."),
    ], S["sm"]),
    stack([
        primary_btn("I&rsquo;m in for another"),
        primary_btn("I&rsquo;ll stop here", subtle=True),
    ], S["sm"]),
    caption("You can decide any time before 3 October.", C["tertiary"]),
    spacer(),
], S["lg"]), pad_top=S["xxl"]))

# 16. Graduation, a cycle where not every week landed. The copy this product
#     exists to get right — and the one a reviewer is least likely to see.
write("GraduationNone", screen(stack([
    spacer(),
    stack([
        f'<div style="{t("display")};text-wrap:pretty">6 weeks, done</div>',
        body_text("That&rsquo;s the cycle. Your pod got to the end of it together."),
    ], S["md"]),
    ring(["posted", "resting", "posted"],
         streak_line="Not every week landed. Getting to the end still counts."),
    divider(),
    stack([
        f'<div style="{t("heading")}">Another 6?</div>',
        body_text("Same pod, same goal, a fresh cycle. Everyone decides for themselves."),
    ], S["sm"]),
    stack([
        primary_btn("I&rsquo;m in for another"),
        primary_btn("I&rsquo;ll stop here", subtle=True),
    ], S["sm"]),
    spacer(),
], S["lg"]), pad_top=S["xxl"]))

# 17. Waiting on the rest of the pod to decide (graduation.waiting.*)
write("GraduationWaiting", screen(stack([
    spacer(),
    stack([
        h_title("You&rsquo;re in"),
        body_text("Waiting to hear from the others. We&rsquo;ll let you know."),
        f'<div style="{t("caption")};color:{C["accent"]}">2 of you are carrying on so far.</div>',
    ], S["md"]),
    spacer(),
    f'<div style="text-align:center">{text_link("Change my mind", C["secondary"])}</div>',
], S["lg"])))

# 18. The paywall — a second thing, never the first (slots.*)
write("SecondThing", screen(stack([
    spacer(),
    stack([
        h_title("Something else you&rsquo;ve been putting off?"),
        body_text("You can run a second thing alongside this one. Same shape: "
                  "one small action a week, a different pod."),
    ], S["md"]),
    primary_btn("Start a second thing"),
    spacer(),
], S["xl"])))

# 19. Two lanes, with the switcher. Goals, never numbers.
def lane_chip(label, selected):
    bg = C["accent"] if selected else C["sunken"]
    fg = C["onAccent"] if selected else C["secondary"]
    return (f'<div style="background:{bg};color:{fg};border-radius:{R["pill"]}px;'
            f'padding:{S["sm"]}px {S["md"]}px;{t("caption")};white-space:nowrap">{label}</div>')

write("Lanes",
    f'<div style="width:{W}px;height:{H}px;background:{C["canvas"]};font-family:{FONT};'
    f'color:{C["primary"]};box-sizing:border-box;display:flex;flex-direction:column;'
    f'overflow:hidden;padding-top:{S["xl"]}px">'
    f'<div style="display:flex;gap:{S["sm"]}px;padding:{S["sm"]}px {S["gutter"]}px;overflow:hidden">'
    + lane_chip("Finally sort out the spare room", True)
    + lane_chip("Learn Spanish properly", False)
    + '</div>'
    f'<div style="flex-grow:1;padding:{S["xl"]}px {S["gutter"]}px {S["xl"]}px;'
    f'display:flex;flex-direction:column;gap:{S["xl"]}px">'
    + stack([
        eyebrow("This week"),
        f'<div style="{t("title")}">Clear one shelf</div>',
        f'<div style="{t("caption")};color:{C["tertiary"]}">Closes in 2 days</div>',
      ], S["sm"])
    + ring(["posted", "posted", "open"], streak_line="3 weeks together")
    + stack([
        member_row("You", "open", "Your 10 seconds is still open."),
        divider(),
        member_row("Priya", "posted", "Priya is in this week."),
        divider(),
        member_row("Marcus", "posted", "Marcus is in this week."),
      ], 0)
    + primary_btn("Post your 10 seconds")
    + '</div></div>')

# The week screen is the canvas entry point; the editor wants that file named Main.
(OUT / "Week.dc.html").replace(OUT / "Main.dc.html")

print("wrote", len(list(OUT.glob("*.dc.html"))), "artboards")
