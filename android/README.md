# Pod for Android

Kotlin, Compose, Material 3, minSdk 26. No cross-platform frameworks (`CLAUDE.md` §5).

```
core/api/        plain Kotlin/JVM (ADR-009): models, client, copy, tokens, session state
  src/main/kotlin/app/pod/api/generated/   from openapi.yaml, microcopy.json, tokens.json
app/             the Compose app, Hilt wiring, the encrypted token store
```

## Building

```bash
cd android
./gradlew :app:assembleDebug
./gradlew test
```

Debug builds point at `http://10.0.2.2:8080` — the host machine as the emulator sees it.
Release points at production. Both come from `BuildConfig.API_BASE_URL`.

`:core:api` has no Android dependency, so its tests run without the SDK:

```bash
./gradlew :core:api:test --configure-on-demand
```

The `--configure-on-demand` flag is what keeps `:app` — and therefore the Android Gradle
Plugin — out of the picture on a machine that has no SDK installed. The root build file
declares no plugins for the same reason.

## What is generated

Never edit anything under `core/api/src/main/kotlin/app/pod/api/generated/` or
`core/api/src/main/resources/`. They come from `backend/openapi.yaml`,
`content/microcopy.json` and `design/tokens.json` via `make generate` at the repo root.

* `Models.kt` — wire types. Enums decode an unrecognised value to `UNKNOWN` rather than
  throwing, so a backend deploy cannot brick an installed app.
* `Copy.kt` — a constant per copy key. A composable resolves text through `Copywriter`;
  a string literal in a composable is a bug (`CLAUDE.md` §2 rule 1). The only strings in
  `res/values/strings.xml` are the ones the platform reads before the app runs.
* `Tokens.kt` — colours as `0xAARRGGBB`, spacing and sizes in dp, type in sp, durations in
  ms. A raw hex in a composable is a bug.

## Architecture

* Unidirectional: a screen sends events to `AuthViewModel`, and observes one `StateFlow`
  coming back.
* `SessionRepository` is a `@Singleton` and holds the sign-in state machine, so a
  configuration change cannot restart it mid-flow. It lives in `:core:api` because the
  state machine is not a UI concern.
* `PodApiClient` serialises refresh behind a mutex, and re-checks freshness after taking
  it: two calls meeting a stale access token must produce one rotation, not two, or the
  second revokes what the first just stored (ADR-002).
* Tokens live in `EncryptedSharedPreferences`, as one entry. Backups and device transfer
  are switched off in `data_extraction_rules.xml` — the session is device-bound.
* Dynamic colour is deliberately off. The palette is part of the product's promise, not a
  surface for the launcher wallpaper to redecorate.
