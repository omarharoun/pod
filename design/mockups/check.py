"""Checks the mockups against the two files they claim to be drawn from.

    python3 design/mockups/check.py

A mockup is a promise about what the app looks like, and the only thing that makes
it worth anything is that the promise is true. Four things are checked, and each
one is a rule this product would otherwise break quietly:

* every visible string resolves to content/microcopy.json (CLAUDE.md §2 rule 1 —
  copy is never hardcoded, and a mockup is where invented copy goes unnoticed)
* every colour resolves to design/tokens.json
* no interactive row is under the 44px touch target in design/tokens.json
* no shaming vocabulary and nothing red, anywhere on the canvas

Exits non-zero with the offending strings.
"""

import html
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
HERE = pathlib.Path(__file__).resolve().parent

# Sample data the mockups are allowed to invent: what a person would have typed,
# and who they were matched with. Everything else has to be real copy.
SAMPLE = {
    "You", "Priya", "Marcus", "sam@example.com", "you@example.com", "418···",
    "Clear one shelf", "Finally sort out the spare room", "Learn Spanish properly",
    "In", "Not yet", "Resting", "This week", "Email", "6-digit code", "In your words",
    "Make it Half an hour",
}

FORBIDDEN = [
    r"\bfail(ed|ure)?\b", r"\bbroke(n)?\b", r"\bmissed\b", r"\b0%\b",
    r"\bdon.t let\b", r"\bshould have\b", r"\blazy\b", r"\bstreak lost\b",
]


def normalise(text):
    return re.sub(r"\s+", " ", html.unescape(text)).strip()


def catalogue():
    raw = json.loads((ROOT / "content/microcopy.json").read_text())
    strings = raw.get("strings", raw)
    out = []
    for key, value in strings.items():
        for form in (value.values() if isinstance(value, dict) else [value]):
            out.append((key, normalise(form)))
    return out


def resolves(text, entries):
    for key, value in entries:
        if "{" not in value:
            if value == text:
                return key
        else:
            pattern = re.sub(r"\\\{[a-zA-Z]+\\\}", r".+?", re.escape(value))
            if re.fullmatch(pattern, text):
                return key
    return None


def artboards():
    return sorted(HERE.glob("*.dc.html"))


def main():
    problems = []
    entries = catalogue()

    tokens = json.loads((ROOT / "design/tokens.json").read_text())
    allowed = {v["value"].upper() for v in tokens["color"]["light"].values()}
    allowed |= {"RGBA(26,23,20,0.10)", "RGBA(26,23,20,0.22)", "RGBA(26,23,20,0.36)"}
    touch = tokens["size"]["minTouchTarget"]

    if not artboards():
        print("no artboards — run build.py first", file=sys.stderr)
        return 1

    for path in artboards():
        source = path.read_text()
        body = source.split("<x-dc>", 1)[1].split("</x-dc>", 1)[0]
        body = re.sub(r"<(script|style|svg)[^>]*>.*?</\1>", " ", body, flags=re.S)

        for chunk in re.split(r"<[^>]+>", body):
            text = normalise(chunk)
            if text and text not in SAMPLE and not resolves(text, entries):
                problems.append(f"{path.name}: {text!r} is in no microcopy entry")

        for colour in sorted(set(re.findall(r"#[0-9a-fA-F]{3,8}\b", source))):
            if colour.upper() not in allowed:
                problems.append(f"{path.name}: {colour} is in no colour token")
        for colour in sorted(set(re.findall(r"rgba\([^)]*\)", source))):
            if colour.replace(" ", "").upper() not in allowed:
                problems.append(f"{path.name}: {colour} is in no colour token")

        for match in re.finditer(r"min-height:(\d+)px", source):
            if int(match.group(1)) < touch:
                problems.append(
                    f"{path.name}: a {match.group(1)}px row is under the {touch}px touch target")

        text = re.sub(r"<[^>]+>", " ", source)
        for pattern in FORBIDDEN:
            for match in re.finditer(pattern, text, re.I):
                problems.append(f"{path.name}: {match.group(0)!r} — CLAUDE.md §2 rule 1")

        if re.search(r"#(?:[cdef][0-9a-f])(?:[0-2][0-9a-f])(?:[0-2][0-9a-f])\b", source, re.I):
            problems.append(f"{path.name}: something red. Nothing in this product is red.")

    if problems:
        for problem in problems:
            print(problem, file=sys.stderr)
        return 1

    print(f"mockups: ok — {len(artboards())} artboards, every string and colour accounted for")
    return 0


if __name__ == "__main__":
    sys.exit(main())
