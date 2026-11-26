#!/usr/bin/env python3
"""Exercise the narrow review escape hatch, never waive domain/runtime changes."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("boundary", Path(__file__).with_name("super-niuma-boundary-test.py"))
boundary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(boundary)


class BoundaryReviewTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.files = {"desktop-v2/main.go": b"package main\n", "internal/session/engine.go": b"package session\n",
                      "docs/docs.go": b"package docs\n",
                      "internal/computerbridge/listener_test.go": b"package computerbridge\n",
                      "internal/computerbridge/other_test.go": b"package computerbridge\n",
                      "internal/anthropic/client.go": b"package anthropic\n",
                      "internal/anthropic/other.go": b"package anthropic\n"}
        for name, data in self.files.items():
            self.write(name, data)
        self.write("go.mod", f"module {boundary.NEW_MODULE}\n".encode())
        self.manifest = {"files": {name: hashlib.sha256(data).hexdigest() for name, data in self.files.items()}}
        self.write_json(boundary.MANIFEST.relative_to(boundary.ROOT), self.manifest)

    def write(self, name, data):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def write_json(self, name, value):
        self.write(name, json.dumps(value).encode())

    def approve(self, name, data):
        self.write(name, data)
        self.write_json(boundary.REVIEW.relative_to(boundary.ROOT), {name: {
            "baseline_sha256": self.manifest["files"][name], "reviewed_sha256": hashlib.sha256(data).hexdigest(),
            "reason": "composition only", "verification": "focused test + native acceptance",
        }})

    def test_unchanged_baseline_passes(self):
        self.assertEqual(boundary.verify(self.root), [])

    def test_exact_composition_review_passes(self):
        self.approve("desktop-v2/main.go", b"package main\n// composition\n")
        self.assertEqual(boundary.verify(self.root), [])

    def test_exact_generated_contract_review_passes(self):
        self.approve("docs/docs.go", b"package docs\n// regenerated\n")
        self.assertEqual(boundary.verify(self.root), [])

    def test_post_review_generated_contract_changes_fail(self):
        self.approve("docs/docs.go", b"package docs\n// regenerated\n")
        self.write("docs/docs.go", b"package docs\n// unreviewed\n")
        self.assertTrue(any("baseline changed: docs/docs.go" in error for error in boundary.verify(self.root)))

    def test_exact_test_fixture_review_passes(self):
        self.approve("internal/computerbridge/listener_test.go", b"package computerbridge\n// short fixture\n")
        self.assertEqual(boundary.verify(self.root), [])

    def test_other_legacy_test_exception_is_forbidden(self):
        self.approve("internal/computerbridge/other_test.go", b"package computerbridge\n// changed\n")
        self.assertTrue(any("forbidden" in error for error in boundary.verify(self.root)))

    def test_post_review_changes_fail(self):
        self.approve("desktop-v2/main.go", b"package main\n// composition\n")
        self.write("desktop-v2/main.go", b"package main\n// unreviewed\n")
        self.assertTrue(boundary.verify(self.root))

    def test_domain_exception_is_forbidden(self):
        self.approve("internal/session/engine.go", b"package session\n// changed behavior\n")
        self.assertTrue(any("forbidden" in error for error in boundary.verify(self.root)))

    def test_new_session_code_is_forbidden(self):
        self.write("internal/session/new.go", b"package session\n")
        self.assertTrue(any("outside Super boundary" in error for error in boundary.verify(self.root)))

    def test_exact_free_model_onboarding_review_passes(self):
        self.approve("internal/anthropic/client.go", b"package anthropic\n// quota fallback\n")
        self.assertEqual(boundary.verify(self.root), [])

    def test_other_runtime_file_in_free_model_package_is_forbidden(self):
        self.approve("internal/anthropic/other.go", b"package anthropic\n// changed\n")
        self.assertTrue(any("forbidden" in error for error in boundary.verify(self.root)))

    def test_named_new_go_file_is_allowed(self):
        self.write("internal/server/free_models.go", b"package server\n")
        self.assertEqual(boundary.verify(self.root), [])

    def test_unnamed_new_server_go_file_is_forbidden(self):
        self.write("internal/server/other_new.go", b"package server\n")
        self.assertTrue(any("outside Super boundary" in error for error in boundary.verify(self.root)))

    def test_review_cannot_change_baseline_identity(self):
        self.approve("desktop-v2/main.go", b"package main\n// composition\n")
        path = self.root / boundary.REVIEW.relative_to(boundary.ROOT)
        review = json.loads(path.read_text())
        review["desktop-v2/main.go"]["baseline_sha256"] = "wrong"
        path.write_text(json.dumps(review))
        self.assertTrue(any("invalid composition review" in error for error in boundary.verify(self.root)))


if __name__ == "__main__":
    unittest.main()
