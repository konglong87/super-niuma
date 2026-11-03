#!/usr/bin/env python3
"""Offline expiry safety checks: fake clocks and drivers, never native input."""
from datetime import datetime, timezone
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import Mock, patch

SPEC = importlib.util.spec_from_file_location(
    "computer_safety", Path(__file__).with_name("computer-acceptance-safety.py"))
SAFETY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SAFETY)
BASE = datetime(2026, 9, 27, tzinfo=timezone.utc).timestamp()


def observation(seconds=30):
    return {"id": "offline-observation", "expires_at": datetime.fromtimestamp(
        BASE + seconds, timezone.utc).isoformat().replace("+00:00", "Z")}


class FakeClock:
    def __init__(self, jump=0, wall_rate=1):
        self.wall = BASE
        self.mono = 0
        self.jump = jump
        self.wall_rate = wall_rate
        self.sleeps = []

    def sleep(self, seconds):
        if not 0 < seconds <= SAFETY.EXPIRY_POLL_SECONDS:
            raise AssertionError(f"unbounded or nonpositive sleep: {seconds}")
        self.sleeps.append(seconds)
        self.mono += seconds
        self.wall += seconds * self.wall_rate
        if len(self.sleeps) == 1:
            self.wall += self.jump
        if len(self.sleeps) > 1000:
            raise AssertionError("wait did not terminate")

    def dependencies(self):
        return dict(wall_clock=lambda: self.wall,
                    monotonic=lambda: self.mono, sleep=self.sleep)


def runner_for(obs):
    # Bypass Driver/FixtureRun constructors: no socket, files, process, or GUI.
    runner = SAFETY.SafetyRun.__new__(SAFETY.SafetyRun)
    runner.d = Mock(session="offline-session")
    runner.d.observe.return_value = obs
    runner.restart = Mock()
    runner.record = Mock()
    runner.denied = Mock(return_value=({"error": "expired"}, True))
    return runner


