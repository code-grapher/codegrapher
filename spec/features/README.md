---
format: https://specscore.md/features-index-specification
---

# Features

Feature specifications for this project.

## Index

| Feature | Status | Description |
|---------|--------|-------------|
| [Version-gated reindex](version-gated-reindex/README.md) | Stable | Gate codegrapher sync on the scanner version stored in the index: same version performs an additive sync, a changed or missing version escalates to a full reindex. |
| [Whole-repo file-node indexing](whole-repo-file-nodes/README.md) | Stable | Emit a file-level node for every non-gitignored file, not only files in recognized source languages. |
| [WB Fleet Integration](wb-fleet-integration/README.md) | Draft | Fleet-safe CodeGrapher behavior for WB-managed repositories and worktrees. |
| [SpecScore Source Traceability](specscore-source-traceability/README.md) | Stable | Connect accepted SpecScore source directives to canonical Feature, REQ, AC, and scenario nodes. |
| [Automatic index freshness](automatic-index-freshness/README.md) | Stable | Keep CodeGrapher indexes current automatically through one observable incremental reconciliation engine, beginning with a foreground watch command. |
| [Authenticated browser API](live-daemon-api/README.md) | Stable | Expose the one repository owned by a foreground server or background daemon through a bounded, authenticated, versioned HTTP API that the CodeGrapher.dev repository browser can consume without learning local filesystem paths. |
| [Sync initialization on demand](sync-initialize-if-missing/README.md) | Stable | Let automation explicitly initialize an unindexed repository through the normal sync command, then use incremental reconciliation thereafter. |
| [Secure remote browser API](secure-remote-browser-api/README.md) | Implementing | Serve the browser API remotely only through an authority-matching, browser-trusted HTTPS endpoint. |
| [Install](install/README.md) | Implementing | `codegrapher install` lists and installs the fleet CLIs relevant to codegrapher, and `codegrapher upgrade` reports and upgrades every installed catalog CLI plus codegrapher itself, both built entirely on the shared [CLI Install Command Library](https://github.com/strongo/cli-helpers/blob/main/spec/features/cli-install/README.md). `codegrapher self-update` is `codegrapher upgrade codegrapher`: both reach the identical library call, because codegrapher is always upgraded last and classified from its own self-update Config, never a `PATH` probe of its own binary. |
| [Test-writing context bundle](test-context/README.md) | Implementing | `codegrapher context <symbol...>` returns everything a test writer needs about a set of symbols in one bounded read: source, the types each symbol touches, its direct callees as signatures, existing tests that already exercise it, and (with `--for test`) the package's test helpers and fakes. One call replaces the turn-by-turn sequence of `node --source`, `node --relations`, and `callees` that a coverage-writing agent currently repeats per symbol. |

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/features-index-specification*
