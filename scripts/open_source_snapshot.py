#!/usr/bin/env python3
"""Prepare a committed source snapshot, never publish or copy Git history."""

import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import subprocess
import tarfile


LOCAL_STATE_ROOTS = frozenset({
    ".git", ".superpowers", ".codegraph", ".code-review-graph", ".codex",
    ".cursor", ".gemini", ".vscode", ".idea", ".gstack", ".claude",
    ".e2e-config", ".tmp", ".mcp.json", "opencode.jsonc",
})
REQUIRED_FILES = ("LICENSE", "UPSTREAM.md", "third_party/termenv/LICENSE", "go.mod", "README.md")
ARCHIVE_NAME = "source.tar"
MANIFEST_NAME = "manifest.json"
SOURCE_DIRECTORY = "source"


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args])


def safe_member(member):
    path = PurePosixPath(member.name)
    if path.is_absolute() or ".." in path.parts or not path.parts or "\\" in member.name or ":" in member.name:
        raise ValueError("unsafe archive path")
    if any(part.casefold() == ".git" for part in path.parts):
        raise ValueError("Git metadata is not source")
    if not (member.isfile() or member.isdir()):
        raise ValueError(f"unsupported archive entry: {member.name}")
    return path


def prepare(repo, source, output):
    revision = git(repo, "rev-parse", "--verify", "--end-of-options",
                   f"{source}^{{commit}}").decode().strip()
    tree = git(repo, "rev-parse", f"{revision}^{{tree}}").decode().strip()
    timestamp = int(git(repo, "show", "-s", "--format=%ct", revision))
    # Archiving the tree, not the commit, omits the commit's global PAX header.
    raw = git(repo, "archive", "--format=tar", tree)
    files = {}
    excluded = []
    archive_bytes = io.BytesIO()
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as original:
        with tarfile.open(fileobj=archive_bytes, mode="w", format=tarfile.PAX_FORMAT) as archive:
            for member in original:
                path = safe_member(member)
                if path.parts[0] in LOCAL_STATE_ROOTS:
                    if member.isfile():
                        excluded.append(member.name)
                    continue
                member.uid = member.gid = 0
                member.uname = member.gname = ""
                member.mtime = timestamp
                member.pax_headers = {}
                member.mode = 0o755 if member.isdir() or member.mode & 0o111 else 0o644
                data = original.extractfile(member).read() if member.isfile() else b""
                archive.addfile(member, io.BytesIO(data) if member.isfile() else None)
                if member.isfile():
                    files[member.name] = {
                        "sha256": hashlib.sha256(data).hexdigest(),
                        "size": len(data),
                        "mode": oct(member.mode),
                    }
    for name in REQUIRED_FILES:
        if name not in files:
            raise ValueError(f"required source file is missing: {name}")
    payload = archive_bytes.getvalue()
    manifest = {
        "status": "PREPARED_NOT_RELEASE_APPROVED",
        "source_commit": revision,
        "source_tree": tree,
        "history_included": False,
        "excluded_files": sorted(excluded),
        "archive_sha256": hashlib.sha256(payload).hexdigest(),
        "files": files,
    }
    # Never replace an earlier candidate or merge with an existing directory.
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    (output / ARCHIVE_NAME).write_bytes(payload)
    destination = output / SOURCE_DIRECTORY
    destination.mkdir(mode=0o700)
    with tarfile.open(fileobj=io.BytesIO(payload), mode="r:") as archive:
        for member in archive:
            path = safe_member(member)
            target = destination.joinpath(*path.parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(archive.extractfile(member).read())
                target.chmod(member.mode)
                if hashlib.sha256(target.read_bytes()).hexdigest() != files[member.name]["sha256"]:
                    raise ValueError("snapshot readback mismatch")
    (output / MANIFEST_NAME).write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path("."))
    parser.add_argument("--source", required=True, help="Committed source revision")
    parser.add_argument("--output", type=Path, required=True, help="New private output directory")
    args = parser.parse_args()
    manifest = prepare(args.repo, args.source, args.output)
    print(json.dumps({key: manifest[key] for key in (
        "status", "source_commit", "source_tree", "history_included", "archive_sha256"
    )}, indent=2))
    print(f"Files: {len(manifest['files'])}; excluded: {len(manifest['excluded_files'])}")


if __name__ == "__main__":
    main()
