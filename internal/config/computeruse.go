package config

// ComputerUseSettings contains operator assertions, not model capability
// discovery or host approval. Only trusted configuration may supply these
// settings; query text and request bodies must never populate this allowlist.
type ComputerUseSettings struct {
	// ImageInputRoutes declares routes that can accept screenshot image blocks.
	// Provider is the exact selected/named provider registry name, or the
	// runtime name "primary" / "fallback-N" when no registry name is set. Model is
	// the exact effective model ID. No wildcards or protocol/name inference.
	//
	// Layered configuration inherits an omitted/null list. An explicit list
	// replaces (never unions with) the inherited list; [] revokes all routes.
	// Do not omit empty slices during serialization: [] must survive a save.
	ImageInputRoutes []ComputerUseImageInputRoute `json:"imageInputRoutes" yaml:"imageInputRoutes"`
}

type ComputerUseImageInputRoute struct {
	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
}

// SupportsImageInput is an exact, case-sensitive operator assertion. It does
// not grant access to a host session and never assumes a model supports images.
func (s *ComputerUseSettings) SupportsImageInput(provider, model string) bool {
	if s == nil || provider == "" || model == "" {
		return false
	}
	for _, route := range s.ImageInputRoutes {
		if route.Provider == provider && route.Model == model {
			return true
		}
	}
	return false
}

func mergeComputerUseSettings(base, override *ComputerUseSettings) *ComputerUseSettings {
	if base == nil && override == nil {
		return nil
	}
	merged := &ComputerUseSettings{}
	if base != nil {
		*merged = *base
	}
	if override != nil && override.ImageInputRoutes != nil {
		merged.ImageInputRoutes = override.ImageInputRoutes
	}
	if merged.ImageInputRoutes != nil {
		merged.ImageInputRoutes = append([]ComputerUseImageInputRoute{}, merged.ImageInputRoutes...)
	}
	return merged
}
