//go:build !linux && !darwin

package files

// Extended attributes are not a captured capability outside Linux/macOS.
func captureXattrs(path string) (map[string]string, bool) { return nil, false }

func applyXattrs(path string, xattrs map[string]string) []string {
	if len(xattrs) == 0 {
		return nil
	}
	return []string{"xattr: unsupported on this platform"}
}
