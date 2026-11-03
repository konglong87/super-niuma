package tenant

import (
	"github.com/konglong87/super-niuma/internal/pendinginput"
)

// PendingInputQueue exposes the optional durable queue without expanding the
// large TenantService/Repository contracts used by existing embedders and fakes.
// The server falls back to an in-memory queue when the repository does not
// implement this capability.
func (s *Service) PendingInputQueue() pendinginput.Queue {
	if s == nil || s.repo == nil {
		return nil
	}
	queue, _ := s.repo.(pendinginput.Queue)
	return queue
}
