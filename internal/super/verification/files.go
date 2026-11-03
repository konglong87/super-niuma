package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxWorkspaceDepth = 128

type snapshotFailure struct{ code string }

func (e snapshotFailure) Error() string { return e.code }
func fileFailure(code string) error     { return snapshotFailure{code: code} }

// Walk using directory handles. No symlink is accepted in the workspace path,
// even when that link happens to point back into the same directory tree.
func openWorkspace(ctx context.Context, workspace string) (*os.Root, error) {
	if !secureOpenSupported {
		return nil, ErrUnsupportedPlatform
	}
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || unsafeText(workspace) {
		return nil, ErrInvalidWorkspace
	}
	anchor := filepath.VolumeName(workspace) + string(filepath.Separator)
	if workspace == anchor {
		return nil, ErrInvalidWorkspace
	}
	parts := strings.Split(strings.TrimPrefix(workspace, anchor), string(filepath.Separator))
	if len(parts) > maxWorkspaceDepth {
		return nil, ErrInvalidWorkspace
	}
	current, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, ErrInvalidWorkspace
	}
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			current.Close()
			return nil, err
		}
		if part == "" || reservedComponent(part) {
			current.Close()
			return nil, ErrInvalidWorkspace
		}
		next, err := openChildDirectory(current, part)
		current.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidWorkspace, failureCode(err))
		}
		current = next
	}
	return current, nil
}

func openChildDirectory(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, fileFailure(failureCode(err))
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fileFailure(CodeSymlink)
	}
	if !before.IsDir() {
		return nil, fileFailure(CodeNotRegular)
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fileFailure(failureCode(err))
	}
	opened, statErr := child.Stat(".")
	after, pathErr := parent.Lstat(name)
	if statErr != nil || pathErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		child.Close()
		return nil, fileFailure(CodeChanged)
	}
	return child, nil
}

func readSnapshot(ctx context.Context, root *os.Root, rule Rule) ([]byte, error) {
	parts := strings.Split(rule.Path, "/")
	parent := root
	defer func() {
		if parent != root {
			parent.Close()
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next, err := openChildDirectory(parent, part)
		if err != nil {
			return nil, err
		}
		if parent != root {
			parent.Close()
		}
		parent = next
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := parts[len(parts)-1]
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, fileFailure(failureCode(err))
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fileFailure(CodeSymlink)
	}
	if !before.Mode().IsRegular() {
		return nil, fileFailure(CodeNotRegular)
	}
	limit := effectiveMax(rule)
	if before.Size() > limit {
		return nil, fileFailure(CodeTooLarge)
	}
	file, err := parent.OpenFile(name, secureReadFlags, 0)
	if err != nil {
		return nil, fileFailure(failureCode(err))
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fileFailure(CodeReadFailed)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fileFailure(CodeChanged)
	}
	if opened.Size() > limit {
		return nil, fileFailure(CodeTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, limit+1))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fileFailure(CodeTooLarge)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, fileFailure(CodeReadFailed)
	}
	pathInfo, err := parent.Lstat(name)
	if err != nil {
		return nil, fileFailure(CodeChanged)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fileFailure(CodeSymlink)
	}
	if !os.SameFile(opened, after) || !os.SameFile(after, pathInfo) || opened.Size() != int64(len(data)) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || pathInfo.Size() != after.Size() || !pathInfo.ModTime().Equal(after.ModTime()) {
		return nil, fileFailure(CodeChanged)
	}
	return data, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	const readChunkBytes = 32 << 10
	if len(p) > readChunkBytes {
		p = p[:readChunkBytes]
	}
	return r.reader.Read(p)
}
func failureCode(err error) string {
	var failure snapshotFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	if errors.Is(err, os.ErrNotExist) {
		return CodeMissing
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CodeCancelled
	}
	return CodeReadFailed
}
