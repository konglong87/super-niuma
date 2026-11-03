package query

import "github.com/konglong87/super-niuma/internal/gitpolicy"

const sharedStateGateRetryLimit = 3

type sharedStateGateRetryTracker struct {
	counts map[string]int
}

func newSharedStateGateRetryTracker() *sharedStateGateRetryTracker {
	return &sharedStateGateRetryTracker{counts: map[string]int{}}
}

func (t *sharedStateGateRetryTracker) observe(ruleID string, analysis gitpolicy.Analysis) (int, string, bool) {
	if t == nil || !isSharedStateAuthorizationRule(ruleID) || len(analysis.Effects) == 0 {
		return 0, "", false
	}
	fingerprint := gitpolicy.AnalysisFingerprint(analysis)
	key := ruleID + "\x00" + fingerprint
	t.counts[key]++
	count := t.counts[key]
	return count, fingerprint, count >= sharedStateGateRetryLimit
}

func isSharedStateAuthorizationRule(ruleID string) bool {
	switch ruleID {
	case string(gitpolicy.ViolationSharedState), string(gitpolicy.ViolationDestructive), string(gitpolicy.ViolationForceAdd):
		return true
	default:
		return false
	}
}
