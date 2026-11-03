package runtimeprofile

import "fmt"

type Profile string

const (
	ProfileDefault Profile = "default"
	ProfileBare    Profile = "bare"
)

type ToolSet string

const (
	ToolSetFull    ToolSet = "full"
	ToolSetMinimal ToolSet = "minimal"
)

const (
	BareToolRead = "Read"
	BareToolEdit = "Edit"
	BareToolBash = "Bash"
)

type Policy struct {
	Profile                  Profile
	ToolSet                  ToolSet
	DiscoverWorkspaceContext bool
	DiscoverSkills           bool
	DiscoverAgents           bool
	DiscoverPlugins          bool
	DiscoverMCP              bool
	RunHooks                 bool
	RunStartupUpdate         bool
	RunBackgroundEnrichment  bool
	AllowAutoMemoryRoot      bool
}

func Normalize(profile Profile) (Profile, error) {
	switch profile {
	case "", ProfileDefault:
		return ProfileDefault, nil
	case ProfileBare:
		return ProfileBare, nil
	default:
		return "", fmt.Errorf("unknown runtime profile: %q", string(profile))
	}
}

func Resolve(profile Profile) (Policy, error) {
	normalized, err := Normalize(profile)
	if err != nil {
		return Policy{}, err
	}
	if normalized == ProfileBare {
		return Policy{
			Profile: normalized,
			ToolSet: ToolSetMinimal,
		}, nil
	}
	return Policy{
		Profile:                  normalized,
		ToolSet:                  ToolSetFull,
		DiscoverWorkspaceContext: true,
		DiscoverSkills:           true,
		DiscoverAgents:           true,
		DiscoverPlugins:          true,
		DiscoverMCP:              true,
		RunHooks:                 true,
		RunStartupUpdate:         true,
		RunBackgroundEnrichment:  true,
		AllowAutoMemoryRoot:      true,
	}, nil
}

func (profile Profile) String() string {
	normalized, err := Normalize(profile)
	if err != nil {
		return string(profile)
	}
	return string(normalized)
}

func (profile Profile) IsBare() bool {
	normalized, err := Normalize(profile)
	return err == nil && normalized == ProfileBare
}

func BareBuiltinTools() []string {
	return []string{BareToolRead, BareToolEdit, BareToolBash}
}
