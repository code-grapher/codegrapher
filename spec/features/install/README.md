---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Install

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/install?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/install?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/install?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/install?op=request-change) |
**Status:** Implementing
**Source Ideas:** —

## Summary

`codegrapher install` lists and installs the fleet CLIs relevant to
codegrapher, and `codegrapher upgrade` reports and upgrades every installed
catalog CLI plus codegrapher itself, both built entirely on the shared
[CLI Install Command Library](https://github.com/strongo/cli-helpers/blob/main/spec/features/cli-install/README.md).
`codegrapher self-update` is `codegrapher upgrade codegrapher`: both reach
the identical library call, because codegrapher is always upgraded last and
classified from its own self-update Config, never a `PATH` probe of its own
binary.

## Problem

codegrapher's catalog entry declares it relevant to `specscore`, `wb` and
`cover100` (the [CLI Install Command Library](https://github.com/strongo/cli-helpers/blob/main/spec/features/cli-install/README.md)'s
relevance matrix), and each of those CLIs' own `install` command lists
`codegrapher` in turn. Without `codegrapher install` itself, a user of one of
those CLIs could discover and install `codegrapher`, but a user who started
from `codegrapher` had no equivalent way to discover SpecScore, WB or
cover100, or to install any of them consistently with how they installed
`codegrapher`.

## Behavior

### Built on the shared library

`codegrapher install` is built entirely on the fleet-wide CLI Install Command
Library (`github.com/strongo/cli-helpers/cliinstall`, Implementing),
configured from codegrapher's own compiled-in catalog entry — the same entry
`self-update` builds its `selfupdate.Config` from
(cli-install#req:host-identity-from-catalog,
cli-install#req:catalog-identity-single-source). Listing, details, the
destination and Homebrew-cask decision, the confirmation gate,
checksum-verified download and `--dry-run`/`--format json` are all specified
once in that library, not restated here; this Feature specifies only
codegrapher's own host id and exit-code mapping.

### Command surface

#### REQ: command-name

The CLI MUST expose the command as `codegrapher install`, built from
`github.com/strongo/cli-helpers/cliinstall/cobracmd`. The command inherits
the library's full flag surface — `--all`, `--yes`/`-y`, `--dry-run`,
`--dir`, and `--format text|json` — none of which is re-specified here.
`install` MUST NOT gain an `update` alias: codegrapher's existing `update`
alias stays on `self-update` only
(cli-install#req:update-alias-policy).

#### REQ: upgrade-command

The CLI MUST expose `codegrapher upgrade [name...]`, built from
`github.com/strongo/cli-helpers/cliinstall/cobracmd`'s `cobracmd.NewUpgrade`.
The command inherits the library's full upgrade flag surface — `--all`,
`--check`, `--yes`/`-y`, `--dry-run`, and `--format text|json` — none of
which is re-specified here, and MUST carry no `update` alias
(cli-install#req:update-alias-policy). `upgrade`'s `HostConfig` and
`HostAfterUpdate` MUST be the exact SAME Config (resolved through the
`selfUpdateConfigFunc` seam) and after-update skills-sync hook
(`syncSkillsAfterSelfUpdate`) `self-update` itself builds and calls, so
`codegrapher self-update` and `codegrapher upgrade codegrapher` reach the
identical library call and the identical hook
(cli-install#req:self-update-equals-upgrade-self,
cli-install#req:host-target-is-running-binary,
cli-install#req:self-update-hook-hint — codegrapher's catalog entry declares
`SelfUpdateHooks: true`).

### Host identity

#### REQ: host-id

`codegrapher install` MUST identify the host to the library as catalog id
`"codegrapher"` only (`cobracmd.CommandOptions.HostID`), never a hand-written
duplicate of the catalog entry (cli-install#req:host-identity-from-catalog).
`cliinstall.ByID("codegrapher")` is the same entry `self-update` resolves
(`cliinstall/catalog_codegrapher.go` in `strongo/cli-helpers`), so `install`'s
relevant-targets listing, and every other fleet CLI's `install codegrapher`,
resolve codegrapher's own release identically. `cobracmd.New` panics if
`"codegrapher"` is absent from the compiled catalog — a programming error
`TestInstall_Registration` catches, never a runtime state a user sees.

### Exit codes

#### REQ: exit-codes

codegrapher has no usage or invalid-state exit code distinct from its
generic error exit `1` (`main.go` prints and exits `1` for any non-nil error
from `root.Execute()`). Every install failure — a `*cobracmd.UsageError` (an
invalid `--format`, or `--all` combined with names), an explicit
`selfupdate.KindUnknownTarget` mapping to that same `*cobracmd.UsageError`
shape, `selfupdate.KindNoInstallDir`, `selfupdate.KindDestinationExists`, or
any self-update-shared kind (download, checksum, permission, non-interactive
refusal, a managed-command failure, ...) — is mapped explicitly, never
through a default branch (cli-install#req:host-owned-exit-codes), and every
non-`KindUnknownTarget` failure passes through unchanged, exactly like
`self-update`'s own passthrough. `install nosuchcli` MUST exit `1` and name
the unknown target. `upgrade` MUST use the exact SAME `installErrors` mapper
(cli-install#req:host-owned-exit-codes: "The upgrade command MUST use the
same error mapper"); it declares no upgrades-available method, so
`upgrade --check` never signals a dedicated exit code for an available
update — matching `self-update`'s own contract, which sets no `Errors` at
all and so never signals one either. `upgrade nosuchcli` MUST exit `1` and
name the unknown target, matching `install nosuchcli` exactly.

| Exit code | Meaning |
|---|---|
| `0` | Success: every named target installed/upgraded, already installed/current, redirected, or dry run — including `upgrade --check` regardless of verdict |
| `1` | Any failure: an unknown target, no usable install directory, a destination that already exists, any self-update-shared failure kind, or an invalid `--format`/`--all` usage |

## Implementation

Source files implementing this feature:

- [`internal/cli/install.go`](../../../internal/cli/install.go) — the
  `installErrors` exit-code mapper and the `cobracmd.New` wiring against
  `HostID: "codegrapher"`.
- [`internal/cli/upgrade.go`](../../../internal/cli/upgrade.go) — the
  `cobracmd.NewUpgrade` wiring, reusing `installErrors` and resolving
  `HostConfig`/`HostAfterUpdate` through the same `selfUpdateConfigFunc`
  seam and `syncSkillsAfterSelfUpdate` hook `self-update` uses.
- [`internal/cli/root.go`](../../../internal/cli/root.go) — registers
  `newInstallCmd()` and `newUpgradeCmd()` on the root command.

The shared behavior lives upstream, not in this repository:
`github.com/strongo/cli-helpers` `cliinstall/`, `cliinstall/cliui/`,
`cliinstall/cobracmd/` (the library and its Cobra adapter), and
`cliinstall/catalog_codegrapher.go` (codegrapher's own catalog entry, shared
with `self-update`).

## Interaction with Other Features

| Feature | Interaction |
|---|---|
| self-update (`internal/cli/self_update.go`) | Both `install` and `upgrade` build from the same `cliinstall.ByID("codegrapher")` catalog entry and the same `selfUpdateConfigFunc`-resolved `selfupdate.Config` self-update itself builds, so `install`'s/`upgrade`'s view of codegrapher (shown by other CLIs) and `self-update`'s own release identity never disagree; all three share the `SelfUpdateHooks`-driven skills re-sync hint, and `self-update`/`upgrade codegrapher` reach the identical library call (cli-install#req:self-update-equals-upgrade-self). |

## Acceptance Criteria

### AC: registration-and-host-id

**Requirements:** install#req:command-name, install#req:host-id

**Given** the compiled `cliinstall` catalog
**When** `newInstallCmd()` builds the command
**Then** it registers `--all`, `--yes`/`-y`, `--dry-run`, `--dir` and
`--format`, resolves against catalog id `"codegrapher"`, and carries no
`update` alias.

### AC: unknown-target-exit-code

**Requirements:** install#req:exit-codes

**Given** the real command built exactly as `root.go` wires it
**When** the user runs `codegrapher install nosuchcli`
**Then** the command fails before any confirmation, network request or
write, names `nosuchcli` in its error as a `*cobracmd.UsageError`, and exits
`1`.

### AC: upgrade-registration-and-self-update-equivalence

**Requirements:** install#req:upgrade-command, cli-install#req:self-update-equals-upgrade-self

**Given** the compiled `cliinstall` catalog
**When** `newUpgradeCmd()` builds the command, and separately `codegrapher self-update --check` and `codegrapher upgrade codegrapher --check` run against the same release state
**Then** the command registers `--all`, `--check`, `--yes`/`-y`, `--dry-run` and `--format`, carries no `update` alias, and both commands report the same current/latest verdict.

### AC: upgrade-unknown-target-exit-code

**Requirements:** install#req:exit-codes

**Given** the real command built exactly as `root.go` wires it
**When** the user runs `codegrapher upgrade nosuchcli`
**Then** the command fails before any confirmation, network request or
write, names `nosuchcli` in its error as a `*cobracmd.UsageError`, and exits
`1`, matching `install nosuchcli` exactly.

The remaining behavior — the relevance matrix, listing and status probing,
destination policy, Homebrew-cask installs, checksum-verified direct
installs, the confirmation gate, `--dry-run`, and `--format json` — is
specified and tested once in the
[CLI Install Command Library](https://github.com/strongo/cli-helpers/blob/main/spec/features/cli-install/README.md)'s
own Acceptance Criteria, which this command inherits by construction rather
than re-proving.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*
