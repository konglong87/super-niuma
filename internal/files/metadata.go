package files

import (
	"os"
	"time"
)

// Metadata is capability-aware filesystem metadata captured alongside a file
// change. Each field carries a "known" flag so an absent capability (old
// transcript, unsupported platform, or unreadable attribute) is distinguishable
// from a genuine zero value. Restoring metadata is best-effort: a field that
// cannot be applied is reported as a degradation rather than failing the whole
// rewind or being silently dropped.
type Metadata struct {
	ModTimeUnixNano int64             `json:"mtime_unix_nano,omitempty"`
	ModTimeKnown    bool              `json:"mtime_known,omitempty"`
	UID             int               `json:"uid,omitempty"`
	GID             int               `json:"gid,omitempty"`
	OwnerKnown      bool              `json:"owner_known,omitempty"`
	Xattrs          map[string]string `json:"xattrs,omitempty"` // name -> base64(value)
	XattrsKnown     bool              `json:"xattrs_known,omitempty"`
}

// Empty reports whether no capability was captured, so callers can omit the
// metadata object entirely.
func (m Metadata) Empty() bool {
	return !m.ModTimeKnown && !m.OwnerKnown && !m.XattrsKnown
}

// CaptureMetadata records the metadata of the object at path without following
// symlinks. isSymlink is passed by the caller to avoid a redundant Lstat and to
// skip attributes that do not apply to links (e.g. mtime, which has no portable
// no-follow setter in the standard library).
func CaptureMetadata(path string, info os.FileInfo, isSymlink bool) Metadata {
	m := Metadata{}
	if info != nil && !isSymlink {
		m.ModTimeUnixNano = info.ModTime().UnixNano()
		m.ModTimeKnown = true
	}
	if uid, gid, ok := captureOwner(path); ok {
		m.UID, m.GID, m.OwnerKnown = uid, gid, true
	}
	if !isSymlink {
		if xattrs, ok := captureXattrs(path); ok {
			m.Xattrs, m.XattrsKnown = xattrs, true
		}
	}
	return m
}

// ApplyMetadata restores the captured metadata to path, returning a list of
// human-readable degradation messages for anything that could not be applied
// (permission denied, unsupported platform/filesystem). It never returns an
// error: metadata restoration is best-effort and must not abort a rewind whose
// content restore already succeeded.
func ApplyMetadata(path string, m Metadata, isSymlink bool) []string {
	var degraded []string
	if m.OwnerKnown {
		if msg := applyOwner(path, m.UID, m.GID); msg != "" {
			degraded = append(degraded, msg)
		}
	}
	if m.XattrsKnown && !isSymlink {
		degraded = append(degraded, applyXattrs(path, m.Xattrs)...)
	}
	// mtime is applied last because chown/xattr writes can update it.
	if m.ModTimeKnown && !isSymlink {
		t := time.Unix(0, m.ModTimeUnixNano)
		if err := os.Chtimes(path, t, t); err != nil {
			degraded = append(degraded, "mtime: "+err.Error())
		}
	}
	return degraded
}
