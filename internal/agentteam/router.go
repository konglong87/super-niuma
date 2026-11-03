package agentteam

import (
	"strings"
)

type RouteResult struct {
	TenantID         uint64
	TeamID           uint64
	TeamVersion      uint
	AccountKey       string
	ExternalChatID   string
	ExternalThreadID string
	Binding          Binding
}

type Router struct {
	bindings map[string][]Binding
	bots     map[string]struct{}
}

func NewRouter(bindings []Binding, botKeys map[string]struct{}) Router {
	router := Router{bindings: make(map[string][]Binding), bots: make(map[string]struct{}, len(botKeys))}
	for key := range botKeys {
		router.bots[key] = struct{}{}
	}
	for _, binding := range bindings {
		key := routeKey(binding.Provider, binding.AccountKey, binding.ExternalChatID, binding.ExternalThreadID)
		router.bindings[key] = append(router.bindings[key], binding)
	}
	return router
}

func (r Router) Route(message InboundEvent) (RouteResult, bool) {
	if _, isBot := r.bots[strings.TrimSpace(message.ExternalUserID)]; isBot {
		return RouteResult{}, false
	}
	key := routeKey(message.Provider, message.AccountKey, message.ExternalConversationID, message.ExternalThreadID)
	for _, binding := range r.bindings[key] {
		if binding.Trigger == TriggerInternalOnly || !triggerMatches(binding, message) {
			continue
		}
		return RouteResult{TenantID: binding.TenantID, TeamID: binding.TeamID, TeamVersion: binding.TeamVersion, AccountKey: binding.AccountKey, ExternalChatID: binding.ExternalChatID, ExternalThreadID: binding.ExternalThreadID, Binding: binding}, true
	}
	return RouteResult{}, false
}

func triggerMatches(binding Binding, message InboundEvent) bool {
	switch binding.Trigger {
	case TriggerMention:
		return message.MentionedBot || message.ChatType == "p2p"
	case TriggerCommand:
		text := strings.TrimSpace(message.Text)
		if !strings.HasPrefix(text, "/") {
			return false
		}
		name := strings.Fields(strings.TrimPrefix(text, "/"))
		if len(name) == 0 {
			return false
		}
		if len(binding.Commands) == 0 {
			return true
		}
		for _, command := range binding.Commands {
			if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(command), "/"), name[0]) {
				return true
			}
		}
	}
	return false
}

func routeKey(provider, accountKey, chatID, threadID string) string {
	return provider + "\x00" + accountKey + "\x00" + chatID + "\x00" + threadID
}
