#!/usr/bin/env python3
"""Rebaseline only version fields when extraction semantics change.

The full capture script records machine paths, wall-clock timestamps and
incidental ordering. This narrow script preserves those historical fields
while refreshing the version asserted by the parity status goldens.
"""

from pathlib import Path
import re


root = Path(__file__).resolve().parents[2]
source = (root / "indexer/indexer.go").read_text()
version_match = re.search(r"const ExtractionVersion = (\d+)", source)
if version_match is None:
    raise SystemExit("ExtractionVersion not found")
version = version_match.group(1)

changed = 0
for path in sorted((root / "testdata/golden").glob("**/status.json")):
    original = path.read_text()
    updated, count = re.subn(
        r'("(?:builtWithExtractionVersion|currentExtractionVersion)"\s*:\s*)\d+',
        lambda match: match.group(1) + version,
        original,
    )
    if count not in (0, 2):
        raise SystemExit(f"unexpected version fields in {path}: {count}")
    if updated != original:
        path.write_text(updated)
        changed += 1

print(f"Rebaselined extraction version {version} in {changed} status goldens")
