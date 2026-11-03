# Project Memory Recall

## Scope

Project memory keeps `MEMORY.md` as the always-loaded index. During code-mode
context assembly, the runtime may additionally recall indexed memory files when
their frontmatter and index hints match the current user prompt.

The recall path is intentionally conservative:

- Only Markdown files linked from `MEMORY.md` are eligible.
- Frontmatter is previewed for at most 12 lines and 25 KiB before scoring,
  stopping at an unindented `---` closing line (trailing spaces/tabs allowed).
  Indented `---` inside YAML block scalars is content, not a closing fence.
  Prefixes such as `---suffix` and `----` are not closing fences.
  Top-level `name` and `description` use
  the existing YAML parser (including folded/quoted scalars); nested fields
  cannot replace them and malformed YAML is not recalled.
- Lexical retrieval uses exact normalized words, snake/camel identifier components,
  and Han bigrams (including two-character Chinese queries). Arbitrary English
  substring matches such as `art` → `artifact` are not accepted.
- Name, description and index label/hook matches have weights 4, 2 and 1.
  Scores below 2 do not preload a body. Index hints cannot make missing or invalid
  frontmatter eligible; one weak index-only word is insufficient.
- Each index link owns only its label and the hook before the next link. HTML
  comments are ignored, just as in the injected index. Multiple aliases for one
  normalized path use their maximum score, never a sum of weak hints. A 6-case
  real-provider follow-up covers these boundaries; see the October acceptance report.
- At most 4 files are recalled.
- Files outside the memory directory, including symlink targets, are rejected.
- Index, preview and body reads share an opened `os.Root`. Final reads remain
  rooted even if a symlink changes after preview. Absolute symlinks resolving
  inside the root remain supported via a rooted relative-path open.
- Each index/body read is bounded before allocation, then limited to 25 KiB /
  200 lines with UTF-8-safe truncation.
- Empty prompts and explicit ignore-memory requests recall no files.
- Existing `MEMORY.md` loading and the model's manual `Read` path remain intact.

## Cost And Safety

Recalled files use the existing memory document byte budgets. Recall adds file
reads and prompt bytes only for matching entries; it does not add model turns
or tool calls. A false match is possible because the matcher is deliberately
keyword-based, so the system prompt still requires current-state verification
before relying on a memory claim.

This is a `B2_MODE` prompt change for code mode. Chat mode and explicit bare
runtime context discovery do not use this path. The existing `MEMORY.md`
index remains loaded even on an ignore-memory request: the deterministic
disable boundary applies only to additional recalled documents. Manual tool
reads retain their existing permission boundaries.

`os.Root` is a path-traversal boundary, not a complete filesystem sandbox:
hard links, mounted filesystems and special files are not excluded by it.
Recall is lexical (words/identifiers/Han bigrams), not semantic retrieval; the native project slug
encoding also is not a collision-free multi-tenant isolation mechanism.
No new shared prompt, gate, tool permission or persistence format is introduced.
Rollback is limited to the memory fix commit and does not remove saved memories.

## Verification

Unit tests cover:

- matching an indexed memory by native `metadata.type` frontmatter;
- ignoring explicit memory-disable requests;
- rejecting `..` traversal;
- rejecting symlinks that resolve outside the memory directory;
- preserving the existing index-only behavior for empty prompts.
- YAML folded/quoted values, nested fields and malformed input;
- in-root relative/absolute links, replaced links and opened-root directory rename;
- oversized bodies, UTF-8, candidate caps, duplicate/missing/unindexed files
  and distinct workspace directories.

`scripts/memory-release-acceptance.py` adds isolated real-provider checks:
actual indexed writes, separate-process cold recall without tools, code/chat/bare
scope, readonly hashes, ignore/missing/unindexed negative paths and byte budgets.
Evidence uses the runtime's logical pre-provider request dump plus the actual
model response, not a claimed HTTP wire capture. See
[the dated acceptance report](../deployment/2026-09-10-dependencies-memory-acceptance.md).

## Coding file acceptance

`TestCodingFileMemoryWriteAndColdProcess` executes the real Write tool using a
code session's memory writable root, including a fresh root under a symlinked
configuration directory. A new process with no transcript verifies index/body
injection, lexical-miss Read fallback, readonly bytes, ignore, chat and other-project
boundaries. Its model transport is a test double: real-model lifecycle acceptance
is a separate gate, never inferred from this test.

The real-provider and native-window acceptance, including an
explicit check that old task titles/transcripts cannot supply the expected answer,
is recorded in [the October acceptance report](../deployment/2026-10-22-coding-file-memory-acceptance.md).
File-memory forgetting does not erase other context sources such as session
histories or runtime task notifications; the report separates that limitation
from file lifecycle correctness.
