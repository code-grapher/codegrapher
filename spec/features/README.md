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
| [SpecScore Source Traceability](specscore-source-traceability/README.md) | Implementing | Connect accepted SpecScore source directives to canonical Feature, REQ, AC, and scenario nodes. |
| [Automatic index freshness](automatic-index-freshness/README.md) | Implementing | Keep CodeGrapher indexes current automatically through one observable incremental reconciliation engine, beginning with a foreground watch command. |
| [Authenticated browser API](live-daemon-api/README.md) | Amending | Expose the one repository owned by a foreground server or background daemon through a bounded, authenticated, versioned HTTP API that the codegrapher.com repository browser can consume without learning local filesystem paths. |
| [Sync initialization on demand](sync-initialize-if-missing/README.md) | Implementing | Let automation explicitly initialize an unindexed repository through the normal sync command, then use incremental reconciliation thereafter. |

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/features-index-specification*
