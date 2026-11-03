#!/usr/bin/env python3
"""Regression checks for the public Computer Use documentation surface."""

from __future__ import annotations

from pathlib import Path
import subprocess
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
PUBLIC_DOCS = (
    ROOT / "README.md",
    ROOT / "README.en.md",
    ROOT / "docs/README.md",
    ROOT / "desktop-v2/README.md",
    ROOT / "docs/usage/computer-use.md",
    ROOT / "docs/plans/2026-09-27-computer-use-product-functions.md",
)
REMOVED_HISTORICAL_DOCS = (
    ROOT / "docs/plans/2026-09-26-computer-use-acceptance.md",
    ROOT / "docs/plans/2026-10-04-computer-use-autonomous-loop-p0.md",
    ROOT / "docs/reports/computer-use-acceptance-20260926.md",
)
STALE_STATUS_MARKERS = (
    "final model closure remains separate and unrun",
    "Remains open; no replay or further model retry while deterministic work is unfinished",
    "但不能据此称为全部功能稳定版验收通过",
    "审查快照：**2026-09-24** · 代码基线：`fdc3497`",
    "Snapshot: **2026-09-24** · Code baseline: `fdc3497`",
    "Computer Use row updated: **2026-09-30**",
)
PRIVATE_PATH_MARKERS = (
    "/Users/",
    "/private/var/",
    "Bearer ",
)
LOCAL_USER_PATH = "/Users/" + "konglong"
LOCAL_EVIDENCE_PATH = "/Users/Shared/" + "niuma"


class PublicComputerUseDocsTest(unittest.TestCase):
    def test_current_public_entry_points_exist(self) -> None:
        for path in PUBLIC_DOCS:
            self.assertTrue(path.is_file(), f"missing public documentation entry point: {path}")

        guide = (ROOT / "docs/usage/computer-use.md").read_text(encoding="utf-8")
        for marker in (
            "Developer Preview",
            "macOS",
            "single-display",
            "Developer ID signing",
            "second physical display",
        ):
            self.assertIn(marker, guide, f"Computer Use guide is missing scope marker: {marker}")

    def test_public_docs_do_not_publish_stale_completion_status(self) -> None:
        for path in PUBLIC_DOCS:
            content = path.read_text(encoding="utf-8")
            for marker in STALE_STATUS_MARKERS:
                self.assertNotIn(marker, content, f"stale status in {path}: {marker}")

    def test_public_reader_docs_do_not_contain_private_local_details(self) -> None:
        for path in PUBLIC_DOCS:
            content = path.read_text(encoding="utf-8")
            for marker in PRIVATE_PATH_MARKERS:
                self.assertNotIn(marker, content, f"private detail in {path}: {marker}")

    def test_superseded_acceptance_documents_are_not_active(self) -> None:
        for path in REMOVED_HISTORICAL_DOCS:
            self.assertFalse(path.exists(), f"superseded historical document remains active: {path}")


    def test_public_docs_use_the_current_cli_entrypoint(self) -> None:
        paths = [ROOT / "README.md", ROOT / "README.en.md", ROOT / "docs/getting-started.md"]
        paths.extend((ROOT / "docs/usage").glob("*.md"))
        for path in paths:
            content = path.read_text(encoding="utf-8", errors="replace")
            for entry in re.findall(r"go run\s+(\./cmd/[a-zA-Z0-9_-]+)", content):
                self.assertTrue((ROOT / entry / "main.go").is_file(), f"unknown CLI entry {entry} in {path}")
            self.assertNotIn("github.com/konglong87/niuma/cmd/", content, f"old module path in {path}")

    def test_english_readme_has_current_computer_use_scope(self) -> None:
        content = (ROOT / "README.en.md").read_text(encoding="utf-8")
        for marker in ("macOS single-display Developer Preview", "second physical display", "formal signing"):
            self.assertIn(marker, content, f"README.en.md is missing current Computer Use scope: {marker}")

    def test_tracked_text_does_not_expose_local_user_paths(self) -> None:
        suffixes = {".md", ".json", ".yaml", ".yml", ".go", ".ts", ".tsx", ".js", ".mjs", ".py", ".sh", ".swift"}
        tracked = subprocess.check_output(["git", "ls-files"], cwd=ROOT, text=True).splitlines()
        for relative in tracked:
            path = ROOT / relative
            if path == Path(__file__) or path.suffix.lower() not in suffixes:
                continue
            content = path.read_text(encoding="utf-8", errors="replace")
            self.assertNotIn(LOCAL_USER_PATH, content, f"local user path in tracked text: {path}")
            self.assertNotIn(LOCAL_EVIDENCE_PATH, content, f"local evidence path in tracked text: {path}")


if __name__ == "__main__":
    unittest.main()
