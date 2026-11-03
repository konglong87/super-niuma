package mysql

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// knowledgeSearchMaxTerms bounds how many LIKE patterns a single search
	// builds. The callers pass a whole user prompt, so an unbounded term list
	// would put hundreds of OR-ed LIKE predicates on a LONGTEXT column.
	knowledgeSearchMaxTerms = 8
	// knowledgeSearchMaxMatchBytes bounds the MATCH ... AGAINST argument.
	knowledgeSearchMaxMatchBytes = 512
	// knowledgeSearchMinTermRunes drops single characters, which match nearly
	// every chunk and carry no ranking signal.
	knowledgeSearchMinTermRunes = 2
	// knowledgeSearchMaxTermRunes drops long clauses, which can only ever LIKE
	// match a chunk that repeats them verbatim.
	knowledgeSearchMaxTermRunes = 32
)

// knowledgeSearchQuery is the pair of shapes a raw prompt is turned into:
// Match feeds MATCH ... AGAINST (the real retrieval path, which needs the ngram
// parser from migration 000009 for CJK), and Terms feed the LIKE fallback used
// when the FULLTEXT index yields nothing.
type knowledgeSearchQuery struct {
	Match string
	Terms []string
}

// buildKnowledgeSearchQuery derives a bounded search shape from a raw prompt.
// The previous behaviour passed the entire prompt as one LIKE pattern, so the
// fallback only matched a chunk containing the whole prompt verbatim.
func buildKnowledgeSearchQuery(raw string) knowledgeSearchQuery {
	fields := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(fields) == 0 {
		return knowledgeSearchQuery{}
	}
	out := knowledgeSearchQuery{Match: truncateRuneSafe(strings.Join(fields, " "), knowledgeSearchMaxMatchBytes)}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if seen[field] {
			continue
		}
		seen[field] = true
		if runes := utf8.RuneCountInString(field); runes < knowledgeSearchMinTermRunes || runes > knowledgeSearchMaxTermRunes {
			continue
		}
		out.Terms = append(out.Terms, field)
		if len(out.Terms) == knowledgeSearchMaxTerms {
			break
		}
	}
	return out
}

// likePatterns renders the terms as escaped LIKE patterns.
func (q knowledgeSearchQuery) likePatterns() []any {
	patterns := make([]any, 0, len(q.Terms))
	for _, term := range q.Terms {
		patterns = append(patterns, "%"+escapeLike(term)+"%")
	}
	return patterns
}

// likeAny renders `(column LIKE ? OR column LIKE ? ...)` for the terms, or an
// empty string when there is nothing to fall back on.
func (q knowledgeSearchQuery) likeAny(column string) string {
	return q.likeAnyWithEscape(column, `\\`)
}

func (q knowledgeSearchQuery) likeAnySQLite(column string) string {
	return q.likeAnyWithEscape(column, `\`)
}

func (q knowledgeSearchQuery) likeAnyWithEscape(column, escape string) string {
	if len(q.Terms) == 0 {
		return ""
	}
	predicates := make([]string, 0, len(q.Terms))
	for range q.Terms {
		predicates = append(predicates, column+` LIKE ? ESCAPE '`+escape+`'`)
	}
	return "(" + strings.Join(predicates, " OR ") + ")"
}

func truncateRuneSafe(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	cut := text[:limit]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(cut)
}
