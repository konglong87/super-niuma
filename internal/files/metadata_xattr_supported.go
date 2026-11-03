//go:build linux || darwin

package files

import (
	"encoding/base64"

	"golang.org/x/sys/unix"
)

// captureXattrs reads all extended attributes of path. A listing error means
// the filesystem/platform does not support xattrs, reported as "not known"
// (ok=false) so restore does not claim to have preserved attributes it never
// captured.
func captureXattrs(path string) (map[string]string, bool) {
	size, err := unix.Listxattr(path, nil)
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	if size == 0 {
		return out, true
	}
	buf := make([]byte, size)
	n, err := unix.Listxattr(path, buf)
	if err != nil {
		return nil, false
	}
	for _, name := range splitNul(buf[:n]) {
		if name == "" {
			continue
		}
		vsize, err := unix.Getxattr(path, name, nil)
		if err != nil {
			continue
		}
		val := make([]byte, vsize)
		vn, err := unix.Getxattr(path, name, val)
		if err != nil {
			continue
		}
		out[name] = base64.StdEncoding.EncodeToString(val[:vn])
	}
	return out, true
}

func applyXattrs(path string, xattrs map[string]string) []string {
	var degraded []string
	for name, encoded := range xattrs {
		val, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			degraded = append(degraded, "xattr "+name+": "+err.Error())
			continue
		}
		if err := unix.Setxattr(path, name, val, 0); err != nil {
			degraded = append(degraded, "xattr "+name+": "+err.Error())
		}
	}
	return degraded
}

func splitNul(buf []byte) []string {
	var out []string
	start := 0
	for i, b := range buf {
		if b == 0 {
			out = append(out, string(buf[start:i]))
			start = i + 1
		}
	}
	if start < len(buf) {
		out = append(out, string(buf[start:]))
	}
	return out
}
