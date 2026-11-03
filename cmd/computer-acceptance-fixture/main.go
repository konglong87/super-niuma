// computer-acceptance-fixture serves the isolated, in-memory event target.
// It does not operate the desktop. Keep its connection file outside Git.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/konglong87/super-niuma/internal/computeracceptance"
)

func run(ctx context.Context, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("--connection-file must be an absolute private path")
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("connection directory must have mode 0700")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	fixture, err := computeracceptance.StartFixture()
	if err != nil {
		file.Close()
		return err
	}
	defer fixture.Close()
	err = json.NewEncoder(file).Encode(map[string]string{"url": fixture.URL()})
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Println("Fixture ready; connection capability written to private file.")
	<-ctx.Done()
	return nil
}
func main() {
	path := flag.String("connection-file", "", "absolute file in an existing mode-0700 directory")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
