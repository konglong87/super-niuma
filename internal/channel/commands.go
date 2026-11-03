package channel

import "strings"

type CommandName string

const (
	CommandHelp       CommandName = "help"
	CommandNew        CommandName = "new"
	CommandStop       CommandName = "stop"
	CommandStatus     CommandName = "status"
	CommandCWD        CommandName = "cwd"
	CommandPermission CommandName = "permission"
	CommandImage      CommandName = "image"
)

type CommandClass string

const (
	CommandClassNone       CommandClass = "none"
	CommandClassSafe       CommandClass = "safe"
	CommandClassControlled CommandClass = "controlled"
	CommandClassSkill      CommandClass = "skill"
)

type CommandRouteKind string

const (
	CommandRoutePass        CommandRouteKind = "pass"
	CommandRouteBuiltin     CommandRouteKind = "builtin"
	CommandRouteSkill       CommandRouteKind = "skill"
	CommandRouteDenied      CommandRouteKind = "denied"
	CommandRouteUnsupported CommandRouteKind = "unsupported"
)

type CommandInvocation struct {
	Name CommandName
	Args string
}

type CommandInput struct {
	Text           string
	ChatType       ChatType
	ExternalUserID string
}

type CommandRoute struct {
	Kind       CommandRouteKind
	Class      CommandClass
	Invocation CommandInvocation
}

type ChannelCommandRouter struct {
	adminIDs map[string]struct{}
}

func NewChannelCommandRouter(adminIDs []string) *ChannelCommandRouter {
	admins := make(map[string]struct{}, len(adminIDs))
	for _, id := range adminIDs {
		if id = strings.TrimSpace(id); id != "" {
			admins[id] = struct{}{}
		}
	}
	return &ChannelCommandRouter{adminIDs: admins}
}

func (r *ChannelCommandRouter) Route(input CommandInput) CommandRoute {
	invocation, ok := parseChannelCommand(input.Text)
	if !ok {
		return CommandRoute{Kind: CommandRoutePass}
	}
	if class, known := builtinCommandClasses[invocation.Name]; known {
		if class == CommandClassControlled && !r.controlledAllowed(input) {
			return CommandRoute{Kind: CommandRouteDenied, Class: class, Invocation: invocation}
		}
		return CommandRoute{Kind: CommandRouteBuiltin, Class: class, Invocation: invocation}
	}
	if _, unsupported := unsupportedChannelBuiltins[invocation.Name]; unsupported {
		return CommandRoute{Kind: CommandRouteUnsupported, Invocation: invocation}
	}
	return CommandRoute{Kind: CommandRouteSkill, Class: CommandClassSkill, Invocation: invocation}
}

func (r *ChannelCommandRouter) controlledAllowed(input CommandInput) bool {
	if r == nil || input.ChatType != ChatTypeP2P {
		return false
	}
	_, ok := r.adminIDs[strings.TrimSpace(input.ExternalUserID)]
	return ok
}

func parseChannelCommand(text string) (CommandInvocation, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return CommandInvocation{}, false
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	if body == "" {
		return CommandInvocation{}, false
	}
	name, args := body, ""
	if index := strings.IndexFunc(body, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}); index >= 0 {
		name, args = body[:index], strings.TrimSpace(body[index:])
	}
	return CommandInvocation{Name: CommandName(strings.ToLower(name)), Args: args}, true
}

var builtinCommandClasses = map[CommandName]CommandClass{
	CommandHelp:       CommandClassSafe,
	CommandNew:        CommandClassSafe,
	CommandStop:       CommandClassSafe,
	CommandStatus:     CommandClassSafe,
	CommandCWD:        CommandClassControlled,
	CommandPermission: CommandClassControlled,
	CommandImage:      CommandClassSafe,
}

var unsupportedChannelBuiltins = map[CommandName]struct{}{
	"clear": {}, "exit": {}, "quit": {}, "attach": {},
	"mcp": {}, "plugins": {}, "hooks": {}, "init": {}, "loop": {}, "goal": {},
}
