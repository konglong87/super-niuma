package gitpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/shellcmd"
)

type Operation string

const (
	OperationCommit   Operation = "commit"
	OperationPush     Operation = "push"
	OperationTag      Operation = "tag"
	OperationForceAdd Operation = "force_add"
)

type Effect struct {
	Operation   Operation
	Destructive bool
	Remote      string
	Refs        []string
	Tag         string
	Paths       []string
	Author      string
	Messages    []string
	NonLiteral  bool
}

type Analysis struct {
	Effects    []Effect
	ParseError bool
}

func (a Analysis) Has(operation Operation) bool {
	for _, effect := range a.Effects {
		if effect.Operation == operation {
			return true
		}
	}
	return false
}

type Scope struct {
	Operation   Operation
	Destructive bool
	Remote      string
	Refs        []string
	Tag         string
	Paths       []string
	Author      string
	Messages    []string
}

type Authorization struct {
	Scopes []Scope
}

func (a Authorization) Allows(effect Effect) bool {
	for _, scope := range a.Scopes {
		if scopeAllows(scope, effect) {
			return true
		}
	}
	return false
}

type ViolationKind string

const (
	ViolationSharedState ViolationKind = "shared_state_authorization"
	ViolationDestructive ViolationKind = "destructive_shared_state"
	ViolationForceAdd    ViolationKind = "force_add_ignored_path"
)

type Violation struct {
	Kind      ViolationKind
	Operation Operation
	Reason    string
}

type scopeField string

const (
	scopeFieldDestructive scopeField = "destructive mode"
	scopeFieldRemote      scopeField = "remote"
	scopeFieldRefs        scopeField = "ref"
	scopeFieldTag         scopeField = "tag"
	scopeFieldAuthor      scopeField = "commit author"
	scopeFieldMessages    scopeField = "commit message"
	scopeFieldPaths       scopeField = "force-add paths"
)

func Check(authorization Authorization, analysis Analysis) *Violation {
	for _, effect := range analysis.Effects {
		if effect.Operation != OperationForceAdd || authorization.Allows(effect) {
			continue
		}
		reason := "git add --force requires explicit authorization naming every literal pathspec"
		if effect.NonLiteral {
			reason = "git add --force with indirect, expanded, or implicit pathspecs is not allowed"
		}
		return &Violation{Kind: ViolationForceAdd, Operation: effect.Operation, Reason: reason}
	}
	for _, effect := range analysis.Effects {
		if effect.Operation == OperationForceAdd || !effect.Destructive || authorization.Allows(effect) {
			continue
		}
		return &Violation{Kind: ViolationDestructive, Operation: effect.Operation, Reason: authorizationMismatchReason(authorization, effect)}
	}
	for _, effect := range analysis.Effects {
		if effect.Operation == OperationForceAdd || effect.Destructive || authorization.Allows(effect) {
			continue
		}
		return &Violation{Kind: ViolationSharedState, Operation: effect.Operation, Reason: authorizationMismatchReason(authorization, effect)}
	}
	return nil
}

func authorizationMismatchReason(authorization Authorization, effect Effect) string {
	if effect.NonLiteral {
		return "the attempted git " + string(effect.Operation) + " contains dynamic arguments that cannot match an exact current-turn authorization"
	}
	var closest []scopeField
	for _, scope := range authorization.Scopes {
		if scope.Operation != effect.Operation {
			continue
		}
		fields := scopeMismatchFields(scope, effect)
		if closest == nil || len(fields) < len(closest) {
			closest = fields
		}
	}
	if closest == nil {
		return "the current user turn does not authorize git " + string(effect.Operation)
	}
	verb := "does"
	if len(closest) > 1 {
		verb = "do"
	}
	return "the attempted " + joinScopeFields(closest) + " " + verb + " not match the exact git " + string(effect.Operation) + " scope authorized in the current user turn"
}

func scopeMismatchFields(scope Scope, effect Effect) []scopeField {
	var fields []scopeField
	if effect.Destructive && !scope.Destructive {
		fields = append(fields, scopeFieldDestructive)
	}
	if scope.Remote != "" && scope.Remote != effect.Remote {
		fields = append(fields, scopeFieldRemote)
	}
	if len(scope.Refs) > 0 && !sameStringSet(scope.Refs, effect.Refs) {
		fields = append(fields, scopeFieldRefs)
	}
	if scope.Tag != "" && scope.Tag != effect.Tag {
		fields = append(fields, scopeFieldTag)
	}
	if scope.Author != "" && scope.Author != effect.Author {
		fields = append(fields, scopeFieldAuthor)
	}
	if len(scope.Messages) > 0 && !sameStringSequence(scope.Messages, effect.Messages) {
		fields = append(fields, scopeFieldMessages)
	}
	if effect.Operation == OperationForceAdd && !sameStringSet(scope.Paths, effect.Paths) {
		fields = append(fields, scopeFieldPaths)
	}
	return fields
}

