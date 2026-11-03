package workorder

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

func normalizeInput(input CreateInput) (CreateInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Objective = strings.TrimSpace(input.Objective)
	input.Workspace = strings.TrimSpace(input.Workspace)
	if !validText(input.Title, MaxTitleBytes, false) || !validText(input.Objective, MaxObjectiveBytes, true) {
		return CreateInput{}, ErrInvalidInput
	}
	if len(input.AcceptanceCriteria) == 0 || len(input.AcceptanceCriteria) > MaxAcceptanceCriteria {
		return CreateInput{}, ErrInvalidInput
	}
	criteria := make([]string, len(input.AcceptanceCriteria))
	for i, criterion := range input.AcceptanceCriteria {
		criteria[i] = strings.TrimSpace(criterion)
		if !validText(criteria[i], MaxCriterionBytes, true) {
			return CreateInput{}, ErrInvalidInput
		}
	}
	input.AcceptanceCriteria = criteria
	if input.Workspace != "" {
		if !validText(input.Workspace, MaxWorkspaceBytes, false) || !filepath.IsAbs(input.Workspace) {
			return CreateInput{}, ErrInvalidInput
		}
		// Lexical normalization only. Never stat, open, or grant access to a workspace.
		input.Workspace = filepath.Clean(input.Workspace)
	}
	return input, nil
}

func validText(value string, maxBytes int, multiline bool) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}

func validKey(key string) bool {
	if len(key) == 0 || len(key) > MaxIdempotencyKeyBytes {
		return false
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			continue
		}
		return false
	}
	return true
}

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
