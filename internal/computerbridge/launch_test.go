package computerbridge

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLaunchConfigPipeRoundTrip(t *testing.T) {
	cfg := Config{SocketPath: "/private/test/socket", Token: testToken}
	file, err := NewLaunchFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := readLaunchConfig(file)
	if err != nil || got.SocketPath != cfg.SocketPath || got.Token != cfg.Token {
		t.Fatal("launch config mismatch", err)
	}
}
func TestLaunchConfigStrictBounded(t *testing.T) {
	for _, data := range []string{`{}`, `{"socket_path":"relative","token":"test"}`, `{"socket_path":"/x","token":"test","approved":true}`, strings.Repeat("x", maxLaunchBytes+1)} {
		if _, err := readLaunchConfig(strings.NewReader(data)); err == nil {
			t.Fatal("invalid launch accepted")
		}
	}
}
func TestLaunchConfigInheritedChild(t *testing.T) {
	const childMarker = "NIUMA_TEST_COMPUTER_LAUNCH_CHILD"
	if os.Getenv(childMarker) == "1" {
		cfg, err := ConsumeLaunchConfig(true)
		if err != nil || cfg == nil || cfg.Token != testToken || cfg.SocketPath != "/private/test/socket" {
			os.Exit(21)
		}
		if _, exists := os.LookupEnv(LaunchFDEnv); exists {
			os.Exit(22)
		}
		os.Exit(0)
	}
	file, err := NewLaunchFile(Config{SocketPath: "/private/test/socket", Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLaunchConfigInheritedChild$")
	cmd.Env = append(os.Environ(), childMarker+"=1", LaunchFDEnv+"="+LaunchFD)
	cmd.ExtraFiles = []*os.File{file}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child failed %v %s", err, out)
	}
}