func joinScopeFields(fields []scopeField) string {
	labels := make([]string, len(fields))
	for i, field := range fields {
		labels[i] = string(field)
	}
	if len(labels) < 2 {
		return strings.Join(labels, "")
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
}

func Analyze(command string) Analysis {
	script, err := shellcmd.Parse(command)
	analysis := Analysis{ParseError: err != nil}
	for _, command := range script.Commands {
		if command.Name != "git" {
			continue
		}
		subcommand, args, ok := splitGitSubcommand(command.Args)
		if !ok {
			continue
		}
		switch subcommand {
		case "commit":
			if !hasExactArg(args, "--dry-run") {
				analysis.Effects = append(analysis.Effects, analyzeCommit(args))
			}
		case "push":
			if effect, ok := analyzePush(args, command.Dynamic); ok {
				analysis.Effects = append(analysis.Effects, effect)
			}
		case "tag":
			if effect, ok := analyzeTag(args, command.Dynamic); ok {
				analysis.Effects = append(analysis.Effects, effect)
			}
		case "add":
			if effect, ok := analyzeForceAdd(args, command.Dynamic); ok {
				analysis.Effects = append(analysis.Effects, effect)
			}
		}
	}
	return analysis
}

func analyzeCommit(args []string) Effect {
	effect := Effect{Operation: OperationCommit}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--author" && i+1 < len(args):
			i++
			effect.Author = args[i]
		case strings.HasPrefix(arg, "--author="):
			effect.Author = strings.TrimPrefix(arg, "--author=")
		case (arg == "-m" || arg == "--message") && i+1 < len(args):
			i++
			effect.Messages = append(effect.Messages, args[i])
		case strings.HasPrefix(arg, "--message="):
			effect.Messages = append(effect.Messages, strings.TrimPrefix(arg, "--message="))
		case strings.HasPrefix(arg, "-m") && len(arg) > len("-m"):
			effect.Messages = append(effect.Messages, strings.TrimPrefix(arg, "-m"))
		}
	}
	return effect
}

var gitGlobalOptionsWithValue = map[string]bool{
	"-c": true, "-C": true,
	"--config-env": true, "--exec-path": true, "--git-dir": true,
	"--namespace": true, "--super-prefix": true, "--work-tree": true,
}

func splitGitSubcommand(args []string) (string, []string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 >= len(args) {
				return "", nil, false
			}
			return strings.ToLower(args[i+1]), args[i+2:], true
		}
		if takesGitGlobalOptionValue(arg) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return strings.ToLower(arg), args[i+1:], true
	}
	return "", nil, false
}

func SplitSubcommand(args []string) (string, []string, bool) {
	return splitGitSubcommand(args)
}

func takesGitGlobalOptionValue(arg string) bool {
	if gitGlobalOptionsWithValue[arg] {
		return true
	}
	for option := range gitGlobalOptionsWithValue {
		if strings.HasPrefix(option, "--") && strings.HasPrefix(arg, option+"=") {
			return false
		}
	}
	return false
}

func analyzePush(args []string, dynamic bool) (Effect, bool) {
	effect := Effect{Operation: OperationPush, NonLiteral: dynamic}
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dry-run" || shortPushOptionContains(arg, 'n'):
			return Effect{}, false
		case arg == "--force" || shortPushOptionContains(arg, 'f') || strings.HasPrefix(arg, "--force-with-lease") || strings.HasPrefix(arg, "--force-if-includes"):
			effect.Destructive = true
		case arg == "--delete" || shortPushOptionContains(arg, 'd') || arg == "--mirror" || arg == "--prune":
			effect.Destructive = true
		case pushOptionTakesValue(arg):
			i++
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) > 0 {
		effect.Remote = positional[0]
	}
	if len(positional) > 1 {
		effect.Refs = append([]string(nil), positional[1:]...)
	}
	for _, ref := range effect.Refs {
		if strings.HasPrefix(ref, "+") || strings.HasPrefix(ref, ":") {
			effect.Destructive = true
		}
	}
	return effect, true
}

func shortPushOptionContains(arg string, want rune) bool {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return false
	}
	flags := arg[1:]
	for _, flag := range flags {
		if !strings.ContainsRune("dfnquv46", flag) {
			return false
		}
	}
	return strings.ContainsRune(flags, want)
}

