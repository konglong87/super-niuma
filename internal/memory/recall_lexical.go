package memory

import (
	"strings"
	"unicode"
)

const (
	recallNameWeight        = 4
	recallDescriptionWeight = 2
	recallIndexWeight       = 1
	recallMinimumScore      = 2
	recallMinimumWordRunes  = 2
)

// memoryRecallTokens is lexical, not semantic: Han bigrams retain short Chinese
// queries, while non-Han words and identifier components use exact boundaries.
// No dictionary, embedding service, additional model turn or persistent index is
// needed. Query and candidate tokens share the same normalization.
func memoryRecallTokens(text string) []string {
	var tokens []string
	seen := make(map[string]bool)
	add := func(word string) {
		word = strings.ToLower(word)
		if len([]rune(word)) < recallMinimumWordRunes || memoryRecallStopWords[word] || seen[word] {
			return
		}
		seen[word] = true
		tokens = append(tokens, word)
	}
	runes := []rune(text)
	for i := 0; i < len(runes); {
		if unicode.Is(unicode.Han, runes[i]) {
			start := i
			for i < len(runes) && unicode.Is(unicode.Han, runes[i]) {
				i++
			}
			add(string(runes[start:i]))
			for j := start; j+1 < i; j++ {
				add(string(runes[j : j+2]))
			}
			continue
		}
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(runes) && !unicode.Is(unicode.Han, runes[i]) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
			i++
		}
		word := runes[start:i]
		add(string(word))
		component := 0
		for j := 1; j < len(word); j++ {
			if unicode.IsUpper(word[j]) && (unicode.IsLower(word[j-1]) || unicode.IsDigit(word[j-1]) || (unicode.IsUpper(word[j-1]) && j+1 < len(word) && unicode.IsLower(word[j+1]))) {
				add(string(word[component:j]))
				component = j
			}
		}
		add(string(word[component:]))
	}
	return tokens
}

var memoryRecallStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "from": true, "with": true,
	"this": true, "that": true, "please": true, "what": true, "which": true,
	"how": true, "should": true, "would": true, "could": true, "show": true,
	"me": true, "my": true, "we": true, "our": true, "you": true, "your": true,
	"to": true, "of": true, "in": true, "on": true, "at": true, "as": true,
	"is": true, "it": true, "be": true, "an": true, "or": true, "by": true,
	"do": true, "not": true, "use": true, "read": true, "memory": true,
	"当前": true, "项目": true, "记忆": true, "检查": true, "读取": true, "查看": true,
	"请问": true, "如何": true, "怎么": true, "什么": true, "哪些": true, "这个": true,
}

func memoryRecallScore(queryTokens []string, summary string) int {
	candidates := make(map[string]bool)
	for _, token := range memoryRecallTokens(summary) {
		candidates[token] = true
	}
	score := 0
	for _, query := range queryTokens {
		if candidates[query] {
			score++
			delete(candidates, query)
		}
	}
	return score
}

func memoryRecallCandidateScore(query []string, name, description, index string) int {
	return recallNameWeight*memoryRecallScore(query, name) +
		recallDescriptionWeight*memoryRecallScore(query, description) +
		recallIndexWeight*memoryRecallScore(query, index)
}
