package computeruse

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// TargetID is the stable, model-visible identifier for a launchable desktop
// target. It is intentionally not a bundle ID, path, or display name.
type TargetID string

func (id TargetID) String() string { return string(id) }

func (id TargetID) valid() bool {
	value := strings.TrimSpace(string(id))
	if value == "" || len(value) > 256 || value != string(id) {
		return false
	}
	for _, r := range value {
		if r < ' ' || r == 127 {
			return false
		}
	}
	return true
}

// LaunchPolicy contains trusted host launch metadata. ProviderKey is an
// opaque native/backend key and is never accepted directly from the model.
type LaunchPolicy struct {
	ProviderKey string `json:"provider_key,omitempty"`
}

// WindowPolicy describes the identity checks required for the window returned
// by a launch. Empty fields mean that the platform/backend owns that check.
type WindowPolicy struct {
	BundleID         string `json:"bundle_id,omitempty"`
	RequireVisible   bool   `json:"require_visible,omitempty"`
	RequireFrontmost bool   `json:"require_frontmost,omitempty"`
}

// ApplicationTarget is one statically registered launch target.
type ApplicationTarget struct {
	ID          TargetID     `json:"target_id"`
	DisplayName string       `json:"display_name"`
	Launch      LaunchPolicy `json:"launch"`
	Window      WindowPolicy `json:"window"`
}

func (t ApplicationTarget) Validate() error {
	if !t.ID.valid() {
		return errors.New("target id is required")
	}
	if strings.TrimSpace(t.DisplayName) == "" || len(t.DisplayName) > 256 {
		return errors.New("target display name is required")
	}
	if strings.TrimSpace(t.Launch.ProviderKey) == "" {
		return errors.New("target launch provider key is required")
	}
	if strings.TrimSpace(t.Window.BundleID) == "" {
		return errors.New("target window bundle id is required")
	}
	return nil
}

// TargetRegistry resolves trusted target IDs. Implementations must not derive
// launch metadata from model input.
type TargetRegistry interface {
	Resolve(TargetID) (ApplicationTarget, error)
}

// StaticTargetRegistry is a process-local registry for trusted launch targets.
// Registration is intended during host construction; resolution is safe for
// concurrent Computer Use calls.
type StaticTargetRegistry struct {
	mu      sync.RWMutex
	targets map[TargetID]ApplicationTarget
}

func NewStaticTargetRegistry(targets ...ApplicationTarget) (*StaticTargetRegistry, error) {
	r := &StaticTargetRegistry{targets: make(map[TargetID]ApplicationTarget, len(targets))}
	for _, target := range targets {
		if err := r.Register(target); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *StaticTargetRegistry) Register(target ApplicationTarget) error {
	if r == nil {
		return errors.New("target registry is nil")
	}
	if err := target.Validate(); err != nil {
		return fmt.Errorf("register target %q: %w", target.ID, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.targets == nil {
		r.targets = make(map[TargetID]ApplicationTarget)
	}
	if _, exists := r.targets[target.ID]; exists {
		return fmt.Errorf("target %q is already registered", target.ID)
	}
	r.targets[target.ID] = target
	return nil
}

func (r *StaticTargetRegistry) Resolve(id TargetID) (ApplicationTarget, error) {
	if r == nil || !id.valid() {
		return ApplicationTarget{}, errors.New("target is not registered")
	}
	r.mu.RLock()
	target, ok := r.targets[id]
	r.mu.RUnlock()
	if !ok {
		return ApplicationTarget{}, fmt.Errorf("target %q is not registered", id)
	}
	return target, nil
}
