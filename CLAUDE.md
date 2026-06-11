# CLAUDE.md — project policies (codegrapher)

codegrapher is a behavior-parity Go port of codegraph
(github.com/colbymchenry/codegraph, MIT; local checkout at `../codegraph`).
See AGENT-BRIEF.md for the original mandate, KNOWN-BUGS.md for the parity
policy and bug ledger.

## Quiet-experiment policy (owner decision, 2026-06-11)

This port is an experiment we're not ready to show around yet. We're not
hiding anything — attribution is full and honest — we just don't want to
bother the codegraph project before we know this is real:

- **Don't reach out to codegraph yet**: no PRs, issues, or comments on
  github.com/colbymchenry/codegraph until the owner says we're ready.
- In contributions to other projects (e.g. gotreesitter), describe
  codegrapher on its own terms ("a code-intelligence indexer") rather
  than as a codegraph port — let the repo's own docs tell that story.
- Attribution stays everywhere it belongs: LICENSE/NOTICE, README, and
  the website keep the MIT attribution to the original.
- Practical consequence: upstream-first bug fixing is paused. Fixes to
  reproduced upstream bugs (KNOWN-BUGS §A) happen as our own documented
  divergences with golden re-baselines for now; once we go public we can
  offer them upstream.

The owner lifts this explicitly; don't assume it expired.

## Mandate change (owner decision, 2026-06-11, supersedes bug-for-bug parity)

**Backward compatibility with codegraph is no longer a goal. Speed and
correct behavior win.** The original-derived goldens proved the port
faithful (verified baseline achieved); from here:

- Reproduced upstream bugs (KNOWN-BUGS §A) are TO BE FIXED, not preserved.
- Where our behavior is more correct than codegraph's, keep ours and
  document it.
- Goldens get re-baselined against OUR OWN output (deterministic
  self-goldens = regression net). The original-capture scripts remain for
  informational comparison only.
- CLI verbs and JSON shapes stay stable unless there's a concrete reason
  to change them (don't break consumers gratuitously — but bug-shaped
  output is a concrete reason).

## Standing rules

- **Goldens are immutable by hand.** Never hand-edit testdata/golden/**.
  Changes happen via re-baseline scripts; after the mandate change above,
  the spec source is our own deterministic output (original captures, when
  needed for comparison, must use Node 22 — never Node 26, see KNOWN-BUGS
  B-2).
- Bugs that crash, corrupt, or hang are fixed immediately, always.
- **License:** codegrapher is Apache-2.0 (owner decision); the original's MIT
  notice must be preserved in attribution (NOTICE).
- **CGO_ENABLED=0** is a launch requirement (single static binary,
  cross-compilation). Parser is gotreesitter via a patched fork
  (go.mod `replace` → github.com/trakhimenok/gotreesitter) until upstream
  PR odvcencio/gotreesitter#113 merges; remove the replace before advertising
  library consumption (replace doesn't propagate to importers).
- Gates for any change: gofmt, go vet ./..., CGO_ENABLED=0 go build ./...,
  CGO_ENABLED=0 go test -count=1 ./... — all green, fixture goldens and the
  46-golden binary parity test included.
