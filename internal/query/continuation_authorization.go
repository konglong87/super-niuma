package query

import "github.com/konglong87/super-niuma/internal/gitpolicy"

// continuationAuthorizationGrant owns the one-time capability recovered from
// the latest assistant authorization request. Direct current-turn authorization
// remains reusable for the duration of the turn.
type continuationAuthorizationGrant struct {
	remaining map[string]int
	effects   map[string]gitpolicy.Effect
}

func newContinuationAuthorizationGrant(effects []gitpolicy.Effect) *continuationAuthorizationGrant {
	grant := &continuationAuthorizationGrant{
		remaining: make(map[string]int, len(effects)),
		effects:   make(map[string]gitpolicy.Effect, len(effects)),
	}
	grant.addEffects(effects)
	return grant
}

func (g *continuationAuthorizationGrant) addEffects(effects []gitpolicy.Effect) {
	if g == nil {
		return
	}
	for _, effect := range effects {
		key := gitpolicy.EffectKey(effect)
		g.remaining[key]++
		g.effects[key] = effect
	}
}

func (g *continuationAuthorizationGrant) authorizationFor(direct gitpolicy.Authorization, analysis gitpolicy.Analysis) (gitpolicy.Authorization, []string) {
	if g == nil || len(analysis.Effects) == 0 {
		return direct, nil
	}
	needed := map[string]int{}
	for _, effect := range analysis.Effects {
		if direct.Allows(effect) {
			continue
		}
		needed[gitpolicy.EffectKey(effect)]++
	}
	for key, count := range needed {
		if g.remaining[key] < count {
			return direct, nil
		}
	}
	confirmed := make([]gitpolicy.Effect, 0, len(needed))
	consumeKeys := make([]string, 0, len(needed))
	for key, count := range needed {
		confirmed = append(confirmed, g.effects[key])
		for i := 0; i < count; i++ {
			consumeKeys = append(consumeKeys, key)
		}
	}
	return gitpolicy.MergeAuthorizations(direct, gitpolicy.AuthorizationFromEffects(confirmed)), consumeKeys
}

func (g *continuationAuthorizationGrant) authorizationForOnly(direct gitpolicy.Authorization, analysis gitpolicy.Analysis) gitpolicy.Authorization {
	authorization, _ := g.authorizationFor(direct, analysis)
	return authorization
}

func (g *continuationAuthorizationGrant) consume(keys []string) {
	if g == nil {
		return
	}
	for _, key := range keys {
		if g.remaining[key] > 0 {
			g.remaining[key]--
		}
	}
}
