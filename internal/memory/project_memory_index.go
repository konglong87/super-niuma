package memory

import (
	"path/filepath"
	"regexp"
	"strings"
)

var projectMemoryLinkRE = regexp.MustCompile(`\[([^\]]+)\]\(([^)#]+\.md)\)`)

// projectMemoryIndexHints binds each label and trailing hook to its own link.
// Alias hints stay separate so duplicate links cannot amplify a weak match.
// The caller reads each normalized path once and performs the rooted file open.
func projectMemoryIndexHints(content string) map[string][]string {
	entries := make(map[string][]string)
	for _, line := range strings.Split(stripHTMLComments(content), "\n") {
		matches := projectMemoryLinkRE.FindAllStringSubmatchIndex(line, -1)
		for i, match := range matches {
			relative := filepath.Clean(strings.TrimSpace(line[match[4]:match[5]]))
			if relative == "." || relative == "MEMORY.md" || !filepath.IsLocal(relative) {
				continue
			}
			hookEnd := len(line)
			if i+1 < len(matches) {
				hookEnd = matches[i+1][0]
			}
			label := line[match[2]:match[3]]
			hook := line[match[1]:hookEnd]
			entries[relative] = append(entries[relative], label+" "+hook)
		}
	}
	return entries
}
