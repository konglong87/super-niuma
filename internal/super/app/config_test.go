package app

import (
	"bytes"
	"errors"
	"flag"
	"testing"
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
