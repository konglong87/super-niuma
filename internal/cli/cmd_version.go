package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/konglong87/super-niuma/internal/buildinfo"
)

func versionCommand(args []string, stdout io.Writer) error {
	switch len(args) {
	case 0:
		fmt.Fprintf(stdout, "%s (niuma)\n", versionForDisplay())
		return nil
	case 1:
		if args[0] != "--json" {
			return fmt.Errorf("unknown version option: %s", args[0])
		}
		return json.NewEncoder(stdout).Encode(buildinfo.Current())
	default:
		return fmt.Errorf("version accepts only --json")
	}
}
