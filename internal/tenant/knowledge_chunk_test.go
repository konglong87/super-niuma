package tenant

import (
	"strings"
	"testing"
	"unicode/utf8"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// A CJK paragraph longer than the chunk limit used to be sliced with byte
// offsets, which cuts multi-byte runes in half and writes replacement
// characters into the knowledge base.
func TestBuildKnowledgeChunksKeepsChineseRunesIntact(t *testing.T) {
	// The leading ASCII rune shifts every following 3-byte rune off the byte
	// budget, so a byte-sliced cut necessarily lands mid-rune.
	paragraph := "x" + strings.Repeat("知识库检索", 600)
	chunks := buildKnowledgeChunks(mysqlstore.KnowledgeDocumentInput{TenantID: 7, Content: paragraph})
	if len(chunks) < 2 {
		t.Fatalf("expected the paragraph to be split, got %d chunk(s)", len(chunks))
	}
	for i, chunk := range chunks {
		if !utf8.ValidString(chunk.Content) {
			t.Fatalf("chunk %d is not valid UTF-8: %q", i, chunk.Content)
		}
		if strings.ContainsRune(chunk.Content, utf8.RuneError) {
			t.Fatalf("chunk %d contains a broken rune: %q", i, chunk.Content)
		}
	}
	if joined := strings.Join(chunkContents(chunks), ""); joined != paragraph {
		t.Fatalf("rejoined chunks lost content: %d runes, want %d", utf8.RuneCountInString(joined), utf8.RuneCountInString(paragraph))
	}
}

// maxChunkChars is documented as a character budget, so a CJK paragraph must be
// allowed the same number of characters as an ASCII one instead of being cut at
// one third of it.
func TestBuildKnowledgeChunksCountsCharactersNotBytes(t *testing.T) {
	chunks := buildKnowledgeChunks(mysqlstore.KnowledgeDocumentInput{TenantID: 7, Content: strings.Repeat("检索", 600)})
	if len(chunks) != 1 {
		t.Fatalf("1200 CJK characters should fit one chunk, got %d chunks", len(chunks))
	}
}

func TestBuildKnowledgeChunksSplitsOversizedParagraphsOnCharacterBudget(t *testing.T) {
	chunks := buildKnowledgeChunks(mysqlstore.KnowledgeDocumentInput{TenantID: 7, Content: strings.Repeat("检索", 700)})
	if len(chunks) != 2 {
		t.Fatalf("1400 CJK characters should split into 2 chunks, got %d", len(chunks))
	}
	if got := utf8.RuneCountInString(chunks[0].Content); got != 1200 {
		t.Fatalf("first chunk = %d characters, want the full 1200-character budget", got)
	}
	if got := utf8.RuneCountInString(chunks[1].Content); got != 200 {
		t.Fatalf("second chunk = %d characters, want 200", got)
	}
}

func chunkContents(chunks []mysqlstore.KnowledgeChunkInput) []string {
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, chunk.Content)
	}
	return out
}
