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
REVIEW = ROOT / "docs/architecture/super-niuma-composition-review.json"
# Narrow composition-only scope. Session/domain/runtime files cannot be waived.
COMPOSITION_FILES = frozenset({
    "desktop-v2/main.go",
    "web/src/v2/WebUIV2App.tsx",
    "web/src/v2/components/SessionSidebar.tsx",
    "web/src/v2/desktopServiceBridge.ts",
})
ALLOWED_NEW_GO_ROOTS = ("cmd/super-niuma/", "internal/super/")


def verify(root=ROOT):
    manifest = json.loads((root / MANIFEST.relative_to(ROOT)).read_text())
    failures = []
    review_path = root / REVIEW.relative_to(ROOT)
    review = json.loads(review_path.read_text()) if review_path.is_file() else {}
    for name, item in review.items():
        if name not in COMPOSITION_FILES:
            failures.append(f"non-composition exception forbidden: {name}")
        if not isinstance(item, dict) or item.get("baseline_sha256") != manifest["files"].get(name) or not item.get("reason") or not item.get("verification"):
            failures.append(f"invalid composition review: {name}")
    for name, expected in manifest["files"].items():
        path = root / name
        if not path.is_file():
            failures.append(f"missing legacy file: {name}")
            continue
        data = path.read_bytes()
        if path.suffix == ".go" or path.name == "go.mod":
            data = data.replace(NEW_MODULE.encode(), OLD_MODULE.encode())
        data = data.rstrip(b"\n") + b"\n"
        actual = hashlib.sha256(data).hexdigest()
        item = review.get(name, {})
        approved = item.get("reviewed_sha256") if name in COMPOSITION_FILES and isinstance(item, dict) else None
        if actual != (approved or expected):
            failures.append(f"legacy behavioral baseline changed: {name}")
    for folder in ("internal", "cmd"):
        for path in (root / folder).rglob("*.go"):
            name = path.relative_to(root).as_posix()
            if name not in manifest["files"] and not name.startswith(ALLOWED_NEW_GO_ROOTS):
                failures.append(f"new Go file outside Super boundary: {name}")
            if OLD_MODULE in path.read_text():
                failures.append(f"old module dependency: {name}")
    module = (root / "go.mod").read_text()
    if not module.startswith(f"module {NEW_MODULE}\n"):
        failures.append("public successor module identity is not independent")
    return failures


def main():
    failures = verify()
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print("PASS: legacy baseline protected; exact reviewed Desktop composition exceptions only")
    return 0


if __name__ == "__main__":
    sys.exit(main())
