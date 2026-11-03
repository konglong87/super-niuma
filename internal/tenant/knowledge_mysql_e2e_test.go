package tenant

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// TestMySQLE2EKnowledgeChineseSearchUsesFulltext is the only way to prove the
// ngram parser from migration 000009 actually works: MATCH ... AGAINST is
// evaluated by the server, so no in-process test can observe it. The repository
// reports which branch produced a row in KnowledgeChunk.SearchMode, and this
// test fails when a Chinese query falls through to the LIKE fallback.
//
// Requires a MySQL server with a non-zero ngram_token_size (default 2) set in
// my.cnf before migrations run -- it is read-only at runtime. See
// docs/deployment/mysql_fulltext_ngram.md.
func TestMySQLE2EKnowledgeChineseSearchUsesFulltext(t *testing.T) {
	dsn := os.Getenv("NIUMA_MYSQL_E2E_DSN")
	if dsn == "" {
		t.Skip("set NIUMA_MYSQL_E2E_DSN to run real MySQL knowledge search e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	migrator, err := mysqlstore.NewMigrator(mysqlstore.MigrationOptions{DSN: dsn, Path: mysqlE2EMigrationsPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}

	repo, err := mysqlstore.OpenGormRepository(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.Close()
	}()
	svc := NewService(repo, nil)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userKey := "user-kb-e2e-" + suffix
	reqCtx := observability.WithRequestValues(ctx, "trace-kb-e2e-"+suffix, userKey, "yutang")
	if _, err := svc.SaveCurrentUser(reqCtx, UserRequest{
		Email:       userKey + "@example.test",
		DisplayName: "KB E2E User",
		Role:        "owner",
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}

	// Long enough to exercise chunk splitting, so the round trip also proves the
	// rune-safe chunker never writes broken UTF-8 into MySQL.
	body := strings.Repeat("多租户知识库检索依赖 MySQL 的 ngram 分词器。", 120)
	if _, err := svc.SaveKnowledgeDocument(reqCtx, KnowledgeDocumentRequest{
		Title:      "知识库检索前提-" + suffix,
		SourceType: "manual",
		Content:    body,
	}); err != nil {
		t.Fatal(err)
	}

	chunks, err := svc.SearchKnowledgeChunks(reqCtx, KnowledgeSearchRequest{Query: "知识库检索的分词器前提是什么？", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("Chinese knowledge query returned no chunks; the ngram FULLTEXT index is missing or ngram_token_size is 0")
	}
	if chunks[0].SearchMode != "fulltext" {
		t.Fatalf("search_mode = %q, want %q: the Chinese query fell through to the unranked LIKE fallback", chunks[0].SearchMode, "fulltext")
	}
	if chunks[0].Score <= 0 {
		t.Fatalf("score = %v, want a positive MATCH score", chunks[0].Score)
	}
	for i, chunk := range chunks {
		if !utf8.ValidString(chunk.Content) || strings.ContainsRune(chunk.Content, utf8.RuneError) {
			t.Fatalf("chunk %d stored broken UTF-8: %q", i, chunk.Content)
		}
	}
}
