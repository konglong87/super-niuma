package repair

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/shellcmd"
	"mvdan.cc/sh/v3/syntax"
)

type VerificationResult struct {
	Kind               EvidenceKind      `json:"kind"`
	ProbeID            string            `json:"probe_id,omitempty"`
	Phase              VerificationPhase `json:"phase,omitempty"`
	Purpose            string            `json:"purpose,omitempty"`
	Targets            []string          `json:"targets,omitempty"`
	Fingerprint        string            `json:"fingerprint"`
	Grade              EvidenceGrade     `json:"grade"`
	ExitCode           int               `json:"exit_code"`
	ExpectedExit       ExitExpectation   `json:"expected_exit,omitempty"`
	MatchedExpectation bool              `json:"matched_expectation,omitempty"`
	ContentEpoch       uint64            `json:"content_epoch,omitempty"`
}

type Observation struct {
	Verification  *VerificationResult
	ContentChange bool
}

type Ledger struct {
	ContentEpoch uint64               `json:"content_epoch"`
	Evidence     []VerificationResult `json:"evidence,omitempty"`
}

func EvaluateShellCommand(command string, request *VerificationRequest, exitCode int) VerificationResult {
	result := InspectShellCommand(command, exitCode)
	if request == nil {
		return result
	}
	result.ProbeID = strings.TrimSpace(request.ProbeID)
	result.Phase = request.Phase
	result.Purpose = strings.TrimSpace(request.Purpose)
	result.Targets = normalizeTargets(request.Targets)
	result.ExpectedExit = request.ExpectExit
	result.Fingerprint = shellFingerprint(command, result.Targets)
	result.MatchedExpectation = matchesExitExpectation(exitCode, request.ExpectExit)
	return result
}

func InspectShellCommand(command string, exitCode int) VerificationResult {
	return VerificationResult{
		Kind:        EvidenceKindShellCommand,
		Fingerprint: shellFingerprint(command, nil),
		Grade:       GradeShellCommand(command),
		ExitCode:    exitCode,
	}
}

func GradeShellCommand(command string) EvidenceGrade {
	script, _ := shellcmd.Parse(command)
	grade := EvidenceGradeUnknown
	for _, cmd := range script.Commands {
		switch {
		case isBehavioralTest(cmd):
			return EvidenceGradeBehavioralTest
		case isAssertion(cmd):
			grade = EvidenceGradeAssertion
		case isObservation(cmd) && grade == EvidenceGradeUnknown:
			grade = EvidenceGradeObservation
		}
	}
	if grade == EvidenceGradeUnknown && strings.TrimSpace(command) != "" {
		return EvidenceGradeObservation
	}
	return grade
}

func BuildLedger(observations []Observation) Ledger {
	var ledger Ledger
	for _, observation := range observations {
		if observation.ContentChange {
			ledger.ContentEpoch++
		}
		if observation.Verification == nil {
			continue
		}
		evidence := *observation.Verification
		evidence.ContentEpoch = ledger.ContentEpoch
		ledger.Evidence = append(ledger.Evidence, evidence)
	}
	promotePairedProbes(ledger.Evidence)
	return ledger
}

func (l Ledger) CurrentPassingEvidence() []VerificationResult {
	var out []VerificationResult
	for _, evidence := range l.Evidence {
		if evidence.ContentEpoch != l.ContentEpoch || evidence.ExitCode != 0 {
			continue
		}
		if evidence.ExpectedExit != "" && !evidence.MatchedExpectation {
			continue
		}
		if EvidenceGradeRank(evidence.Grade) >= EvidenceGradeRank(EvidenceGradeAssertion) {
			out = append(out, evidence)
		}
	}
	return out
}

func (l Ledger) HasCurrentPassingEvidence() bool {
	return len(l.CurrentPassingEvidence()) > 0
}

func EvidenceGradeRank(grade EvidenceGrade) int {
	switch grade {
	case EvidenceGradeObservation:
		return 1
	case EvidenceGradeAssertion:
		return 2
	case EvidenceGradeBehavioralTest:
		return 3
	case EvidenceGradePairedProbe:
		return 4
	default:
		return 0
	}
}

func promotePairedProbes(evidence []VerificationResult) {
	type key struct{ probeID, fingerprint string }
	baselines := map[key]bool{}
	for i := range evidence {
		item := &evidence[i]
		pairKey := key{probeID: item.ProbeID, fingerprint: item.Fingerprint}
		if item.ProbeID == "" || item.Fingerprint == "" || !item.MatchedExpectation {
			continue
		}
		if item.Phase == VerificationPhaseBaseline && item.ExpectedExit == ExitExpectationNonzero && item.ExitCode != 0 {
			baselines[pairKey] = true
			continue
		}
		if item.Phase == VerificationPhasePostChange && item.ExpectedExit == ExitExpectationZero && item.ExitCode == 0 && baselines[pairKey] {
			item.Grade = EvidenceGradePairedProbe
		}
	}
}

func matchesExitExpectation(exitCode int, expectation ExitExpectation) bool {
	switch expectation {
	case ExitExpectationZero:
		return exitCode == 0
	case ExitExpectationNonzero:
		return exitCode != 0
	default:
		return false
	}
}

func isBehavioralTest(cmd shellcmd.Command) bool {
	switch cmd.Name {
	case "pytest", "jest", "vitest", "rspec", "phpunit":
		return true
	case "go", "cargo", "dotnet":
		return firstArg(cmd.Args) == "test"
	case "npm", "pnpm", "yarn", "bun":
		return firstArg(cmd.Args) == "test" || containsArg(cmd.Args, "test")
	case "mvn", "mvnw", "gradle", "gradlew":
		return containsArg(cmd.Args, "test")
	}
	return strings.HasSuffix(cmd.Name, "_test")
}

func isAssertion(cmd shellcmd.Command) bool {
	switch cmd.Name {
	case "test", "[", "[[", "diff", "cmp":
		return true
	case "grep", "rg":
		for _, arg := range cmd.Args {
			if arg == "--quiet" || arg == "-q" || strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(strings.TrimPrefix(arg, "-"), "q") {
				return true
			}
		}
	case "bash", "sh", "zsh":
		return containsArg(cmd.Args, "-n")
	}
	return false
}

func isObservation(cmd shellcmd.Command) bool {
	switch cmd.Name {
	case "cat", "sed", "head", "tail", "grep", "rg", "find", "ls", "git":
		return true
	default:
		return false
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(args[0]))
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), want) {
			return true
		}
	}
	return false
}

func shellFingerprint(command string, targets []string) string {
	normalized := strings.Join(strings.Fields(command), " ")
	if file, err := syntax.NewParser().Parse(strings.NewReader(command), ""); err == nil {
		var printed bytes.Buffer
		if err := syntax.NewPrinter().Print(&printed, file); err == nil {
			normalized = strings.TrimSpace(printed.String())
		}
	}
	payload := normalized + "\x00" + strings.Join(normalizeTargets(targets), "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func normalizeTargets(targets []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		target = filepath.ToSlash(filepath.Clean(strings.TrimSpace(target)))
		if target == "" || target == "." || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}
