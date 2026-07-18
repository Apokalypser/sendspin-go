# Plan: Phase 3 execution — extract sendspin-player and sendspin-server repos

**Status:** Proposed
**Owner:** Chris
**Created:** 2026-07-18
**Builds on:** `2026-06-11-three-repo-split.md` (Phases 0–2 complete; this doc details Phase 3)

## Where we are

The 2026-06-11 plan settled the target shape (one SDK keeping both client and
server APIs; two thin CLI repos) and drove all in-module decomposition:

- Phase 0: dead code removed (legacy `internal/server` server, orphaned
  `pkg/discovery` and `pkg/audio/encode`).
- Phase 1: `pkg/sendspin` decomposed by file; shared constants hoisted to
  `constants.go`; config split into common/player/server.
- Phase 2 **gate met**: `go list -deps ./pkg/...` imports **zero** `internal/`
  packages (re-verified 2026-07-18).

What remains is the actual extraction. Current binary import graphs:

```
main.go (player)            → internal/ui, internal/version,
                              pkg/audio/output, pkg/discovery, pkg/sendspin
cmd/sendspin-server/main.go → internal/server (sources + TUI), pkg/sendspin
```

Nothing else crosses the SDK boundary, so the extraction is mechanical — the
work is in repo scaffolding, CI/release pipelines, and migration comms.

## Target repos

| Repo | Module path | Contents | Native deps |
|------|-------------|----------|-------------|
| **Library (SDK)** | `github.com/Sendspin/sendspin-go` (unchanged) | `pkg/{sendspin,protocol,audio,sync,discovery}`, `examples/`, conformance wiring | libopus (encode/decode) |
| **Server** | `github.com/Sendspin/sendspin-server` | root `main.go` (from `cmd/sendspin-server`), `internal/server` (file/MP3/FLAC/HLS sources + server TUI), `internal/version` copy, server dist/ assets | libopus (via SDK); ffmpeg optional at runtime for HLS |
| **CLI client (player)** | `github.com/Sendspin/sendspin-player` | root `main.go`, `internal/ui`, `internal/version` copy, player dist/ assets | libopus + ALSA/CoreAudio (malgo) |

Dependency direction stays acyclic: both CLIs `require` a tagged SDK; the SDK
requires neither.

## Decisions to lock before executing

1. **D1 — audio source decoders stay private to the server repo.**
   `internal/server`'s `MP3Source`/`FLACSource`/`HTTPMP3Source`/`FFmpegSource`
   are used only by the server CLI. Keep them `internal/` in
   `sendspin-server`. Promoting them to `pkg/audio/source` in the SDK remains
   an additive, non-breaking follow-up if library consumers ever ask for
   built-in sources. (Matches the "optional, deferred" note from Phase 2.)
2. **D2 — `internal/version` is duplicated, not shared.** It is ~10 lines of
   ldflags target. Each CLI repo gets its own copy under its own module path;
   the SDK (no binary) drops it. A shared `pkg/version` would couple release
   cadences for nothing.
3. **D3 — history preservation: `git filter-repo` per new repo.** Each CLI
   repo is created from a clone of sendspin-go filtered to its paths, so
   `git log`/`git blame` survive for the moved files. A plain file copy is the
   fallback if filter-repo proves troublesome — acceptable, since the monorepo
   history remains browsable in sendspin-go.
4. **D4 — root `main.go` in both CLI repos** so
   `go install github.com/Sendspin/sendspin-{player,server}@latest` works with
   no `/cmd/...` suffix.
5. **D5 — clean cut, no transition release.** After the split lands, the SDK
   repo ships no binaries. Release notes + README pointers handle migration;
   we do not maintain a deprecated in-SDK binary build in parallel.

## Phase 3a — Pre-extraction hardening (in this repo, full test suite still applies)

- [ ] Add a CI step that fails if `go list -deps ./pkg/...` ever matches
      `sendspin-go/internal` — makes the Phase 2 gate permanent instead of a
      one-time check.
- [ ] Sweep docs/README for statements that will become false after the split
      (install instructions, "two binaries" language) and stage rewrites.
- [ ] Cut the last monorepo release tag (`v1.3.x`): the final version where
      binaries and library ship together, and the minimum SDK version the CLI
      repos will pin.
- Gate: `make test` + `make conformance` green; tag published.

## Phase 3b — Create `sendspin-server`

- [ ] New repo from filtered history (D3): keep `cmd/sendspin-server/`,
      `internal/server/`, `internal/version/`, `dist/config/` (server file),
      `dist/systemd/` (server unit).
- [ ] Move `cmd/sendspin-server/main.go` → root `main.go` (D4); repoint
      imports to `github.com/Sendspin/sendspin-server/internal/...`.
- [ ] `go.mod`: `module github.com/Sendspin/sendspin-server`, `go 1.24`,
      `require github.com/Sendspin/sendspin-go v1.3.x`. `replace` allowed only
      locally; CI release job greps `go.mod` and fails if a `replace` survives.
- [ ] Makefile: `server`, `test`, `lint`, `install-server-daemon` targets;
      keep `BUILDTAGS ?= nolibopusfile` and the ldflags version stamp
      (new module path).
- [ ] CI: test + lint on linux (libopus-dev); release workflow reuses the
      existing matrix (linux amd64/arm64/armv6, darwin, windows) trimmed to
      the server binary.
