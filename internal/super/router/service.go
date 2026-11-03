package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Service struct {
	engine   *control.Engine
	bindings func() []Binding
	grant    GrantResolver
	logger   *slog.Logger
}

func New(engine *control.Engine, bindings func() []Binding, grant func(context.Context, control.Principal, string) (control.Grant, error), logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{engine: engine, bindings: bindings, grant: grant, logger: logger}
}
func (s *Service) Handle(ctx context.Context, e Envelope) (decision Decision, err error) {
	decision = Decision{Action: ActionRejected, Code: CodeInvalid}
	if ctx == nil {
		return decision, control.ErrInvalid
	}
	if s == nil || s.engine == nil || s.engine.Store == nil || s.bindings == nil {
		return rejected(CodeUnavailable), control.ErrUnavailable
	}
	defer func() {
		s.logger.DebugContext(ctx, "Super router decision", "action", decision.Action, "code", decision.Code, "used_llm", false)
	}()
	if err = ctx.Err(); err != nil {
		return decision, err
	}
	if !e.Authenticated {
		return rejected(CodeUnauthenticated), control.ErrDenied
	}
	if !validEnvelope(e) {
		return decision, control.ErrInvalid
	}
	if e.Bot || e.Self {
		return Decision{Action: ActionIgnored, Code: CodeBot}, nil
	}
	if !e.ThreadKnown {
		return rejected(CodeThreadUnknown), control.ErrDenied
	}
	b, code := s.match(e)
	if code != "" {
		return rejected(code), control.ErrDenied
	}
	if b.RequireMention && !e.Mentioned {
		return rejected(CodeMention), control.ErrDenied
	}
	p := control.Principal{Scope: b.Scope, Owner: b.Owner}
	text := strings.TrimSpace(e.Text)
	if !strings.HasPrefix(text, "/") {
		return Decision{Action: ActionClarification, Code: CodeClarification}, nil
	}
	command, arg, _ := strings.Cut(text, " ")
	// Fields handles tabs/newlines without changing the objective's contents.
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return decision, control.ErrInvalid
	}
	command = fields[0]
	arg = strings.TrimSpace(strings.TrimPrefix(text, command))
	in := ingress(e, b)
	switch command {
	case "/run":
		if arg == "" || s.grant == nil {
			return decision, control.ErrInvalid
		}
		g, grantErr := s.grant(ctx, p, b.ProfileID)
		if grantErr != nil || g.ProfileID != b.ProfileID {
			return rejected(CodeDenied), control.ErrDenied
		}
		g.SourceBindingID = b.ID
		g.SourceBindingHash = control.Digest(b)
		key := "router." + sum([]byte(e.Provider+"\x00"+e.Account+"\x00"+e.MessageID))
		run, _, startErr := s.engine.Start(ctx, p, key, arg, "", g, &in)
		if startErr != nil {
			return rejected(errorCode(startErr)), startErr
		}
		return Decision{Action: ActionRun, Code: CodeAccepted, RunID: run.ID}, nil
	case "/status", "/cancel":
		if len(fields) != 2 || !control.ValidID(arg) {
			return decision, control.ErrInvalid
		}
		action := ActionStatus
		if command == "/cancel" {
			action = ActionCancel
		}
		return s.command(ctx, p, in, action, arg)
	default:
		return rejected(CodeCommand), control.ErrDenied
	}
}
func validEnvelope(e Envelope) bool {
	if (e.Provider != control.ProviderFeishu && e.Provider != control.ProviderWebhook) || !control.ValidID(e.Account) || !safeField(e.EventID, 128, false) || !safeField(e.MessageID, 128, false) || !safeField(e.Chat, 256, false) || !safeField(e.Thread, 256, true) || !safeField(e.Sender, 256, false) || !safeField(e.Nonce, 128, true) {
		return false
	}
	return len(e.Text) <= MaxTextBytes && utf8.ValidString(e.Text) && !strings.ContainsRune(e.Text, 0)
}
func safeField(s string, max int, optional bool) bool {
	return (optional || s != "") && len(s) <= max && utf8.ValidString(s) && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}
func (s *Service) match(e Envelope) (Binding, string) {
	bindings := s.bindings()
	if len(bindings) > MaxBindings {
		return Binding{}, CodeAmbiguous
	}
	scopes := map[string]string{}
	ids := map[string]bool{}
	var found Binding
	matches := 0
	for _, b := range bindings {
		if !b.Enabled {
			continue
		}
		if !control.ValidID(b.ID) || !control.ValidID(b.Scope) || !control.ValidID(b.Owner) || !control.ValidID(b.ProfileID) || !control.ValidID(b.Account) || !safeField(b.Chat, 256, false) || !safeField(b.Thread, 256, true) || !safeField(b.Sender, 256, false) || b.Sender == "*" || b.Chat == "*" || b.Thread == "*" {
			return Binding{}, CodeAmbiguous
		}
		if ids[b.ID] {
			return Binding{}, CodeAmbiguous
		}
		ids[b.ID] = true
		if owner, ok := scopes[b.Scope]; ok && owner != b.Owner {
			return Binding{}, CodeAmbiguous
		}
		scopes[b.Scope] = b.Owner
		if b.Provider == e.Provider && b.Account == e.Account && b.Chat == e.Chat && b.Thread == e.Thread && b.Sender == e.Sender {
			found = b
			matches++
		}
	}
	if matches > 1 {
		return Binding{}, CodeAmbiguous
	}
	if matches == 0 {
		return Binding{}, CodeUnbound
	}
	return found, ""
}
func ingress(e Envelope, b Binding) control.Ingress {
	// Transport retries may change event/nonce, but not message or authority.
	data, _ := json.Marshal(struct{ Provider, Account, MessageID, Chat, Thread, Sender, Text, Binding, Scope, Owner, Profile string }{e.Provider, e.Account, e.MessageID, e.Chat, e.Thread, e.Sender, e.Text, b.ID, b.Scope, b.Owner, b.ProfileID})
	return control.Ingress{Account: e.Account, EventID: e.EventID, MessageID: e.MessageID, Nonce: e.Nonce, ContentHash: sum(data)}
}
func sum(data []byte) string        { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func rejected(code string) Decision { return Decision{Action: ActionRejected, Code: code} }
func errorCode(err error) string {
	switch {
	case errors.Is(err, control.ErrDenied), errors.Is(err, control.ErrNotFound):
		return CodeDenied
	case errors.Is(err, control.ErrConflict):
		return CodeConflict
	case errors.Is(err, control.ErrInvalid):
		return CodeInvalid
	default:
		return CodeUnavailable
	}
}
