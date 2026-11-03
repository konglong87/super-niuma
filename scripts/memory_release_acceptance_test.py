#!/usr/bin/env python3
"""Offline checks for the real-provider memory gate; no settings or API access."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace
import yaml

SPEC = importlib.util.spec_from_file_location(
    "memory_gate", Path(__file__).with_name("memory-release-acceptance.py"))
GATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GATE)


class MemoryGateTests(unittest.TestCase):
    def test_pacing_never_replays_or_delays_first_scenario(self):
        gate = GATE.Gate.__new__(GATE.Gate)
        gate.args = SimpleNamespace(case_interval=65)
        gate.last_completed_at = None
        with patch.object(GATE.time, "monotonic", return_value=110), patch.object(GATE.time, "sleep") as sleep:
            gate.pace()
            sleep.assert_not_called()
            gate.last_completed_at = 100
            gate.pace()
            sleep.assert_called_once_with(55)
            sleep.reset_mock()
            gate.last_completed_at = 40
            gate.pace()
            sleep.assert_not_called()
            gate.args.case_interval = 0
            gate.last_completed_at = 110
            gate.pace()
            sleep.assert_not_called()

    def test_context_includes_runtime_messages_and_system_blocks(self):
        context = GATE.request_context({"request": {
            "system": "SYSTEM",
            "system_blocks": [{"text": "BLOCK"}],
            "messages": [{"content": [{"text": "BODY_ONLY_MEMORY"}]}],
        }})
        for value in ("SYSTEM", "BLOCK", "BODY_ONLY_MEMORY"):
            self.assertIn(value, context)

    def test_saved_memory_requires_body_and_valid_metadata(self):
        code = "SYNTHETIC_CONFIRMATION"
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            index = root / "MEMORY.md"
            memory = root / "deploy.md"
            GATE.write_private(index, "- [deployment](deploy.md)\n")
            valid = "---\nname: deploy\ndescription: >-\n  deployment verification\n" \
                    "metadata:\n  type: project\n---\n" + code
            GATE.write_private(memory, valid)
            self.assertEqual(GATE.validate_saved_memory(root, code), memory.resolve())
            for invalid in (valid.replace("name: deploy", "name: []"),
                            valid.replace("type: project", "type: unknown"),
                            valid.replace("name: deploy", "name: " + code),
                            valid.replace("name: deploy", "name: [invalid"),
                            valid.replace("\n---\n", "\n---not-a-delimiter\n"),
                            valid.replace("\n---\n", "\n----\n")):
                with self.subTest(content=invalid):
                    GATE.write_private(memory, invalid)
                    with self.assertRaises((AssertionError, yaml.YAMLError)):
                        GATE.validate_saved_memory(root, code)
            GATE.write_private(memory, valid)
            GATE.write_private(index, "- [deployment](deploy.md)\n" + code)
            with self.assertRaises(AssertionError):
                GATE.validate_saved_memory(root, code)

    def test_index_cannot_escape_root(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "memory"
            GATE.write_private(root / "MEMORY.md", "- [private](../private.md)\n")
            with self.assertRaisesRegex(AssertionError, "escapes"):
                GATE.validate_saved_memory(root, "SYNTHETIC")

    def test_snapshot_detects_content_and_file_set_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            GATE.write_private(root / "MEMORY.md", "index")
            before = GATE.memory_snapshot(root)
            GATE.write_private(root / "MEMORY.md", "updated")
            self.assertNotEqual(before, GATE.memory_snapshot(root))
            GATE.write_private(root / "MEMORY.md", "index")
            self.assertEqual(before, GATE.memory_snapshot(root))
            GATE.write_private(root / "extra.md", "extra")
            self.assertNotEqual(before, GATE.memory_snapshot(root))

    def test_read_fallback_requires_actual_read_evidence(self):
        code = "SYNTHETIC_BODY"
        result = {"response": code, "tool_calls": [{"name": "Read", "output": code}]}
        GATE.validate_read_fallback(result, "index only", code)
        for invalid, context in ((result, code),
                                 ({"response": code, "tool_calls": []}, "index only"),
                                 ({"response": "unknown", "tool_calls": result["tool_calls"]}, "index only"),
                                 ({"response": code, "tool_calls": result["tool_calls"] + [{"name": "Write"}]}, "index only")):
            with self.subTest(result=invalid, context=context):
                with self.assertRaises(AssertionError):
                    GATE.validate_read_fallback(invalid, context, code)

    def test_forget_checks_unindexed_files_too(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            GATE.write_private(root / "MEMORY.md", "# Memory\n")
            GATE.write_private(root / "unindexed.md", "SYNTHETIC_OLD_FACT")
            with self.assertRaises(AssertionError):
                GATE.validate_forgotten(root, ("SYNTHETIC_OLD_FACT",))
            GATE.write_private(root / "unindexed.md", "no retained fact")
            GATE.validate_forgotten(root, ("SYNTHETIC_OLD_FACT",))


if __name__ == "__main__":
    unittest.main()
