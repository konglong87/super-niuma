#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "computer_use_build_verifier", Path(__file__).with_name("verify-computer-use-build.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ComputerUseBuildVerifierTests(unittest.TestCase):
    def make_app(self, root: Path, source_dirty=False, release_candidate=True):
        app = root / "niuma.app"
        desktop = app / "Contents/MacOS/niuma-desktop"
        service = app / "Contents/MacOS/niuma"
        helper = app / "Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos"
        manifest = app / "Contents/Resources/computer-use-build.json"
        for path in (desktop, service, helper, manifest):
            path.parent.mkdir(parents=True, exist_ok=True)
        desktop.write_bytes(b"desktop")
        service.write_bytes(b"service")
        helper.write_bytes(b"helper")
        manifest.write_text(json.dumps({
            "schema_version": "computer-use-build.v1",
            "source_commit": "abc123",
            "source_dirty": source_dirty,
            "web_source_dirty": False,
            "release_candidate": release_candidate,
            "version": "v0.0.0-rc.1",
            "go_version": "go1.25.0",
            "node_version": "v24.0.0",
            "wails_version": "v2.10.2",
            "target": "darwin/arm64",
            "signing_mode": "adhoc",
            "desktop_build_id": "build-1",
            "helper_sha256": MODULE.sha256_file(helper),
            "service_sha256": MODULE.sha256_file(service),
        }))
        return app

    def test_release_candidate_requires_clean_attested_manifest(self):
        with tempfile.TemporaryDirectory() as directory:
            app = self.make_app(Path(directory))
            with mock.patch.object(MODULE, "source_commit", return_value="abc123"), \
                    mock.patch.object(MODULE, "executable_build_id", return_value="build-1"), \
                    mock.patch.object(MODULE, "git_status", return_value=[]):
                result = MODULE.verify_build(app, Path(directory), require_clean=True, require_release_candidate=True)
                self.assertEqual(result["source_commit"], "abc123")

    def test_release_candidate_rejects_dirty_source(self):
        with tempfile.TemporaryDirectory() as directory:
            app = self.make_app(Path(directory), source_dirty=True)
            with mock.patch.object(MODULE, "source_commit", return_value="abc123"), \
                    mock.patch.object(MODULE, "executable_build_id", return_value="build-1"), \
                    mock.patch.object(MODULE, "git_status", return_value=[" M web/src/App.tsx"]):
                with self.assertRaises(MODULE.BuildVerificationError):
                    MODULE.verify_build(app, Path(directory), require_clean=True, require_release_candidate=True)


if __name__ == "__main__":
    unittest.main()
