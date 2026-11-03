package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type WatchEvent struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Source    Source `json:"source"`
	Plugin    string `json:"plugin,omitempty"`
	Operation string `json:"operation"`
}

func Watch(ctx context.Context, cwd string) (<-chan WatchEvent, <-chan error, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}
	roots := normalizedDiscoveryRoots(cwd)
	for _, root := range roots {
		if err := watchRoot(watcher, root.path); err != nil {
			_ = watcher.Close()
			return nil, nil, err
		}
	}
	events := make(chan WatchEvent, 32)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
					errs <- err
				}
				return
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				errs <- err
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Create) {
					_ = watchRoot(watcher, event.Name)
				}
				watchEvent, ok := classifyWatchEvent(roots, event)
				if !ok {
					continue
				}
				select {
				case events <- watchEvent:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return events, errs, nil
}

func normalizedDiscoveryRoots(cwd string) []discoveryRoot {
	roots := discoveryRoots(cwd)
	out := make([]discoveryRoot, 0, len(roots))
	for _, root := range roots {
		path, err := filepath.Abs(root.path)
		if err == nil {
			root.path = filepath.Clean(path)
		}
		out = append(out, root)
	}
	return out
}

func watchRoot(watcher *fsnotify.Watcher, root string) error {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
		return nil
	})
}

func classifyWatchEvent(roots []discoveryRoot, event fsnotify.Event) (WatchEvent, bool) {
	if event.Op == 0 {
		return WatchEvent{}, false
	}
	path, err := filepath.Abs(event.Name)
	if err == nil {
		path = filepath.Clean(path)
	} else {
		path = filepath.Clean(event.Name)
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root.path, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			continue
		}
		base := filepath.Base(path)
		switch {
		case root.legacy && strings.EqualFold(filepath.Ext(base), ".md"):
			name := strings.TrimSuffix(base, filepath.Ext(base))
			return WatchEvent{Name: qualifyPluginName(root, name), Path: path, Source: root.source, Plugin: root.plugin, Operation: event.Op.String()}, true
		case !root.legacy && strings.EqualFold(base, "SKILL.md"):
			name := filepath.Base(filepath.Dir(path))
			return WatchEvent{Name: qualifyPluginName(root, name), Path: path, Source: root.source, Plugin: root.plugin, Operation: event.Op.String()}, true
		}
	}
	return WatchEvent{}, false
}

func qualifyPluginName(root discoveryRoot, name string) string {
	if root.plugin == "" || strings.Contains(name, ":") {
		return name
	}
	return root.plugin + ":" + name
}
