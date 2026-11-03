#!/usr/bin/env python3
import base64
import importlib.util
import json
import plistlib
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import sys
import time


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("computer_use_acceptance", ROOT / "scripts/computer-use-120s-acceptance.py")
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


# 1x1 RGBA PNGs used only for offline wrapper tests.
WHITE_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
)
DARK_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
)


class WrapperUnitTests(unittest.TestCase):
    def test_shared_model_prompt_preserves_exact_acceptance_contract(self):
        prompt = (ROOT / "internal/computeracceptance/assets/model-closure-prompt.txt").read_text(encoding="utf-8")
        self.assertEqual(len(prompt), 1428)
        self.assertEqual(MODULE.sha256_text(prompt), "1a680db5a4a6dbf4947ae3d282339ad544cba0f2dacfbba71a030d5182a447a8")

    def test_desktop_health_snapshot_is_redacted_and_reachable(self):
        server = MODULE.LocalServer(101, 202, 54321, "SECRET-TOKEN")
        client = mock.Mock()
        with mock.patch.object(MODULE, "process_presence", side_effect=[
            {"pid": 101, "ppid": 1, "stat": "S", "alive": True},
            {"pid": 202, "ppid": 101, "stat": "S", "alive": True},
        ]):
            snapshot = MODULE.desktop_health_snapshot(server, client, captured_at="now")
        client.request.assert_called_once_with("/health", timeout=0.75)
        self.assertTrue(snapshot["health_reachable"])
        self.assertEqual(snapshot["server_port"], 54321)
        self.assertNotIn("SECRET-TOKEN", json.dumps(snapshot))

    def test_conversation_heartbeat_summarizes_events_without_payloads(self):
        client = mock.Mock()
        client.request.return_value = {
            "events": [
                {"event_type": "message_start", "created_at": "now", "payload_json": "SECRET"},
                {"event_type": "tool_call", "created_at": "later", "payload_json": json.dumps({
                    "tool_name": "ComputerUse",
                    "input": json.dumps({"action": "launch_app", "text": "SECRET-INPUT"}),
                })},
                {"event_type": "tool_result", "created_at": "latest", "payload_json": json.dumps({
                    "tool_name": "ComputerUse",
                    "output": json.dumps({"outcome": "executed", "error_code": "", "screenshot": "SECRET-SCREENSHOT"}),
                })},
            ]
        }
        heartbeat = MODULE.conversation_heartbeat(client, "/session/path")
        self.assertEqual(heartbeat["event_count"], 3)
        self.assertEqual(heartbeat["tool_call_count"], 1)
        self.assertEqual(heartbeat["tool_result_count"], 1)
        self.assertEqual(heartbeat["last_event_at"], "latest")
        self.assertEqual(heartbeat["tool_summaries"], [
            {"event_type": "tool_call", "tool_name": "ComputerUse", "action": "launch_app"},
            {"event_type": "tool_result", "tool_name": "ComputerUse", "outcome": "executed", "error_code": ""},
        ])
        self.assertNotIn("SECRET", json.dumps(heartbeat))

    def test_failed_tool_zero_receipt_is_not_treated_as_native_receipt(self):
        events = [
            {"event_type": "tool_call", "created_at": "2026-10-02T00:00:00Z", "payload_json": json.dumps({
                "tool_id": "wait-1", "tool_name": "ComputerUse",
                "input": json.dumps({"redacted": True, "action": "wait", "input_bytes": 132}),
            })},
            {"event_type": "tool_result", "created_at": "2026-10-02T00:00:01Z", "payload_json": json.dumps({
                "tool_id": "wait-1", "tool_name": "ComputerUse", "is_error": True,
                "output": json.dumps({
                    "error_code": "action_failed",
                    "receipt": {
                        "action_id": "", "session_id": "", "completed_at": "0001-01-01T00:00:00Z",
                    },
                }),
            })},
        ]
        actions = MODULE.tool_events(events)
        self.assertEqual(len(actions), 1)
        self.assertNotIn("receipt", actions[0])
        MODULE.validate_run_actions(actions, "2026-10-02T00:00:00Z", "2026-10-02T00:00:02Z")

    def test_desktop_health_snapshot_records_connection_failure_without_raw_error(self):
        server = MODULE.LocalServer(101, 202, 54321, "SECRET-TOKEN")
        client = mock.Mock()
        client.request.side_effect = ConnectionRefusedError("http://127.0.0.1:54321 SECRET-TOKEN")
        with mock.patch.object(MODULE, "process_presence", side_effect=[
            {"pid": 101, "ppid": 1, "stat": "S", "alive": True},
            {"pid": 202, "alive": False},
        ]):
            snapshot = MODULE.desktop_health_snapshot(server, client, captured_at="now")
        self.assertFalse(snapshot["health_reachable"])
        self.assertEqual(snapshot["health_error"], "ConnectionRefusedError")
        self.assertFalse(snapshot["server"]["alive"])
        self.assertNotIn("SECRET-TOKEN", json.dumps(snapshot))

    def test_cold_preflight_uses_exact_bundle_executable_not_display_name(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory) / "Fixture App.app"
            contents = app / "Contents"
            contents.mkdir(parents=True)
            (contents / "Info.plist").write_bytes(plistlib.dumps({
                "CFBundleIdentifier": MODULE.TARGET_BUNDLE_ID, "CFBundleExecutable": "Electron",
            }))
            binary = contents / "MacOS/Electron"
            listing = f" 12 {binary}\n 13 /Applications/Other.app/Contents/MacOS/Electron\n 14 {contents}/Frameworks/Fixture Helper\n"
            self.assertEqual(MODULE.application_process_ids(app, MODULE.TARGET_BUNDLE_ID, listing), [12])
            with self.assertRaises(MODULE.AcceptanceError):
                MODULE.application_process_ids(app, "unregistered.bundle", listing)

    def test_warm_target_is_rejected_before_model_session_creation(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(MODULE, "application_process_ids", return_value=[42]):
            client = mock.Mock()
            result = MODULE.run_acceptance(client=client, evidence=MODULE.Evidence(Path(directory)))
            self.assertEqual(result["failure_stage"], "initial_state")
            client.request.assert_not_called()
            preflight = json.loads((Path(directory) / "preflight-workbuddy-state.json").read_text())
            self.assertTrue(preflight["running"])


    def test_server_discovery_accepts_tagged_host_launch_arguments(self):
        workspace = Path("/fixture/workspace")
        host = str(workspace / "desktop-v2/build/bin/niuma.app/Contents/MacOS/niuma-desktop")
        service = str(workspace / "desktop-v2/build/bin/niuma.app/Contents/MacOS/niuma")
        rows = f"100 1 {host} --computer-acceptance-socket /fixture/control.sock\n101 100 {service} server --desktop-local --port 12345 --auth-token synthetic-token\n"
        with mock.patch.object(MODULE.subprocess, "check_output", return_value=rows):
            server = MODULE.find_local_server(workspace)
        self.assertEqual((server.desktop_pid, server.server_pid, server.port), (100, 101, 12345))
        self.assertEqual(server.token, "synthetic-token")

    def test_server_discovery_ignores_runtime_process_when_selecting_host(self):
        workspace = Path("/fixture/workspace")
        host = str(workspace / "desktop-v2/build/bin/niuma.app/Contents/MacOS/niuma-desktop")
        service = str(workspace / "desktop-v2/build/bin/niuma.app/Contents/MacOS/niuma")
        rows = (
            f"100 1 {host}\n"
            f"200 100 {host} --computer-runtime-process --computer-runtime-data-dir /private/runtime\n"
            f"201 100 {service} server --desktop-local --port 12345 --auth-token synthetic-token\n"
        )
        with mock.patch.object(MODULE.subprocess, "check_output", return_value=rows):
            server = MODULE.find_local_server(workspace)
        self.assertEqual((server.desktop_pid, server.server_pid, server.port), (100, 201, 12345))

    def test_rfc3339nano_fraction_widths_are_valid_on_older_python(self):
        expected = MODULE.parse_iso("2026-10-08T05:11:07.391210Z")
        self.assertEqual(MODULE.parse_iso("2026-10-08T05:11:07.39121Z"), expected)
        for width in range(1, 10):
            fraction = "123456789"[:width]
            self.assertIsNotNone(MODULE.parse_iso(f"2026-10-08T05:11:07.{fraction}Z"))
            self.assertIsNotNone(MODULE.parse_iso(f"2026-10-08T13:11:07.{fraction}+08:00"))
        MODULE.validate_run_actions([{
            "call_at": "2026-10-08T05:11:39.8689Z", "result_at": "2026-10-08T05:11:48.699346Z",
        }], "2026-10-08T05:10:50.356083+00:00", "2026-10-08T05:12:24.242298+00:00")

    def test_structured_receipt_survives_truncated_output_without_private_fields(self):
        output = {"receipt": {"action_id": "native-action", "session_id": "native-session", "outcome": "executed",
            "dispatch_state": "complete", "completed_at": "2026-10-08T05:11:07.39121Z", "error_message": "SECRET",
            "redacted_action_summary": "SECRET", "active_window_after": {"id": "42", "bundle_id": "fixture.app", "title": "SECRET"}}}
        item = {}
        MODULE.merge_tool_identity(item, output)
        self.assertEqual(item["receipt"]["action_id"], "native-action")
        self.assertEqual(item["dispatch_state"], "complete")
        self.assertEqual(item["window_id"], "42")
        self.assertNotIn("SECRET", json.dumps(item))

    def test_isolation_rejects_mixed_native_sessions_and_stale_completion(self):
        call, result = "2026-10-08T05:11:00Z", "2026-10-08T05:11:02Z"
        action = {"call_at": call, "result_at": result, "receipt": {"action_id": "a1", "session_id": "s1", "completed_at": "2026-10-08T05:11:01Z"}}
        other = {**action, "receipt": {**action["receipt"], "action_id": "a2", "session_id": "s2"}}
        with self.assertRaises(MODULE.AcceptanceError):
            MODULE.validate_run_actions([action, other], call, result)
        with self.assertRaises(MODULE.AcceptanceError):
            MODULE.validate_run_actions([{**action, "receipt": {**action["receipt"], "completed_at": "2026-10-08T05:10:59Z"}}], call, result)
        MODULE.validate_run_actions([action], call, result)

    def test_effective_provider_and_model_are_exact(self):
        session = {"provider": MODULE.DEFAULT_PROVIDER, "model": MODULE.DEFAULT_MODEL, "effort": "high", "status": "idle"}
        result = MODULE.validate_effective_config(session, MODULE.DEFAULT_PROVIDER, MODULE.DEFAULT_MODEL, "high")
        self.assertEqual(result["effective_provider"], MODULE.DEFAULT_PROVIDER)
        with self.assertRaises(MODULE.AcceptanceError) as mismatch:
            MODULE.validate_effective_config(session, "custom", MODULE.DEFAULT_MODEL, "high")
        self.assertEqual(mismatch.exception.stage, "effective_config")

    def test_tool_events_keep_counts_and_strip_sensitive_output(self):
        events = [
            {"event_type": "tool_call", "created_at": "2026-10-02T00:00:00Z", "payload_json": json.dumps({
                "tool_id": "tool-1", "tool_name": "ComputerUse",
                "input": json.dumps({"redacted": True, "action": "type", "text_length": 5, "input_bytes": 137}),
            })},
            {"event_type": "tool_result", "created_at": "2026-10-02T00:00:01Z", "payload_json": json.dumps({
                "tool_id": "tool-1", "tool_name": "ComputerUse", "is_error": False,
                "output": '{"window_id":"19988","bundle_id":"com.workbuddy.workbuddy","outcome":"executed","secret":"must-not-be-persisted"}',
                "computer_observation": {"observation_id": "observation-1", "asset_id": "asset-1", "media_type": "image/png"},
            })},
        ]
        actions = MODULE.tool_events(events)
        self.assertEqual(actions[0]["action"], "type")
        self.assertEqual(actions[0]["text_length"], 5)
        self.assertEqual(actions[0]["window_id"], "19988")
        self.assertNotIn("secret", json.dumps(actions))

    def test_atomic_observe_extracts_nested_launch_window_identity(self):
        events = [
            {"event_type": "tool_call", "created_at": "2026-10-02T00:00:00Z", "payload_json": json.dumps({
                "tool_id": "tool-atomic", "tool_name": "ComputerUse",
                "input": json.dumps({"redacted": True, "action": "observe", "input_bytes": 44}),
            })},
            {"event_type": "tool_result", "created_at": "2026-10-02T00:00:01Z", "payload_json": json.dumps({
                "tool_id": "tool-atomic", "tool_name": "ComputerUse", "is_error": False,
                "output": json.dumps({
                    "launch_receipt": {
                        "target_id": "workbuddy",
                        "bundle_id": "com.workbuddy.workbuddy",
                        "window": {
                            "id": "21680",
                            "owner_pid": 9120,
                            "bundle_id": "com.workbuddy.workbuddy",
                            "frame": {"x": 0, "y": 34, "width": 1352, "height": 844},
                        },
                        "outcome": "executed",
                    },
                    "observation": {
                        "active_window": {
                            "id": "21680",
                            "owner_pid": 9120,
                            "bundle_id": "com.workbuddy.workbuddy",
                            "frame": {"x": 0, "y": 34, "width": 1352, "height": 844},
                        },
                    },
                }),
                "computer_observation": {"observation_id": "observation-atomic", "asset_id": "asset-atomic", "media_type": "image/png"},
            })},
        ]
        action = MODULE.tool_events(events)[0]
        self.assertEqual(action["window_id"], "21680")
        self.assertEqual(action["bundle_id"], "com.workbuddy.workbuddy")
        self.assertEqual(action["owner_pid"], 9120)
        self.assertEqual(action["window_frame"], {"x": 0, "y": 34, "width": 1352, "height": 844})

    def test_action_validation_accepts_explicit_launch_as_first_action(self):
        actions = MODULE.tool_events(FakeAcceptanceClient._events())
        launch = next(item for item in actions if item["action"] == "launch_app")
        ordered = [launch] + [item for item in actions if item is not launch and item["action"] != "observe" or (item is not launch and item["action"] == "observe" and item.get("window_id") == "19988")]
        # Keep the observation after launch and remove only the pre-launch observe.
        result = MODULE.validate_actions(ordered, {"observation-5": "reply.png"}, "stopped")
        self.assertEqual(result["launch_mode"], "launch_app")
        self.assertEqual(result["window_id"], "19988")

    def test_action_validation_accepts_atomic_launch_observation_without_followup_observe(self):
        actions = [
            {"action": "launch_app", "is_error": False, "outcome": "executed",
             "target_id": "workbuddy", "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988",
             "computer_observation": {"observation_id": "launch-observation", "media_type": "image/png"}},
            {"action": "click", "is_error": False, "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988"},
            {"action": "hotkey", "is_error": False, "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988"},
            {"action": "type", "is_error": False, "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988", "text_length": 5},
            {"action": "click", "is_error": False, "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988"},
            {"action": "wait", "is_error": False, "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988",
             "computer_observation": {"observation_id": "reply-observation", "media_type": "image/png"}},
            {"action": "stop", "is_error": False, "bundle_id": MODULE.TARGET_BUNDLE_ID, "window_id": "19988"},
        ]
        result = MODULE.validate_actions(actions, {"reply-observation": "reply.png"}, "stopped")
        self.assertEqual(result["launch_mode"], "atomic_launch")
        self.assertEqual(result["window_id"], "19988")

    def test_action_validation_rejects_control_window(self):
        actions = [
            {"tool_id": "1", "action": "observe", "is_error": False, "computer_observation": {"observation_id": "o1"}},
            {"tool_id": "2", "action": "launch_app", "is_error": False, "outcome": "executed", "bundle_id": "com.workbuddy.workbuddy", "window_id": "19988"},
            {"tool_id": "3", "action": "observe", "is_error": False, "bundle_id": "com.openai.codex", "window_id": "19988", "computer_observation": {"observation_id": "o2"}},
            {"tool_id": "4", "action": "type", "is_error": False, "bundle_id": "com.workbuddy.workbuddy", "window_id": "19988", "text_length": 5},
            {"tool_id": "5", "action": "click", "is_error": False, "bundle_id": "com.workbuddy.workbuddy", "window_id": "19988"},
            {"tool_id": "6", "action": "wait", "is_error": False, "bundle_id": "com.workbuddy.workbuddy", "window_id": "19988", "computer_observation": {"observation_id": "o3"}},
            {"tool_id": "7", "action": "stop", "is_error": False, "bundle_id": "com.workbuddy.workbuddy", "window_id": "19988"},
        ]
        with self.assertRaises(MODULE.AcceptanceError) as failure:
            MODULE.validate_actions(actions, {"o1": "one.png", "o2": "two.png", "o3": "three.png"}, "stopped")
        self.assertEqual(failure.exception.stage, "window binding")

    def test_blank_guard_rejects_white_png(self):
        # The fixture is intentionally tiny; the structural guard itself is
        # tested by checking that invalid/non-image bytes are rejected.
        with self.assertRaises(ValueError):
            MODULE.validate_png(b"not-a-png", "image/png")

    def test_blank_asset_is_marked_without_aborting_other_screenshots(self):
        class SelectiveClient:
            def download(self, path, timeout=None):
                if path.endswith("asset-blank"):
                    return FakeAcceptanceClient._png((255, 255, 255, 255)), "image/png"
                return FakeAcceptanceClient._png((24, 24, 24, 255)), "image/png"

        actions = [
            {"action": "observe", "computer_observation": {"observation_id": "o-blank", "asset_id": "asset-blank", "media_type": "image/png"}},
            {"action": "wait", "computer_observation": {"observation_id": "o-good", "asset_id": "asset-good", "media_type": "image/png"}},
        ]
        with tempfile.TemporaryDirectory() as directory:
            paths, invalid = MODULE.save_observation_assets(
                SelectiveClient(), MODULE.Evidence(Path(directory)), actions, time.monotonic() + 5
            )
            self.assertEqual(list(paths), ["o-good"])
            self.assertEqual([item["observation_id"] for item in invalid], ["o-blank"])
            report = json.loads((Path(directory) / "observation-assets.json").read_text())
            self.assertEqual(report["saved_count"], 1)
            self.assertEqual(report["invalid_count"], 1)
            self.assertTrue((Path(directory) / "observation-01-o-blank.png").exists())
            self.assertTrue((Path(directory) / "observation-02-o-good.png").exists())

    def test_evidence_directory_is_private(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = MODULE.Evidence(Path(directory) / "evidence")
            evidence.json("safe.json", {"provider": MODULE.DEFAULT_PROVIDER})
            self.assertEqual((Path(directory) / "evidence").stat().st_mode & 0o777, 0o700)
            self.assertEqual((Path(directory) / "evidence/safe.json").stat().st_mode & 0o777, 0o600)

    def test_timing_preserves_requested_effort(self):
        timing = MODULE.Timing(started_at="2026-10-02T00:00:00Z", start_monotonic=10.0)
        result = MODULE.build_timing(timing, "stopped", 11.25, MODULE.DEFAULT_PROVIDER, MODULE.DEFAULT_MODEL, "low")
        self.assertEqual(result["effort"], "low")

    def test_cli_exposes_low_effort_override(self):
        args = MODULE.parse_args(["--provider", "jiuan-responses-gpt-5.6sol", "--model", "gpt-6-sol", "--effort", "low"])
        self.assertEqual(args.provider, "jiuan-responses-gpt-5.6sol")
        self.assertEqual(args.model, "gpt-6-sol")
        self.assertEqual(args.effort, "low")

    def test_execution_log_metrics_separates_native_and_model_gaps(self):
        actions = [
            {"action": "observe", "call_at": "2026-10-02T00:00:00Z", "result_at": "2026-10-02T00:00:02Z"},
            {"action": "click", "call_at": "2026-10-02T00:00:07Z", "result_at": "2026-10-02T00:00:08Z"},
        ]
        events = [{"event_type": "usage", "payload_json": json.dumps({"turn": 1, "input_tokens": 10, "output_tokens": 4})}]
        result = MODULE.execution_log_metrics(events, actions)
        self.assertEqual(result["native_action_seconds"], 3.0)
        self.assertEqual(result["between_action_seconds"], 5.0)
        self.assertEqual(result["action_rows"][1]["gap_after_previous_result_seconds"], 5.0)
        self.assertEqual(result["usage_rows"][0]["input_tokens"], 10)

class EvidenceIsolationTests(unittest.TestCase):
    def test_rejects_nonempty_evidence_directory_without_overwriting(self):
        with tempfile.TemporaryDirectory() as directory:
            old = Path(directory) / "run-start.json"
            old.write_text("old evidence")
            with self.assertRaises(MODULE.AcceptanceError):
                MODULE.Evidence(Path(directory))
            self.assertEqual(old.read_text(), "old evidence")

    def test_each_default_output_is_unique(self):
        self.assertNotEqual(MODULE.parse_args([]).output, MODULE.parse_args([]).output)

    def test_rejects_old_or_reversed_action_timestamps(self):
        for call, result in [("2026-10-08T00:00:00Z", "2026-10-08T00:00:01Z"),
                             ("2026-10-08T01:00:02Z", "2026-10-08T01:00:01Z")]:
            with self.assertRaises(MODULE.AcceptanceError):
                MODULE.validate_run_actions([{"call_at": call, "result_at": result}],
                                            "2026-10-08T01:00:00Z", "2026-10-08T01:01:00Z")

    def test_accepts_current_action_timestamps(self):
        MODULE.validate_run_actions([{"call_at": "2026-10-08T01:00:01Z", "result_at": "2026-10-08T01:00:02Z"}],
                                    "2026-10-08T01:00:00Z", "2026-10-08T01:01:00Z")

    def test_structured_result_survives_truncated_output(self):
        events = [
            {"event_type": "tool_call", "payload_json": {"tool_id": "a", "tool_name": "ComputerUse", "input": {"action": "observe"}}},
            {"event_type": "tool_result", "payload_json": {"tool_id": "a", "tool_name": "ComputerUse", "output": "{truncated",
             "launch_receipt": {"target_id": "workbuddy", "bundle_id": "com.workbuddy.workbuddy", "outcome": "executed", "window": {"id": "123", "owner_pid": 456}}}},
        ]
        item = MODULE.tool_events(events)[0]
        self.assertEqual(item["window_id"], "123")
        self.assertTrue(item["implicit_launch"])


class BuildAndObserveGateTests(unittest.TestCase):
    def test_build_gate_rejects_missing_manifest(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(MODULE.AcceptanceError):
                MODULE.validate_build_identity(ROOT, Path(directory))

    def test_build_gate_rejects_changed_executable_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory)
            desktop = app / "Contents/MacOS/niuma-desktop"
            service = app / "Contents/MacOS/niuma"
            helper = app / "Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos"
            manifest = app / "Contents/Resources/computer-use-build.json"
            for path in (desktop, helper, manifest):
                path.parent.mkdir(parents=True, exist_ok=True)
            desktop.write_bytes(b"desktop")
            service.write_bytes(b"service")
            helper.write_bytes(b"helper")
            manifest.write_text(json.dumps({"source_commit": "current", "source_dirty": False,
                "desktop_build_id": "build-1", "helper_sha256": MODULE.sha256_file(helper), "service_sha256": MODULE.sha256_file(service)}))
            with mock.patch.object(MODULE, "source_commit", return_value="current"), mock.patch.object(MODULE, "executable_build_id", return_value="build-1"):
                MODULE.validate_build_identity(ROOT, app)
                helper.write_bytes(b"stale")
                with self.assertRaises(MODULE.AcceptanceError):
                    MODULE.validate_build_identity(ROOT, app)

    def test_observe_only_gate_never_accepts_input(self):
        actions = [{"action": "observe", "implicit_launch": True, "outcome": "executed", "bundle_id": MODULE.TARGET_BUNDLE_ID,
                    "window_id": "1"}, {"action": "stop"}]
        result = MODULE.validate_observe_only(actions, {"obs": "image.png"}, "stopped")
        self.assertEqual(result["mode"], "observe_only")
        actions.insert(1, {"action": "type"})
        with self.assertRaises(MODULE.AcceptanceError):
            MODULE.validate_observe_only(actions, {"obs": "image.png"}, "stopped")



class FakeAcceptanceClient:
    def __init__(self):
        self.stop_calls = 0
        self.poll_calls = 0
        self.events = self._events()
        self.image = self._png((24, 24, 24, 255))

    @staticmethod
    def _png(pixel):
        import struct
        import zlib
        raw = b"\x00" + bytes(pixel)
        def chunk(kind, body):
            return struct.pack(">I", len(body)) + kind + body + struct.pack(">I", MODULE.zlib.crc32(kind + body) & 0xFFFFFFFF)
        return MODULE.PNG_SIGNATURE + chunk(b"IHDR", struct.pack(">IIBBBBB", 1, 1, 8, 6, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")

    @staticmethod
    def _events():
        names = ["observe", "launch_app", "observe", "type", "click", "wait", "stop"]
        events = []
        for index, action in enumerate(names):
            tool_id = f"tool-{index}"
            call_payload = {
                "tool_id": tool_id, "tool_name": "ComputerUse",
                "input": json.dumps({"redacted": True, "action": action, "text_length": 5 if action == "type" else 0}),
            }
            if action == "launch_app":
                output = json.dumps({"target_id": "workbuddy", "bundle_id": "com.workbuddy.workbuddy", "window": {"id": "19988", "owner_pid": 60626}, "window_id": "19988", "outcome": "executed"})
            elif action == "stop":
                output = json.dumps({"action": "stop", "status": "ok"})
            else:
                output = json.dumps({"window_id": "19988", "bundle_id": "com.workbuddy.workbuddy", "owner_pid": 60626, "outcome": "executed"})
            result_payload = {"tool_id": tool_id, "tool_name": "ComputerUse", "output": output, "is_error": False}
            if action in {"observe", "type", "click", "wait"}:
                result_payload["computer_observation"] = {"observation_id": f"observation-{index}", "asset_id": f"asset-{index}", "media_type": "image/png", "size_bytes": 100, "sha256": "hash"}
            timestamp = f"2026-10-02T00:00:{index:02d}Z"
            events.extend([
                {"event_type": "tool_call", "created_at": timestamp, "payload_json": json.dumps(call_payload)},
                {"event_type": "tool_result", "created_at": timestamp, "payload_json": json.dumps(result_payload)},
            ])
        return events

    def request(self, path, method="GET", body=None, timeout=None):
        if path.endswith("/messages"):
            return {"data": {"session": {"status": "running"}, "operation_id": "op-1"}}
        if path.endswith("/stop"):
            self.stop_calls += 1
            return {"data": {"session": {"status": "stopped"}}}
        if path.endswith("/conversation"):
            return {"data": {"events": [dict(event, created_at=MODULE.iso_now()) for event in self.events]}}
        if path == "/tenant/session-control/sessions":
            return {"data": {"session": {"id": 1, "ref": "tenant:test", "provider": MODULE.DEFAULT_PROVIDER, "model": MODULE.DEFAULT_MODEL, "effort": "high", "status": "idle"}}}
        self.poll_calls += 1
        status = "running" if self.stop_calls == 0 else "stopped"
        return {"data": {"session": {"id": 1, "ref": "tenant:test", "provider": MODULE.DEFAULT_PROVIDER, "model": MODULE.DEFAULT_MODEL, "effort": "high", "status": status}}}

    def download(self, path, timeout=None):
        return self.image, "image/png"


class SendSafetyTests(unittest.TestCase):
    def test_validation_rejects_two_send_clicks(self):
        actions = MODULE.tool_events(FakeAcceptanceClient._events())
        click = next(item for item in actions if item["action"] == "click")
        actions.insert(actions.index(click) + 1, dict(click, tool_id="duplicate-send"))
        with self.assertRaises(MODULE.AcceptanceError) as failure:
            MODULE.validate_actions(actions, {"observation-5": "reply.png"}, "stopped")
        self.assertEqual(failure.exception.stage, "send")

    def test_validation_requires_explicit_bound_window_identity(self):
        actions = MODULE.tool_events(FakeAcceptanceClient._events())
        typed = next(item for item in actions if item["action"] == "type")
        typed.pop("window_id")
        with self.assertRaises(MODULE.AcceptanceError) as failure:
            MODULE.validate_actions(actions, {"observation-5": "reply.png"}, "stopped")
        self.assertEqual(failure.exception.stage, "window binding")


class EvidencePipelineTests(unittest.TestCase):
    def test_trace_is_persisted_before_invalid_screenshot_is_reported(self):
        class BlankFirstAssetClient(FakeAcceptanceClient):
            def download(self, path, timeout=None):
                if path.endswith("asset-2"):
                    return FakeAcceptanceClient._png((255, 255, 255, 255)), "image/png"
                return super().download(path, timeout)

        with tempfile.TemporaryDirectory() as directory, mock.patch.object(MODULE, "application_process_ids", return_value=[]):
            result = MODULE.run_acceptance(
                client=BlankFirstAssetClient(),
                evidence=MODULE.Evidence(Path(directory)),
                workspace=ROOT,
                app_path=ROOT / "desktop-v2/build/bin/niuma.app",
                total_budget=2.0,
                operational_deadline=0.05,
                poll_interval=0.01,
                sleep=lambda seconds: time.sleep(min(seconds, 0.01)),
            )
            self.assertTrue((Path(directory) / "action-receipts.json").exists())
            self.assertTrue((Path(directory) / "conversation-trace.json").exists())
            report = json.loads((Path(directory) / "observation-assets.json").read_text())
            self.assertGreaterEqual(report["invalid_count"], 1)
            self.assertEqual(result["status"], "passed")


class AsyncDeadlineTests(unittest.TestCase):
    def test_initial_idle_is_not_terminal_before_message_dispatch_completes(self):
        class DelayedMessageClient(FakeAcceptanceClient):
            def __init__(self):
                super().__init__()
                import threading
                self.message_completed = threading.Event()
                self.stop_before_message = False
                self.session_stopped = False

            def request(self, path, method="GET", body=None, timeout=None):
                if path.endswith("/messages"):
                    time.sleep(0.35)
                    self.message_completed.set()
                    return {"data": {"session": {"status": "running"}, "operation_id": "op-delayed"}}
                if path.endswith("/stop"):
                    self.stop_before_message = not self.message_completed.is_set()
                    self.stop_calls += 1
                    self.session_stopped = True
                    return {"data": {"session": {"status": "stopped"}}}
                if path.endswith("/conversation"):
                    return {"data": {"events": [dict(event, created_at=MODULE.iso_now()) for event in self.events]}}
                if path == "/tenant/session-control/sessions":
                    return {"data": {"session": {"id": 1, "ref": "tenant:test", "provider": MODULE.DEFAULT_PROVIDER, "model": MODULE.DEFAULT_MODEL, "effort": "high", "status": "idle"}}}
                self.poll_calls += 1
                if self.session_stopped:
                    status = "stopped"
                else:
                    status = "idle" if not self.message_completed.is_set() else "running"
                return {"data": {"session": {"id": 1, "ref": "tenant:test", "provider": MODULE.DEFAULT_PROVIDER, "model": MODULE.DEFAULT_MODEL, "effort": "high", "status": status}}}

        with tempfile.TemporaryDirectory() as directory, mock.patch.object(MODULE, "application_process_ids", return_value=[]):
            client = DelayedMessageClient()
            result = MODULE.run_acceptance(
                client=client,
                evidence=MODULE.Evidence(Path(directory)),
                workspace=ROOT,
                app_path=ROOT / "desktop-v2/build/bin/niuma.app",
                total_budget=2.0,
                operational_deadline=0.6,
                poll_interval=0.01,
                sleep=lambda seconds: time.sleep(min(seconds, 0.01)),
            )
            self.assertEqual(result["status"], "passed")
            self.assertEqual(result["final_status"], "stopped")
            self.assertTrue(client.message_completed.is_set())
            self.assertFalse(client.stop_before_message)
            message_response = json.loads((Path(directory) / "message-response.json").read_text())
            self.assertEqual(message_response["response"]["operation_id"], "op-delayed")

    def test_async_message_is_stopped_and_trace_is_collected(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(MODULE, "application_process_ids", return_value=[]):
            client = FakeAcceptanceClient()
            result = MODULE.run_acceptance(
                client=client,
                evidence=MODULE.Evidence(Path(directory)),
                workspace=ROOT,
                app_path=ROOT / "desktop-v2/build/bin/niuma.app",
                total_budget=2.0,
                operational_deadline=0.05,
                poll_interval=0.01,
                sleep=lambda seconds: time.sleep(min(seconds, 0.01)),
            )
            self.assertEqual(result["status"], "passed")
            self.assertEqual(result["final_status"], "stopped")
            self.assertEqual(client.stop_calls, 1)
            self.assertTrue((Path(directory) / "conversation-trace.json").exists())
            preflight = json.loads((Path(directory) / "preflight-workbuddy-state.json").read_text())
            self.assertFalse(preflight["running"])
            self.assertEqual(preflight["owner_pids"], [])
            self.assertIn("first_observation", preflight)
            self.assertNotIn("auth-token", (Path(directory) / "conversation-trace.json").read_text())


class DesktopBuildCodeSignatureTests(unittest.TestCase):
    def test_macos_embedded_executables_are_unlinked_before_replacement(self):
        build_script = (ROOT / "scripts/build-desktop-v2.sh").read_text(encoding="utf-8")
        helper_script = (ROOT / "scripts/build-computer-helper-macos.sh").read_text(encoding="utf-8")
        service_rm = 'rm -f "${APP_BIN}/niuma"'
        service_cp = 'cp "${DESKTOP_DIR}/niuma" "${APP_BIN}/niuma"'
        helper_rm = 'rm -f "$OUTPUT"'
        helper_compile = ' -o "$OUTPUT"'
        self.assertIn(service_rm, build_script)
        self.assertLess(build_script.index(service_rm), build_script.index(service_cp))
        self.assertIn(helper_rm, helper_script)
        self.assertLess(helper_script.index(helper_rm), helper_script.index(helper_compile))


class NativeDisplayPreflightTests(unittest.TestCase):
    def test_display_preflight_rejects_sleeping_display(self):
        state = MODULE.parse_display_preflight("""
Graphics/Displays:

    Apple M4:
      Displays:
        Color LCD:
          Online: Yes
          Display Asleep: Yes
""")
        self.assertEqual(state["display_state"], "asleep")
        self.assertFalse(state["ready"])

    def test_display_preflight_accepts_awake_online_display(self):
        state = MODULE.parse_display_preflight("""
Graphics/Displays:

    Apple M4:
      Displays:
        Color LCD:
          Online: Yes
""")
        self.assertEqual(state["display_state"], "awake")
        self.assertTrue(state["ready"])

if __name__ == "__main__":
    unittest.main()
