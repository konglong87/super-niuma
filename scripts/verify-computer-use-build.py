#!/usr/bin/env python3
"""Verify a reproducible Computer Use desktop build manifest.

The verifier checks only public build identity metadata and executable hashes. It
never reads credentials, desktop content, screenshots, provider requests, or
runtime logs.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
from typing import Any


class BuildVerificationError(RuntimeError):
    pass


def sha256_file(path: Path) -> str:
    if not path.is_file():
        raise BuildVerificationError(f"missing build file: {path}")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def source_commit(workspace: Path) -> str:
    try:
        return subprocess.check_output(
            ["git", "-C", str(workspace), "rev-parse", "HEAD"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise BuildVerificationError("workspace is not a readable git checkout") from exc


def git_status(workspace: Path) -> list[str]:
    try:
        output = subprocess.check_output(
            ["git", "-C", str(workspace), "status", "--porcelain=v1", "--untracked-files=all"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        raise BuildVerificationError("cannot inspect workspace status") from exc
    return [line for line in output.splitlines() if line.strip()]


def executable_build_id(path: Path) -> str:
    try:
        return subprocess.check_output(
            ["go", "tool", "buildid", str(path)],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise BuildVerificationError("cannot read desktop executable build id") from exc


def verify_signature(app: Path) -> None:
    try:
        subprocess.run(
            ["codesign", "--verify", "--deep", "--strict", str(app)],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        raise BuildVerificationError("desktop app signature verification failed") from exc


def verify_build(
    app: Path,
    workspace: Path,
    *,
    require_clean: bool = False,
    require_release_candidate: bool = False,
    require_signature: bool = False,
) -> dict[str, Any]:
    app = app.resolve()
    workspace = workspace.resolve()
    manifest_path = app / "Contents/Resources/computer-use-build.json"
    desktop = app / "Contents/MacOS/niuma-desktop"
    service = app / "Contents/MacOS/niuma"
    helper = app / "Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos"
    if not manifest_path.is_file():
        raise BuildVerificationError(f"missing build manifest: {manifest_path}")
    try:
        manifest = json.loads(manifest_path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise BuildVerificationError("invalid build manifest") from exc
    if not isinstance(manifest, dict):
        raise BuildVerificationError("build manifest must be an object")
    if manifest.get("schema_version") != "computer-use-build.v1":
        raise BuildVerificationError("unsupported build manifest schema")
    if manifest.get("source_commit") != source_commit(workspace):
        raise BuildVerificationError("desktop app source commit does not match workspace HEAD")
    status = git_status(workspace)
    if require_clean and status:
        raise BuildVerificationError("workspace is dirty; release candidate requires a clean checkout")
    if manifest.get("source_dirty") is not False:
        raise BuildVerificationError("build manifest records dirty runtime source")
    if require_clean and manifest.get("web_source_dirty") is not False:
        raise BuildVerificationError("build manifest records dirty web source")
    if require_release_candidate and manifest.get("release_candidate") is not True:
        raise BuildVerificationError("build was not marked as a release candidate")
    for field in ("version", "go_version", "node_version", "wails_version", "target", "signing_mode"):
        if not isinstance(manifest.get(field), str) or not manifest[field].strip():
            raise BuildVerificationError(f"build manifest is missing {field}")
    if manifest.get("desktop_build_id") != executable_build_id(desktop):
        raise BuildVerificationError("desktop executable build id does not match manifest")
    if manifest.get("helper_sha256") != sha256_file(helper):
        raise BuildVerificationError("helper hash does not match manifest")
    if manifest.get("service_sha256") != sha256_file(service):
        raise BuildVerificationError("service hash does not match manifest")
    if require_signature:
        verify_signature(app)
    return {
        "app": str(app),
        "source_commit": manifest["source_commit"],
        "release_candidate": bool(manifest.get("release_candidate")),
        "desktop_build_id": manifest["desktop_build_id"],
        "helper_sha256": manifest["helper_sha256"],
        "service_sha256": manifest["service_sha256"],
        "signature_verified": require_signature,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--workspace", type=Path, default=Path.cwd())
    parser.add_argument("--require-clean", action="store_true")
    parser.add_argument("--require-release-candidate", action="store_true")
    parser.add_argument("--require-signature", action="store_true")
    args = parser.parse_args(argv)
    try:
        result = verify_build(
            args.app,
            args.workspace,
            require_clean=args.require_clean,
            require_release_candidate=args.require_release_candidate,
            require_signature=args.require_signature,
        )
    except BuildVerificationError as exc:
        print(f"build verification failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