func pushOptionTakesValue(arg string) bool {
	switch arg {
	case "--exec", "--receive-pack", "--repo", "--push-option", "-o":
		return true
	default:
		return false
	}
}

func analyzeTag(args []string, dynamic bool) (Effect, bool) {
	if len(args) == 0 {
		return Effect{}, false
	}
	effect := Effect{Operation: OperationTag, NonLiteral: dynamic}
	readOnly := false
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isTagReadOnlyOption(arg):
			readOnly = true
			if tagOptionTakesValue(arg) && !strings.Contains(arg, "=") {
				i++
			}
		case arg == "-d" || arg == "--delete" || arg == "-f" || arg == "--force":
			effect.Destructive = true
		case tagMutationOptionTakesValue(arg):
			if i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			positional = append(positional, arg)
		}
	}
	if readOnly && !effect.Destructive {
		return Effect{}, false
	}
	if len(positional) == 0 && !effect.Destructive {
		return Effect{}, false
	}
	if len(positional) > 0 {
		effect.Tag = positional[0]
	}
	return effect, true
}

func isTagReadOnlyOption(arg string) bool {
	return arg == "-l" || arg == "--list" || arg == "-v" || arg == "--verify" || arg == "--column" || arg == "--no-column" ||
		strings.HasPrefix(arg, "-n") || strings.HasPrefix(arg, "--points-at") || strings.HasPrefix(arg, "--contains") ||
		strings.HasPrefix(arg, "--merged") || strings.HasPrefix(arg, "--no-merged") || strings.HasPrefix(arg, "--sort") || strings.HasPrefix(arg, "--format")
}

func tagOptionTakesValue(arg string) bool {
	switch arg {
	case "--points-at", "--contains", "--merged", "--no-merged", "--sort", "--format":
		return true
	default:
		return false
	}
}

func tagMutationOptionTakesValue(arg string) bool {
	switch arg {
	case "-m", "--message", "-F", "--file", "-u", "--local-user", "--cleanup":
		return true
	default:
		return false
	}
}

func analyzeForceAdd(args []string, dynamic bool) (Effect, bool) {
	effect := Effect{Operation: OperationForceAdd, NonLiteral: dynamic}
	forced := false
	afterSeparator := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterSeparator {
			effect.Paths = append(effect.Paths, cleanPathspec(arg))
			continue
		}
		switch {
		case arg == "--":
			afterSeparator = true
		case arg == "--force" || shortOptionContains(arg, 'f'):
			forced = true
			if shortOptionContains(arg, 'A') || shortOptionContains(arg, 'a') {
				effect.NonLiteral = true
			}
		case arg == "--all" || arg == "-A" || arg == "--update" || arg == "-u":
			effect.NonLiteral = true
		case arg == "--pathspec-from-file" || strings.HasPrefix(arg, "--pathspec-from-file="):
			effect.NonLiteral = true
			if arg == "--pathspec-from-file" {
				i++
			}
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			effect.Paths = append(effect.Paths, cleanPathspec(arg))
		}
	}
	if !forced {
		return Effect{}, false
	}
	if len(effect.Paths) == 0 {
		effect.NonLiteral = true
	}
	for _, path := range effect.Paths {
		if strings.ContainsAny(path, "*?[") || strings.Contains(path, "$(") || strings.Contains(path, "${") {
			effect.NonLiteral = true
		}
	}
	return effect, true
}

func shortOptionContains(arg string, want rune) bool {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return false
	}
	return strings.ContainsRune(arg[1:], want)
}

func cleanPathspec(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "`'\""))
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func ParseAuthorization(prompt string) Authorization {
	var authorization Authorization
	for _, clause := range authorizationClauses(prompt) {
		if !clauseIsPositiveDirective(clause) {
			continue
		}
		analysis := analyzePromptGitSnippet(clause)
		explicitOperations := make(map[Operation]bool, len(analysis.Effects))
		for _, effect := range analysis.Effects {
			authorization.add(scopeFromEffect(effect))
			explicitOperations[effect.Operation] = true
		}
		for _, operation := range []Operation{OperationCommit, OperationPush, OperationTag} {
			if explicitOperations[operation] || !clauseMentionsOperation(clause, operation) {
				continue
			}
			scope := Scope{Operation: operation}
			if operation == OperationPush {
				scope.Remote, scope.Refs = naturalPushScope(clause)
				scope.Destructive = mentionsDestructivePush(clause)
			}
			if operation == OperationTag {
				scope.Tag = naturalTagScope(clause)
				scope.Destructive = mentionsDestructiveTag(clause)
			}
			authorization.add(scope)
		}
	}
	return authorization
}

