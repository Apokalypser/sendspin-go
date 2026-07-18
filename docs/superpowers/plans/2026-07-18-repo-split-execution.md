# Plan: Phase 3 execution — extract sendspin-go-cli and sendspin-go-server repos

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
| **Server** | `github.com/Sendspin/sendspin-go-server` | root `main.go` (from `cmd/sendspin-server`), `internal/server` (file/MP3/FLAC/HLS sources + server TUI), `internal/version` copy, server dist/ assets | libopus (via SDK); ffmpeg optional at runtime for HLS |
| **CLI client (player)** | `github.com/Sendspin/sendspin-go-cli` | root `main.go`, `internal/ui`, `internal/version` copy, player dist/ assets | libopus + ALSA/CoreAudio (malgo) |

Dependency direction stays acyclic: both CLIs `require` a tagged SDK; the SDK
requires neither.

## Decisions to lock before executing

1. **D1 — audio source decoders stay private to the server repo.**
   `internal/server`'s `MP3Source`/`FLACSource`/`HTTPMP3Source`/`FFmpegSource`
   are used only by the server CLI. Keep them `internal/` in
   `sendspin-go-server`. Promoting them to `pkg/audio/source` in the SDK remains
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
   `go install github.com/Sendspin/sendspin-go-{cli,server}@latest` works
   with no `/cmd/...` suffix. Naming caveat: `go install` names the binary
   after the module's last path element, so it produces `sendspin-go-cli` /
   `sendspin-go-server`. Makefiles and release tarballs keep shipping the
   binaries as `sendspin-player` / `sendspin-server` (systemd units and docs
   depend on those names); the `go install` spelling is documented as-is.
5. **D5 — clean cut, no transition release.** After the split lands, the SDK
   repo ships no binaries. Release notes + README pointers handle migration;
   we do not maintain a deprecated in-SDK binary build in parallel.

## Phase 3a — Pre-extraction hardening (in this repo, full test suite still applies)

- [x] Add a CI step that fails if `go list -deps ./pkg/...` ever matches
      `sendspin-go/internal` — makes the Phase 2 gate permanent instead of a
      one-time check. *(ci.yml test job, "SDK boundary guard" step)*
- [x] Sweep docs/README for statements that will become false after the split
      (install instructions, "two binaries" language) and stage rewrites.
      *(inventory below)*
- [ ] Cut the last monorepo release tag (`v1.8.x`; latest today is `v1.8.1`):
      the final version where
      binaries and library ship together, and the minimum SDK version the CLI
      repos will pin.
- Gate: `make test` + `make conformance` green; tag published.

### 3a sweep results — where every doc/asset lands

Nothing below changes in 3a (it is all still true while the binaries live
here); this is the staged disposition for 3b–3e.

**README.md (640 lines — splits three ways in 3d):**

| Lines (approx) | Content | Lands in |
|-------|---------|----------|
| 13 | Windows player memory note | player repo |
| 141–153 | Pi quickstart (`quickstart-pi.sh`, player tarball) | player repo |
| 160–208 | Native deps, MSYS2/Windows toolchain, `BUILDTAGS` notes | SDK keeps (tests still cgo); CLI repos copy the parts their builds need |
| 200–306 | `make server`, server usage: `--audio` sources (MP3/FLAC/HLS), `--no-tui`, flags, daemon install, `server.yaml` | server repo |
| 307–430 | Player usage: flags, `--list-audio-devices`, multi-instance, `player.yaml` | player repo |
| 469–470 | Architecture bullets naming `cmd/sendspin-server` + root `main.go` | rewritten in SDK (drop cmd/ mentions) |
| 500–540 | Combined walkthrough (run server + two players) | split across CLI READMEs; SDK keeps a library quick-start instead |
| 549+ | Conformance suite section | SDK keeps |

