package buildinfo

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestResolvePrefersInjectedBuildMetadata(t *testing.T) {
	got := resolve(
		linkerMetadata{
			version:   " v2.0.0 ",
			revision:  " release-revision ",
			dirty:     "true",
			buildTime: "2026-08-11T03:04:05Z",
		},
		embeddedMetadata{
			moduleVersion: "v1.0.0",
			revision:      "embedded-revision",
			dirty:         false,
			dirtyKnown:    true,
			goToolchain:   "go1.25.0",
		},
	)
	want := Info{
		SchemaVersion: SchemaVersion,
		Product:       Product,
		Version:       "v2.0.0",
		Revision:      "release-revision",
		Dirty:         true,
		DirtyKnown:    true,
		BuildTime:     "2026-08-11T03:04:05Z",
		GoToolchain:   "go1.25.0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveFallsBackToEmbeddedMetadata(t *testing.T) {
	got := resolve(
		linkerMetadata{},
		embeddedMetadata{
			moduleVersion: "v1.4.2",
			revision:      "embedded-revision",
			dirty:         true,
			dirtyKnown:    true,
			goToolchain:   "go1.25.0",
		},
	)
	if got.Version != "v1.4.2" || got.Revision != "embedded-revision" || !got.Dirty || !got.DirtyKnown {
		t.Fatalf("resolve() did not use embedded metadata: %+v", got)
	}
}

func TestResolveKeepsUnknownDirtyDistinctFromClean(t *testing.T) {
	got := resolve(linkerMetadata{version: "dev", dirty: "not-a-bool"}, embeddedMetadata{goToolchain: "go-test"})
	if got.Version != "dev" || got.Dirty || got.DirtyKnown {
		t.Fatalf("unknown metadata = %+v", got)
	}
}

func TestInfoJSONHasStableMachineReadableFields(t *testing.T) {
	data, err := json.Marshal(Info{
		SchemaVersion: SchemaVersion,
		Product:       Product,
		Version:       "dev",
		GoToolchain:   "go-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"schema_version", "product", "version", "revision", "dirty", "dirty_known", "build_time", "go_toolchain"}
	if len(got) != len(wantKeys) {
		t.Fatalf("JSON keys = %v", got)
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("JSON is missing %q: %s", key, data)
		}
	}
}