- [ ] README: server usage, config/daemon docs, link back to SDK + player.
      Copy `install-deps.sh` trimmed to server needs; carry over the AI-policy
      and contribution sections.
- Gate: `go build` + `go test ./...` green against the *tagged* SDK (no
      replace); binary runs and serves a test tone to a player built from the
      monorepo tag.

## Phase 3c — Create `sendspin-player`

Same recipe as 3b with: root `main.go` (already at root), `internal/ui/`,
`internal/version/`, player dist assets. Extra native deps in CI:
`libasound2-dev` (malgo) alongside libopus. Release matrix keeps the player
rows including armv6 (Pi Zero) — this binary is the reason those rows exist.

- Gate: mirror of 3b's, with the e2e direction reversed (new player against a
  monorepo-tag server).

## Phase 3d — Slim the SDK repo (breaking-change PR in sendspin-go)

- [ ] Delete: root `main.go`, `cmd/`, `internal/server`, `internal/ui`,
      `internal/version`, `dist/`, player/server sections of the release
      workflow (keep tag-triggered releases only for pkg testing, or drop the
      workflow and rely on ci.yml).
- [ ] Keep: `pkg/`, `examples/`, `install-deps.sh` (tests still need
      libopus), conformance workflow, pre-commit config.
- [ ] Makefile: drop `player`/`server`/daemon targets; `all` becomes
      `test` + `lint`; keep `conformance` and the `BUILDTAGS` machinery.
- [ ] Rewrite README as a library README (install, Receiver/Player/Server
      quick-starts, links to the two CLI repos for ready-made binaries) and
      update CLAUDE.md (project overview, commands, layout sections).
- [ ] Tag `v1.4.0` — first library-only release.
- Gate: `make test` + `make conformance` green; `go list ./...` contains only
      `pkg/...` and `examples/...`; both CLI repos build against `v1.4.0`.

## Phase 3e — Release, repoint, announce

- [ ] Re-pin both CLI repos from `v1.3.x` to SDK `v1.4.0`; tag each CLI
      `v1.4.0` (version streams start aligned, then drift independently —
      SDK leads, CLIs pin a minimum).
- [ ] Conformance harness (`Sendspin/conformance`): adapter import paths are
      untouched (SDK kept its module path); update any CI checkout refs that
      assumed binaries exist in sendspin-go.
- [ ] Update `Sendspin/website` docs/spec links, README badges, and the
      Music Assistant integration notes to the new install paths.
- [ ] GitHub: transfer open issues that concern the binaries/TUI to the new
      repos; add repo descriptions/topics; enable Dependabot (or Renovate) in
      both CLI repos so SDK bumps arrive as PRs.
- [ ] Release notes in all three repos explaining the split and the new
      `go install` paths.

## Cross-cutting invariants

- **Conformance stays on the SDK** — it exercises `pkg/` symbols only
  (`NewServerClientFromConn`, `CreateAudioChunk`, `ServerConn`). Wire-format
  invariants (20 ms chunks, µs timestamps, 9-byte header) all live in `pkg/`
  and never cross a repo boundary.
- **`GOFLAGS=-tags=nolibopusfile`** must survive in every repo's Makefile,
  CI, and release pipeline; the SDK keeps its `BUILDTAGS=` override job.
- **Cross-repo e2e smoke test** (post-split replacement for the monorepo's
  in-process client↔server tests at the binary level): a scheduled/dispatch
  workflow in each CLI repo that builds the other repo's binary at `@latest`
  tag and runs a connect-and-stream smoke check. Nice-to-have; the SDK's
  in-process integration tests remain the primary net.
- **Per-repo hygiene:** each new repo gets `.pre-commit-config.yaml`,
  golangci-lint config, LICENSE, and the Open Home Foundation AI-policy
  notice copied from sendspin-go.

## Risks

1. **`replace` directives leaking into releases** — mitigated by the CI grep
   guard (3b/3c) in the release job, not just in tests.
2. **Version skew** (CLI pinning an SDK with a wire-relevant fix missing) —
   mitigated by Dependabot auto-bump PRs + the cross-repo smoke test.
3. **`go install` breakage for existing users** of
   `github.com/Sendspin/sendspin-go@latest` — unavoidable under D5; handled
   by release notes and README redirects. Old tags keep working.
4. **filter-repo mistakes** (dropped paths, rewritten SDK history) — the
   filter runs only on throwaway clones for the *new* repos; sendspin-go
   history is never rewritten.
5. **Issue/PR fragmentation** — triage rule: wire/protocol/sync → SDK;
   TUI/daemon/packaging → the respective CLI repo.

## Suggested PR sequence

| # | Repo | Change | Depends on |
|---|------|--------|------------|
| 1 | sendspin-go | 3a: CI boundary guard + doc sweep | — |
| 2 | sendspin-go | tag `v1.3.x` (last monorepo release) | 1 |
| 3 | sendspin-server | 3b scaffold (initial import) | 2 |
| 4 | sendspin-player | 3c scaffold (initial import) | 2 |
| 5 | sendspin-go | 3d slim-down + README/CLAUDE.md rewrite, tag `v1.4.0` | 3, 4 green |
| 6 | all three | 3e re-pin, tags, announcements | 5 |
