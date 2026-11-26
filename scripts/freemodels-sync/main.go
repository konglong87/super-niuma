// freemodels-sync copies the latest free-provider catalog snapshot from the
// free-AI-free-token data set into internal/super/freemodels/catalog.json.
//
// Usage (from the repository root):
//
//	go run ./scripts/freemodels-sync [path/to/desktop-catalog.json]
//
// The default source is $FREE_AI_FREE_TOKEN_DIR/build/desktop-catalog.json,
// falling back to ../free-AI-free-token/build/desktop-catalog.json. The
// snapshot is validated with the same loader the server uses before the
// embedded file is replaced, so an invalid snapshot never lands.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/konglong87/super-niuma/internal/super/freemodels"
)

const destination = "internal/super/freemodels/catalog.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "freemodels-sync:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: go run ./scripts/freemodels-sync [source.json]")
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run from the repository root: %w", err)
	}
	src := defaultSource()
	if len(args) == 1 {
		src = args[0]
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	catalog, err := freemodels.Parse(data)
	if err != nil {
		return fmt.Errorf("%s is not a valid catalog: %w", src, err)
	}
	tmp := destination + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, destination); err != nil {
		os.Remove(tmp)
		return err
	}
	fmt.Printf("synced %d providers (generated_at %s) from %s\n", len(catalog.Providers), catalog.GeneratedAt, src)
	return nil
}

func defaultSource() string {
	dir := os.Getenv("FREE_AI_FREE_TOKEN_DIR")
	if dir == "" {
		dir = filepath.Join("..", "free-AI-free-token")
	}
	return filepath.Join(dir, "build", "desktop-catalog.json")
}
