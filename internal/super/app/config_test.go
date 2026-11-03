package app

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/super"
)

func TestOptionsRejectNonLoopbackAndUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{{"--listen", "0.0.0.0:7391"}, {"--listen", "example.org:7391"}, {"--listen", "localhost:7391"}, {"extra"}, {"--state-dir", ""}} {
		if _, err := parseOptions(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
}

func TestOptionsPreserveIsolationAndAllowEphemeralPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	opts, err := parseOptions([]string{"--listen", "127.0.0.1:0"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Address != "127.0.0.1:0" || opts.StateDirectory != defaultStateDirectory() {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if opts.StateDirectory == "" {
		t.Fatal("empty isolated state directory")
	}
}

func TestHelpDoesNotInitializeRuntime(t *testing.T) {
	var output bytes.Buffer
	if _, err := parseOptions([]string{"--help"}, &output); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help: %v", err)
	}
	if output.Len() == 0 {
		t.Fatal("missing help")
	}
}

func TestOptionsUseSuperStateOverrideWithoutCreatingIt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "native-acceptance")
	t.Setenv(super.StateDirectoryEnv, directory)
	opts, err := parseOptions(nil, &bytes.Buffer{})
	if err != nil || opts.StateDirectory != directory {
		t.Fatalf("override options = %+v, %v", opts, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("options created state: %v", err)
	}
	explicit := filepath.Join(t.TempDir(), "explicit")
	opts, err = parseOptions([]string{"--state-dir", explicit}, &bytes.Buffer{})
	if err != nil || opts.StateDirectory != explicit {
		t.Fatalf("explicit flag did not override environment: %+v, %v", opts, err)
	}
}

func TestOptionsRejectInvalidStateOverride(t *testing.T) {
	for _, value := range []string{"", "relative-state"} {
		t.Setenv(super.StateDirectoryEnv, value)
		if _, err := parseOptions(nil, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid override %q", value)
		}
		if opts, err := parseOptions([]string{"--version"}, &bytes.Buffer{}); err != nil || !opts.Version {
			t.Fatalf("version consulted state: %+v, %v", opts, err)
		}
	}
}
