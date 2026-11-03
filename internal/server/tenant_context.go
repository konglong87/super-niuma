package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/skills"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

const tenantContextMemoryLimit = 12
const tenantContextKnowledgeLimit = 4
const tenantSkillCatalogLimit = 100

var tenantCodeMemoryCategories = []string{"preference", "convention", "project_fact", "user_fact", "general", "managed", "team"}

type TenantPromptContext struct {
	Text     string
	Manifest query.TenantContextManifest
}

func TenantContextAddendum(ctx context.Context, svc TenantService) (string, error) {
	return TenantContextAddendumForPrompt(ctx, svc, "")
}

func TenantContextAddendumForPrompt(ctx context.Context, svc TenantService, prompt string) (string, error) {
	result, err := BuildTenantContextAddendumForPrompt(ctx, svc, prompt)
	return result.Text, err
}

func BuildTenantContextAddendumForPrompt(ctx context.Context, svc TenantService, prompt string) (TenantPromptContext, error) {
	if svc == nil {
		return TenantPromptContext{}, nil
	}
	var parts []string
	manifest := query.TenantContextManifest{}
	if memories, err := svc.ListMemories(ctx, "", tenantContextMemoryLimit); err != nil {
		return TenantPromptContext{}, err
	} else if text, count := formatTenantMemoriesWithCount(memories); text != "" {
		parts = append(parts, text)
		manifest.MemoryItems = count
	}
	if profile, err := svc.GetProfile(ctx, 0); err != nil {
		if !errors.Is(err, mysqlstore.ErrNotFound) {
			return TenantPromptContext{}, err
		}
	} else if text := formatTenantProfile(profile); text != "" {
		parts = append(parts, text)
		manifest.Profile = true
	}
	if doc, err := svc.GetActiveDocument(ctx, "CLAUDE.md"); err != nil {
		if !errors.Is(err, mysqlstore.ErrNotFound) {
			return TenantPromptContext{}, err
		}
	} else if text := formatTenantDocument(doc); text != "" {
		parts = append(parts, text)
		manifest.Documents = 1
	}
	if strings.TrimSpace(prompt) != "" {
		if chunks, err := svc.SearchKnowledgeChunks(ctx, tenantservice.KnowledgeSearchRequest{Query: prompt, Limit: tenantContextKnowledgeLimit}); err != nil {
			return TenantPromptContext{}, err
		} else if text := formatTenantKnowledge(chunks); text != "" {
			parts = append(parts, text)
			manifest.KnowledgeChunks = len(chunks)
		}
	}
	if len(parts) == 0 {
		return TenantPromptContext{}, nil
	}
	manifest.Active = true
	manifest.Addendum = true
	return TenantPromptContext{Text: "# Tenant Context\nThe following context belongs to the authenticated tenant/user for this conversation. Use it as user-scoped guidance unless it conflicts with higher-priority instructions.\n\n" + strings.Join(parts, "\n\n"), Manifest: manifest}, nil
}

func TenantCodeMemoryAddendum(ctx context.Context, svc TenantService) (string, error) {
	result, err := BuildTenantCodeMemoryAddendum(ctx, svc)
	return result.Text, err
}

func BuildTenantCodeMemoryAddendum(ctx context.Context, svc TenantService) (TenantPromptContext, error) {
	if svc == nil {
		return TenantPromptContext{}, nil
	}
	var parts []string
	manifest := query.TenantContextManifest{}
	for _, category := range tenantCodeMemoryCategories {
		memories, err := svc.ListMemories(ctx, category, tenantContextMemoryLimit)
		if err != nil {
			return TenantPromptContext{}, err
		}
		if text, count := formatTenantMemoriesWithCount(memories); text != "" {
			parts = append(parts, text)
			switch category {
			case "managed":
				manifest.ManagedMemory = true
			case "team":
				manifest.TeamMemory = true
			}
			manifest.MemoryItems += count
		}
	}
	if len(parts) == 0 {
		return TenantPromptContext{}, nil
	}
	manifest.Active = true
	manifest.Addendum = true
	return TenantPromptContext{Text: "# Tenant Code Memory\nThe following approved user, managed, and team memory is maintained through tenant APIs. Treat it as durable user or organization guidance unless it conflicts with higher-priority instructions.\n\n" + strings.Join(parts, "\n\n"), Manifest: manifest}, nil
}

type TenantSkillProvider struct {
	Service TenantService
}

