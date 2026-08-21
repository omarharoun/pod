#!/usr/bin/env python3
"""Mechanical enforcement of CLAUDE.md §2 rule 1 ("never shame") and of the
"no hardcoded user-facing strings" rule.

Checks:
  1. Banned vocabulary anywhere in content/microcopy.json.
  2. Every key a client references (Copy.key("...") / copy("...")) exists.
  3. Placeholder sets are internally consistent for plural forms.
  4. No exclamation marks, no ALL-CAPS shouting.
  5. The intensity rungs name increasing amounts of time (ADR-010, ADR-020).

Usage: python3 tools/copylint/copylint.py [--root .]
Exit code 1 on any violation.
"""
from __future__ import annotations
import argparse, json, pathlib, re, sys

# Words/phrases that reintroduce shame, individual scoring, or punishment.
BANNED = [
    r"\bfail(ed|ure|ing)?\b",
    r"\bmissed\b", r"\byou missed\b",
    r"\bbroke(n)?\b(?!\s*down)",
    r"streak (broken|lost|ended)",
    r"\bdon'?t let\b",
    r"\blet (down|your pod down)\b",
    r"\bslack(ing|er|ed)\b",
    r"\blazy\b", r"\bexcuse(s)?\b",
    r"\bfell behind\b", r"\bbehind schedule\b",
    r"\b0\s*%", r"\b0/\d",
    r"\bpenalt(y|ies)\b",
    r"\bscore\b",              # the reliability score is never shown to users
    r"\brank(ed|ing)?\b",
    r"\bleaderboard\b",
    r"\bshame\b", r"\bguilt(y)?\b",
    r"\bdisappoint(ed|ing)\b",
    r"\bonly you\b",
    r"\bstill haven'?t\b",
    r"\bwhy didn'?t you\b",
]
BANNED_RE = [(p, re.compile(p, re.I)) for p in BANNED]

KEY_REF_RE = re.compile(r"""(?:Copy\.(?:key|text|string)\(|copy\(|microcopy\(|stringResource\(Copy\.)\s*["']([A-Za-z0-9_.]+)["']""")
CLIENT_GLOBS = ("ios/**/*.swift", "android/**/*.kt")


def flatten(obj, prefix=""):
    for k, v in obj.items():
        if k.startswith("$"):
            continue
        path = f"{prefix}{k}"
        if isinstance(v, dict):
            yield from flatten(v, path + ".")
        else:
            yield path, v


# The five intensity rungs are ordinal — the hard filter compares them as integers and
# the M5 recovery flow offers rung N-1 as "smaller". The phrases must therefore increase.
# Renaming a rung means giving it a duration here, which is the step that was skipped when
# rung 4 ("Half a day") was briefly larger than rung 5 ("Several hours").
INTENSITY_MINUTES = {
    "A few minutes": 10,
    "Half an hour": 30,
    "An hour or two": 90,
    "A few hours": 210,
    "Half a day": 300,
}


def check_intensity_rungs(strings: dict[str, str]) -> list[str]:
    problems: list[str] = []
    previous_key = previous_minutes = None
    for rung in range(1, 6):
        key = f"onboarding.intensity.{rung}"
        phrase = strings.get(key)
        if phrase is None:
            problems.append(f"{key}: missing — the rungs are 1..5 (ADR-010)")
            continue
        minutes = INTENSITY_MINUTES.get(phrase)
        if minutes is None:
            problems.append(
                f'{key}: "{phrase}" has no duration in copylint\'s INTENSITY_MINUTES. '
                f"The rungs are ordinal; adding one means saying how long it is."
            )
            continue
        if previous_minutes is not None and minutes <= previous_minutes:
            problems.append(
                f'{key}: "{phrase}" is not longer than {previous_key} '
                f'("{strings[previous_key]}") — the scale must increase, or "make it '
                f'smaller" offers something bigger (ADR-020)'
            )
        previous_key, previous_minutes = key, minutes
    return problems


def placeholders(s: str) -> set[str]:
    return set(re.findall(r"\{([a-zA-Z0-9_]+)\}", s))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=".")
    args = ap.parse_args()
    root = pathlib.Path(args.root).resolve()
    copy_path = root / "content" / "microcopy.json"
    data = json.loads(copy_path.read_text())
    problems: list[str] = []

    strings = dict(flatten(data))

    for key, value in strings.items():
        if not isinstance(value, str):
            problems.append(f"{key}: expected a string leaf, got {type(value).__name__}")
            continue
        for pattern, rx in BANNED_RE:
            if rx.search(value):
                problems.append(
                    f'{key}: shaming/forbidden vocabulary matched /{pattern}/ in "{value}" '
                    f"(CLAUDE.md §2 rule 1)"
                )
        if "!" in value:
            problems.append(f'{key}: exclamation mark in "{value}" — the voice is calm, not peppy')
        for word in re.findall(r"\b[A-Z]{3,}\b", value):
            if word not in {"OK"}:
                problems.append(f'{key}: shouting ("{word}") in "{value}"')

    # plural forms must share a placeholder set
    for key, value in data.items():
        if isinstance(value, dict) and not key.startswith("$"):
            forms = {k: v for k, v in value.items() if isinstance(v, str)}
            sets = {k: placeholders(v) for k, v in forms.items()}
            if len(set(map(frozenset, sets.values()))) > 1:
                problems.append(f"{key}: plural forms disagree on placeholders: {sets}")

    problems.extend(check_intensity_rungs(strings))

    # keys referenced from client code must exist
    missing: dict[str, set[str]] = {}
    for glob in CLIENT_GLOBS:
        for path in root.glob(glob):
            try:
                text = path.read_text()
            except (UnicodeDecodeError, OSError):
                continue
            for key in KEY_REF_RE.findall(text):
                if key not in strings:
                    missing.setdefault(key, set()).add(str(path.relative_to(root)))
    for key, files in sorted(missing.items()):
        problems.append(f"{key}: referenced by {', '.join(sorted(files))} but absent from content/microcopy.json")

    if problems:
        print(f"copylint: {len(problems)} problem(s)\n", file=sys.stderr)
        for p in problems:
            print(f"  ✗ {p}", file=sys.stderr)
        return 1

    print(f"copylint: ok — {len(strings)} strings, 0 problems")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
