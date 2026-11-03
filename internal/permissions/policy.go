package permissions

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/konglong87/super-niuma/internal/config"
)

type Policy struct {
	Allow       []string
	Deny        []string
	AlwaysAsk   []string
	DefaultMode string
	Source      string
	Preference  string
	AutoMode    bool
	Bypass      bool
	RuleSources map[string]string
}

type Decision struct {
	Allowed bool
	Reason  string
	Rule    string
	Request string
	Source  string
}

func FromSettings(settings config.PermissionSettings) Policy {
	rawMode := strings.TrimSpace(settings.DefaultMode)
	mode, ok := NormalizeMode(settings.DefaultMode)
	if !ok {
		mode = settings.DefaultMode
	}
	if mode == "" {
		mode = "allow"
	}
	return Policy{
		Allow:       append([]string(nil), settings.Allow...),
		Deny:        append([]string(nil), settings.Deny...),
		AlwaysAsk:   append(append([]string(nil), settings.Ask...), settings.AlwaysAsk...),
		DefaultMode: mode,
		Source:      strings.TrimSpace(settings.Source),
		Preference:  strings.TrimSpace(settings.Preference),
		AutoMode:    strings.EqualFold(rawMode, "auto"),
		Bypass:      settings.Bypass,
		RuleSources: copyMap(settings.RuleSources),
	}
}

func (p Policy) Check(toolName string) Decision {
	return p.CheckRequest(toolName, nil)
}

func (p Policy) CheckRequest(toolName string, input json.RawMessage) Decision {
	request := requestQualifier(toolName, input)
	// Deny rules are evaluated before the bypass short-circuit so an explicit
	// deny stays in force even under --dangerously-skip-permissions.
	if rule, ok := matchAny(p.Deny, toolName, request); ok {
		return Decision{Allowed: false, Reason: fmt.Sprintf("%s is denied by settings", toolName), Rule: rule, Request: request, Source: p.ruleSource("deny", rule)}
	}
	if p.Bypass {
		return Decision{Allowed: true, Rule: "*", Request: request, Source: "bypass"}
	}
	if rule, ok := matchAny(p.AlwaysAsk, toolName, request); ok {
		return Decision{Allowed: false, Reason: fmt.Sprintf("%s requires permission approval by alwaysAsk rule", toolName), Rule: rule, Request: request, Source: firstNonEmpty(p.Preference, p.ruleSource("alwaysAsk", rule))}
	}
	if rule, ok := matchBestAllow(p.Allow, toolName, request); ok {
		if risk := ClassifyRequestRisk(toolName, request, input); risk.RequiresPrompt() && isShellTool(toolName) && ClassifyRuleRisk(rule).Wildcard {
			return Decision{Allowed: false, Reason: fmt.Sprintf("%s requires permission approval for %s request matched by broad allow rule", toolName, risk.Reason), Rule: rule, Request: request, Source: firstNonEmpty(p.Preference, p.ruleSource("allow", rule))}
		}
		return Decision{Allowed: true, Rule: rule, Request: request, Source: p.ruleSource("allow", rule)}
	}
	switch p.DefaultMode {
	case ModeDeny:
		return Decision{Allowed: false, Reason: fmt.Sprintf("%s is not in allow list", toolName), Request: request, Source: p.ruleSource("defaultMode", "")}
	case ModeAsk:
		return p.askForMutatingTools(toolName, request, input)
	case ModeAcceptEdits:
		// acceptEdits only auto-approves file edits. Every other tool — Bash,
		// PowerShell, MCP tools — keeps the interactive ask flow.
		if !IsFileEditTool(toolName) {
			return p.askForMutatingTools(toolName, request, input)
		}
		return p.allowUnlessRisky(toolName, request, input)
	case ModeBypassPermissions:
		// bypassPermissions as a *default mode* only relaxes the fallback to the
		// permissive branch. The real short-circuit above requires Policy.Bypass,
		// which only the CLI flag and settings-level bypass can set.
		return p.allowUnlessRisky(toolName, request, input)
	default:
		return p.allowUnlessRisky(toolName, request, input)
	}
}