**CLAUDE.md (3d rewrite):** project overview ("ships as a library plus two
CLI binaries"), the `make player`/`make server`/daemon targets in Commands,
the Internal layout section (`internal/server`, `internal/ui`,
`internal/version` all leave), and the Configuration & Daemon Mode section
(daemon halves move to the CLI repos; the `config.go` API description stays).

**scripts/quickstart-pi.sh:** hardcodes `REPO_NAME="sendspin-go"` for both
the release-tarball download and `RAW_URL_BASE`. Moves to the player repo in
3c; repoint both constants to `sendspin-go-cli` in 3e once its first release
exists (the download 404s until then — do not repoint earlier).

**examples/README.md:** library-focused already; minor 3d touch-up where it
tells the reader to run `./sendspin-server` for a counterpart (point at the
server repo's releases instead).

**Makefile:** `player`, `server`, `build-all`, `install-*-daemon` targets
leave in 3d; `test`/`lint`/`conformance`/`BUILDTAGS` stay.

**.github/workflows:** `ci.yml` build matrix + `release.yml` move to the CLI
repos (3b/3c); SDK keeps test/lint/conformance (and the boundary guard).

**dist/:** `config/server.example.yaml` + `systemd/sendspin-server.*` →
server repo; `config/player.example.yaml` + `systemd/sendspin-player.*` →
player repo. Nothing stays.

**install-deps.sh:** stays in the SDK (tests need libopus); CLI repos get
trimmed copies in 3b/3c.

## Phase 3b — Create `sendspin-go-server` ✅ (2026-07-18)

- [x] New repo from filtered history (D3): keep `cmd/sendspin-server/`,
      `internal/server/`, `internal/version/`, `dist/config/` (server file),
      `dist/systemd/` (server unit).
- [ ] Move `cmd/sendspin-server/main.go` → root `main.go` (D4); repoint
      imports to `github.com/Sendspin/sendspin-go-server/internal/...`.
- [ ] `go.mod`: `module github.com/Sendspin/sendspin-go-server`, `go 1.24`,
      `require github.com/Sendspin/sendspin-go v1.8.x`. `replace` allowed only
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

## Phase 3c — Create `sendspin-go-cli` ✅ (2026-07-18)

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
- [ ] Tag `v1.9.0` — first library-only release.
- Gate: `make test` + `make conformance` green; `go list ./...` contains only
      `pkg/...` and `examples/...`; both CLI repos build against `v1.9.0`.

## Phase 3e — Release, repoint, announce

- [ ] Re-pin both CLI repos from `v1.8.x` to SDK `v1.9.0`; tag each CLI
      `v1.9.0` (version streams start aligned, then drift independently —
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
| 2 | sendspin-go | tag `v1.8.x` (last monorepo release) | 1 |
| 3 | sendspin-go-server | 3b scaffold (initial import) | 2 |
| 4 | sendspin-go-cli | 3c scaffold (initial import) | 2 |
| 5 | sendspin-go | 3d slim-down + README/CLAUDE.md rewrite, tag `v1.9.0` | 3, 4 green |
| 6 | all three | 3e re-pin, tags, announcements | 5 |

## Progress log

- 2026-07-18: Phase 3a complete — CI boundary guard merged (PR #145), doc
  sweep recorded, last monorepo tag `v1.8.2` published.
- 2026-07-18: Repos created with final names `sendspin-go-server` and
  `sendspin-go-cli` (renamed from the planned `sendspin-server-go` /
  `sendspin-player-go`; lowercase chosen for Go module-path convention).
- 2026-07-18: Phases 3b + 3c complete — both repos populated via
  git filter-repo (path history preserved), root `main.go`, go.mod pinning
  SDK `v1.8.2` with no-replace CI guards, server/player-only Makefiles +
  CI + release workflows, README/CLAUDE.md, Apache-2.0 license matching
  the SDK. `quickstart-pi.sh` still points at sendspin-go releases until
  sendspin-go-cli publishes its first release (3e). Next: Phase 3d (slim
  the SDK) once both repos' CI is green.