func AuthorizationFromEffects(effects []Effect) Authorization {
	var authorization Authorization
	for _, effect := range effects {
		authorization.add(scopeFromEffect(effect))
	}
	return authorization
}

func MergeAuthorizations(authorizations ...Authorization) Authorization {
	var merged Authorization
	for _, authorization := range authorizations {
		for _, scope := range authorization.Scopes {
			merged.add(scope)
		}
	}
	return merged
}

// EffectKey is a stable, non-reversible identity for one normalized Git effect.
// It deliberately excludes Bash presentation fields such as tool IDs and descriptions.
func EffectKey(effect Effect) string {
	normalized := effect
	normalized.Refs = sortedCopy(effect.Refs)
	normalized.Paths = sortedCopy(effect.Paths)
	payload, _ := json.Marshal(normalized)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func AnalysisFingerprint(analysis Analysis) string {
	keys := make([]string, 0, len(analysis.Effects))
	for _, effect := range analysis.Effects {
		keys = append(keys, EffectKey(effect))
	}
	payload, _ := json.Marshal(struct {
		Effects    []string `json:"effects"`
		ParseError bool     `json:"parse_error"`
	}{Effects: keys, ParseError: analysis.ParseError})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func scopeFromEffect(effect Effect) Scope {
	return Scope{
		Operation: effect.Operation, Destructive: effect.Destructive,
		Remote: effect.Remote, Refs: append([]string(nil), effect.Refs...), Tag: effect.Tag,
		Paths: append([]string(nil), effect.Paths...), Author: effect.Author,
		Messages: append([]string(nil), effect.Messages...),
	}
}

func (a *Authorization) add(scope Scope) {
	for _, existing := range a.Scopes {
		if scopesEqual(existing, scope) {
			return
		}
	}
	a.Scopes = append(a.Scopes, scope)
}

func scopesEqual(a, b Scope) bool {
	return a.Operation == b.Operation && a.Destructive == b.Destructive && a.Remote == b.Remote && a.Tag == b.Tag && a.Author == b.Author &&
		strings.Join(a.Refs, "\x00") == strings.Join(b.Refs, "\x00") && strings.Join(a.Paths, "\x00") == strings.Join(b.Paths, "\x00") &&
		strings.Join(a.Messages, "\x00") == strings.Join(b.Messages, "\x00")
}

func scopeAllows(scope Scope, effect Effect) bool {
	if scope.Operation != effect.Operation || effect.NonLiteral {
		return false
	}
	return len(scopeMismatchFields(scope, effect)) == 0
}

func sameStringSequence(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, item := range a {
		counts[item]++
	}
	for _, item := range b {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	return true
}

func authorizationClauses(prompt string) []string {
	replacer := strings.NewReplacer("但是", "，", "不过", "，", " but ", ",", " however ", ",")
	prompt = replacer.Replace(prompt)
	return strings.FieldsFunc(prompt, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '。', '!', '！', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func clauseIsPositiveDirective(clause string) bool {
	lower := strings.ToLower(strings.TrimSpace(clause))
	if lower == "" || strings.ContainsAny(lower, "?？") || containsAny(lower, negativeOrDiscussionMarkers) {
		return false
	}
	if containsAny(lower, readOnlyMarkers) {
		return false
	}
	if startsWithOperation(lower) {
		return true
	}
	return containsAny(lower, directiveMarkers)
}

var negativeOrDiscussionMarkers = []string{
	"不要", "不允许", "没有让", "没让", "未让", "未经", "禁止", "无需", "不用", "别", "不能", "不应", "不该",
	"do not", "don't", "did not", "didn't", "never", "not allowed", "not authorize", "not ask", "without authorization",
	"为什么", "为啥", "怎么会", "什么意思", "是否", "能否", "可否", "有没有", "会有什么影响", "会发生什么", "吗",
	"why", "what happens", "what does", "what if", "did you", "have you", "can you", "could you", "should we", "should i",
}

var readOnlyMarkers = []string{
	"查看", "检查", "分析", "解释", "讨论", "对比", "历史", "状态",
	"review", "inspect", "analyze", "analyse", "explain", "discuss", "compare", "history", "status", "behavior", "behaviour",
}

var directiveMarkers = []string{
	"请", "帮我", "执行", "允许", "同意", "确认", "继续", "现在", "直接", "然后", "把", "将", "创建", "明确允许",
	"please", "go ahead", "execute", "run ", "allow", "authorize", "proceed", "create", "verify ", "now ", "yes ",
}

func startsWithOperation(clause string) bool {
	for _, marker := range []string{"commit", "push", "tag", "force push", "git commit", "git push", "git tag", "提交", "推送", "创建 tag", "创建标签", "强制推送"} {
		if strings.HasPrefix(clause, marker) {
			return true
		}
	}
	return false
}

func clauseMentionsOperation(clause string, operation Operation) bool {
	lower := strings.ToLower(clause)
	switch operation {
	case OperationCommit:
		english := removePhrases(lower, "commit hook", "commit history", "commit message", "commit hash", "commit id")
		chinese := removePhrases(lower, "已提交", "已经提交", "提交记录", "提交历史", "提交信息")
		return containsWord(english, "commit") || strings.Contains(chinese, "提交")
	case OperationPush:
		english := removePhrases(lower, "push notification", "push event")
		return containsWord(english, "push") || strings.Contains(lower, "推送")
	case OperationTag:
		english := removePhrases(lower, "html tag", "tag component", "tag selector")
		chinese := removePhrases(lower, "标签组件", "标签选择器")
		return containsWord(english, "tag") || strings.Contains(chinese, "标签")
	default:
		return false
	}
}

func removePhrases(text string, phrases ...string) string {
	for _, phrase := range phrases {
		text = strings.ReplaceAll(text, phrase, "")
	}
	return text
}

func analyzePromptGitSnippet(clause string) Analysis {
	lower := strings.ToLower(clause)
	index := strings.Index(lower, "git ")
	if index < 0 {
		return Analysis{}
	}
	return Analyze(clause[index:])
}

var naturalPushScopeRE = regexp.MustCompile(`(?i)(?:push|推送)\s+([a-z0-9._/-]+)\s+(?:to|到)\s+([a-z0-9._/-]+)`)
var naturalPushObjectScopeRE = regexp.MustCompile(`(?i)(?:push|推送)\s+(?:it|this|these|the\s+changes?|changes?|commits?|current\s+branch|当前分支|这些改动|改动)\s+(?:to|到)\s+([a-z0-9._/-]+)(?:\s+([a-z0-9._/-]+))?`)
var naturalPushRemoteRE = regexp.MustCompile(`(?i)(?:push|推送)\s+(?:to|到)\s+([a-z0-9._/-]+)`)
var naturalTagScopeRE = regexp.MustCompile(`(?i)(?:create|创建|打)\s+([a-z0-9._/-]+)\s+(?:tag|标签)`)

func naturalPushScope(clause string) (string, []string) {
	if match := naturalPushObjectScopeRE.FindStringSubmatch(clause); len(match) == 3 {
		if match[2] != "" {
			return trimNaturalToken(match[1]), []string{trimNaturalToken(match[2])}
		}
		return trimNaturalToken(match[1]), nil
	}
	if match := naturalPushScopeRE.FindStringSubmatch(clause); len(match) == 3 {
		return trimNaturalToken(match[2]), []string{trimNaturalToken(match[1])}
	}
	if match := naturalPushRemoteRE.FindStringSubmatch(clause); len(match) == 2 && match[1] != "the" {
		return trimNaturalToken(match[1]), nil
	}
	return "", nil
}

func trimNaturalToken(value string) string {
	return strings.TrimRight(value, ".,;:!?)]}")
}

func naturalTagScope(clause string) string {
	if match := naturalTagScopeRE.FindStringSubmatch(clause); len(match) == 2 {
		switch strings.ToLower(match[1]) {
		case "a", "the", "new", "release", "current":
			return ""
		}
		return match[1]
	}
	return ""
}

func mentionsDestructivePush(clause string) bool {
	lower := strings.ToLower(clause)
	return containsAny(lower, []string{"force push", "force-push", "--force", "--force-with-lease", "强制推送", "删除远端分支", "删除远程分支"})
}

func mentionsDestructiveTag(clause string) bool {
	lower := strings.ToLower(clause)
	return containsAny(lower, []string{"delete remote tag", "remove remote tag", "move tag", "retag", "overwrite tag", "删除远端 tag", "删除远程 tag", "移动 tag", "覆盖 tag", "重打 tag"})
}

func hasExactArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func containsWord(text, word string) bool {
	for index := 0; ; {
		found := strings.Index(text[index:], word)
		if found < 0 {
			return false
		}
		found += index
		beforeOK := true
		if found > 0 {
			before, _ := utf8.DecodeLastRuneInString(text[:found])
			beforeOK = !isWordRune(before)
		}
		after := found + len(word)
		afterOK := true
		if after < len(text) {
			next, _ := utf8.DecodeRuneInString(text[after:])
			afterOK = !isWordRune(next)
		}
		if beforeOK && afterOK {
			return true
		}
		index = found + len(word)
	}
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
