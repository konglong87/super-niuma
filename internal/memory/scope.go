package memory

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The `paths:` / `excludes:` frontmatter scopes a memory document to the files a
// turn works on. It used to be evaluated with strings.Contains against the raw
// prompt, which meant a document scoped to `internal/memory/**` only loaded when
// the user literally typed "internal/memory", loaded for any prose containing
// that substring, and could never honour a wildcard in the middle of a pattern.
//
// The file set is what is knowable when the prompt is assembled: the paths named
// in the prompt itself. Files a turn only discovers later, through tool calls, do
// not exist yet -- the frontmatter is evaluated once, before the model runs. When
// no path is identifiable the scope is unknown, and scoped documents still load.

// turnFileCandidates builds the match forms for this turn's files. Absolute
// paths also contribute their cwd-relative form so repo-relative patterns match.
func turnFileCandidates(cwd, prompt string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(value string) {
		value = normalizeCandidate(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, candidate := range promptFileCandidates(prompt) {
		add(candidate)
		if rel := relativeToCWD(cwd, candidate); rel != "" {
			add(rel)
		}
	}
	return out
}

func relativeToCWD(cwd, file string) string {
	if strings.TrimSpace(cwd) == "" || !filepath.IsAbs(file) {
		return ""
	}
	rel, err := filepath.Rel(cwd, file)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
}

func normalizeCandidate(value string) string {
	value = strings.TrimSpace(filepath.ToSlash(value))
	value = strings.TrimPrefix(value, "./")
	return strings.TrimRight(value, "/")
}

// promptSplitters are the characters that cannot be part of a path reference in
// prose: quoting, bracketing and list punctuation.
var promptSplitters = " \t\r\n\"'`()[]{}<>,;|*=" + "、。，；（）「」“”"

var promptFileExtension = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)

var promptLineSuffix = regexp.MustCompile(`:\d+(?::\d+)?$`)

// promptFileCandidates pulls path-looking tokens out of a prompt. A token counts
// when it contains a slash or ends in a file extension.
func promptFileCandidates(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, token := range strings.FieldsFunc(filepath.ToSlash(prompt), func(r rune) bool {
		return strings.ContainsRune(promptSplitters, r)
	}) {
		token = strings.TrimLeft(token, "@#-")
		token = promptLineSuffix.ReplaceAllString(token, "")
		token = strings.TrimRight(token, ".,;:!?")
		token = normalizeCandidate(token)
		if token == "" || seen[token] || strings.Contains(token, "://") {
			continue
		}
		if !strings.Contains(token, "/") && !promptFileExtension.MatchString(token) {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

// pathMatchesPattern reports whether a file path is in scope for a frontmatter
// pattern. `**` crosses directory separators, every other segment uses
// path.Match, and a pattern with no wildcards also matches its whole subtree so
// `paths: [internal/memory]` keeps covering the directory.
func pathMatchesPattern(pattern, value string) bool {
	pattern = normalizeCandidate(pattern)
	value = normalizeCandidate(value)
	if pattern == "" || value == "" {
		return false
	}
	if pattern == "**" {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return value == pattern || strings.HasPrefix(value, pattern+"/")
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(value, "/"))
}

func matchSegments(pattern, value []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(value); i++ {
				if matchSegments(pattern[1:], value[i:]) {
					return true
				}
			}
			return false
		}
		if len(value) == 0 {
			return false
		}
		if ok, err := path.Match(pattern[0], value[0]); err != nil || !ok {
			return false
		}
		pattern, value = pattern[1:], value[1:]
	}
	return len(value) == 0
}