// askForMutatingTools implements the "ask" fallback: mutating tools always prompt,
// everything else is allowed unless the risk classifier objects.
func (p Policy) askForMutatingTools(toolName, request string, input json.RawMessage) Decision {
	if IsMutatingTool(toolName) {
		return Decision{Allowed: false, Reason: fmt.Sprintf("%s requires permission approval in interactive mode", toolName), Request: request, Source: firstNonEmpty(p.Preference, p.ruleSource("defaultMode", ""))}
	}
	return p.allowUnlessRisky(toolName, request, input)
}

// allowUnlessRisky implements the permissive fallback: allow unless the request
// risk classifier flags the call as dangerous or sensitive.
func (p Policy) allowUnlessRisky(toolName, request string, input json.RawMessage) Decision {
	if risk := ClassifyRequestRisk(toolName, request, input); risk.RequiresPrompt() {
		return Decision{Allowed: false, Reason: fmt.Sprintf("%s requires permission approval for %s request", toolName, risk.Reason), Request: request, Source: firstNonEmpty(p.Preference, p.ruleSource("defaultMode", ""))}
	}
	return Decision{Allowed: true, Request: request, Source: p.ruleSource("defaultMode", "")}
}

func (p Policy) ruleSource(kind, rule string) string {
	if p.RuleSources != nil {
		if source := strings.TrimSpace(p.RuleSources[ruleSourceKey(kind, rule)]); source != "" {
			return source
		}
	}
	return strings.TrimSpace(p.Source)
}

func ruleSourceKey(kind, rule string) string {
	if strings.TrimSpace(rule) == "" {
		return strings.TrimSpace(kind)
	}
	return strings.TrimSpace(kind) + ":" + strings.TrimSpace(rule)
}

func copyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type RequestRisk struct {
	Level  string
	Reason string
}

func (r RequestRisk) RequiresPrompt() bool {
	return r.Level == "high" || r.Level == "sensitive"
}

type RuleRisk struct {
	Broad    bool
	Wildcard bool
	Reason   string
}

func ClassifyRuleRisk(rule string) RuleRisk {
	tool, qualifier := splitRule(strings.TrimSpace(rule))
	if tool == "" {
		return RuleRisk{}
	}
	if tool == "*" || qualifier == "*" {
		return RuleRisk{Broad: true, Wildcard: true, Reason: "wildcard tool rule"}
	}
	if qualifier == "" {
		return RuleRisk{Broad: true, Reason: "wildcard tool rule"}
	}
	if strings.HasSuffix(tool, "*") || strings.Contains(tool, "*") {
		return RuleRisk{Broad: true, Wildcard: true, Reason: "wildcard tool rule"}
	}
	qualifier = strings.TrimSpace(qualifier)
	if qualifier == "" {
		return RuleRisk{Broad: true, Reason: "wildcard qualifier rule"}
	}
	if qualifier == "*" || strings.HasSuffix(qualifier, "*") || strings.Contains(qualifier, "*") {
		return RuleRisk{Broad: true, Wildcard: true, Reason: "wildcard qualifier rule"}
	}
	return RuleRisk{}
}

func ClassifyRequestRisk(toolName, qualifier string, input json.RawMessage) RequestRisk {
	toolName = strings.TrimSpace(toolName)
	qualifier = strings.TrimSpace(qualifier)
	if qualifier == "" {
		qualifier = requestQualifier(toolName, input)
	}
	if isSensitiveQualifier(qualifier) {
		return RequestRisk{Level: "sensitive", Reason: "sensitive path"}
	}
	switch toolName {
	case "Bash":
		return withRiskPrefix(classifyShellScript(qualifier), "dangerous shell command")
	case "PowerShell":
		return withRiskPrefix(powerShellRisk(qualifier), "dangerous PowerShell command")
	}
	return RequestRisk{}
}

func withRiskPrefix(risk RequestRisk, prefix string) RequestRisk {
	if !risk.RequiresPrompt() || strings.Contains(strings.ToLower(risk.Reason), strings.ToLower(prefix)) {
		return risk
	}
	risk.Reason = prefix + ": " + risk.Reason
	return risk
}

func MatchAnyRule(patterns []string, toolName string, input json.RawMessage) bool {
	request := requestQualifier(toolName, input)
	_, ok := matchAny(patterns, toolName, request)
	return ok
}

