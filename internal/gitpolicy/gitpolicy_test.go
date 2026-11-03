package gitpolicy

import (
	"strings"
	"testing"
)

func TestAnalyzeRecognizesGitSyntaxAndIgnoresQuotedData(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		operation   Operation
		destructive bool
		wantEffect  bool
	}{
		{name: "global cwd", command: "git -C /repo commit -m x", operation: OperationCommit, wantEffect: true},
		{name: "absolute executable", command: "/usr/bin/git commit -m x", operation: OperationCommit, wantEffect: true},
		{name: "global git dir", command: "git --git-dir=.git push origin main", operation: OperationPush, wantEffect: true},
		{name: "push comment is not dry run", command: "git push origin HEAD # --dry-run", operation: OperationPush, wantEffect: true},
		{name: "real dry run", command: "git push --dry-run origin HEAD", operation: OperationPush, wantEffect: false},
		{name: "delete branch refspec", command: "git push origin :refs/heads/main", operation: OperationPush, destructive: true, wantEffect: true},
		{name: "force refspec", command: "git push origin +HEAD:refs/heads/main", operation: OperationPush, destructive: true, wantEffect: true},
		{name: "combined delete force flags", command: "git push -df origin main", operation: OperationPush, destructive: true, wantEffect: true},
		{name: "combined dry run force flags", command: "git push -fn origin main", operation: OperationPush, wantEffect: false},
		{name: "quoted search", command: "rg -n 'git push' internal/query", operation: OperationPush, wantEffect: false},
		{name: "quoted print", command: "printf 'git add -f CLAUDE.md'", operation: OperationForceAdd, wantEffect: false},
		{name: "compound tag", command: "git tag --list && git tag v1", operation: OperationTag, wantEffect: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			analysis := Analyze(tc.command)
			var got *Effect
			for i := range analysis.Effects {
				if analysis.Effects[i].Operation == tc.operation {
					got = &analysis.Effects[i]
					break
				}
			}
			if (got != nil) != tc.wantEffect {
				t.Fatalf("effects=%+v wantEffect=%v", analysis.Effects, tc.wantEffect)
			}
			if got != nil && got.Destructive != tc.destructive {
				t.Fatalf("destructive=%v want=%v effect=%+v", got.Destructive, tc.destructive, *got)
			}
		})
	}
}

func TestForceAddAnalysisRejectsNonLiteralAndCombinedFlags(t *testing.T) {
	tests := []struct {
		command    string
		paths      []string
		nonLiteral bool
	}{
		{command: "git add -f CLAUDE.md", paths: []string{"CLAUDE.md"}},
		{command: "git add -fA", nonLiteral: true},
		{command: "git add -Af", nonLiteral: true},
		{command: "git -C . add -f ignored.file", paths: []string{"ignored.file"}},
		{command: "git add -f --pathspec-from-file paths.txt", nonLiteral: true},
		{command: "git add -f '*.md'", paths: []string{"*.md"}, nonLiteral: true},
	}
	for _, tc := range tests {
		analysis := Analyze(tc.command)
		if len(analysis.Effects) != 1 || analysis.Effects[0].Operation != OperationForceAdd {
			t.Fatalf("%q effects=%+v", tc.command, analysis.Effects)
		}
		effect := analysis.Effects[0]
		if effect.NonLiteral != tc.nonLiteral {
			t.Fatalf("%q nonLiteral=%v want=%v", tc.command, effect.NonLiteral, tc.nonLiteral)
		}
		if !sameStringSet(effect.Paths, tc.paths) {
			t.Fatalf("%q paths=%v want=%v", tc.command, effect.Paths, tc.paths)
		}
	}
}

func TestForceAddAnalysisKeepsPathAfterNonValueOptions(t *testing.T) {
	for _, command := range []string{
		"git add --force --renormalize README.md",
		"git add --force --chmod=+x scripts/run.sh",
	} {
		analysis := Analyze(command)
		if len(analysis.Effects) != 1 || analysis.Effects[0].NonLiteral || len(analysis.Effects[0].Paths) != 1 {
			t.Fatalf("Analyze(%q) = %+v, want one literal force-add path", command, analysis)
		}
	}
}

