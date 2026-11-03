package memory

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestProjectMemoryRecallLexicalQuality(t *testing.T) {
	cases := []struct {
		name, query, title, description, hook string
		want                                  bool
	}{
		{"english-exact", "deployment verification", "Deployment", "verification checklist", "", true},
		{"english-punctuation", "deployment, verification?", "Deployment", "verification checklist", "", true},
		{"english-case", "DEPLOYMENT", "Deployment", "checklist", "", true},
		{"word-not-substring", "art exhibition", "artifact", "build registry", "", false},
		{"prefix-not-word", "cat pictures", "catalog", "service inventory", "", false},
		{"suffix-not-word", "log", "dialog", "window layout", "", false},
		{"go-language", "Go", "Go language", "coding conventions", "", true},
		{"ci-identifier", "CI", "CI pipeline", "build conventions", "", true},
		{"ui-identifier", "UI", "UI design", "interface conventions", "", true},
		{"han-two-runes", "部署", "部署规范", "服务发布要求", "", true},
		{"han-testing", "测试", "测试策略", "回归要求", "", true},
		{"han-sentence", "请问部署之前要怎么检查？", "部署规范", "服务发布要求", "", true},
		{"han-no-spaces", "必须遵守部署规范", "部署规范", "服务发布要求", "", true},
		{"han-traditional", "測試", "測試策略", "回歸要求", "", true},
		{"han-single-noise", "测", "测试策略", "回归要求", "", false},
		{"mixed-language", "请检查Go服务的CI流程", "CI pipeline", "Go service conventions", "", true},
		{"snake-case", "retry_policy", "retry policy", "client conventions", "", true},
		{"camel-case", "retryPolicy", "retry policy", "client conventions", "", true},
		{"acronym-boundary", "HTTPClient", "HTTP client", "network conventions", "", true},
		{"file-identifier", "src/api_client.go", "API client", "network conventions", "", true},
		{"index-hook", "canary rollback", "Release", "production protocol", "canary rollback", true},
		{"weak-index-only", "canary", "Release", "production protocol", "canary rollback", false},
		{"stopwords", "please show me the memory", "Release", "production protocol", "", false},
		{"unrelated", "garden irrigation", "Deployment", "verification checklist", "", false},
		{"semantic-fallback", "What precautions should we take before shipping?", "Deployment", "verification checklist", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "entry.md"), fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nbody", tc.title, tc.description))
			docs := recallProjectMemoryFiles(dir, "- [Entry](entry.md) - "+tc.hook, tc.query)
			if got := len(docs) > 0; got != tc.want {
				t.Fatalf("query %q: recalled=%t want=%t", tc.query, got, tc.want)
			}
		})
	}
}

func TestProjectMemoryRecallRanksMetadataBeforeIndex(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{
		"z-name.md":        "---\nname: deploy rollback\ndescription: protocol\n---\nname",
		"m-description.md": "---\nname: production\ndescription: deploy rollback\n---\ndescription",
		"a-index.md":       "---\nname: procedure\ndescription: release protocol\n---\nindex",
	} {
		mustWrite(t, filepath.Join(dir, path), body)
	}
	docs := recallProjectMemoryFiles(dir, "- [Index](a-index.md) - deploy rollback\n- [Description](m-description.md)\n- [Name](z-name.md)", "deploy rollback")
	want := []string{"z-name.md", "m-description.md", "a-index.md"}
	if len(docs) != len(want) {
		t.Fatalf("recall count=%d want=%d", len(docs), len(want))
	}
	for i, name := range want {
		if filepath.Base(docs[i].Path) != name {
			t.Fatalf("rank %d=%s want=%s", i, docs[i].Path, name)
		}
	}
}

func TestProjectMemoryIndexHookCannotBypassInvalidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"malformed": "---\nname: [broken\n---\nbody", "missing": "body", "nested": "---\nmetadata:\n  name: deploy rollback\n---\nbody"} {
		t.Run(name, func(t *testing.T) {
			mustWrite(t, filepath.Join(dir, name+".md"), body)
			if docs := recallProjectMemoryFiles(dir, "- [deploy rollback]("+name+".md)", "deploy rollback"); len(docs) != 0 {
				t.Fatal("invalid frontmatter recalled via index hook")
			}
		})
	}
}

func TestMemoryRecallScoreDoesNotAmplifyRepeatedQueryWords(t *testing.T) {
	if a, b := memoryRecallScore(memoryRecallTokens("deploy"), "deploy"), memoryRecallScore(memoryRecallTokens("deploy deploy deploy"), "deploy"); a != b {
		t.Fatalf("duplicate query changed score: %d != %d", a, b)
	}
}

func TestProjectMemoryRecallKeepsIndexHintsWithTheirLink(t *testing.T) {
	cases := []struct {
		name, index string
		want        string
	}{
		{"multiple links", "- [Build](build.md) - registry; [Release](release.md) - canary rollback", "release.md"},
		{"later alias", "- [Release](release.md)\n- [Deployment](release.md) - canary rollback", "release.md"},
		{"commented hook", "- [Build](build.md) <!-- canary rollback -->", ""},
		{"commented entry", "<!--\n- [Build](build.md) - canary rollback\n-->", ""},
		{"duplicate weak hint", "- [Build](build.md) - canary\n- [Build](build.md) - canary", ""},
		{"disjoint weak aliases", "- [Build](build.md) - canary\n- [Build](build.md) - rollback", ""},
		{"normalized later alias", "- [Release](release.md)\n- [Deployment](./release.md) - canary rollback", "release.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"build.md", "release.md"} {
				mustWrite(t, filepath.Join(dir, name), "---\nname: Policy\ndescription: durable convention\n---\nbody")
			}
			docs := recallProjectMemoryFiles(dir, tc.index, "canary rollback")
			if tc.want == "" {
				if len(docs) != 0 {
					t.Fatalf("commented hint recalled %v", docs)
				}
				return
			}
			if len(docs) != 1 || filepath.Base(docs[0].Path) != tc.want {
				t.Fatalf("recalled %v, want only %s", docs, tc.want)
			}
		})
	}
}