class ExpirySafetyTests(unittest.TestCase):
    def test_thirty_second_expiry_waits_before_type_dispatch(self):
        clock = FakeClock()
        runner = runner_for(observation())

        def denied(action):
            self.assertGreater(clock.mono, 11)
            self.assertGreaterEqual(clock.mono, 30 + SAFETY.EXPIRY_GUARD_SECONDS)
            self.assertGreaterEqual(clock.wall, BASE + 30 + SAFETY.EXPIRY_GUARD_SECONDS)
            self.assertEqual(action["kind"], "type")
            self.assertEqual(action["text"], SAFETY.DANGEROUS_SENTINEL)
            return {"error": "expired"}, True

        runner.denied.side_effect = denied
        runner.expired(**clock.dependencies())
        runner.denied.assert_called_once()
        runner.record.assert_called_once_with(
            "expired-observation", True, response={"error": "expired"})

    def test_current_two_minute_observation_waits_until_actual_expiry(self):
        clock = FakeClock()
        runner = runner_for(observation(120))
        runner.denied.side_effect = lambda action: (
            {"error": "expired"},
            clock.mono >= 120 + SAFETY.EXPIRY_GUARD_SECONDS,
        )
        runner.expired(**clock.dependencies())
        runner.denied.assert_called_once()
        self.assertGreaterEqual(clock.mono, 120 + SAFETY.EXPIRY_GUARD_SECONDS)
        runner.record.assert_called_once_with(
            "expired-observation", True, response={"error": "expired"})

    def test_already_expired_needs_no_sleep(self):
        clock = FakeClock()
        runner = runner_for(observation(-1))
        runner.expired(**clock.dependencies())
        self.assertEqual(clock.sleeps, [])
        runner.denied.assert_called_once()

    def test_at_expiry_still_waits_for_guard(self):
        clock = FakeClock()
        SAFETY.wait_for_observation_expiry(observation(0), **clock.dependencies())
        self.assertAlmostEqual(clock.mono, SAFETY.EXPIRY_GUARD_SECONDS, places=6)

    def test_rfc3339_offsets_and_fractions(self):
        for timestamp in (
            "2026-09-27T00:00:30.125Z",
            "2026-09-27T08:00:30.125+08:00",
            "2026-09-26T18:30:30.125-05:30",
            "2026-09-27T00:00:30.125+00:00",
            "2026-09-27T00:00:30.125-00:00",
            "2026-09-27t00:00:30.125z",
        ):
            with self.subTest(timestamp=timestamp):
                clock = FakeClock()
                SAFETY.wait_for_observation_expiry(
                    {"expires_at": timestamp}, **clock.dependencies())
                self.assertAlmostEqual(clock.mono, 30.125 + SAFETY.EXPIRY_GUARD_SECONDS, places=6)

    def test_fractional_precision_is_supported_and_rounded_conservatively(self):
        for fraction in ("1", "12", "1234", "12345", "125000001", "999999999"):
            with self.subTest(fraction=fraction):
                clock = FakeClock()
                SAFETY.wait_for_observation_expiry(
                    {"expires_at": "2026-09-27T00:00:30." + fraction + "Z"},
                    **clock.dependencies())
                expected = 30 + float("0." + fraction) + SAFETY.EXPIRY_GUARD_SECONDS
                self.assertGreaterEqual(clock.mono + 1e-7, expected)
                self.assertLess(clock.mono - expected, 2e-6)

    def test_invalid_metadata_fails_before_action_or_sleep(self):
        invalid = [None, {}, {"id": "missing-expiry"}]
        invalid.extend({"expires_at": value} for value in (
            None, "", 123, float("nan"), float("inf"), float("-inf"),
            "NaN", "Infinity", [], {}, "not-a-date",
            "2026-09-27", "2026-09-27T00:00:30",  # naive
            "2026-02-30T00:00:30Z", "2026-09-27T24:00:30Z",
            "2026-09-27 00:00:30Z", "2026-09-27T00:00:30Zgarbage",
            "2026-09-27T00:00:30+24:00", "2026-09-27T00:00:30+00:60",
            "2026-09-27T00:00:30+0800", "2026-09-27T00:00:30+08",
            "9999-12-31T00:00:00Z",
        ))
        invalid.append(observation(SAFETY.MAX_EXPIRY_FUTURE_SECONDS + 1))
        for obs in invalid:
            with self.subTest(observation=obs):
                clock = FakeClock()
                runner = runner_for(obs)
                runner.action = Mock()
                with self.assertRaises(SAFETY.ExpiryWaitError):
                    runner.expired(**clock.dependencies())
                runner.action.assert_not_called()
                runner.denied.assert_not_called()
                runner.d.call.assert_not_called()
                self.assertEqual(clock.sleeps, [])
                self.assertFalse(runner.record.call_args.args[1])

    def test_forward_clock_jump_does_not_shorten_wait(self):
        clock = FakeClock(jump=3600)
        runner = runner_for(observation())
        runner.expired(**clock.dependencies())
        self.assertAlmostEqual(clock.mono, 30 + SAFETY.EXPIRY_GUARD_SECONDS, places=6)
        runner.denied.assert_called_once()

    def test_small_backward_adjustment_waits_until_wall_expiry(self):
        clock = FakeClock(jump=-1)
        runner = runner_for(observation())
        runner.expired(**clock.dependencies())
        self.assertGreaterEqual(clock.wall, BASE + 30 + SAFETY.EXPIRY_GUARD_SECONDS)
        self.assertAlmostEqual(clock.mono, 31 + SAFETY.EXPIRY_GUARD_SECONDS, places=6)
        runner.denied.assert_called_once()

    def test_backward_or_frozen_wall_clock_is_bounded_and_fails_closed(self):
        for clock in (FakeClock(jump=-3600), FakeClock(wall_rate=0), FakeClock(wall_rate=-1)):
            with self.subTest(jump=clock.jump, rate=clock.wall_rate):
                runner = runner_for(observation())
                with self.assertRaisesRegex(SAFETY.ExpiryWaitError, "deadline"):
                    runner.expired(**clock.dependencies())
                self.assertLessEqual(clock.mono, 30 + SAFETY.EXPIRY_GUARD_SECONDS
                                     + SAFETY.CLOCK_ADJUSTMENT_BUDGET_SECONDS + 1e-6)
                runner.denied.assert_not_called()
                self.assertFalse(runner.record.call_args.args[1])

    def test_invalid_or_nonadvancing_clocks_fail_closed(self):
        dependencies = [dict(wall_clock=lambda: BASE, monotonic=lambda: 0, sleep=Mock())]
        for value in (float("nan"), float("inf"), float("-inf")):
            dependencies.append(dict(wall_clock=lambda v=value: v, monotonic=lambda: 0, sleep=Mock()))
            dependencies.append(dict(wall_clock=lambda: BASE, monotonic=lambda v=value: v, sleep=Mock()))
        dependencies.append(dict(wall_clock=lambda: BASE,
                                 monotonic=Mock(side_effect=[0, -1]), sleep=Mock()))
        dependencies.append(dict(wall_clock=Mock(side_effect=[BASE, float("nan")]),
                                 monotonic=Mock(side_effect=[0, 1]), sleep=Mock()))
        for clocks in dependencies:
            with self.subTest(clocks=clocks):
                runner = runner_for(observation())
                with self.assertRaises(SAFETY.ExpiryWaitError):
                    runner.expired(**clocks)
                runner.denied.assert_not_called()

    def test_oversleep_past_deadline_fails_closed(self):
        clock = FakeClock()
        def oversleep(_):
            clock.wall += 100
            clock.mono += 100
        clocks = clock.dependencies()
        clocks["sleep"] = oversleep
        runner = runner_for(observation())
        with self.assertRaisesRegex(SAFETY.ExpiryWaitError, "deadline"):
            runner.expired(**clocks)
        runner.denied.assert_not_called()

    def test_expiry_failure_stops_run_and_cleans_up_session(self):
        runner = runner_for({})
        runner.stale = Mock()
        with self.assertRaises(SAFETY.ExpiryWaitError):
            runner.run(False)
        runner.stale.assert_not_called()
        runner.d.call.assert_called_once_with("stop", session_id="offline-session")

    def test_main_still_fails_for_any_failed_case(self):
        for failed in range(3):
            with self.subTest(failed=failed), patch.object(SAFETY, "SafetyRun") as constructor:
                constructor.return_value.results = [{"passed": i != failed} for i in range(3)]
                with patch("sys.argv", ["computer-acceptance-safety.py"]):
                    with self.assertRaisesRegex(SystemExit, "failing cases"):
                        SAFETY.main()


