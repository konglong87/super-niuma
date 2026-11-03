#!/usr/bin/env python3
"""Closure gate 软规则真实触发率统计。

口径定义见 docs/superpowers/plans/2026-07-16-gate-trigger-rate-measurement.md：
- 分子：pre-tool 软规则拦截事件（pre_commit_scope / shared_state_git_push /
  shared_state_git_tag）。新版事件按 content.rule_id 识别；旧版事件无
  rule_id/action 字段，按 reason 文本兜底（Pre-Commit Scope Gate /
  Shared-State Git Gate）。
- 分母：Bash tool_call 中的受保护操作尝试（git commit / git push /
  git tag 创建，排除只读 tag 查询）。
- 排除：项目 slug 含 niuma-agent-eval 的脚本工作区。

用法：
  scripts/gate-trigger-rate.py [--root ~/.niuma/projects] [--since 2026-07-10] [--json]
"""

import argparse
import json
import os
import re
import sys
from collections import defaultdict

SOFT_RULES = {"pre_commit_scope", "shared_state_git_push", "shared_state_git_tag"}
REASON_RULE_MARKERS = {
    "Pre-Commit Scope Gate": "pre_commit_scope",
    "Shared-State Git Gate": "shared_state_git_push_or_tag",
}
EXCLUDE_SLUG_MARKER = "niuma-agent-eval"

COMMIT_RE = re.compile(r"\bgit\s+commit\b")
PUSH_RE = re.compile(r"\bgit\s+push\b")
TAG_MUTATE_RE = re.compile(r"\bgit\s+tag\s+(?!--list|-l\b|--sort|--contains)\S")


def classify_gate_event(content: dict):
    """返回软规则名，非软规则 pre-tool 事件返回 None。"""
    rule = content.get("rule_id", "")
    if rule:
        return rule if rule in SOFT_RULES else None
    reason = content.get("reason", "")
    for marker, rule_name in REASON_RULE_MARKERS.items():
        if marker in reason:
            return rule_name
    return None


def classify_attempt(command: str):
    lower = command.lower()
    if COMMIT_RE.search(lower):
        return "git_commit"
    if PUSH_RE.search(lower):
        return "git_push"
    if TAG_MUTATE_RE.search(lower):
        return "git_tag_mutate"
    return None


def scan(root: str, since: str):
    # 结构：{(day, slug): {"gates": {rule: n}, "attempts": {kind: n}}}
    stats = defaultdict(lambda: {"gates": defaultdict(int), "attempts": defaultdict(int)})
    files = errors = 0
    for dirpath, _, filenames in os.walk(root):
        slug = os.path.relpath(dirpath, root)
        if EXCLUDE_SLUG_MARKER in slug:
            continue
        for name in filenames:
            if not name.endswith(".jsonl"):
                continue
            files += 1
            path = os.path.join(dirpath, name)
            try:
                with open(path, encoding="utf-8") as fh:
                    for line in fh:
                        line = line.strip()
                        if not line or '"type":"' not in line:
                            continue
                        if '"completion_gate"' not in line and '"tool_call"' not in line:
                            continue
                        try:
                            entry = json.loads(line)
                        except json.JSONDecodeError:
                            errors += 1
                            continue
                        day = (entry.get("timestamp") or "")[:10]
                        if since and day and day < since:
                            continue
                        etype = entry.get("type")
                        if etype == "completion_gate":
                            try:
                                content = json.loads(entry.get("content") or "{}")
                            except json.JSONDecodeError:
                                errors += 1
                                continue
                            rule = classify_gate_event(content)
                            if rule:
                                stats[(day, slug)]["gates"][rule] += 1
                        elif etype == "tool_call" and entry.get("tool_name", "") in ("Bash", ""):
                            try:
                                content = json.loads(entry.get("content") or "{}")
                            except json.JSONDecodeError:
                                continue
                            command = content.get("command", "")
                            kind = classify_attempt(command) if command else None
                            if kind:
                                stats[(day, slug)]["attempts"][kind] += 1
            except OSError:
                errors += 1
    return stats, files, errors


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--root", default=os.path.expanduser("~/.niuma/projects"))
    parser.add_argument("--since", default="", help="只统计该日期（含）之后，如 2026-07-10")
    parser.add_argument("--json", action="store_true", dest="as_json")
    args = parser.parse_args()

    if not os.path.isdir(args.root):
        print(f"store root not found: {args.root}", file=sys.stderr)
        return 1
    stats, files, errors = scan(args.root, args.since)

    total_gates = defaultdict(int)
    total_attempts = defaultdict(int)
    rows = []
    for (day, slug), item in sorted(stats.items()):
        gates = sum(item["gates"].values())
        attempts = sum(item["attempts"].values())
        if gates == 0 and attempts == 0:
            continue
        for rule, n in item["gates"].items():
            total_gates[rule] += n
        for kind, n in item["attempts"].items():
            total_attempts[kind] += n
        rows.append({
            "day": day,
            "project": slug,
            "gate_events": dict(item["gates"]),
            "protected_attempts": dict(item["attempts"]),
        })

    gates_sum = sum(total_gates.values())
    attempts_sum = sum(total_attempts.values())
    summary = {
        "files_scanned": files,
        "parse_errors": errors,
        "since": args.since or None,
        "soft_rule_gate_events": dict(total_gates),
        "soft_rule_gate_total": gates_sum,
        "protected_attempts": dict(total_attempts),
        "protected_attempt_total": attempts_sum,
        "trigger_rate": round(gates_sum / attempts_sum, 4) if attempts_sum else None,
        "rows": rows,
    }
    if args.as_json:
        json.dump(summary, sys.stdout, ensure_ascii=False, indent=2)
        print()
        return 0

    print(f"scanned {files} transcripts ({errors} parse errors), since={args.since or 'beginning'}")
    print(f"soft-rule gate events: {gates_sum}  {dict(total_gates)}")
    print(f"protected attempts:    {attempts_sum}  {dict(total_attempts)}")
    if attempts_sum:
        print(f"trigger rate:          {gates_sum / attempts_sum:.1%}")
    else:
        print("trigger rate:          n/a (no protected attempts found)")
    print()
    print(f"{'day':<12} {'gates':>5} {'attempts':>8}  project")
    for row in rows:
        print(f"{row['day']:<12} {sum(row['gate_events'].values()):>5} {sum(row['protected_attempts'].values()):>8}  {row['project']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
