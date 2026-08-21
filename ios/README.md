# Pod for iOS

Swift 6, strict concurrency, SwiftUI, iOS 17+. No cross-platform frameworks
(`CLAUDE.md` §5).

```
PodKit/          a plain Swift package: models, client, copy, tokens, session
  Sources/PodAPI/Generated/   from openapi.yaml, microcopy.json and tokens.json
App/             the SwiftUI app
project.yml      XcodeGen input; the .xcodeproj is generated, not committed
```

## Building

```bash
cd ios
xcodegen generate     # brew install xcodegen
open Pod.xcodeproj
```

Debug builds point at `http://localhost:8080`; release points at production. The value
comes from `PodAPIBaseURL` in the Info.plist, set per configuration in `project.yml`.

`PodKit` has no UI and no simulator dependency, so its tests run on the command line:

```bash
swift test --package-path ios/PodKit
```

## What is generated

Never edit a file under `Sources/PodAPI/Generated/` or `Sources/PodAPI/Resources/`. They
come from `backend/openapi.yaml`, `content/microcopy.json` and `design/tokens.json` via
`make generate` at the repo root.

* `Models.swift` — wire types. Enums are open: a value from a newer server decodes rather
  than throwing, so a backend deploy cannot brick an installed app.
* `Copy.swift` — a typed constant per copy key. A view resolves text through
  `Copywriter`; a string literal in a view is a bug (`CLAUDE.md` §2 rule 1).
* `Tokens.swift` — colours, spacing, type and motion. A raw hex in a view is a bug.

## Architecture

* One injected container, `PodEnvironment`, created in `PodApp` and passed down. No
  singletons.
* `SessionController` is `@MainActor @Observable` and holds the sign-in state machine.
* `APIClient` is an `actor`. It owns the refresh path so that two calls meeting a stale
  access token produce one refresh, not two — a second refresh would rotate the token the
  first one just stored and trip the server's reuse detection (ADR-002).
* Tokens live in the Keychain, as one item, accessible after first unlock on this device
  only.
