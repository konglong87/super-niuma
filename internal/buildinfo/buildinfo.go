// Package buildinfo exposes the immutable identity embedded in a niuma binary.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/konglong87/super-niuma/internal/product"
)

const (
	// SchemaVersion is persisted/consumed metadata and remains at v1 across the
	// executable rename.
	SchemaVersion = "niuma.build-info/v1"
	Product       = product.Name
	devVersion    = "dev"
)

// These values are populated by release builds through -ldflags -X.
var (
	Version   string
	Revision  string
	Dirty     string
	BuildTime string
)

// Info is the stable, machine-readable identity of the running binary.
type Info struct {
	SchemaVersion string `json:"schema_version"`
	Product       string `json:"product"`
	Version       string `json:"version"`
	Revision      string `json:"revision"`
	Dirty         bool   `json:"dirty"`
	DirtyKnown    bool   `json:"dirty_known"`
	BuildTime     string `json:"build_time"`
	GoToolchain   string `json:"go_toolchain"`
}

type linkerMetadata struct {
	version   string
	revision  string
	dirty     string
	buildTime string
}

type embeddedMetadata struct {
	moduleVersion string
	revision      string
	dirty         bool
	dirtyKnown    bool
	goToolchain   string
}

var (
	currentOnce sync.Once
	currentInfo Info
)

// Current returns the process-wide build identity. It does not inspect the
// working tree or invoke external commands.
func Current() Info {
	currentOnce.Do(func() {
		currentInfo = resolve(linkerMetadata{
			version:   Version,
			revision:  Revision,
			dirty:     Dirty,
			buildTime: BuildTime,
		}, readEmbeddedMetadata())
	})
	return currentInfo
}

func readEmbeddedMetadata() embeddedMetadata {
	metadata := embeddedMetadata{goToolchain: runtime.Version()}
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return metadata
	}
	metadata.moduleVersion = info.Main.Version
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			metadata.revision = setting.Value
		case "vcs.modified":
			if dirty, err := strconv.ParseBool(strings.TrimSpace(setting.Value)); err == nil {
				metadata.dirty = dirty
				metadata.dirtyKnown = true
			}
		}
	}
	return metadata
}

func resolve(linker linkerMetadata, embedded embeddedMetadata) Info {
	version := strings.TrimSpace(linker.version)
	if version == "" {
		version = strings.TrimSpace(embedded.moduleVersion)
	}
	if version == "" || version == "(devel)" {
		version = devVersion
	}

	revision := strings.TrimSpace(linker.revision)
	if revision == "" {
		revision = strings.TrimSpace(embedded.revision)
	}

	dirty, dirtyKnown := embedded.dirty, embedded.dirtyKnown
	if value := strings.TrimSpace(linker.dirty); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			dirty, dirtyKnown = parsed, true
		} else {
			dirty, dirtyKnown = false, false
		}
	}

	return Info{
		SchemaVersion: SchemaVersion,
		Product:       Product,
		Version:       version,
		Revision:      revision,
		Dirty:         dirty,
		DirtyKnown:    dirtyKnown,
		BuildTime:     strings.TrimSpace(linker.buildTime),
		GoToolchain:   strings.TrimSpace(embedded.goToolchain),
	}
}
