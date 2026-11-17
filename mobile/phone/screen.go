// Package phone is the on-phone side of a Super attempt that operates the
// phone: it turns the accessibility snapshot into the screen the model sees,
// enforces the task's app scope and risk rules, holds risky steps for the
// person's approval, records every step, and runs tasks end to end. The
// Android app supplies a Bridge; nothing here imports Android.
package phone

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Node is one accessibility element. Parent is the index of its parent in
// the same snapshot, -1 for the window root.
type Node struct {
	Parent    int    `json:"parent"`
	Class     string `json:"cls"`
	Text      string `json:"text,omitempty"`
	Desc      string `json:"desc,omitempty"`
	ViewID    string `json:"rid,omitempty"`
	Bounds    [4]int `json:"b"`
	Clickable bool   `json:"click,omitempty"`
	Editable  bool   `json:"edit,omitempty"`
	Scrolls   bool   `json:"scroll,omitempty"`
	Password  bool   `json:"pwd,omitempty"`
	Checked   bool   `json:"checked,omitempty"`
}

// Snapshot is the active window as the app's accessibility service read it.
// Home is the package of the phone's home screen.
type Snapshot struct {
	Package string `json:"package"`
	Home    string `json:"home,omitempty"`
	Nodes   []Node `json:"nodes"`
}

// ErrNoWindow means no readable window was available.
var ErrNoWindow = errors.New("no readable window")

// ParseSnapshot validates a snapshot from the bridge.
func ParseSnapshot(data string) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	if s.Package == "" || len(s.Nodes) == 0 {
		return Snapshot{}, ErrNoWindow
	}
	for i, n := range s.Nodes {
		if n.Parent < -1 || n.Parent >= i {
			return Snapshot{}, fmt.Errorf("snapshot: node %d has an invalid parent", i)
		}
	}
	return s, nil
}

// Label is what the person would read on the element: its own text or
// description, or for an unlabelled control the text of its first labelled
// descendants (a button whose words sit in a child view).
func (s Snapshot) Label(i int) string {
	n := s.Nodes[i]
	if l := clean(n.Text); l != "" {
		return l
	}
	if l := clean(n.Desc); l != "" {
		return l
	}
	if !n.Clickable && !n.Editable {
		return ""
	}
	var parts []string
	for j := i + 1; j < len(s.Nodes) && len(parts) < 3; j++ {
		if !s.descendant(j, i) {
			continue
		}
		if l := clean(s.Nodes[j].Text); l != "" {
			parts = append(parts, l)
		} else if l := clean(s.Nodes[j].Desc); l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " · ")
}

func (s Snapshot) descendant(j, ancestor int) bool {
	for p := s.Nodes[j].Parent; p >= 0; p = s.Nodes[p].Parent {
		if p == ancestor {
			return true
		}
	}
	return false
}

// usable reports whether the model needs to see the element.
func (s Snapshot) usable(i int) bool {
	n := s.Nodes[i]
	return n.Clickable || n.Editable || n.Scrolls || clean(n.Text) != "" || clean(n.Desc) != ""
}

// Find returns the index of the element in s that is the same element as
// other's element at index i: same class, label, view id and bounds, or -1.
func (s Snapshot) Find(other Snapshot, i int) int {
	if i < 0 || i >= len(other.Nodes) || s.Package != other.Package {
		return -1
	}
	want, label := other.Nodes[i], other.Label(i)
	for j, n := range s.Nodes {
		if n.Class == want.Class && n.ViewID == want.ViewID && n.Bounds == want.Bounds && s.Label(j) == label {
			return j
		}
	}
	return -1
}

const maxScreenLines = 160

// Render is the screen text the model reads. Refs are node indexes.
func (s Snapshot) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "app: %s\n", s.Package)
	shown, hidden := 0, 0
	for i, n := range s.Nodes {
		if !s.usable(i) {
			continue
		}
		if shown == maxScreenLines {
			hidden++
			continue
		}
		shown++
		kind := n.Class[strings.LastIndex(n.Class, ".")+1:]
		label := s.Label(i)
		if n.Password {
			label = "(password field)"
		}
		fmt.Fprintf(&b, "[%d] %s %q @%d,%d", i, kind, truncate(label, 80), (n.Bounds[0]+n.Bounds[2])/2, (n.Bounds[1]+n.Bounds[3])/2)
		for _, f := range []struct {
			on   bool
			name string
		}{{n.Clickable, "click"}, {n.Editable, "edit"}, {n.Scrolls, "scroll"}, {n.Checked, "checked"}} {
			if f.on {
				b.WriteString(" " + f.name)
			}
		}
		b.WriteByte('\n')
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "… %d more elements not shown; scroll or narrow down\n", hidden)
	}
	return b.String()
}

// Contains reports whether any element on the screen shows text.
func (s Snapshot) Contains(text string) bool {
	text = strings.ToLower(clean(text))
	if text == "" {
		return false
	}
	for _, n := range s.Nodes {
		if strings.Contains(strings.ToLower(clean(n.Text)), text) || strings.Contains(strings.ToLower(clean(n.Desc)), text) {
			return true
		}
	}
	return false
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
