package provisioning

import "path/filepath"

const ChannelWorkerStateDirName = "channel-workers"

func DefaultChannelWorkerStateDir(home string) string {
	return filepath.Join(home, ".niuma", ChannelWorkerStateDirName)
}