func FormatRule(toolName, qualifier string) string {
	toolName = strings.TrimSpace(toolName)
	qualifier = strings.TrimSpace(qualifier)
	if qualifier == "" {
		return toolName
	}
	return toolName + "(" + escapeRuleContent(qualifier) + ")"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Canonical permission modes returned by NormalizeMode and stored in
// Policy.DefaultMode.
const (
	ModeAllow             = "allow"
	ModeAsk               = "ask"
	ModeDeny              = "deny"
	ModeAcceptEdits       = "acceptEdits"
	ModeBypassPermissions = "bypassPermissions"
)

// AcceptedModes 列出 NormalizeMode 接受的全部拼写，供 CLI 在拒绝一个
// 非法模式时把合法值直接摆出来 —— 只回 "invalid permission mode: X"
// 等于让用户去翻源码。顺序即文档顺序。
func AcceptedModes() []string {
	return []string{
		ModeAllow, ModeAsk, ModeDeny, ModeAcceptEdits, ModeBypassPermissions,
		"auto", "plan", "default", "delegate",
		"accept-edits", "bypass-permissions", "bypass", "dontAsk", "dont-ask",
	}
}

func NormalizeMode(mode string) (string, bool) {
	switch strings.TrimSpace(mode) {
	case "":
		return "", true
	// auto keeps the permissive fallback; the risk classifier still gates it.
	case ModeAllow, "auto":
		return ModeAllow, true
	case ModeAcceptEdits, "accept-edits":
		return ModeAcceptEdits, true
	case ModeBypassPermissions, "bypass-permissions", "bypass":
		return ModeBypassPermissions, true
	// Upstream "default" prompts on first use of each tool, and "delegate"
	// hands the decision to the user; both are the ask flow, not allow.
	case ModeAsk, "plan", "default", "delegate":
		return ModeAsk, true
	case ModeDeny, "dontAsk", "dont-ask":
		return ModeDeny, true
	default:
		return "", false
	}
}

// ModeGrantsBypass reports whether a raw permission mode explicitly asks for the
// full bypass of --dangerously-skip-permissions. Only the bypassPermissions
// family qualifies: allow, auto and acceptEdits keep the alwaysAsk rules and the
// request risk classifier in effect.
func ModeGrantsBypass(mode string) bool {
	normalized, ok := NormalizeMode(mode)
	return ok && normalized == ModeBypassPermissions
}

// IsFileEditTool reports whether a tool only writes files, which is the set the
// acceptEdits mode auto-approves.
func IsFileEditTool(name string) bool {
	switch name {
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return true
	default:
		return false
	}
}

func IsMutatingTool(name string) bool {
	if strings.HasPrefix(name, "mcp__") {
		return true
	}
	switch name {
	case "Bash", "PowerShell", "Write", "Edit", "MultiEdit", "NotebookEdit", "TodoWrite":
		return true
	default:
		return false
	}
}

func matchAny(patterns []string, toolName, qualifier string) (string, bool) {
	for _, pattern := range patterns {
		if matchRule(pattern, toolName, qualifier) {
			return pattern, true
		}
	}
	return "", false
}

func matchBestAllow(patterns []string, toolName, qualifier string) (string, bool) {
	firstBroad := ""
	for _, pattern := range patterns {
		if !matchRule(pattern, toolName, qualifier) {
			continue
		}
		if !ClassifyRuleRisk(pattern).Broad {
			return pattern, true
		}
		if firstBroad == "" {
			firstBroad = pattern
		}
	}
	if firstBroad != "" {
		return firstBroad, true
	}
	return "", false
}

func matchRule(pattern, toolName, qualifier string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	toolPattern, qualifierPattern := splitRule(pattern)
	if !matchText(toolPattern, toolName) {
		return false
	}
	if qualifierPattern == "" || qualifierPattern == "*" {
		return true
	}
	if matchText(qualifierPattern, qualifier) {
		return true
	}
	return isPathQualifierTool(toolName) && matchPathSubtree(qualifierPattern, qualifier)
}

func splitRule(pattern string) (string, string) {
	toolPattern := pattern
	qualifierPattern := ""
	if before, inside, ok := cutClaudeStyleRule(pattern); ok {
		toolPattern = before
		qualifierPattern = inside
	} else if before, after, ok := strings.Cut(pattern, ":"); ok {
		toolPattern = before
		qualifierPattern = after
	}
	return strings.TrimSpace(toolPattern), strings.TrimSpace(qualifierPattern)
}

func cutClaudeStyleRule(pattern string) (string, string, bool) {
	open := firstUnescaped(pattern, '(')
	if open <= 0 {
		return "", "", false
	}
	close := lastUnescaped(pattern, ')')
	if close <= open || close != len(pattern)-1 {
		return "", "", false
	}
	raw := pattern[open+1 : close]
	if raw == "" || raw == "*" {
		return strings.TrimSpace(pattern[:open]), "", true
	}
	return strings.TrimSpace(pattern[:open]), unescapeRuleContent(raw), true
}

func escapeRuleContent(content string) string {
	content = strings.ReplaceAll(content, `\`, `\\`)
	content = strings.ReplaceAll(content, "(", `\(`)
	content = strings.ReplaceAll(content, ")", `\)`)
	return content
}

func unescapeRuleContent(content string) string {
	content = strings.ReplaceAll(content, `\(`, "(")
	content = strings.ReplaceAll(content, `\)`, ")")
	content = strings.ReplaceAll(content, `\\`, `\`)
	return content
}

func firstUnescaped(value string, char byte) int {
	for i := 0; i < len(value); i++ {
		if value[i] != char {
			continue
		}
		if countPrecedingBackslashes(value, i)%2 == 0 {
			return i
		}
	}
	return -1
}

func lastUnescaped(value string, char byte) int {
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] != char {
			continue
		}
		if countPrecedingBackslashes(value, i)%2 == 0 {
			return i
		}
	}
	return -1
}

func countPrecedingBackslashes(value string, index int) int {
	count := 0
	for i := index - 1; i >= 0 && value[i] == '\\'; i-- {
		count++
	}
	return count
}

func matchText(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == value || pattern == "*" {
		return true
	}
	if ok, _ := path.Match(pattern, value); ok {
		return true
	}
	if strings.HasSuffix(pattern, ":*") && strings.HasPrefix(value, strings.TrimSuffix(pattern, ":*")) {
		return true
	}
	if strings.HasSuffix(pattern, "*") && strings.HasPrefix(value, strings.TrimSuffix(pattern, "*")) {
		return true
	}
	return false
}

// isPathQualifierTool lists the tools whose qualifier is a filesystem path, so a
// rule ending in /** matches the whole subtree. Without this, path.Match applies
// and its * never crosses a /, which silently defeats rules like
// Write(~/.ssh/**) for anything below the first level.
func isPathQualifierTool(toolName string) bool {
	switch toolName {
	case "LS", "Read", "Glob", "Grep",
		"Write", "Edit", "MultiEdit", "NotebookRead", "NotebookEdit",
		"Bash", "PowerShell":
		return true
	default:
		return false
	}
}

func isShellTool(toolName string) bool {
	return toolName == "Bash" || toolName == "PowerShell"
}

func matchPathSubtree(pattern, value string) bool {
	pattern = filepathSlash(strings.TrimSpace(pattern))
	value = filepathSlash(strings.TrimSpace(value))
	if strings.HasSuffix(pattern, "/**") {
		pattern = strings.TrimSuffix(pattern, "/**")
	} else if strings.ContainsAny(pattern, "*?[") {
		return false
	}
	pattern = strings.TrimRight(pattern, "/")
	value = strings.TrimRight(value, "/")
	if pattern == "" || value == "" {
		return false
	}
	return value == pattern || strings.HasPrefix(value, pattern+"/")
}

func requestQualifier(toolName string, input json.RawMessage) string {
	input = json.RawMessage(strings.TrimSpace(string(input)))
	if len(input) == 0 || string(input) == "null" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(input, &obj); err == nil {
		for _, key := range qualifierKeys(toolName) {
			if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return string(input)
}

func qualifierKeys(toolName string) []string {
	if strings.HasPrefix(toolName, "mcp__") {
		return []string{"command", "query", "path", "file_path", "url", "name"}
	}
	switch toolName {
	case "Bash", "PowerShell":
		return []string{"command"}
	case "Read", "Write", "Edit", "MultiEdit", "NotebookRead", "NotebookEdit":
		return []string{"file_path", "path", "notebook_path"}
	case "LS", "Glob", "Grep":
		return []string{"path", "pattern"}
	case "WebFetch":
		return []string{"url"}
	case "Skill":
		return []string{"skill", "name"}
	default:
		return []string{"command", "file_path", "path", "url", "name"}
	}
}

func isSensitiveQualifier(value string) bool {
	value = strings.ToLower(filepathSlash(strings.TrimSpace(value)))
	if value == "" {
		return false
	}
	sensitiveSubstrings := []string{
		"/.ssh/", ".ssh/",
		"/.gnupg/", ".gnupg/",
		"/.aws/", ".aws/",
		"/.kube/", ".kube/",
		"/.docker/config.json", ".docker/config.json",
		"/.config/gcloud/", ".config/gcloud/",
		"/.npmrc", ".npmrc",
		"/.pypirc", ".pypirc",
		"/.netrc", ".netrc",
		"/.git/config", ".git/config",
		"/.git/hooks/", ".git/hooks/",
		"/.niuma/settings", ".niuma/settings",
		"/.niuma-legacy/settings", ".niuma-legacy/settings",
		"/.claude/settings", ".claude/settings",
		"/.claude/skills", ".claude/skills",
		".env", "id_rsa", "id_ed25519", "private_key", "secret", "token", "credential",
	}
	for _, needle := range sensitiveSubstrings {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

type commandRiskPattern struct {
	re     *regexp.Regexp
	reason string
}

// powerShellRiskPatterns stay regex-based: mvdan.cc/sh parses POSIX shell, not
// PowerShell. normalizePowerShellForRisk turns statement separators — including
// newlines, which the previous normalizer collapsed into plain spaces — back
// into `;` so the leading anchor can still match every statement.
var powerShellRiskPatterns = []commandRiskPattern{
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(remove-item|rm|del|erase)\b[^;&|]*(\s-Recurse|\s-r\b)[^;&|]*(\s-Force|\s-f\b)`), "destructive filesystem command"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(clear-disk|initialize-disk|format-volume|new-partition)\b`), "disk or formatting command"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)set-executionpolicy\b`), "execution policy command"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)start-process\b[^;&|]*\s-verb\s+runas\b`), "privileged command"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(stop-service|restart-service|set-service|new-service|sc\.exe)\b`), "system service command"},
	{regexp.MustCompile(`(?i)(invoke-webrequest|iwr|invoke-restmethod|irm)\b[^|;&]*\|\s*(invoke-expression|iex|powershell|pwsh|cmd)\b`), "network download piped to code execution"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(invoke-expression|iex)\b`), "arbitrary shell evaluation"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(powershell|pwsh|cmd)\s+(-enc|-encodedcommand|-command|-c)\b`), "arbitrary shell evaluation"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(python|python3|node|deno|ruby|perl|php)\s+(-c|-e)\s+`), "arbitrary interpreter execution"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(netsh|new-netfirewallrule|set-netfirewallprofile|disable-netfirewallrule)\b`), "network policy command"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)(nc|ncat|netcat)\b[^;&|]*\s-l(\s|p|$)`), "network listener or tunnel"},
	{regexp.MustCompile(`(?i)(^|[;&|]\s*)ssh\b.*\s-[LRD]\s*`), "network listener or tunnel"},
}

func powerShellRisk(command string) RequestRisk {
	command = normalizePowerShellForRisk(command)
	if command == "" {
		return RequestRisk{}
	}
	for _, pattern := range powerShellRiskPatterns {
		if pattern.re.MatchString(command) {
			return RequestRisk{Level: "high", Reason: pattern.reason}
		}
	}
	if strings.Contains(command, ">") && isSensitiveQualifier(command) {
		return RequestRisk{Level: "sensitive", Reason: "sensitive path write"}
	}
	if isLikelyCredentialExfiltration(command) {
		return RequestRisk{Level: "sensitive", Reason: "credential access or exfiltration"}
	}
	return RequestRisk{}
}

func normalizePowerShellForRisk(command string) string {
	command = strings.NewReplacer("\r\n", ";", "\n", ";", "\r", ";").Replace(command)
	command = strings.NewReplacer(`"`, "", `'`, "", "`", "").Replace(command)
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(command))), " ")
}

func normalizeCommandForRisk(command string) string {
	command = strings.NewReplacer(`"`, "", `'`, "", "`", "").Replace(command)
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(command))), " ")
}

func isLikelyCredentialExfiltration(command string) bool {
	if !isSensitiveQualifier(command) {
		return false
	}
	for _, sink := range []string{"curl ", "wget ", "nc ", "ncat ", "netcat ", "scp ", "sftp ", "rsync ", "aws s3 cp", "gh gist create", "pbcopy", "clip.exe"} {
		if strings.Contains(command, sink) {
			return true
		}
	}
	return false
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}