func TestContainsWordUsesUTF8Boundaries(t *testing.T) {
	if containsWord("提交commit", "commit") {
		t.Fatal("ASCII operation adjacent to a Chinese letter must not form a separate word")
	}
	if !containsWord("请 commit 这些改动", "commit") {
		t.Fatal("space-delimited operation should be recognized")
	}
}

func TestAuthorizationRequiresPositiveOperationScopedIntent(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		command string
		allow   bool
	}{
		{name: "explicit commit push", prompt: "好，你帮我提交并 push", command: "git commit -m x && git push origin main", allow: true},
		{name: "negated commit", prompt: "不要执行 git commit -m x", command: "git commit -m x"},
		{name: "read commit", prompt: "请查看这个 commit 的状态", command: "git commit -m x"},
		{name: "question command", prompt: "What happens if I run git commit?", command: "git commit -m x"},
		{name: "compare operations", prompt: "Compare commit and push behavior.", command: "git push origin main"},
		{name: "question without punctuation", prompt: "提交会有什么影响", command: "git commit -m x"},
		{name: "commit hook is not commit authorization", prompt: "commit hook needs a fix", command: "git commit -m x"},
		{name: "push notification is not push authorization", prompt: "push notification needs a fix", command: "git push origin main"},
		{name: "tag component is not tag authorization", prompt: "tag component needs a fix", command: "git tag v1"},
		{name: "push does not grant commit", prompt: "push 已提交分支", command: "git commit -m x"},
		{name: "negative force add", prompt: "不要执行 git add -f CLAUDE.md", command: "git add -f CLAUDE.md"},
		{name: "exact force add", prompt: "明确允许执行 git add -f CLAUDE.md", command: "git add -f CLAUDE.md", allow: true},
		{name: "indirect force add", prompt: "明确允许执行 git add -f paths.txt", command: "git add -f --pathspec-from-file paths.txt"},
		{name: "negative destructive", prompt: "不要 force push，只检查状态", command: "git push --force-with-lease origin main"},
		{name: "explicit destructive", prompt: "Force push current branch with --force-with-lease.", command: "git push --force-with-lease origin main", allow: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			violation := Check(ParseAuthorization(tc.prompt), Analyze(tc.command))
			if (violation == nil) != tc.allow {
				t.Fatalf("allow=%v want=%v violation=%+v auth=%+v effects=%+v", violation == nil, tc.allow, violation, ParseAuthorization(tc.prompt), Analyze(tc.command).Effects)
			}
		})
	}
}

func TestAuthorizationBindsExplicitPushScope(t *testing.T) {
	authorization := ParseAuthorization("push dev-c/foo 到 origin")
	if violation := Check(authorization, Analyze("git push origin dev-c/foo")); violation != nil {
		t.Fatalf("matching target denied: %+v auth=%+v", violation, authorization)
	}
	if violation := Check(authorization, Analyze("git push backup main")); violation == nil {
		t.Fatal("different remote/ref must not inherit scoped authorization")
	}
}

func TestAuthorizationBindsNaturalPushObjectScope(t *testing.T) {
	authorization := ParseAuthorization("Commit the local change and push it to origin main.")
	if violation := Check(authorization, Analyze("git push origin main")); violation != nil {
		t.Fatalf("matching pronoun-based push target denied: %+v auth=%+v", violation, authorization)
	}
	if violation := Check(authorization, Analyze("git push backup dev")); violation == nil {
		t.Fatalf("pronoun-based push target was broadened: auth=%+v", authorization)
	}
}

func TestAuthorizationDoesNotBroadenExplicitGitSnippetScope(t *testing.T) {
	push := ParseAuthorization("Please run git push origin main")
	if violation := Check(push, Analyze("git push origin main")); violation != nil {
		t.Fatalf("matching explicit push denied: %+v auth=%+v", violation, push)
	}
	if violation := Check(push, Analyze("git push backup dev")); violation == nil {
		t.Fatalf("explicit push scope was broadened: auth=%+v", push)
	}

	tag := ParseAuthorization("Please run git tag v1.2.3")
	if violation := Check(tag, Analyze("git tag v1.2.3")); violation != nil {
		t.Fatalf("matching explicit tag denied: %+v auth=%+v", violation, tag)
	}
	if violation := Check(tag, Analyze("git tag v2.0.0")); violation == nil {
		t.Fatalf("explicit tag scope was broadened: auth=%+v", tag)
	}
}

