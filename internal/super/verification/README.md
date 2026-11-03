# Read-only Super verification

This package is an A5 foundation, not a scheduler, execution engine, UI verifier,
model judge, or proof that a business objective has been fulfilled. It executes
no commands, repository test scripts, hooks, network requests, or model calls.

## API and authority

- `ValidatePolicy(Policy) error`: requires a bounded, nonempty version and rules;
  rejects policies consisting only of `nonempty` checks.
- `PolicyHash(Policy) string`: canonical SHA-256; **empty string for invalid
  policies**. Commits the policy format, version, effective limits, paths, kinds,
  expected contents and keys. Rule/key ordering is immaterial. Does not mutate
  caller slices. A digest is not a signature or an authorization credential.
- `Verify(context.Context, workspace string, Policy) (Report, error)`: reads
  actual files, calculates hashes, and checks the same immutable byte buffers.
  Never accepts a worker-provided artifact hash or report as evidence.

Only the control plane may author a policy. In particular, the expected SHA-256
must be independently trusted, not copied from a candidate worker manifest.
The caller owns the workspace read lock for the **whole Verify call**, immutable
policy ownership, active authorization, and run/attempt/policy binding. This
package deliberately does not implement these responsibilities.

`Passed` is true only when every rule produced a real successful check. Semantic
or per-file failures return a failed report and nil error. Invalid policy,
invalid/unavailable workspace, unsupported platform and cancellation return a
non-nil error with `Passed=false`. An errored report may contain partial evidence,
never success. `VerifiedAtMS` is observation time, not a durable trusted timestamp.
Reports are observations, not reusable authorization: reverify changed artifacts.

## Assertions and limits

| Kind | Meaning |
| --- | --- |
| `nonempty` | At least one byte. Never sufficient as the only kind of assertion. |
| `text_contains` | Valid UTF-8 contains a nonblank, trusted literal. |
| `json_keys` | Valid UTF-8 JSON with an object root and every required **top-level** key. Rejects duplicate decoded keys at all nesting levels, trailing documents/text, excessive depth and token count. Keys mean presence, not value/schema correctness. |
| `sha256` | Actual bytes match exactly 64 hex digits supplied by the trusted policy. Hex is case insensitive; prefixes/whitespace are rejected. |

Limits are exported constants: 32 rules, 1 MiB default per file, 8 MiB maximum
per file, 32 MiB sum of effective rule budgets, 64 JSON keys per rule, 64 nesting
levels and 100,000 JSON tokens per file. `MaxBytes=0` selects the default; negative
or excessive limits are invalid. Paths must be unique canonical relative slash
paths; duplicate case-folded paths are rejected conservatively across filesystems.
No normalization silently repairs `..`, absolute paths or separator aliases.

## Filesystem safety and limitations

- Uses `os.OpenRoot` / `os.Root` directory handles. Every workspace and artifact
  directory component is checked with Lstat; **all symlinks are rejected**,
  including symlinks that stay inside the workspace. Pass a canonical absolute
  workspace; macOS `/var` aliases must be resolved by the trusted caller first.
- Only regular files are read. Unix leaf opens add `O_NOFOLLOW | O_NONBLOCK`,
  preventing a swapped leaf symlink and a FIFO-open hang. Non-Unix builds compile
  but fail closed with `ErrUnsupportedPlatform`; no weaker fallback is used.
- `.git`, `.super-niuma`, `work-orders.db` and database suffix/sidecar paths are
  rejected case-insensitively, including workspace ancestors. No directory tree
  is recursively scanned. No candidate file is written or chmodded.
- Before/after inode, size and timestamp checks reject observed replacement or
  modification. Hash and assertions always operate on the **same read buffer**.
  These checks do not replace the caller's lock or constitute a filesystem-wide
  atomic snapshot against malicious writers, hardlinks or mounted filesystems.
  The control plane must exclude private/custom state and credentials from the
  authorized workspace and prevent hostile mounts/hardlink aliases. Arbitrary
  custom private directory names cannot be inferred from this API.
- Context is checked between directory operations, read chunks, rules and JSON
  tokens. Cancellation cannot interrupt an already blocked kernel filesystem
  operation; use a trusted local filesystem. Resource limits bound user-space
  work; no goroutine is abandoned to simulate I/O cancellation.
- A worker-supplied manifest, screenshot or copied report has no special status.
  Reading such a file as a candidate cannot establish native UI acceptance,
  independent model acceptance, provider delivery, or full business semantics.
- Debug logs include only rule counts/index, rule kind, safe code and result;
  no artifact contents, expected values or credentials are logged.

## Verification and handoff

Task-owned files are confined to `internal/super/verification/`. Other concurrently
created control/execution/eventbus files belong to other tasks and are untouched.
No commit or push is performed, per the assignment.

Run the entire package suite (no name filter):

```sh
go test -race -count=1 ./internal/super/verification/...
go vet ./internal/super/verification/...
```

Tests cover all assertions; invalid policy/path/budget; symlinks at leaf, ancestor
and workspace; FIFO; invalid/trailing/duplicate JSON (including escaped keys);
resource limits; content changed after prior success; forged worker manifests;
cancellation before and during reads; immutable policy and concurrent Verify.
All filesystem fixtures use `testing.T.TempDir` and are cleaned automatically.
