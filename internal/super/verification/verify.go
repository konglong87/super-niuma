package verification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// Verify reads each artifact once into bounded memory, then hashes and checks
// exactly those bytes. Failed assertions/files produce a failed Check and nil
// error. Invalid policies/workspaces, unsupported hosts and cancellation return
// an error and can never return Passed=true. The caller must hold its workspace
// read lock and bind the report to its run/attempt; this function does neither.
func Verify(ctx context.Context, workspace string, policy Policy) (report Report, err error) {
	report.Artifacts = []Artifact{}
	report.Checks = []Check{}
	defer func() { report.VerifiedAtMS = time.Now().UTC().UnixMilli() }()
	if ctx == nil {
		return report, errors.New("verification: context is required")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := ValidatePolicy(policy); err != nil {
		return report, err
	}
	report.PolicyHash = PolicyHash(policy)
	root, err := openWorkspace(ctx, workspace)
	if err != nil {
		return report, err
	}
	defer root.Close()
	slog.DebugContext(ctx, "super.verification.started", "rules", len(policy.Rules))
	for index, rule := range policy.Rules {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		check := Check{Path: rule.Path, Kind: rule.Kind}
		data, err := readSnapshot(ctx, root, rule)
		if err != nil {
			check.Code = failureCode(err)
			report.Checks = append(report.Checks, check)
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
		} else {
			hash := sha256.Sum256(data)
			digest := hex.EncodeToString(hash[:])
			report.Artifacts = append(report.Artifacts, Artifact{Path: rule.Path, SHA256: digest, Bytes: int64(len(data))})
			check.Code = evaluate(ctx, rule, data, digest)
			check.Passed = check.Code == CodeOK
			report.Checks = append(report.Checks, check)
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
		}
		slog.DebugContext(ctx, "super.verification.checked", "rule_index", index, "kind", check.Kind, "code", check.Code)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	report.Passed = len(report.Checks) == len(policy.Rules)
	for _, check := range report.Checks {
		report.Passed = report.Passed && check.Passed
	}
	slog.DebugContext(ctx, "super.verification.finished", "checks", len(report.Checks), "passed", report.Passed)
	return report, nil
}

func evaluate(ctx context.Context, rule Rule, data []byte, digest string) string {
	switch rule.Kind {
	case KindNonempty:
		if len(data) == 0 {
			return CodeEmpty
		}
	case KindTextContains:
		if !utf8.Valid(data) || !bytes.Contains(data, []byte(rule.Expected)) {
			return CodeTextMissing
		}
	case KindSHA256:
		if !strings.EqualFold(rule.Expected, digest) {
			return CodeHashMismatch
		}
	case KindJSONKeys:
		ok, err := checkJSONKeys(ctx, data, rule.Keys)
		if err != nil {
			if ctx.Err() != nil {
				return CodeCancelled
			}
			return CodeJSONInvalid
		}
		if !ok {
			return CodeKeysMissing
		}
	default:
		return CodeInvalidRule
	}
	return CodeOK
}
