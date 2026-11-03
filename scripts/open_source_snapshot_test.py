#!/usr/bin/env python3

import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location(
    "snapshot", Path(__file__).with_name("open_source_snapshot.py"))
snapshot = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(snapshot)


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-q")
        self.git("config", "user.name", "Synthetic Author")
        self.git("config", "user.email", "author@example.com")
        for name in snapshot.REQUIRED_FILES:
            self.write(name, "fixture\n")
        self.write("removed.txt", "synthetic-old-private-marker\n")
        self.commit()
        (self.repo / "removed.txt").unlink()
        self.write(".superpowers/report.md", "local task record\n")
        self.write("source.go", "committed source\n")
        self.write("run.sh", "#!/bin/sh\n")
        (self.repo / "run.sh").chmod(0o755)
        self.revision = self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args]).decode().strip()

    def write(self, name, content):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-qm", "fixture")
        return self.git("rev-parse", "HEAD")

    def test_only_committed_tree_is_exported_with_hashes_and_no_history(self):
        self.write("source.go", "uncommitted private marker\n")
        self.write("untracked.env", "untracked marker\n")
        before = self.git("status", "--porcelain")
        output = self.root / "candidate"
        manifest = snapshot.prepare(self.repo, self.revision, output)
        self.assertEqual(before, self.git("status", "--porcelain"))
        source = output / snapshot.SOURCE_DIRECTORY
        self.assertEqual((source / "source.go").read_text(), "committed source\n")
        for name in (".git", ".superpowers", "removed.txt", "untracked.env"):
            self.assertFalse((source / name).exists())
        self.assertFalse(manifest["history_included"])
        self.assertEqual(manifest["excluded_files"], [".superpowers/report.md"])
        archive = output / snapshot.ARCHIVE_NAME
        raw = archive.read_bytes()
        self.assertNotIn(b"author@example.com", raw)
        self.assertNotIn(b"synthetic-old-private-marker", raw)
        self.assertEqual(hashlib.sha256(raw).hexdigest(), manifest["archive_sha256"])
        self.assertEqual(json.loads((output / snapshot.MANIFEST_NAME).read_text()), manifest)
        with tarfile.open(archive) as tar:
            self.assertEqual(tar.pax_headers, {})
            for member in tar:
                self.assertEqual((member.uid, member.gid, member.uname, member.gname), (0, 0, "", ""))
            self.assertEqual(tar.getmember("run.sh").mode, 0o755)
        for name, entry in manifest["files"].items():
            self.assertEqual(hashlib.sha256((source / name).read_bytes()).hexdigest(), entry["sha256"])

    def test_repeat_is_deterministic_and_existing_output_is_not_overwritten(self):
        first = self.root / "first"
        a = snapshot.prepare(self.repo, self.revision, first)
        b = snapshot.prepare(self.repo, self.revision, self.root / "second")
        self.assertEqual(a["archive_sha256"], b["archive_sha256"])
        with self.assertRaises(FileExistsError):
            snapshot.prepare(self.repo, self.revision, first)

    def test_links_and_missing_license_fail_before_output(self):
        (self.repo / "unsafe").symlink_to(self.root)
        revision = self.commit()
        output = self.root / "blocked"
        with self.assertRaisesRegex(ValueError, "unsupported archive entry"):
            snapshot.prepare(self.repo, revision, output)
        self.assertFalse(output.exists())
        (self.repo / "unsafe").unlink()
        (self.repo / "LICENSE").unlink()
        revision = self.commit()
        with self.assertRaisesRegex(ValueError, "required source file"):
            snapshot.prepare(self.repo, revision, output)
        self.assertFalse(output.exists())

    def test_unsafe_tar_entries_are_rejected(self):
        for name in ("/escape", "../escape", "nested/.git/config", "C:/escape", "..\\escape"):
            member = tarfile.TarInfo(name)
            with self.subTest(name=name), self.assertRaises(ValueError):
                snapshot.safe_member(member)


if __name__ == "__main__":
    unittest.main()