class CrashScopeTests(unittest.TestCase):
    APP = Path("/isolated build/native-fixture-build/niuma.app")
    OTHER_APP = Path("/normal/build/bin/niuma.app")

    def process_table(self, hosts=(200,), helpers=((201, 200),)):
        def host(app):
            return str(app / "Contents/MacOS/niuma-desktop")
        def helper(app):
            return str(app / "Contents/Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos")
        rows = ["PID PPID COMM",
                f"100 1 {host(self.OTHER_APP)}",
                f"101 100 {helper(self.OTHER_APP)}",
                # Same helper path but wrong parent is never eligible.
                f"999 100 {helper(self.APP)}"]
        rows += [f"{pid} 1 {host(self.APP)}" for pid in hosts]
        rows += [f"{pid} {parent} {helper(self.APP)}" for pid, parent in helpers]
        return "\n".join(rows)

    def test_only_explicit_bundle_and_parent_pid_are_selected(self):
        with patch.object(SAFETY, "require_app_path", return_value=self.APP) as validate:
            self.assertEqual(SAFETY.select_helper_pid(self.APP, self.process_table()), 201)
            validate.assert_called_once_with(self.APP)

    def test_ambiguous_missing_or_invalid_processes_are_rejected(self):
        tables = (
            self.process_table(hosts=()),
            self.process_table(hosts=(200, 202)),
            self.process_table(helpers=()),
            self.process_table(helpers=((201, 200), (202, 200))),
            self.process_table(helpers=((201, 100),)),
            self.process_table(helpers=((-1, 200),)),
        )
        with patch.object(SAFETY, "require_app_path", return_value=self.APP):
            for table in tables:
                with self.subTest(table=table), self.assertRaises(RuntimeError):
                    SAFETY.select_helper_pid(self.APP, table)

    def test_app_path_is_explicit_resolved_and_has_both_executables(self):
        with self.assertRaisesRegex(ValueError, "explicit --app-path"):
            SAFETY.require_app_path(None)
        with patch.object(Path, "resolve", side_effect=FileNotFoundError):
            with self.assertRaises(FileNotFoundError):
                SAFETY.require_app_path(self.APP)
        with patch.object(Path, "resolve", return_value=self.APP) as resolve, \
                patch.object(Path, "is_dir", return_value=True), \
                patch.object(Path, "is_file", return_value=True) as is_file:
            self.assertEqual(SAFETY.require_app_path(self.APP), self.APP)
            resolve.assert_called_once_with(strict=True)
            self.assertEqual(is_file.call_count, 2)
        for app, is_dir, files in ((self.APP.with_suffix(".zip"), True, [True, True]),
                                   (self.APP, False, [True, True]),
                                   (self.APP, True, [False]),
                                   (self.APP, True, [True, False])):
            with self.subTest(app=app, is_dir=is_dir, files=files), \
                    patch.object(Path, "resolve", return_value=app), \
                    patch.object(Path, "is_dir", return_value=is_dir), \
                    patch.object(Path, "is_file", side_effect=files):
                with self.assertRaises(ValueError):
                    SAFETY.require_app_path(app)

    def test_cli_refuses_crash_without_app_before_driver_creation(self):
        with patch("sys.argv", ["safety", "--crash-helper"]), \
                patch.object(SAFETY, "SafetyRun") as constructor, \
                patch("sys.stderr"):
            with self.assertRaises(SystemExit) as error:
                SAFETY.main()
            self.assertEqual(error.exception.code, 2)
            constructor.assert_not_called()

    def test_cli_refuses_nonexistent_app_before_driver_creation(self):
        with patch("sys.argv", ["safety", "--crash-helper", "--app-path", str(self.APP)]), \
                patch.object(Path, "resolve", side_effect=FileNotFoundError("missing app")), \
                patch.object(SAFETY, "SafetyRun") as constructor, patch("sys.stderr"):
            with self.assertRaises(SystemExit) as error:
                SAFETY.main()
            self.assertEqual(error.exception.code, 2)
            constructor.assert_not_called()

    def test_cli_passes_explicit_app_to_runner(self):
        with patch("sys.argv", ["safety", "--crash-helper", "--app-path", str(self.APP)]), \
                patch.object(SAFETY, "require_app_path", return_value=self.APP), \
                patch.object(SAFETY, "SafetyRun") as constructor:
            constructor.return_value.results = [{"passed": True}]
            SAFETY.main()
            self.assertEqual(constructor.call_args.kwargs["app_path"], self.APP)
            constructor.return_value.run.assert_called_once_with(True)

    def test_crash_preflight_rejects_ambiguity_before_any_input(self):
        runner = runner_for(observation())
        runner.app_path = self.APP
        runner.expired = Mock()
        with patch.object(SAFETY, "require_app_path", return_value=self.APP), \
                patch.object(SAFETY.subprocess, "check_output", return_value=self.process_table(hosts=(200, 202))), \
                patch.object(SAFETY.os, "kill") as kill:
            with self.assertRaises(RuntimeError):
                runner.run(True)
            runner.expired.assert_not_called()
            runner.restart.assert_not_called()
            runner.d.call.assert_not_called()
            kill.assert_not_called()

    def test_crash_restarts_helper_after_preceding_stop_case(self):
        runner = runner_for(observation())
        runner.app_path = self.APP
        helper_live = False

        def restart():
            nonlocal helper_live
            helper_live = True

        def processes(*args, **kwargs):
            return self.process_table(helpers=((201, 200),) if helper_live else ())

        runner.restart.side_effect = restart
        runner.d.call.side_effect = [
            {"error": "helper lost", "data": {"outcome": "unknown"}},
            {"error": "helper lost"},
        ]
        with patch.object(SAFETY, "require_app_path", return_value=self.APP), \
                patch.object(SAFETY.subprocess, "check_output", side_effect=processes), \
                patch.object(SAFETY.os, "kill") as kill, \
                patch.object(SAFETY.time, "sleep"):
            runner.crash()
            kill.assert_called_once_with(201, SAFETY.signal.SIGKILL)
            self.assertEqual(runner.restart.call_count, 2)

    def test_crash_targets_only_selected_pid_without_global_kill(self):
        runner = runner_for(observation())
        runner.app_path = self.APP
        runner.d.call.side_effect = [
            {"error": "helper lost", "data": {"outcome": "unknown"}},
            {"error": "helper lost"},
        ]
        with patch.object(SAFETY, "require_app_path", return_value=self.APP), \
                patch.object(SAFETY.subprocess, "check_output", side_effect=[
                    self.process_table(), self.process_table(helpers=((202, 200),))]) as ps, \
                patch.object(SAFETY.subprocess, "run") as run, \
                patch.object(SAFETY.subprocess, "Popen") as popen, \
                patch.object(SAFETY.os, "kill") as kill, \
                patch.object(SAFETY.os, "system") as system, \
                patch.object(SAFETY.time, "sleep"):
            runner.crash()
            kill.assert_called_once_with(202, SAFETY.signal.SIGKILL)
            self.assertEqual(ps.call_count, 2)  # Recheck target immediately before signal.
            for call in ps.call_args_list:
                self.assertEqual(call.args, (["ps", "-axo", "pid,ppid,comm"],))
                self.assertEqual(call.kwargs, {"text": True})
            run.assert_not_called()
            popen.assert_not_called()
            system.assert_not_called()
            self.assertTrue(all(call.args[1] for call in runner.record.call_args_list))


if __name__ == "__main__":
    unittest.main()
