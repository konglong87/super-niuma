package supervisor

import (
	"context"
	"errors"

	"github.com/konglong87/super-niuma/internal/provisioning"
)

var ErrDuplicateAccount = errors.New("supervisor: account already has a worker")

type CommandRunner interface {
	Run(context.Context, string, []string, []string) ([]byte, error)
}
type WorkerSupervisor interface {
	Start(context.Context, provisioning.WorkerSpec) (provisioning.WorkerStatus, error)
	Restart(context.Context, provisioning.WorkerSpec) (provisioning.WorkerStatus, error)
	Stop(context.Context, provisioning.WorkerSpec) (provisioning.WorkerStatus, error)
	Status(context.Context, provisioning.WorkerSpec) (provisioning.WorkerStatus, error)
	Logs(context.Context, provisioning.WorkerSpec, int) (string, error)
}
