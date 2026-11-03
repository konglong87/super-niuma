package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/skilllint"
	"github.com/konglong87/super-niuma/internal/skills"
)

func skillsCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return skillsInstallCommand(ctx, args, cwd, stdout)
		case "sync":
			return skillsSyncCommand(ctx, args, cwd, stdout)
		case "marketplace-search":
			return skillsMarketplaceSearchCommand(ctx, args, cwd, stdout)
		case "marketplace-info":
			return skillsMarketplaceInfoCommand(ctx, args, cwd, stdout)
		case "marketplace-status":
			return skillsMarketplaceStatusCommand(ctx, args, cwd, stdout)
		case "context":
			return skillsContextCommand(ctx, args, cwd, stdout)
		case "feedback":
			return skillsFeedbackCommand(ctx, args, cwd, stdout)
		case "watch":
			return skillsWatchCommand(ctx, args, cwd, stdout)
		case "package":
			return skillsPackageCommand(ctx, args, cwd, stdout)
		case "validate-bundled":
			return skillsValidateBundledCommand(ctx, args, cwd, stdout)
		case "lint":
			return skillsLintCommand(args, cwd, stdout)
		case "show":
			return skillsShowCommand(ctx, args, cwd, stdout)
		}
		if len(args) > 0 && args[0] != "list" {
			return fmt.Errorf("unknown skills command: %s", args[0])
		}
	}
	list, err := skills.List(cwd)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No skills found")
		return nil
	}
	for _, item := range list {
		fmt.Fprintf(stdout, "%s\t%s\n", item.Name, item.Description)
	}
	return nil
}

func skillsLintCommand(args []string, cwd string, stdout io.Writer) error {
	var target string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
		default:
			if target != "" {
				return fmt.Errorf("skills lint accepts at most one path")
			}
			target = args[i]
		}
	}
	var paths []string
	if strings.TrimSpace(target) != "" {
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		paths = []string{target}
	} else {
		items, err := skills.List(cwd)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, item := range items {
			if item.Path == "" || seen[item.Path] {
				continue
			}
			seen[item.Path] = true
			paths = append(paths, item.Path)
		}
	}
	report, err := skilllint.CheckPaths(paths)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, report)
	}
	for _, finding := range report.Findings {
		fmt.Fprintf(stdout, "%s:%d: %s %s: %s\n", finding.Path, finding.Line, finding.Severity, finding.Rule, finding.Message)
	}
	fmt.Fprintf(stdout, "Linted %d Skill document(s): %d error(s), %d warning(s) (report only)\n", report.Documents, report.Errors, report.Warnings)
	return nil
}

func skillsInstallCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	source := ""
	target := ""
	name := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--source":
			v, err := flagValue(args, &i, "--source")
			if err != nil {
				return err
			}
			source = v
		case "--target":
			v, err := flagValue(args, &i, "--target")
			if err != nil {
				return err
			}
			target = v
		case "--name":
			v, err := flagValue(args, &i, "--name")
			if err != nil {
				return err
			}
			name = v
		case "--json":
		default:
			if name == "" {
				name = args[i]
			} else {
				return fmt.Errorf("unknown skills install option: %s", args[i])
			}
		}
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("skills install requires --source")
	}
	skill, err := skills.InstallMarketplace(target, source, name)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, skill)
	}
	fmt.Fprintf(stdout, "Installed skill %s\n", skill.Name)
	return nil
}

func skillsSyncCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	source := ""
	target := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--source":
			v, err := flagValue(args, &i, "--source")
			if err != nil {
				return err
			}
			source = v
		case "--target":
			v, err := flagValue(args, &i, "--target")
			if err != nil {
				return err
			}
			target = v
		case "--json":
		default:
			return fmt.Errorf("unknown skills sync option: %s", args[i])
		}
	}
	synced, err := skills.SyncMarketplace(target, source)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, synced)
	}
	fmt.Fprintf(stdout, "Synced %d skill(s)\n", len(synced))
	return nil
}

func skillsMarketplaceSearchCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	source := ""
	query := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--source":
			v, err := flagValue(args, &i, "--source")
			if err != nil {
				return err
			}
			source = v
		case "--query":
			v, err := flagValue(args, &i, "--query")
			if err != nil {
				return err
			}
			query = v
		case "--json":
		default:
			if query == "" {
				query = args[i]
			} else {
				return fmt.Errorf("unknown skills marketplace-search option: %s", args[i])
			}
		}
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("skills marketplace-search requires --source")
	}
	results, err := skills.SearchMarketplace(source, query)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, results)
	}
	if len(results) == 0 {
		fmt.Fprintln(stdout, "No marketplace skills found")
		return nil
	}
	fmt.Fprintf(stdout, "Marketplace search: %d skill(s)\n", len(results))
	for _, item := range results {
		fmt.Fprintln(stdout, marketplaceSearchLine(item, source))
	}
	return nil
}

func skillsMarketplaceInfoCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	source := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--source":
			v, err := flagValue(args, &i, "--source")
			if err != nil {
				return err
			}
			source = v
		case "--json":
		default:
			return fmt.Errorf("unknown skills marketplace-info option: %s", args[i])
		}
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("skills marketplace-info requires --source")
	}
	report, err := skills.InspectMarketplace(source)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, report)
	}
	fmt.Fprintf(stdout, "Marketplace skills: %d (hashed %d, signed %d)\n", report.Count, report.Hashed, report.Signed)
	for _, item := range report.Items {
		parts := []string{item.Name}
		if item.Version != "" {
			parts = append(parts, "v"+item.Version)
		}
		if item.ContentSHA256 != "" {
			parts = append(parts, "hash")
		}
		if item.Signed {
			parts = append(parts, "signed:"+item.SignatureAlg)
		}
		if item.Path != "" {
			parts = append(parts, "path:"+item.Path)
		} else if item.InlineContent {
			parts = append(parts, "inline")
		}
		if item.Description != "" {
			fmt.Fprintf(stdout, "%s\t%s\n", strings.Join(parts, "\t"), item.Description)
		} else {
			fmt.Fprintln(stdout, strings.Join(parts, "\t"))
		}
	}
	return nil
}

func skillsMarketplaceStatusCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	// Status is a read-only UX command: compare local marketplace installs
	// with the index before users decide whether to sync or install.
	source := ""
	target := ""
	outdatedOnly := false
	missingOnly := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--source":
			v, err := flagValue(args, &i, "--source")
			if err != nil {
				return err
			}
			source = v
		case "--target":
			v, err := flagValue(args, &i, "--target")
			if err != nil {
				return err
			}
			target = v
		case "--outdated-only":
			outdatedOnly = true
		case "--missing-only":
			missingOnly = true
		case "--json":
		default:
			return fmt.Errorf("unknown skills marketplace-status option: %s", args[i])
		}
	}
	if outdatedOnly && missingOnly {
		return errors.New("skills marketplace-status cannot combine --outdated-only and --missing-only")
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("skills marketplace-status requires --source")
	}
	report, err := skills.MarketplaceStatus(target, source)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, report)
	}
	fmt.Fprintf(stdout, "Marketplace status: %d skill(s), %d current, %d outdated, %d missing\n", report.Count, report.Current, report.Outdated, report.Missing)
	for _, item := range report.Items {
		if outdatedOnly && item.Status != "outdated" {
			continue
		}
		if missingOnly && item.Status != "missing" {
			continue
		}
		fmt.Fprintln(stdout, marketplaceStatusLine(item, source))
	}
	return nil
}

func skillsContextCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	prompt := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--prompt":
			v, err := flagValue(args, &i, "--prompt")
			if err != nil {
				return err
			}
			prompt = v
		case "--json":
		default:
			if prompt == "" {
				prompt = args[i]
			} else {
				return fmt.Errorf("unknown skills context option: %s", args[i])
			}
		}
	}
	catalog, err := skills.CatalogPromptForPrompt(cwd, prompt)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, map[string]string{"prompt": prompt, "catalog": catalog})
	}
	if strings.TrimSpace(catalog) == "" {
		fmt.Fprintln(stdout, "No skills selected for context")
		return nil
	}
	fmt.Fprintln(stdout, catalog)
	return nil
}

func skillsFeedbackCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	name := ""
	comment := ""
	target := ""
	rating := 0
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--rating":
			v, err := flagValue(args, &i, "--rating")
			if err != nil {
				return err
			}
			parsed, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("--rating must be an integer: %w", err)
			}
			rating = parsed
		case "--comment":
			v, err := flagValue(args, &i, "--comment")
			if err != nil {
				return err
			}
			comment = v
		case "--target":
			v, err := flagValue(args, &i, "--target")
			if err != nil {
				return err
			}
			target = v
		case "--json":
		default:
			if name == "" {
				name = args[i]
			} else {
				return fmt.Errorf("unknown skills feedback option: %s", args[i])
			}
		}
	}
	feedback, err := skills.RecordFeedback(target, name, rating, comment)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, feedback)
	}
	fmt.Fprintf(stdout, "Recorded feedback for %s (%d/5)\n", feedback.Name, feedback.Rating)
	return nil
}

func skillsWatchCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	once := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--once":
			once = true
		case "--json":
		default:
			return fmt.Errorf("unknown skills watch option: %s", args[i])
		}
	}
	events, errs, err := skills.Watch(ctx, cwd)
	if err != nil {
		return err
	}
	writeEvent := func(event skills.WatchEvent) error {
		if hasArg(args, "--json") {
			return writePrettyJSON(stdout, event)
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", event.Operation, event.Name, event.Path)
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := writeEvent(event); err != nil {
				return err
			}
			if once {
				return nil
			}
		}
	}
}

func skillsPackageCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("skills package requires a skill directory")
	}
	output := ""
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			v, err := flagValue(args, &i, "--output")
			if err != nil {
				return err
			}
			output = v
		default:
			return fmt.Errorf("unknown skills package option: %s", args[i])
		}
	}
	if err := skills.PackageSkill(args[1], output); err != nil {
		return err
	}
	if output == "" {
		output = filepath.Clean(args[1]) + ".skill.zip"
	}
	fmt.Fprintf(stdout, "Packaged skill %s\n", output)
	return nil
}

func skillsValidateBundledCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	source := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--source":
			v, err := flagValue(args, &i, "--source")
			if err != nil {
				return err
			}
			source = v
		case "--json":
		default:
			return fmt.Errorf("unknown skills validate-bundled option: %s", args[i])
		}
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("skills validate-bundled requires --source")
	}
	report, err := skills.ValidateBundledCatalog(cwd, source)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, report)
	}
	if len(report.Missing) == 0 && len(report.Extra) == 0 && len(report.Mismatch) == 0 {
		fmt.Fprintf(stdout, "Bundled catalog matches %d skill(s)\n", len(report.Matched))
		return nil
	}
	return writePrettyJSON(stdout, report)
}

func skillsShowCommand(ctx context.Context, args []string, cwd string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("skills show requires a skill name")
	}
	skill, ok, err := skills.Load(cwd, args[1])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("skill not found: %s", args[1])
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, skill)
	}
	fmt.Fprint(stdout, skill.Content)
	if !strings.HasSuffix(skill.Content, "\n") {
		fmt.Fprintln(stdout)
	}
	return nil
}

func marketplaceSearchLine(item skills.MarketplaceSkill, source string) string {
	parts := []string{item.Name}
	if strings.TrimSpace(item.Version) != "" {
		parts = append(parts, "v"+strings.TrimSpace(item.Version))
	}
	parts = append(parts, marketplaceContentSource(item))
	security := marketplaceSecurityLabel(strings.TrimSpace(item.ContentSHA256) != "", strings.TrimSpace(item.Signature) != "" && strings.TrimSpace(item.PublicKey) != "")
	if security != "" {
		parts = append(parts, security)
	}
	if strings.TrimSpace(item.Description) != "" {
		parts = append(parts, strings.TrimSpace(item.Description))
	}
	parts = append(parts, "install: skills install --source "+source+" "+item.Name)
	return strings.Join(parts, "\t")
}

func marketplaceStatusLine(item skills.MarketplaceStatusSkill, source string) string {
	parts := []string{item.Name, item.Status}
	if item.MarketplaceVersion != "" {
		parts = append(parts, "remote v"+item.MarketplaceVersion)
	}
	if item.InstalledVersion != "" {
		parts = append(parts, "local v"+item.InstalledVersion)
	}
	security := marketplaceSecurityLabel(item.ContentSHA256 != "", item.Signed)
	if security != "" {
		parts = append(parts, security)
	}
	switch item.Status {
	case "missing":
		parts = append(parts, "install: skills install --source "+source+" "+item.Name)
	case "outdated":
		parts = append(parts, "update: skills install --source "+source+" "+item.Name)
	}
	return strings.Join(parts, "\t")
}

func marketplaceContentSource(item skills.MarketplaceSkill) string {
	if strings.TrimSpace(item.Path) != "" {
		return "path:" + strings.TrimSpace(item.Path)
	}
	if strings.TrimSpace(item.Content) != "" {
		return "inline"
	}
	return "metadata-only"
}

func marketplaceSecurityLabel(hasHash, signed bool) string {
	switch {
	case hasHash && signed:
		return "verified:hash+signature"
	case signed:
		return "verified:signature"
	case hasHash:
		return "verified:hash"
	default:
		return "unverified"
	}
}
