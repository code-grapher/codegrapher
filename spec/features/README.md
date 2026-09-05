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

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/features-index-specification*
