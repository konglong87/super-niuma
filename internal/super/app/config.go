package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"

	"github.com/konglong87/super-niuma/internal/super"
)

const (
	DefaultAddress     = "127.0.0.1:7391"
	StateDirectoryName = super.StateDirectoryName
	DatabaseName       = super.DatabaseName
	Version            = "0.1.0-intake"
)

type options struct {
	Address        string
	StateDirectory string
	Version        bool
}

func defaultStateDirectory() string {
	directory, _ := super.StateDirectory()
	return directory
}

func parseOptions(args []string, output io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("super-niuma", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.Address, "listen", DefaultAddress, "loopback HTTP address (execution is disabled)")
	flags.StringVar(&opts.StateDirectory, "state-dir", defaultStateDirectory(), "isolated Super data directory; never reads legacy sessions")
	flags.BoolVar(&opts.Version, "version", false, "print version without opening state")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if flags.NArg() != 0 {
		return opts, errors.New("super-niuma accepts flags only")
	}
	if opts.Version {
		return opts, nil
	}
	if err := validateAddress(opts.Address); err != nil {
		return opts, err
	}
	if opts.StateDirectory == "" {
		return opts, errors.New("a Super state directory is required")
	}
	absolute, err := filepath.Abs(opts.StateDirectory)
	if err != nil {
		return opts, errors.New("invalid Super state directory")
	}
	opts.StateDirectory = absolute
	return opts, nil
}

func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("listen address must include a loopback IP and port")
	}
	if host != "127.0.0.1" && host != "::1" {
		return errors.New("Super intake only binds to 127.0.0.1 or ::1; remote serving is not supported")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("invalid listen port")
	}
	return nil
}
