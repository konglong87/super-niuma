---
name: cross-process-root
---

# Cross-process root fixture

```bash
BUNDLE_ROOT="$(pwd)"
```

```bash
total=$(find "$BUNDLE_ROOT/skills" -name SKILL.md | wc -l)
covered=$(find "$BUNDLE_ROOT/skills" -name SKILL.md | wc -l)
printf '%s/%s\n' "$covered" "$total"
test "$covered" -eq "$total"
```

```bash
BUNDLE_ROOT="${NIUMA_SKILL_DIR:?}/fixture"
```
