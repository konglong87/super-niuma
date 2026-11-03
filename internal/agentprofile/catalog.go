package agentprofile

import (
	"context"
	"errors"
	"strings"
)

const (
	SurfaceWebChat             = "web_chat"
	SurfaceMobileChat          = "mobile_chat"
	SurfaceTenantAgent         = "tenant_agent"
	SurfaceChannelDM           = "channel_dm"
	SurfaceChannelTeam         = "channel_team"
	SurfaceCLI                 = "cli"
	SurfaceTUI                 = "tui"
	SurfaceWebUIV2Orchestrator = "webui_v2_orchestrator"
)

const (
	SourceBuiltin    = "builtin"
	SourceAssignment = "assignment"
	SourceExplicit   = "explicit"
)

var (
	ErrProfileNotFound    = errors.New("agent profile not found")
	ErrAssignmentNotFound = errors.New("agent profile assignment not found")
	ErrProfileNotSelected = errors.New("agent profile is not selected")
	ErrInvalidProfile     = errors.New("agent profile is invalid")
)

type ProfileAssignment struct {
	ProfileKey     string
	ProfileVersion uint
}

type ProfileSource interface {
	FindPublishedProfile(ctx context.Context, tenantID, userID uint64, key string, version uint) (Profile, error)
	FindAssignment(ctx context.Context, tenantID, userID uint64, surface string) (ProfileAssignment, error)
}

type Catalog struct {
	source   ProfileSource
	builtins map[string]Profile
}

func NewCatalog(source ProfileSource) Catalog {
	return Catalog{source: source, builtins: BuiltinProfiles()}
}

func (c Catalog) Find(ctx context.Context, tenantID, userID uint64, surface, key string, version uint) (Profile, string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		if surface == SurfaceCLI || surface == SurfaceTUI {
			return Profile{}, "", ErrProfileNotSelected
		}
		if c.source != nil {
			assignment, err := c.source.FindAssignment(ctx, tenantID, userID, surface)
			if err == nil && strings.TrimSpace(assignment.ProfileKey) != "" {
				profile, profileErr := c.findByKey(ctx, tenantID, userID, assignment.ProfileKey, assignment.ProfileVersion)
				if profileErr == nil {
					return profile, SourceAssignment, nil
				}
				return Profile{}, "", profileErr
			}
			if err != nil && !errors.Is(err, ErrAssignmentNotFound) {
				return Profile{}, "", err
			}
		}
		if surface == SurfaceWebChat || surface == SurfaceMobileChat {
			profile := c.builtins[ProfileChatAssistant]
			return profile, SourceBuiltin, nil
		}
		return Profile{}, "", ErrProfileNotSelected
	}

	profile, err := c.findByKey(ctx, tenantID, userID, key, version)
	if err != nil {
		return Profile{}, "", err
	}
	return profile, SourceExplicit, nil
}

func (c Catalog) findByKey(ctx context.Context, tenantID, userID uint64, key string, version uint) (Profile, error) {
	if builtin, ok := c.builtins[key]; ok && (version == 0 || version == builtin.Version) {
		return builtin, nil
	}
	if c.source == nil {
		return Profile{}, ErrProfileNotFound
	}
	profile, err := c.source.FindPublishedProfile(ctx, tenantID, userID, key, version)
	if errors.Is(err, ErrProfileNotFound) {
		return Profile{}, ErrProfileNotFound
	}
	return profile, err
}
