# Project patterns: architecture and what goes where

Alfred's backend (`backend/`, Go module `github.com/sundbassen/alfred`) has three layers. Follow this layout for all new code, and when planning changes.

## Layers and the dependency rule

```
cmd/       may import anything
skills/    may import internal/            (the domain layer: what Alfred can do)
internal/  may import only internal/       (plus the standard library and third-party modules)
```

- Nothing imports `cmd/`. Go's `internal` rule already stops anything outside `cmd/` from importing `cmd/internal/...`.
- `make layers` (run in CI) fails if `internal/` imports `skills/` or `cmd/`, or `skills/` imports `cmd/`.

## What goes where

| Path (under `backend/`) | Holds |
|---|---|
| `cmd/<binary>/` | `main` only: flags, logging, lifecycle. Binaries: `alfred`, `replaywav`, `reviewrecordings`, `wakestats`, `micdebug` |
| `cmd/internal/wiring/` | The composition root. `skills.go` constructs the providers (external implementations) and calls every skill's `Register`. It's the one place to see which providers Alfred uses |
| `skills/<skill>/` | One skill: its intent types, its handlers, the interfaces its handlers need, and `Register(r *assistant.Router, deps...)` registering all of its intents. There is no `skills` package, only one package per skill |
| `skills/<skill>/<skill>service/` | Providers implementing that skill's interfaces, one file per provider (e.g. `musicservice/spotify.go` with `NewSpotify(...) *Spotify`). Constructors return concrete types. The skill never imports this package; this package may import the skill (for shared types) and `internal/` |
| `internal/assistant/` | The intent contract and router: `Intent`, `Handle[I]`, `Router`, `Response`, `Spec`, `SpokenError`, `SpokenText` |
| `internal/assistant/match/` | The local recognizer (phrase matching). Further recognizers (e.g. the Claude fallback) go alongside it and produce the same intents |
| `internal/app/` | Wires the voice framework into a listener, plus `Assistant` (recognize → dispatch). It never imports skills |
| `internal/voice/` | The voice framework: `listener`, `wakeword`, `stt`, `vad` (+ `silero`), `mic`, `tts`, `recorder`. Its contracts are in `voice.go` |
| `internal/platform/` | Shared helpers: `fault` (error categories, see `go-errors.md`), `pcm`, `wav`, `onnxrt`, `logging`, `wakeclip` |
| `internal/<client>/` | Infrastructure shared by several skills (e.g. `internal/homeassistant`, an API client used by lights and climate). Each skill's service package adapts it to that skill's interface |

## Recipes

**Add a skill:**
1. Create `skills/<skill>/<skill>.go` with:
   - intent types: concrete structs with a value-receiver `IntentName()`; parameters are fields;
   - handlers: `func(ctx, I) (assistant.Response, error)`;
   - the interfaces the handlers need;
   - `Register(r *assistant.Router, deps...)`, which calls `assistant.Handle` for each intent with a `Spec` (description and example phrases).
2. Add one line to `cmd/internal/wiring/skills.go`.

**Add a provider:**
1. Add `skills/<skill>/<skill>service/<provider>.go`, implementing the skill's interface.
2. Construct it in `cmd/internal/wiring/skills.go` and pass it to the skill's `Register`.

**Add shared infrastructure:**
1. Add a client package in `internal/<client>/`.
2. Adapt it per skill in each skill's `<skill>service` package.

## Conventions

- **Handlers are transport-agnostic:** they take `(ctx, concrete intent)` and return `(Response, error)`, and never see voice or HTTP types. A future HTTP API (`POST /intents/{name}`) dispatches to the same handlers.
- **Interfaces live with their consumer** (the skill), not with the implementation. Don't define an interface before something uses it.
- **Dependencies are passed explicitly** through constructors and `Register`: no globals, no `init()` registration, no DI framework.
- **Errors** follow `.claude/rules/go-errors.md`: tozd errors, with generic categories in `internal/platform/fault`.