func TestCommitAnalysisBindsExplicitAuthorAndMessages(t *testing.T) {
	analysis := Analyze(`git commit --author="konglong87 <>" -m "subject" --message="body"`)
	if len(analysis.Effects) != 1 {
		t.Fatalf("effects=%+v, want one commit", analysis.Effects)
	}
	effect := analysis.Effects[0]
	if effect.Author != "konglong87 <>" || !sameStringSequence(effect.Messages, []string{"subject", "body"}) {
		t.Fatalf("commit effect=%+v, want bound author and ordered messages", effect)
	}
}

func TestAuthorizationFromEffectsDoesNotBroadenCommitIdentity(t *testing.T) {
	confirmed := Analyze(`git commit --author="konglong87 <>" -m "docs: exact"`).Effects
	authorization := AuthorizationFromEffects(confirmed)
	for _, command := range []string{
		`git commit --author="konglong87 <konglong@example.com>" -m "docs: exact"`,
		`git commit --author="konglong87 <>" -m "docs: changed"`,
		`git commit -m "docs: exact"`,
	} {
		if violation := Check(authorization, Analyze(command)); violation == nil {
			t.Fatalf("confirmed empty-email author/message scope broadened to %q: %+v", command, authorization)
		}
	}
	if violation := Check(authorization, Analyze(`git commit --author="konglong87 <>" -m "docs: exact"`)); violation != nil {
		t.Fatalf("exact confirmed commit denied: %+v", violation)
	}
}

func TestExplicitCommitPromptBindsAuthorAndMessage(t *testing.T) {
	authorization := ParseAuthorization(`Please run git commit --author="konglong87 <>" -m "docs: exact"`)
	if violation := Check(authorization, Analyze(`git commit --author="konglong87 <>" -m "docs: exact"`)); violation != nil {
		t.Fatalf("exact explicit commit denied: %+v", violation)
	}
	if violation := Check(authorization, Analyze(`git commit --author="konglong87 <old@example.com>" -m "docs: exact"`)); violation == nil {
		t.Fatal("explicit empty-email author was replaced by a nonempty historical email")
	}
}

func TestAuthorizationViolationExplainsClosestExactScopeMismatch(t *testing.T) {
	tests := []struct {
		name       string
		prompt     string
		command    string
		wantReason string
	}{
		{
			name:       "missing operation authorization",
			prompt:     "push origin master",
			command:    `git commit -m "docs: exact"`,
			wantReason: "does not authorize git commit",
		},
		{
			name:       "literal placeholder is not message wildcard",
			prompt:     `Please run git commit --author="konglong87 <>" -m "<original message>"`,
			command:    `git commit --author="konglong87 <>" -m "docs: exact"`,
			wantReason: "commit message does not match",
		},
		{
			name:       "author and message both differ",
			prompt:     `Please run git commit --author="konglong87 <>" -m "docs: exact"`,
			command:    `git commit --author="konglong87 <old@example.com>" -m "docs: changed"`,
			wantReason: "commit author and commit message do not match",
		},
		{
			name:       "closest matching scope wins",
			prompt:     "Please run git push backup main\nPlease run git push origin main",
			command:    "git push origin dev",
			wantReason: "ref does not match",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			violation := Check(ParseAuthorization(tc.prompt), Analyze(tc.command))
			if violation == nil || !strings.Contains(violation.Reason, tc.wantReason) {
				t.Fatalf("violation=%+v, want reason containing %q", violation, tc.wantReason)
			}
		})
	}
}

func TestAnalysisFingerprintTracksGitSemantics(t *testing.T) {
	first := AnalysisFingerprint(Analyze(`git push origin main`))
	if first != AnalysisFingerprint(Analyze(`git push origin main`)) {
		t.Fatal("identical Git effects produced unstable fingerprints")
	}
	if first == AnalysisFingerprint(Analyze(`git push backup main`)) {
		t.Fatal("different push targets produced the same fingerprint")
	}
	if EffectKey(Effect{Operation: OperationForceAdd, Paths: []string{"b", "a"}}) != EffectKey(Effect{Operation: OperationForceAdd, Paths: []string{"a", "b"}}) {
		t.Fatal("path ordering changed a set-semantic effect key")
	}
}
