package gitutil

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

type DiffOptions struct {
	Stat   bool
	Cached bool
}

func Branch(ctx context.Context, cwd string) (string, error) {
	out, err := git(ctx, cwd, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func StatusShort(ctx context.Context, cwd string) (string, error) {
	out, err := git(ctx, cwd, "status", "--short")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func Diff(ctx context.Context, cwd string, stat bool) (string, error) {
	return DiffWithOptions(ctx, cwd, DiffOptions{Stat: stat})
}

func DiffWithOptions(ctx context.Context, cwd string, opts DiffOptions) (string, error) {
	args := []string{"diff"}
	if opts.Cached {
		args = append(args, "--cached")
	}
	if opts.Stat {
		args = append(args, "--stat")
	}
	return git(ctx, cwd, args...)
}

func git(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
