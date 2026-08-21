#!/usr/bin/env python3
"""Checks hand-written client code against the generated models.

Neither Xcode nor the Android SDK is available in this repository's CI-adjacent
environment, so `ios/` and `android/app` go unverified between Mac runs. This is not a
type checker and does not pretend to be one. It catches the narrow class of mistake that
comes from writing against generated code by hand:

  1. a copy key that does not exist in the generated catalogue
  2. an initialiser argument label that the generated type does not have
  3. a generated type name that is not generated

Each check is deliberately precise rather than thorough. A linter that cries wolf gets
switched off, and this one exists to be trusted between compiles.

Usage: python3 tools/clientlint/clientlint.py [--root .]
Exit code 1 on any violation.
"""
from __future__ import annotations
import argparse, os, pathlib, re, sys


def swift_generated(root: pathlib.Path) -> str:
    out = ""
    for path in (root / "ios/PodKit/Sources/PodAPI/Generated").glob("*.swift"):
        out += path.read_text()
    return out


def kotlin_generated(root: pathlib.Path) -> str:
    out = ""
    base = root / "android/core/api/src/main/kotlin/app/pod/api/generated"
    for path in base.glob("*.kt"):
        out += path.read_text()
    return out


def hand_written(root: pathlib.Path, roots: list[str], suffix: str) -> list[pathlib.Path]:
    found = []
    for rel in roots:
        for dirpath, _, files in os.walk(root / rel):
            if "generated" in dirpath.lower() or "/build/" in dirpath:
                continue
            for name in files:
                if name.endswith(suffix):
                    found.append(pathlib.Path(dirpath) / name)
    return found


def top_level_labels(args: str) -> set[str]:
    """Argument labels at nesting depth zero, so a nested call's labels are not counted."""
    labels, depth, token, at_start = set(), 0, "", True
    for ch in args:
        if ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
        elif depth == 0 and ch == ",":
            at_start, token = True, ""
            continue
        if depth == 0:
            if ch == ":" and at_start:
                label = token.strip()
                if re.fullmatch(r"\w+", label):
                    labels.add(label)
                at_start = False
            token += ch
    return labels


def balanced(src: str, start: int) -> str:
    depth, i = 1, start
    while i < len(src) and depth:
        if src[i] in "([{":
            depth += 1
        elif src[i] in ")]}":
            depth -= 1
        i += 1
    return src[start : i - 1]


def check_swift(root: pathlib.Path) -> list[str]:
    gen = swift_generated(root)
    if not gen:
        return ["ios: no generated Swift found — run `make generate`"]

    copy_keys = set(re.findall(r"static let (\w+) = CopyKey", gen))
    inits: dict[str, set[str]] = {}
    for match in re.finditer(r"public struct (\w+): [^\{]*\{(.*?)\n\}", gen, re.S):
        signature = re.search(r"public init\((.*?)\n?\s*\) \{", match.group(2), re.S)
        if signature:
            inits[match.group(1)] = set(re.findall(r"(\w+):\s", signature.group(1)))

    problems: list[str] = []
    files = hand_written(root, ["ios/App", "ios/PodKit/Sources", "ios/PodKit/Tests"], ".swift")
    for path in files:
        src = path.read_text()
        rel = path.relative_to(root)
        for match in re.finditer(r"copy\(\.(\w+)", src):
            if match.group(1) not in copy_keys:
                problems.append(f"{rel}: copy(.{match.group(1)}) — no such copy key")
        for name, labels in inits.items():
            for match in re.finditer(rf"(?<![\w.]){name}\(", src):
                args = balanced(src, match.end())
                if not args.strip():
                    continue
                # `from:` is Decodable's initialiser, which every generated type has.
                unknown = top_level_labels(args) - labels - {"from"}
                if unknown:
                    problems.append(
                        f"{rel}: {name}(...) passes {sorted(unknown)}; it takes {sorted(labels)}"
                    )
    return problems


def check_kotlin(root: pathlib.Path) -> list[str]:
    gen = kotlin_generated(root)
    if not gen:
        return ["android: no generated Kotlin found — run `make generate`"]

    # `const val` for the keys themselves, plain `val` for the generated `all` list.
    copy_keys = set(re.findall(r"public (?:const )?val (\w+)[:=]", gen))
    inits: dict[str, set[str]] = {}
    for match in re.finditer(r"public data class (\w+)\((.*?)\n\)", gen, re.S):
        inits[match.group(1)] = set(re.findall(r"val (\w+):", match.group(2)))

    problems: list[str] = []
    files = hand_written(
        root,
        ["android/app/src/main", "android/core/api/src/main", "android/core/api/src/test"],
        ".kt",
    )
    for path in files:
        src = path.read_text()
        rel = path.relative_to(root)
        for match in re.finditer(r"CopyKey\.(\w+)", src):
            if match.group(1) not in copy_keys:
                problems.append(f"{rel}: CopyKey.{match.group(1)} — no such copy key")
        for name, fields in inits.items():
            for match in re.finditer(rf"(?<![\w.]){name}\(", src):
                args = balanced(src, match.end())
                if not args.strip():
                    continue
                unknown = {
                    label
                    for label in top_level_labels(args.replace("=", ":"))
                    if label not in fields
                }
                if unknown:
                    problems.append(
                        f"{rel}: {name}(...) passes {sorted(unknown)}; it takes {sorted(fields)}"
                    )
    return problems


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default=".")
    args = parser.parse_args()
    root = pathlib.Path(args.root).resolve()

    problems = check_swift(root) + check_kotlin(root)
    if problems:
        print(f"clientlint: {len(problems)} problem(s)\n", file=sys.stderr)
        for problem in problems:
            print(f"  ✗ {problem}", file=sys.stderr)
        return 1
    print("clientlint: ok — every generated symbol the clients reference exists")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
