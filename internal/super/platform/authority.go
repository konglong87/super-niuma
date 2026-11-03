package platform

import (
	"context"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

type runtimeAuthority struct {
	config *configStore
	runner *execution.Runner
}

func (a *runtimeAuthority) Current(ctx context.Context, p control.Principal, g control.Grant) error {
	if err := a.config.Current(ctx, p, g); err != nil {
		return err
	}

	if g.SourceBindingID != "" {
		c, err := a.config.load()
		if err != nil {
			return control.ErrDenied
		}
		allowed := false
		for _, b := range c.Bindings {
			if b.ID == g.SourceBindingID && b.Enabled && b.Scope == p.Scope && b.Owner == p.Owner && b.ProfileID == g.ProfileID && control.Digest(b) == g.SourceBindingHash {
				for _, account := range c.Accounts {
					if account.ID == b.Account && account.Enabled && account.Provider == b.Provider {
						allowed = true
					}
				}
			}
		}
		if !allowed {
			return control.ErrDenied
		}
	}
	current, err := a.runner.RouteFingerprint(ctx, g.Provider, g.Model)
	if err != nil || g.RouteHash == "" || current != g.RouteHash {
		return control.ErrDenied
	}
	return nil
}
