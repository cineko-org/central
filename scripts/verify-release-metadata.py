#!/usr/bin/env python3
"""Validate that Release Please changed every Central version source together."""

import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")


def load_json(relative_path: str) -> dict:
    with (ROOT / relative_path).open(encoding="utf-8") as source:
        return json.load(source)


def main() -> None:
    version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    if not SEMVER.fullmatch(version):
        raise SystemExit(f"VERSION is not strict SemVer: {version!r}")

    manifest = load_json(".release-please-manifest.json")
    package = load_json("frontend/package.json")
    lock = load_json("frontend/package-lock.json")
    observed = {
        "manifest": manifest.get("."),
        "package": package.get("version"),
        "lock": lock.get("version"),
        "lock root": lock.get("packages", {}).get("", {}).get("version"),
    }
    mismatched = {name: value for name, value in observed.items() if value != version}
    if mismatched:
        raise SystemExit(f"release versions do not match VERSION {version}: {mismatched}")

    changelog = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
    if f"## [{version}]" not in changelog:
        raise SystemExit(f"CHANGELOG has no entry for {version}")


if __name__ == "__main__":
    main()
