# Go Line Coverage (cross-repo) — ✅ COMPLETE & SHIPPED

Status: **done.** Feature built, e2e-tested, real-Docker validated, released, and
merged to `main` across all repos. See `tasks/plan.md` for architecture.

## Final shipped state
- **codegrapher** — `coverage` pkg (attribution + INGR format), schema v6,
  `codegrapher coverage` CLI, export/import. Merged (PR #1).
- **server** — `/coverage` upload+serve, `/cover` trigger + isolated Docker
  runner, host runner for e2e, design doc. Merged (PRs #3, #4, #5, #6).
- **codegrapher-dev** — per-line gutter + per-function inclusive % badges,
  "Update test coverage" button, Playwright e2e. Merged (PR #4).
- **synchestra-vm** — sandbox `RunOnce` built then extracted; consumes
  strongo/sandbox. Merged (PRs #1, #2, #3).
- **strongo/sandbox** — `github.com/strongo/sandbox` **v0.1.3** (Apache-2.0,
  © Sneat.co): hardened one-shot Docker runner + `isolation` preset, real-Docker
  integration test wired into CI. Both consumers track v0.1.3.

## Tracks (all complete)
- [x] **T0** shared `coverage` lib API + INGR format (the seam)
- [x] **A1–A5** codegrapher: schema v6, parser, innermost attribution + RLE, CLI, export/import
- [x] **B1–B2** server: `/coverage` collect (validate+store) + serve
- [x] **C1–C3** UI: gutter, per-function inclusive badges, optional fixture load
- [x] **CP1/CP2** contract consistent (shared pkg) + CLI smoke-tested on real data
- [x] **D0** reusable `RunOnce` sandbox (hardened preset + inject + collect)
- [x] **D1** server `/cover` trigger + job + SSE + Docker runner (clone-once, git-archive inject, egress, server-side ingest)
- [x] **D2** UI button → POST /cover → SSE progress → reload
- [x] **E1–E3** extract sandbox → `strongo/sandbox`; both products consume it
- [x] **G1–G5** local-server + Playwright e2e (host runner, no Docker) — **passed**
  - bug caught+fixed by e2e: UI `parseCoverageRanges` rejected object-form ranges
- [x] **F** publish strongo/sandbox, rebase, push, open + merge PRs (dependency order)
- [x] **Real-Docker validation** — found + fixed 3 sandbox bugs (tmpfs-at-create,
  seccomp=default, root-owned non-root workdir → named volume + prep container);
  released v0.1.1 → v0.1.3; integration test in CI; design doc updated to match.
- [x] node_coverage verified end-to-end (Playwright e2e rendered per-function % badges from a real indexed fixture)

## Locked contracts (for the record)
- **Upload:** `POST /coverage/{host}/{org}/{repo}/{ref}/{lang}/{version}`, multipart,
  fields `coverage` (req) + `node_coverage` (opt), raw `.ingr` bytes → 204.
- **Trigger:** `POST /cover/{host}/{org}/{repo}/{ref}` → 202; SSE at `.../events`
  (`progress`/`done`/`error`); recordsets stored before `done`.
- **Sandbox runner (v0.1.3):** `git archive {ref}` → named volume + prep container
  (chmod + inject into the live volume) → hardened non-root main container runs
  `go test -coverprofile` (egress on) → collect → server `coverage.Ingest` + store.
  No host mount, no 2nd clone.

## Nothing outstanding.
