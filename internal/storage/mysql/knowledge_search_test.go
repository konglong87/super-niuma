package mysql

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
)

// MySQL's default full-text parser only breaks on whitespace and punctuation, so
// a FULLTEXT index without `WITH PARSER ngram` never tokenizes CJK text and
// MATCH ... AGAINST is permanently 0 for Chinese queries.
func TestKnowledgeChunkFulltextIndexUsesNgramParser(t *testing.T) {
	up := readMigration(t, "000009_knowledge_fulltext_ngram.up.sql")
	assertContains(t, up, "ALTER TABLE tenant_knowledge_chunks DROP INDEX ft_knowledge_chunks_content")
	assertContains(t, up, "WITH PARSER ngram")
	assertContains(t, up, "ft_knowledge_chunks_content_ngram")

	down := readMigration(t, "000009_knowledge_fulltext_ngram.down.sql")
	assertContains(t, down, "ALTER TABLE tenant_knowledge_chunks DROP INDEX ft_knowledge_chunks_content_ngram")
	assertContains(t, down, "ADD FULLTEXT KEY ft_knowledge_chunks_content (content)")
	if strings.Contains(down, "WITH PARSER") {
		t.Fatalf("down migration must restore the plain parser:\n%s", down)
	}
}

func TestBuildKnowledgeSearchQueryExtractsBoundedTerms(t *testing.T) {
	got := buildKnowledgeSearchQuery("  如何配置 知识库 检索？ ")
	if got.Match != "如何配置 知识库 检索" {
		t.Fatalf("match = %q", got.Match)
	}
	if want := []string{"如何配置", "知识库", "检索"}; !reflect.DeepEqual(got.Terms, want) {
		t.Fatalf("terms = %q, want %q", got.Terms, want)
	}
}

// The whole raw prompt used to be the LIKE pattern, so the fallback could only
// match a chunk that contained the entire prompt verbatim -- i.e. never.
func TestBuildKnowledgeSearchQuerySplitsPunctuationAndMarkdown(t *testing.T) {
	got := buildKnowledgeSearchQuery("Fix `SearchKnowledgeChunks` in gorm_repository.go, please!")
	for _, want := range []string{"fix", "searchknowledgechunks", "in", "gorm", "repository", "go", "please"} {
		if !containsString(got.Terms, want) {
			t.Fatalf("terms = %q, missing %q", got.Terms, want)
		}
	}
	for _, term := range got.Terms {
		if strings.ContainsAny(term, " `,.!") {
			t.Fatalf("term %q still carries punctuation", term)
		}
	}
}

func TestBuildKnowledgeSearchQueryDropsSingleCharacterTermsAndDeduplicates(t *testing.T) {
	got := buildKnowledgeSearchQuery("a 检索 b 检索 c")
	if want := []string{"检索"}; !reflect.DeepEqual(got.Terms, want) {
		t.Fatalf("terms = %q, want %q", got.Terms, want)
	}
	if got.Match != "a 检索 b 检索 c" {
		t.Fatalf("match = %q, want the cleaned prompt kept for MATCH", got.Match)
	}
}

func TestBuildKnowledgeSearchQueryBoundsTermCountAndMatchLength(t *testing.T) {
	got := buildKnowledgeSearchQuery(strings.TrimSpace(strings.Repeat("检索排序 ", 400)))
	if len(got.Terms) > knowledgeSearchMaxTerms {
		t.Fatalf("terms = %d, want at most %d", len(got.Terms), knowledgeSearchMaxTerms)
	}
	if len(got.Match) > knowledgeSearchMaxMatchBytes {
		t.Fatalf("match = %d bytes, want at most %d", len(got.Match), knowledgeSearchMaxMatchBytes)
	}
	if !utf8.ValidString(got.Match) {
		t.Fatalf("match was cut mid-rune: %q", got.Match)
	}
}

func TestBuildKnowledgeSearchQueryEmptyForPunctuationOnlyPrompt(t *testing.T) {
	if got := buildKnowledgeSearchQuery("!!! ??? ---"); got.Match != "" || len(got.Terms) != 0 {
		t.Fatalf("query = %+v, want empty", got)
	}
}

// The generated SQL must score and filter on MATCH plus per-term LIKE patterns,
// never on the raw prompt as a single LIKE pattern.
func TestSearchKnowledgeChunksScoresOnMatchAndPerTermLike(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("MATCH(c.content) AGAINST (? IN NATURAL LANGUAGE MODE) > 0 THEN 'fulltext' ELSE 'like' END AS search_mode")).
		WithArgs(
			"知识库 检索",
			"%知识库%", "%检索%",
			"%知识库%", "%检索%",
			"知识库 检索",
			uint64(3), uint64(3), "active",
			uint64(0), uint64(0),
			"知识库 检索",
			"%知识库%", "%检索%",
			"%知识库%", "%检索%",
			4,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "document_id", "title", "source_type", "chunk_index", "content", "metadata_json", "embedding_ref", "score", "search_mode"}).
			AddRow(9, 5, "知识库", "manual", 0, "中文知识库检索", nil, nil, 21.5, "fulltext"))

	chunks, err := repo.SearchKnowledgeChunks(testContext(), 3, KnowledgeSearchOptions{Query: "知识库 检索？", Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].SearchMode != "fulltext" {
		t.Fatalf("chunks = %+v", chunks)
	}
	assertExpectations(t, mock)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
