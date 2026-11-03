#!/usr/bin/env python3
"""Real Wails inputs against a native fixture with independent window mutations.

The fixture's explicit --control channel only mutates its own windows. It is
fault injection, not evidence that the product/model moved or closed a window.
Every tested screenshot and click goes through niuma's Controller/helper.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import time
import uuid

SPEC = importlib.util.spec_from_file_location('driver', Path(__file__).with_name('computer-acceptance.py'))
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)
FIXTURE_BUNDLE = 'com.niuma.validation.window-fixture'
WAIT_SECONDS = 4
POLL_SECONDS = 0.05
OPERATIONS = ('move', 'resize', 'minimize', 'close')


def wait_for(read, predicate):
    deadline = time.monotonic() + WAIT_SECONDS
    while time.monotonic() < deadline:
        value = read()
        if predicate(value):
            return value
        time.sleep(POLL_SECONDS)
    raise RuntimeError('fixture condition timed out')


def click_point(observation, window):
    geometry = observation['capabilities']['coordinate_space']
    bounds, button = geometry['bounds'], window['buttonScreenFrame']
    scale = geometry['scale_factor']
    return {'x': round((button['x'] + button['width'] / 2 - bounds['x']) * scale),
            'y': round((button['y'] + button['height'] / 2 - bounds['y']) * scale)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture', type=Path, required=True)
    parser.add_argument('--control', type=Path, required=True)
    parser.add_argument('--socket', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    driver = DRIVER.Driver(args.socket, args.output)
    report = {'kind': 'scripted-Wails-native-window-lifecycle', 'passed': False, 'cases': []}
    process_id = None

    def state():
        value = json.loads(args.fixture.read_text())
        if value['coordinateSystem'] != 'CG-global-top-left-points':
            raise RuntimeError('unexpected fixture coordinate system')
        if process_id is not None and value['processID'] != process_id:
            raise RuntimeError('fixture process changed; refusing further input')
        return value

    def window(key):
        return next(w for w in state()['windows'] if w['key'] == key)

    def counts():
        return {w['key']: w['clickCount'] for w in state()['windows']}

    def mutate(operation):
        request = {'id': str(uuid.uuid4()), 'window': 'A', 'operation': operation}
        pending = args.control.with_suffix('.pending')
        pending.write_text(json.dumps(request))
        pending.replace(args.control)
        value = wait_for(state, lambda s: (s.get('controlResult') or {}).get('id') == request['id'])
        if not value['controlResult']['success']:
            raise RuntimeError('fixture mutation failed')
        return value

    def observe(key, label):
        target = window(key)
        caps = driver.require(driver.call('capabilities'))
        native = next(w for w in caps['windows'] if w['id'] == str(target['windowNumber']))
        if native.get('owner_pid') != process_id or native.get('bundle_id') != FIXTURE_BUNDLE:
            raise RuntimeError('target not owned by isolated fixture')
        obs = driver.observe(label, window_id=native['id'])
        if obs.get('active_window', {}).get('id') != native['id']:
            raise RuntimeError('wrong active window')
        return obs, window(key)

    def positive_click(key, label):
        obs, target = observe(key, label + '-fresh')
        expected = counts()
        expected[key] += 1
        receipt = driver.execute({'kind': 'click', 'window_id': obs['window_id'],
                                  'point': click_point(obs, target)}, obs['id'], label)
        if receipt.get('outcome') != 'executed':
            raise RuntimeError('fresh click did not execute; no replay')
        wait_for(counts, lambda actual: actual == expected)
        driver.observe(label + '-verified', window_id=obs['window_id'])
        return {'counts': expected, 'receipt': receipt}

    try:
        process_id = state()['processID']
        if {w['key'] for w in state()['windows']} != {'A', 'B'}:
            raise RuntimeError('start a fresh two-window fixture')
        for operation in OPERATIONS:
            case = {'operation': operation, 'passed': False}
            report['cases'].append(case)
            driver.start()
            obs, target = observe('A', operation + '-before')
            expected = counts()
            changed = mutate(operation)
            if operation in ('move', 'resize'):
                if window('A')['frame'] == target['frame']:
                    raise RuntimeError('geometry mutation had no effect')
            elif operation == 'minimize':
                wait_for(state, lambda s: next(w for w in s['windows'] if w['key'] == 'A')['minimized'])
            else:
                if any(w['key'] == 'A' for w in changed['windows']):
                    raise RuntimeError('close mutation had no effect')
                del expected['A']
            # Do not observe again before testing the stale image's input gate.
            denied = driver.call('execute', session_id=driver.session, action={
                'id': str(uuid.uuid4()), 'session_id': driver.session, 'observation_id': obs['id'],
                'window_id': obs['window_id'], 'kind': 'click', 'point': click_point(obs, target)})
            case['stale_input'] = denied
            if not denied.get('error') or denied.get('data', {}).get('outcome') != 'rejected':
                raise RuntimeError('stale window click was not rejected')
            time.sleep(0.15)
            if counts() != expected:
                raise RuntimeError('rejected click changed native counters')
            if operation in ('minimize', 'close'):
                missing = driver.call('observe', session_id=driver.session, window_id=obs['window_id'])
                case['unavailable_observe'] = missing
                if not missing.get('error'):
                    raise RuntimeError('unavailable window was observed or silently restored')
                driver.observe(operation + '-remaining-B', window_id=str(window('B')['windowNumber']))
                if operation == 'minimize':
                    if not window('A')['minimized']:
                        raise RuntimeError('observe unexpectedly unminimized window')
                    mutate('restore')
                    wait_for(state, lambda s: not next(w for w in s['windows'] if w['key'] == 'A')['minimized'])
            # Every pre-dispatch stale-input rejection pauses the session,
            # including position/size-only geometry changes. Resume explicitly
            # before the fresh recovery observation; never replay the rejected
            # action or reuse its screenshot.
            driver.require(driver.call('capabilities'))
            driver.control('resume')
            case['recovery'] = positive_click('B' if operation == 'close' else 'A', operation + '-recovered')
            driver.control('stop')
            case['passed'] = True
        report['passed'] = True
    except Exception as error:
        report['error'] = str(error) or type(error).__name__
    finally:
        if driver.session:
            try:
                report['stop_confirmed'] = not driver.call('stop', session_id=driver.session).get('error')
            except Exception as error:
                report['stop_error'] = str(error)
        report['passed'] = report['passed'] and report.get('stop_confirmed', False)
        (args.output / 'results.json').write_text(json.dumps(report, indent=2))
    print(json.dumps({'passed': report['passed'], 'cases': [
        {'operation': c['operation'], 'passed': c['passed']} for c in report['cases']],
        'error': report.get('error')}, indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