func (p TenantSkillProvider) ListTenantSkills(ctx context.Context, prompt string, limit int) ([]skills.Skill, error) {
	if p.Service == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = tenantSkillCatalogLimit
	}
	items, err := p.Service.ListEffectiveSkills(ctx, true, limit)
	if err != nil {
		return nil, err
	}
	out := make([]skills.Skill, 0, len(items))
	for _, item := range items {
		skill := tenantEffectiveSkillToSkill(item)
		if strings.TrimSpace(skill.Name) != "" {
			out = append(out, skill)
		}
	}
	return out, nil
}

func (p TenantSkillProvider) GetTenantSkill(ctx context.Context, name string) (skills.Skill, bool, error) {
	if p.Service == nil || strings.TrimSpace(name) == "" {
		return skills.Skill{}, false, nil
	}
	item, err := p.Service.GetEffectiveSkill(ctx, name, 0)
	if err != nil {
		if errors.Is(err, mysqlstore.ErrNotFound) {
			return skills.Skill{}, false, nil
		}
		return skills.Skill{}, false, err
	}
	if !item.Enabled {
		return skills.Skill{}, false, nil
	}
	skill := tenantEffectiveSkillToSkill(item)
	if strings.TrimSpace(skill.Name) == "" {
		return skills.Skill{}, false, nil
	}
	return skill, true, nil
}

func tenantEffectiveSkillToSkill(item mysqlstore.EffectiveSkill) skills.Skill {
	skill := skills.SkillFromContent(item.SkillKey, item.SkillKey, item.ContentMD)
	skill.Source = skills.SourceTenant
	skill.Path = "tenant://" + item.SkillKey
	skill.Name = item.SkillKey
	skill.LocalName = item.SkillKey
	skill.Version = firstNonEmptyTenantSkill(skill.Version, fmt.Sprintf("%d", item.Version))
	skill.Description = firstNonEmptyTenantSkill(skill.Description, item.Description)
	skill.PackageRef = strings.TrimSpace(item.PackageRef)
	skill.PackageSHA256 = strings.TrimSpace(item.PackageSHA256)
	skill.RuntimeRef = strings.TrimSpace(item.RuntimeRef)
	return skill
}

func firstNonEmptyTenantSkill(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func formatTenantMemories(memories []mysqlstore.Memory) string {
	text, _ := formatTenantMemoriesWithCount(memories)
	return text
}

func formatTenantMemoriesWithCount(memories []mysqlstore.Memory) (string, int) {
	var lines []string
	for _, item := range memories {
		if isNonInjectableMemoryCategory(item.Category) {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		label := strings.TrimSpace(item.MemoryKey)
		if category := strings.TrimSpace(item.Category); category != "" {
			if label == "" {
				label = category
			} else {
				label = category + "/" + label
			}
		}
		if label == "" {
			lines = append(lines, "- "+content)
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", label, content))
		}
	}
	if len(lines) == 0 {
		return "", 0
	}
	return "## Tenant Memories\n" + strings.Join(lines, "\n"), len(lines)
}

func isNonInjectableMemoryCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "automem_pending", "automem_approved", "automem_rejected", "automem_archived",
		"explicit_pending", "explicit_approved", "explicit_rejected", "explicit_archived",
		"memory_pending", "memory_approved", "memory_rejected", "memory_archived":
		return true
	default:
		return false
	}
}

func formatTenantProfile(profile mysqlstore.Profile) string {
	var parts []string
	if summary := strings.TrimSpace(profile.Summary); summary != "" {
		parts = append(parts, summary)
	}
	if raw := strings.TrimSpace(profile.ProfileJSON); raw != "" {
		parts = append(parts, "Profile JSON:\n"+raw)
	}
	if len(parts) == 0 {
		return ""
	}
	return "## Tenant User Profile\n" + strings.Join(parts, "\n\n")
}

func formatTenantDocument(doc mysqlstore.Document) string {
	content := strings.TrimSpace(doc.ContentMD)
	if content == "" {
		content = strings.TrimSpace(doc.ContentJSON)
	}
	if content == "" {
		return ""
	}
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		title = doc.DocType
	}
	return "## Tenant Document: " + title + "\n" + content
}

func formatTenantKnowledge(chunks []mysqlstore.KnowledgeChunk) string {
	var lines []string
	for _, chunk := range chunks {
		content := strings.TrimSpace(chunk.Content)
		if content == "" {
			continue
		}
		title := strings.TrimSpace(chunk.Title)
		if title == "" {
			title = fmt.Sprintf("document-%d", chunk.DocumentID)
		}
		lines = append(lines, fmt.Sprintf("- %s#%d: %s", title, chunk.ChunkIndex, content))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Tenant Knowledge Search Results\n" + strings.Join(lines, "\n")
}
