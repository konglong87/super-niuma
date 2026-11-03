#!/usr/bin/env python3
"""Prove the initial successor keeps legacy code unchanged except module imports and empty EOF lines.

This is a migration regression guard, not a perpetual freeze: later shared-core
changes must explicitly review the baseline and the affected legacy acceptance.
No access to the original/private repository or a developer path is required.
"""
import hashlib
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
OLD_MODULE = "github.com/konglong87/niuma"
NEW_MODULE = "github.com/konglong87/super-niuma"
MANIFEST = ROOT / "docs/architecture/super-niuma-legacy-manifest.json"
ALLOWED_NEW_GO_ROOTS = ("cmd/super-niuma/", "internal/super/")


def main():
    manifest = json.loads(MANIFEST.read_text())
    failures = []
    for name, expected in manifest["files"].items():
        path = ROOT / name
        if not path.is_file():
            failures.append(f"missing legacy file: {name}")
            continue
        data = path.read_bytes()
        if path.suffix == ".go" or path.name == "go.mod":
            data = data.replace(NEW_MODULE.encode(), OLD_MODULE.encode())
        data = data.rstrip(b"\n") + b"\n"
        if hashlib.sha256(data).hexdigest() != expected:
            failures.append(f"legacy behavioral baseline changed: {name}")
    for folder in ("internal", "cmd"):
        for path in (ROOT / folder).rglob("*.go"):
            name = path.relative_to(ROOT).as_posix()
            if name not in manifest["files"] and not name.startswith(ALLOWED_NEW_GO_ROOTS):
                failures.append(f"new Go file outside Super boundary: {name}")
            if OLD_MODULE in path.read_text():
                failures.append(f"old module dependency: {name}")
    module = (ROOT / "go.mod").read_text()
    if not module.startswith(f"module {NEW_MODULE}\n"):
        failures.append("public successor module identity is not independent")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(f"PASS: {len(manifest['files'])} legacy files unchanged after import normalization; Super entry isolated")
    return 0


if __name__ == "__main__":
    sys.exit(main())
