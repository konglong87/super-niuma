#!/usr/bin/env python3
"""Opt-in real Wails drag interruption tests, confined to WindowFixture.

Optional --crash-host-pid adds SIGKILL of that exact host's fixture helper.
External AXRaise/SIGKILL are fault injection, not model input. All drag events
originate from niuma's Controller; only the isolated fixture receives input.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import importlib.util
import json
import os
import signal
from pathlib import Path
import subprocess
import time
import uuid

SPEC = importlib.util.spec_from_file_location('native_driver', Path(__file__).with_name('computer-acceptance.py'))
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)
FIXTURE_BUNDLE = 'com.niuma.validation.window-fixture'
INTERRUPTED_DURATION_MS = 4000
POLL_INTERVAL = 0.01
EVENT_TIMEOUT = 3


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--socket', type=Path, required=True)
    parser.add_argument('--fixture', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--crash-host-pid', type=int, help='Opt in to killing this verified niuma host’s helper, never the host')
    args = parser.parse_args()
    driver = DRIVER.Driver(args.socket, args.output)
    report = {'kind': 'native-scripted-drag-interruption', 'cases': [], 'passed': False}

    def state():
        return json.loads(args.fixture.read_text())

    def window(key='A'):
        return next(w for w in state()['windows'] if w['key'] == key)

    def wait_for(predicate):
        until = time.monotonic() + EVENT_TIMEOUT
        while time.monotonic() < until:
            value = state()
            if predicate(value):
                return value
            time.sleep(POLL_INTERVAL)
        raise RuntimeError('fixture event/state timeout; do not replay input')

    def raise_fixture_b():
        snapshot = state()
        b = next(w for w in snapshot['windows'] if w['key'] == 'B')
        x, y = round(b['frame']['x']), round(b['frame']['y'])
        script = f'''tell application "System Events"
set p to first application process whose unix id is {snapshot['processID']}
tell p
repeat with w in windows
if position of w is {{{x}, {y}}} then
perform action "AXRaise" of w
return "raised-fixture-B"
end if
end repeat
end tell
error "fixture B not found"
end tell'''
        return subprocess.check_output(['osascript', '-e', script], text=True).strip()

    def kill_fixture_helper(window_id, label):
        root = Path(__file__).resolve().parents[1]
        app = root / 'desktop-v2/build/bin/niuma.app/Contents'
        host_path = str(app / 'MacOS/niuma-desktop')
        helper_path = str(app / 'Helpers/ComputerHelper.app/Contents/MacOS/computer-helper-macos')
        def processes():
            rows = subprocess.check_output(['ps', '-axo', 'pid=,ppid=,comm='], text=True).splitlines()
            return [row.strip().split(None, 2) for row in rows if len(row.strip().split(None, 2)) == 3]
        rows = processes()
        if not any(int(pid) == args.crash_host_pid and path == host_path for pid, _, path in rows):
            raise RuntimeError('refusing SIGKILL: host PID/path mismatch')
        matches = [int(pid) for pid, parent, path in rows if int(parent) == args.crash_host_pid and path == helper_path]
        if len(matches) != 1:
            raise RuntimeError('refusing SIGKILL: helper identity ambiguous')
        pid = matches[0]
        subprocess.run(['screencapture', '-x', '-l', window_id, str(args.output / (label + '-held.png'))], check=True)
        # Recheck immediately before the destructive fault injection.
        if not any(int(p) == pid and int(parent) == args.crash_host_pid and path == helper_path for p, parent, path in processes()):
            raise RuntimeError('helper changed before SIGKILL')
        os.kill(pid, signal.SIGKILL)
        return {'helper_pid': pid, 'host_pid': args.crash_host_pid, 'signal': 'SIGKILL'}

    cases = [('normal-left', 'left', None), ('normal-right', 'right', None), ('pause', 'left', 'pause'), ('pause-right', 'right', 'pause'), ('stop', 'left', 'stop'), ('stop-right', 'right', 'stop'), ('focus', 'left', 'focus'), ('focus-right', 'right', 'focus')]
    if args.crash_host_pid:
        cases += [('crash-left', 'left', 'crash'), ('crash-right', 'right', 'crash')]
    try:
        for name, button, interruption in cases:
            case = {'name': name, 'button': button, 'passed': False}
            report['cases'].append(case)
            driver.start()
            target = window()
            caps = driver.require(driver.call('capabilities'))
            native = next(w for w in caps['windows'] if w.get('id') == str(target['windowNumber']))
            if native.get('bundle_id') != FIXTURE_BUNDLE or native.get('owner_pid') != state()['processID']:
                raise RuntimeError('refusing input outside test fixture')
            obs = driver.observe(name + '-before', window_id=native['id'])
            bounds = obs['capabilities']['coordinate_space']['bounds']
            area, scale = target['dragAreaScreenFrame'], obs['scale_factor']
            def point(fraction):
                return {'x': round((area['x'] + area['width'] * fraction - bounds['x']) * scale),
                        'y': round((area['y'] + area['height'] / 2 - bounds['y']) * scale)}
            baseline = target['dragEvents'][-1]['seq'] if target['dragEvents'] else 0
            action = {'id': str(uuid.uuid4()), 'session_id': driver.session, 'observation_id': obs['id'],
                      'window_id': native['id'], 'kind': 'drag', 'button': button,
                      'start_point': point(.2), 'point': point(.8),
                      'duration_ms': 500 if name.startswith('normal') else INTERRUPTED_DURATION_MS}
            with ThreadPoolExecutor(max_workers=1) as pool:
                future = pool.submit(driver.call, 'execute', session_id=driver.session, action=action)
                if not name.startswith('normal'):
                    held = wait_for(lambda s: any(w['key'] == 'A' and w['pressed'] and w['dragEvents'][-1]['seq'] > baseline for w in s['windows']))
                    case['held_mask'] = held['pressedMouseButtons']
                    if not held['pressedMouseButtons']:
                        raise RuntimeError('no OS-held mouse button observed')
                    began = time.monotonic()
                    if interruption == 'crash':
                        case['injection'] = kill_fixture_helper(native['id'], name)
                    elif interruption == 'focus':
                        case['injection'] = raise_fixture_b()
                    else:
                        control = driver.call(interruption, session_id=driver.session)
                        case['control'] = control
                        if control.get('error'):
                            raise RuntimeError('control not acknowledged')
                    case['control_seconds'] = time.monotonic() - began
                response = future.result(timeout=12)
            case['receipt'] = response
            released = wait_for(lambda s: s['pressedMouseButtons'] == 0 and all(not w['pressed'] for w in s['windows']))
            target = next(w for w in released['windows'] if w['key'] == 'A')
            events = [e for e in target['dragEvents'] if e['seq'] > baseline]
            case.update(events=events, final_mask=released['pressedMouseButtons'], drop_completed=target['dropCompleted'])
            if not events or events[0]['type'] != 'mouseDown' or events[-1]['type'] != 'mouseUp':
                raise RuntimeError('missing real down/up event pair')
            if sum(e['type'] == 'mouseDown' for e in events) != 1 or sum(e['type'] == 'mouseUp' for e in events) != 1:
                raise RuntimeError('duplicate press/release or replay detected')
            if any(abs(events[-1][axis] - events[-2][axis]) > .5 for axis in ['x', 'y']):
                raise RuntimeError('release did not use the last posted CG point')
            if name.startswith('normal'):
                if response.get('error') or response.get('data', {}).get('outcome') != 'executed' or not target['dropCompleted']:
                    raise RuntimeError('normal drag failed')
            else:
                if response.get('data', {}).get('outcome') != 'unknown':
                    raise RuntimeError('interrupted drag lost uncertain receipt')
            if interruption == 'pause':
                driver.control('resume')
                fresh = driver.observe(name + '-resumed', window_id=native['id'])
                case['fresh_observation'] = fresh['id']
                if fresh['id'] == obs['id']:
                    raise RuntimeError('Resume reused old observation')
            # Allow the fixture's next paint after the observed native mouseUp.
            time.sleep(0.1)
            # Independent native screenshot does not replace the old observation.
            subprocess.run(['screencapture', '-x', '-l', native['id'], str(args.output / (name + '-after.png'))], check=True)
            if interruption == 'crash':
                denied = driver.call('execute', session_id=driver.session, action=dict(action, id=str(uuid.uuid4())))
                case['old_action_denied'] = denied
                if not denied.get('error') or denied.get('data', {}).get('outcome') != 'rejected':
                    raise RuntimeError('dead helper/session accepted old action')
                # A dead helper cannot ACK Stop. Record its explicit failure,
                # then require a new host-owned session and fresh screenshot.
                case['dead_helper_stop'] = driver.call('stop', session_id=driver.session)
                driver.start()
                fresh = driver.observe(name + '-recovery', window_id=native['id'])
                case['recovery_receipt'] = driver.execute({'kind': 'move', 'window_id': native['id'], 'point': point(.2)}, fresh['id'], name + '-recovery')
            stopped = driver.call('stop', session_id=driver.session)
            if stopped.get('error'):
                raise RuntimeError('live Stop not confirmed')
            case['passed'] = True
            (args.output / 'results.json').write_text(json.dumps(report, indent=2))
        report['passed'] = True
    except Exception as error:
        report['error'] = str(error) or type(error).__name__
    finally:
        if driver.session:
            try:
                report['stop_confirmed'] = not driver.call('stop', session_id=driver.session).get('error')
            except Exception as error:
                report['stop_error'] = str(error)
                report['stop_confirmed'] = False
        report['passed'] = report['passed'] and report.get('stop_confirmed', False)
        args.output.mkdir(parents=True, exist_ok=True)
        (args.output / 'results.json').write_text(json.dumps(report, indent=2))
    print(json.dumps({'passed': report['passed'], 'cases': [{k: c.get(k) for k in ['name', 'passed', 'held_mask', 'final_mask', 'control_seconds']} for c in report['cases']], 'error': report.get('error')}, indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
