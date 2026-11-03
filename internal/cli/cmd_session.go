package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/recap"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessiondiag"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tui"
)

func sessionCommand(args []string, stdout io.Writer) error {
	return sessionCommandWithContext(context.Background(), args, stdout)
}

// sessionCommandWithContext keeps the legacy helper callable by local-session
// tests while managed Session Control commands receive their trusted runtime
// identity from the top-level command dispatcher.
func sessionCommandWithContext(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		if len(args) > 0 && hasArg(args[1:], "--source") {
			return sessionManagedListCommand(ctx, args[1:], stdout)
		}
		if hasArg(args, "--json") {
			summaries, err := session.DefaultStore().List()
			if err != nil {
				return err
			}
			return writePrettyJSON(stdout, summaries)
		}
		return sessionListCommand(session.DefaultStore(), "", stdout)
	}
	switch args[0] {
	case "create":
		return sessionManagedCreateCommand(ctx, args[1:], stdout)
	case "get":
		return sessionManagedGetCommand(ctx, args[1:], stdout)
	case "send":
		return sessionManagedSendCommand(ctx, args[1:], stdout)
	case "stop":
		return sessionManagedStopCommand(ctx, args[1:], stdout)
	case "attach":
		return sessionManagedAttachCommand(ctx, args[1:], stdout)
	case "monitor":
		return sessionManagedMonitorCommand(ctx, args[1:], stdout)
	case "locate":
		return sessionLocateCommand(args, stdout)
	case "show":
		return sessionShowCommand(args, stdout)
	case "inspect":
		return sessionInspectCommand(args, stdout)
	case "delete", "rm":
		return sessionDeleteCommand(args, stdout)
	case "rename":
		return sessionRenameCommand(args, stdout)
	case "checkpoint":
		return sessionCheckpointCommand(args, stdout)
	case "rewind":
		return sessionRewindCommand(args, stdout)
	case "branches":
		return sessionBranchesCommand(args, stdout)
	case "redo":
		return sessionRedoCommand(args, stdout)
	case "fork":
		return sessionForkCommand(args, stdout)
	case "search":
		return sessionSearchCommand(args, stdout)
	case "clear":
		return sessionClearCommand(args, stdout)
	case "gc":
		return sessionGCCommand(args, stdout)
	}
	return fmt.Errorf("unknown session command: %s", args[0])
}

// sessionGCCommand reclaims file-history blobs outside the retention window,
// independently of conversation transcripts. It requires --file-history so a bare
// `session gc` cannot be mistaken for a broader sweep, and supports --dry-run.
func sessionGCCommand(args []string, stdout io.Writer) error {
	rest := args[1:]
	if !hasArg(rest, "--file-history") {
		return errors.New("session gc requires --file-history (reclaims file-content blobs outside the retention window; conversation transcripts are untouched)")
	}
	maxTurns, maxAgeDays, maxBytes := config.Load().Settings.ResolvedFileHistory()
	if v, ok := lastIntFlag(rest, "--max-turns"); ok {
		maxTurns = v
	}
	if v, ok := lastIntFlag(rest, "--max-age-days"); ok {
		maxAgeDays = v
	}
	if v, ok := lastInt64Flag(rest, "--max-bytes"); ok {
		maxBytes = v
	}
	result, err := session.DefaultStore().GCFileHistory(session.FileHistoryGCOptions{
		MaxTurns:   maxTurns,
		MaxAgeDays: maxAgeDays,
		MaxBytes:   maxBytes,
		DryRun:     hasArg(rest, "--dry-run"),
	})
	if err != nil {
		return err
	}
	if hasArg(rest, "--json") {
		return writePrettyJSON(stdout, result)
	}
	verb := "Removed"
	if result.DryRun {
		verb = "Would remove"
	}
	fmt.Fprintf(stdout, "%s %d file-history blob(s), %d bytes; kept %d blob(s) (%d bytes) within window (maxTurns=%d maxAgeDays=%d maxBytes=%d)\n",
		verb, result.Removed, result.Freed, result.Kept, result.KeptBytes, maxTurns, maxAgeDays, maxBytes)
	return nil
}

// lastIntFlag returns the last integer value given for flag, if any and valid.
func lastIntFlag(args []string, flag string) (int, bool) {
	values := valuesAfterFlag(args, flag)
	if len(values) == 0 {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(values[len(values)-1]))
	if err != nil {
		return 0, false
	}
	return v, true
}

// lastInt64Flag returns the last int64 value given for flag, if any and valid.
func lastInt64Flag(args []string, flag string) (int64, bool) {
	values := valuesAfterFlag(args, flag)
	if len(values) == 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(values[len(values)-1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func sessionLocateCommand(args []string, stdout io.Writer) error {
	id := ""
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "--") {
			id = arg
			break
		}
	}
	var (
		summary session.Summary
		ok      bool
		err     error
	)
	if id == "" {
		cwd, wdErr := os.Getwd()
		if wdErr != nil {
			return wdErr
		}
		summary, ok, err = session.DefaultStore().LatestForProject(cwd)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no sessions found for project %s", cwd)
		}
	} else {
		summary, ok, err = session.DefaultStore().Locate(id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("session not found: %s", id)
		}
	}
	if hasArg(args[1:], "--json") {
		return writePrettyJSON(stdout, summary)
	}
	_, err = fmt.Fprintf(stdout, "%s\t%s\n", summary.SessionID, summary.Path)
	return err
}

func sessionShowCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session show requires a session id")
	}
	summary, ok, err := session.DefaultStore().Find(args[1])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	entries, err := session.Load(summary.Path)
	if err != nil {
		return err
	}
	return writePrettyJSON(stdout, entries)
}

func sessionInspectCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session inspect requires a session id")
	}
	logPaths := valuesAfterFlag(args[2:], "--log")
	if len(logPaths) == 0 {
		path, err := config.CurrentIdentity("").GlobalStatePath("debug", "niuma-tui.log")
		if err != nil {
			return err
		}
		logPaths, err = filepath.Glob(path + "*")
		if err != nil {
			return err
		}
		if len(logPaths) == 0 {
			logPaths = []string{path}
		}
	}
	report, err := sessiondiag.Inspect(sessiondiag.Options{SessionID: args[1], Store: session.DefaultStore(), LogPaths: logPaths})
	if err != nil {
		return err
	}
	if hasArg(args[2:], "--json") {
		return writePrettyJSON(stdout, report)
	}
	return writeSessionInspection(stdout, report)
}

func sessionDeleteCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session delete requires a session id")
	}
	ok, err := session.DefaultStore().Delete(args[1])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	fmt.Fprintf(stdout, "Deleted session %s\n", args[1])
	return nil
}

func sessionRenameCommand(args []string, stdout io.Writer) error {
	if len(args) < 3 {
		return errors.New("session rename requires a session id and name")
	}
	name := strings.Join(args[2:], " ")
	ok, err := session.DefaultStore().Rename(args[1], name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	fmt.Fprintf(stdout, "Renamed session %s\n", args[1])
	return nil
}

func sessionCheckpointCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session checkpoint requires a session id")
	}
	name := strings.Join(args[2:], " ")
	entry, ok, err := session.DefaultStore().Checkpoint(args[1], name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	if entry.Name == "" {
		fmt.Fprintf(stdout, "Checkpointed session %s\n", args[1])
	} else {
		fmt.Fprintf(stdout, "Checkpointed session %s at %s\n", args[1], entry.Name)
	}
	return nil
}

func sessionRewindCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session rewind requires a session id")
	}
	checkpoint := strings.Join(args[2:], " ")
	result, ok, err := session.DefaultStore().Rewind(args[1], checkpoint)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	return writePrettyJSON(stdout, result)
}

// sessionBranchesCommand lists the branches (leaves) of a v2 message-graph
// transcript, or compares two of them with --compare <leafA> <leafB>.
// Non-destructive rewinds and redos create branches; this is how you find an
// abandoned branch's leaf id to redo back to, and how you diff two branches.
func sessionBranchesCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session branches requires a session id")
	}
	sessionID := args[1]
	if hasArg(args, "--compare") {
		leafA, leafB, ok := twoArgsAfter(args, "--compare")
		if !ok {
			return errors.New("session branches --compare requires two branch leaf ids")
		}
		cmp, ok, err := session.DefaultStore().CompareBranches(sessionID, leafA, leafB)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("session not found: %s", sessionID)
		}
		if hasArg(args, "--json") {
			return writePrettyJSON(stdout, cmp)
		}
		return writeBranchComparison(stdout, cmp)
	}
	branches, ok, err := session.DefaultStore().Branches(sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, branches)
	}
	if len(branches) == 0 {
		fmt.Fprintln(stdout, "No branches")
		return nil
	}
	for _, branch := range branches {
		marker := " "
		if branch.Active {
			marker = "*"
		}
		fmt.Fprintf(stdout, "%s %s  msgs=%d", marker, branch.LeafID, branch.Messages)
		if branch.ForkPoint != "" {
			fmt.Fprintf(stdout, "  forked@%s", branch.ForkPoint)
		}
		if branch.Preview != "" {
			fmt.Fprintf(stdout, "  %s", branch.Preview)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

// twoArgsAfter returns the two arguments immediately following flag, when both
// are present and are not themselves flags.
func twoArgsAfter(args []string, flag string) (string, string, bool) {
	for i := 0; i+2 < len(args); i++ {
		if args[i] != flag {
			continue
		}
		a, b := args[i+1], args[i+2]
		if strings.HasPrefix(a, "-") || strings.HasPrefix(b, "-") {
			return "", "", false
		}
		return a, b, true
	}
	return "", "", false
}

func writeBranchComparison(stdout io.Writer, cmp session.BranchComparison) error {
	fmt.Fprintf(stdout, "Shared history: %d node(s)", cmp.CommonLength)
	if cmp.ForkPoint != "" {
		fmt.Fprintf(stdout, " (forked at %s)", cmp.ForkPoint)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "Only on %s:\n", cmp.LeafA)
	for _, node := range cmp.OnlyA {
		fmt.Fprintf(stdout, "  - %s: %s\n", node.Role, node.Preview)
	}
	fmt.Fprintf(stdout, "Only on %s:\n", cmp.LeafB)
	for _, node := range cmp.OnlyB {
		fmt.Fprintf(stdout, "  + %s: %s\n", node.Role, node.Preview)
	}
	return nil
}

// sessionRedoCommand moves the active leaf back onto a previously abandoned
// branch (append-only; the current branch is not destroyed either). By default it
// also restores the workspace to that branch's end state, the mirror of a
// file-restoring rewind; --conversation-only moves only the conversation pointer.
func sessionRedoCommand(args []string, stdout io.Writer) error {
	if len(args) < 3 {
		return errors.New("session redo requires a session id and a branch leaf id (see `session branches`)")
	}
	store := session.DefaultStore()
	redo := store.RedoToBranch
	if hasArg(args, "--conversation-only") {
		redo = store.Redo
	}
	result, ok, err := redo(args[1], args[2])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Redone to branch %s (%d messages on chain, %d files restored)\n", result.MessageID, result.EntriesKept, result.FilesRestored)
	if len(result.MetadataDegraded) > 0 {
		fmt.Fprintf(stdout, "Note: %d metadata item(s) could not be restored\n", len(result.MetadataDegraded))
	}
	return nil
}

func sessionForkCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session fork requires a session id")
	}
	name := ""
	var checkpointParts []string
	for i := 2; i < len(args); i++ {
		if args[i] == "--name" || args[i] == "-n" {
			if i+1 >= len(args) {
				return errors.New("session fork --name requires a value")
			}
			name = args[i+1]
			i++
			continue
		}
		checkpointParts = append(checkpointParts, args[i])
	}
	result, ok, err := session.DefaultStore().Fork(args[1], strings.Join(checkpointParts, " "), name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session not found: %s", args[1])
	}
	return writePrettyJSON(stdout, result)
}

func sessionSearchCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New("session search requires a query")
	}
	results, err := session.DefaultStore().Search(strings.Join(withoutFlags(args[1:], "--json"), " "))
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, results)
	}
	if len(results) == 0 {
		fmt.Fprintln(stdout, "No sessions found")
		return nil
	}
	return writeSessionList(stdout, results)
}

func sessionClearCommand(args []string, stdout io.Writer) error {
	if err := session.DefaultStore().Clear(); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Cleared sessions")
	return nil
}

func writeSessionInspection(stdout io.Writer, report sessiondiag.Report) error {
	if _, err := fmt.Fprintf(stdout, "Session: %s\nTranscript: %s\nConfidence: %s\n", report.SessionID, report.SessionFile, report.Confidence); err != nil {
		return err
	}
	for _, request := range report.Requests {
		endpoint := "unknown"
		if len(request.Attempts) > 0 && request.Attempts[len(request.Attempts)-1].Endpoint != "" {
			endpoint = request.Attempts[len(request.Attempts)-1].Endpoint
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\tattempts=%d\t%s\n", request.StartedAt.Format(time.RFC3339), request.Purpose, request.Model, request.FinalProvider, endpoint, len(request.Attempts), request.Status); err != nil {
			return err
		}
	}
	for _, warning := range report.Warnings {
		if _, err := fmt.Fprintf(stdout, "Warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func valuesAfterFlag(args []string, flag string) []string {
	var values []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			values = append(values, args[i+1])
			i++
		}
	}
	return values
}

func sessionListCommand(store session.Store, cwd string, stdout io.Writer) error {
	var (
		summaries []session.Summary
		err       error
	)
	if strings.TrimSpace(cwd) != "" {
		summaries, err = store.ListForCWD(cwd)
	} else {
		summaries, err = store.List()
	}
	if err != nil {
		return err
	}
	if len(summaries) == 0 {
		fmt.Fprintln(stdout, "No sessions found")
		return nil
	}
	return writeSessionList(stdout, summaries)
}

type resumeSessionItem struct {
	ID      string
	Title   string
	Preview string
	CWD     string
	Path    string
	Updated time.Time
}

func writeSessionList(stdout io.Writer, summaries []session.Summary) error {
	for i, summary := range summaries {
		item := resumeSessionItemFromSummary(summary)
		title := firstNonEmptyString(item.Title, item.Preview, "(untitled session)")
		fmt.Fprintf(stdout, "%d. %s\n", i+1, title)
		fmt.Fprintf(stdout, "   id: %s\n", item.ID)
		if !item.Updated.IsZero() {
			fmt.Fprintf(stdout, "   updated: %s\n", item.Updated.Format("2006-01-02 15:04:05"))
		}
		if item.Preview != "" && item.Preview != title {
			fmt.Fprintf(stdout, "   recent: %s\n", item.Preview)
		}
		if item.CWD != "" {
			fmt.Fprintf(stdout, "   project: %s\n", item.CWD)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func resumeSessionItems(store session.Store, cwd string, excludedSessionIDs []string, limit int) ([]tui.ResumeSession, error) {
	var (
		summaries []session.Summary
		err       error
	)
	if strings.TrimSpace(cwd) != "" {
		summaries, err = store.ListForCWD(cwd)
	} else {
		summaries, err = store.List()
	}
	if err != nil {
		return nil, err
	}
	out := make([]tui.ResumeSession, 0, len(summaries))
	excluded := make(map[string]struct{}, len(excludedSessionIDs))
	for _, sessionID := range excludedSessionIDs {
		if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
			excluded[sessionID] = struct{}{}
		}
	}
	for _, summary := range summaries {
		if _, skip := excluded[summary.SessionID]; skip {
			continue
		}
		item := resumeSessionItemFromSummary(summary)
		out = append(out, tui.ResumeSession{
			ID:      item.ID,
			Title:   item.Title,
			Preview: item.Preview,
			CWD:     item.CWD,
			Updated: item.Updated,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func resumeSessionItemFromSummary(summary session.Summary) resumeSessionItem {
	item := resumeSessionItem{
		ID:      summary.SessionID,
		Title:   strings.TrimSpace(summary.Title),
		Path:    summary.Path,
		Updated: summary.ModTime,
		CWD:     cwdFromSessionPath(summary.Path),
	}
	entries, err := session.Load(summary.Path)
	if err != nil {
		return item
	}
	if item.Title == "" {
		item.Title = firstMeaningfulMessage(entries, "user", 80)
	}
	item.Preview = recentConversationPreview(entries, 140)
	return item
}

func recentConversationPreview(entries []session.Entry, limit int) string {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type != "message" || strings.TrimSpace(entry.Content) == "" {
			continue
		}
		role := strings.TrimSpace(entry.Role)
		if role == "" {
			role = "message"
		}
		return strings.ToLower(role) + ": " + shortText(entry.Content, limit)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if strings.TrimSpace(entry.Content) != "" {
			label := firstNonEmptyString(entry.Type, "entry")
			return label + ": " + shortText(entry.Content, limit)
		}
	}
	return ""
}

func firstMeaningfulMessage(entries []session.Entry, role string, limit int) string {
	for _, entry := range entries {
		if entry.Type == "message" && strings.EqualFold(entry.Role, role) && strings.TrimSpace(entry.Content) != "" {
			return shortText(entry.Content, limit)
		}
	}
	return ""
}

func cwdFromSessionPath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(filepath.Dir(dir)) != "projects" {
		return ""
	}
	slug := filepath.Base(dir)
	if slug == "" {
		return ""
	}
	return "/" + strings.ReplaceAll(slug, "-", "/")
}

type goalQueryRunner struct {
	opts options
}

func loadResumeMessages(store session.Store, sessionID, resumeAt string) ([]anthropic.MessageParam, error) {
	messages, _, err := loadResumeContext(store, sessionID, resumeAt)
	return messages, err
}

func loadResumeContext(store session.Store, sessionID, resumeAt string) ([]anthropic.MessageParam, []query.ToolResultReplacementRecord, error) {
	_, messages, replacements, err := loadResumeContextWithEntries(store, sessionID, resumeAt)
	return messages, replacements, err
}

func loadResumeContextWithEntries(store session.Store, sessionID, resumeAt string) ([]session.Entry, []anthropic.MessageParam, []query.ToolResultReplacementRecord, error) {
	entries, err := loadResumeEntries(store, sessionID, resumeAt)
	if err != nil || entries == nil {
		return nil, nil, nil, err
	}
	return entries, query.MessagesFromTranscript(entries), query.ToolResultReplacementsFromTranscript(entries), nil
}

// loadResumeEntries centralizes transcript lookup so CLI query context and TUI
// recovery notices are derived from the same resume-at slice.
func loadResumeEntries(store session.Store, sessionID, resumeAt string) ([]session.Entry, error) {
	if sessionID == "" {
		return nil, nil
	}
	var summary session.Summary
	var ok bool
	var err error
	if sessionID == "latest" {
		summaries, err := store.List()
		if err != nil {
			return nil, err
		}
		if len(summaries) == 0 {
			return nil, errors.New("no sessions found")
		}
		summary = summaries[0]
	} else {
		summary, ok, err = store.Find(sessionID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("session not found: %s", sessionID)
		}
	}
	entries, format, err := session.LoadWithFormat(summary.Path)
	if err != nil {
		return nil, err
	}
	if err := session.ValidateResumeFormat(format); err != nil {
		return nil, err
	}
	resumeAt = strings.TrimSpace(resumeAt)
	if format == session.TranscriptFormatGolangCCV2 {
		// v2 is a message graph: derive the current conversation by leaf-walk
		// rather than assuming file order == conversation order. An explicit
		// resume-at node picks the chain ending at that node.
		if resumeAt != "" {
			chain := session.ChainToLeaf(entries, resumeAt)
			if len(chain) == 0 {
				return nil, fmt.Errorf("resume message not found: %s", resumeAt)
			}
			return chain, nil
		}
		return session.CurrentChain(entries), nil
	}
	if resumeAt != "" {
		var ok bool
		entries, ok = session.ThroughEntry(entries, resumeAt)
		if !ok {
			return nil, fmt.Errorf("resume message not found: %s", resumeAt)
		}
	}
	return entries, nil
}

type resumeAgentTaskSeeder interface {
	SeedAgentTask(mysqlstore.AgentTask)
}

func seedResumeAgentTasksFromEntries(ctx context.Context, store agenttasks.Store, entries []session.Entry) {
	seeder, ok := store.(resumeAgentTaskSeeder)
	if !ok || len(entries) == 0 {
		return
	}
	descriptions := map[string]string{}
	stopReasons := map[string]string{}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Type, "tool_call") {
			continue
		}
		switch {
		case strings.EqualFold(entry.ToolName, "AgentCreate"):
			var input struct {
				Description string `json:"description"`
				Prompt      string `json:"prompt"`
			}
			_ = json.Unmarshal([]byte(entry.Content), &input)
			description := strings.TrimSpace(input.Description)
			if description == "" {
				description = strings.TrimSpace(input.Prompt)
			}
			if description != "" {
				descriptions[entry.ToolID] = description
			}
		case strings.EqualFold(entry.ToolName, "AgentStop"):
			var input struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal([]byte(entry.Content), &input)
			if reason := strings.TrimSpace(input.Reason); reason != "" {
				stopReasons[entry.ToolID] = reason
			}
		}
	}
	taskDescriptions := map[uint64]string{}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Type, "tool_result") {
			continue
		}
		switch {
		case strings.EqualFold(entry.ToolName, "AgentCreate"):
			if task, ok := taskFromAgentCreateResumeResult(ctx, entry.Content, descriptions[entry.ToolID]); ok {
				if task.Description != "" {
					taskDescriptions[task.ID] = task.Description
				}
				seeder.SeedAgentTask(task)
			}
		case strings.EqualFold(entry.ToolName, "AgentGet"):
			if task, ok := taskFromAgentGetResumeResult(entry.Content); ok {
				if task.Description != "" {
					taskDescriptions[task.ID] = task.Description
				}
				seeder.SeedAgentTask(task)
			}
		case strings.EqualFold(entry.ToolName, "AgentStop"):
			if task, ok := taskFromAgentStopResumeResult(entry.Content, taskDescriptions, stopReasons[entry.ToolID]); ok {
				seeder.SeedAgentTask(task)
			}
		}
	}
}

func taskFromAgentCreateResumeResult(ctx context.Context, content, description string) (mysqlstore.AgentTask, bool) {
	var decoded struct {
		AgentName      string `json:"agent_name"`
		Model          string `json:"model"`
		OutputFile     string `json:"output_file"`
		PermissionMode string `json:"permission_mode"`
		SessionID      string `json:"session_id"`
		Status         string `json:"status"`
		TaskID         uint64 `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil || decoded.TaskID == 0 {
		return mysqlstore.AgentTask{}, false
	}
	status := strings.TrimSpace(decoded.Status)
	if status == "" {
		status = agenttasks.StatusRunning
	}
	resultJSON := ""
	finished := time.Time{}
	if outputFile := strings.TrimSpace(decoded.OutputFile); outputFile != "" {
		if data, err := os.ReadFile(outputFile); err == nil && len(data) > 0 {
			if state, ok := readAgentResumeOutputState(outputFile); ok {
				if state.Status != "" {
					status = state.Status
				}
				if len(state.Result) > 0 {
					resultJSON = string(state.Result)
				}
			}
			if !agentResumeStatusIsTerminal(status) {
				status = agenttasks.StatusCompleted
			}
			if agentResumeStatusIsTerminal(status) {
				finished = time.Now().UTC()
			}
			if resultJSON == "" {
				resultJSON = resumeAgentResultJSON(decoded.AgentName, decoded.Model, decoded.SessionID, outputFile, strings.TrimSpace(string(data)), "", "")
			}
		} else if err == nil {
			status = agenttasks.StatusFailed
			finished = time.Now().UTC()
			message := "background agent state was restored from resume transcript, but the output_file is empty and no terminal state sidecar was available; the original background process is not attached to this resumed CLI process, so the task is treated as interrupted"
			resultJSON = resumeAgentResultJSON(decoded.AgentName, decoded.Model, decoded.SessionID, outputFile, message, "", agenttasks.StatusFailed)
		} else {
			status = agenttasks.StatusFailed
			finished = time.Now().UTC()
			message := "background agent state was restored from resume transcript, but the output_file is not readable in this process"
			if err != nil {
				observability.Error(ctx, nil, "agent.resume.output_file_unavailable", "cli.seedResumeAgentTasksFromEntries", "resume agent output file unavailable", "path", outputFile, "error", err)
			}
			resultJSON = resumeAgentResultJSON(decoded.AgentName, decoded.Model, decoded.SessionID, outputFile, message, "", agenttasks.StatusFailed)
		}
	}
	metadata, _ := json.Marshal(map[string]any{
		"permission_mode": decoded.PermissionMode,
		"output_file":     decoded.OutputFile,
		"resume_seeded":   true,
	})
	return mysqlstore.AgentTask{
		ID:                 decoded.TaskID,
		SubagentSessionKey: decoded.SessionID,
		AgentName:          firstNonEmptyString(decoded.AgentName, "general-purpose"),
		Description:        description,
		Status:             status,
		Model:              decoded.Model,
		ResultJSON:         resultJSON,
		MetadataJSON:       string(metadata),
		StartedAt:          time.Now().UTC(),
		FinishedAt:         finished,
	}, true
}

type agentResumeOutputState struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

func readAgentResumeOutputState(outputFile string) (agentResumeOutputState, bool) {
	path := strings.TrimSpace(outputFile) + ".state.json"
	if strings.TrimSpace(outputFile) == "" {
		return agentResumeOutputState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return agentResumeOutputState{}, false
	}
	var state agentResumeOutputState
	if err := json.Unmarshal(data, &state); err != nil {
		return agentResumeOutputState{}, false
	}
	state.Status = strings.TrimSpace(strings.ToLower(state.Status))
	return state, state.Status != "" || len(state.Result) > 0
}

func agentResumeStatusIsTerminal(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return true
	default:
		return false
	}
}

func taskFromAgentGetResumeResult(content string) (mysqlstore.AgentTask, bool) {
	var decoded struct {
		Task struct {
			ID          uint64 `json:"id"`
			AgentName   string `json:"agent_name"`
			Description string `json:"description"`
			Status      string `json:"status"`
			Model       string `json:"model"`
			SessionID   string `json:"session_id"`
		} `json:"task"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil || decoded.Task.ID == 0 {
		return mysqlstore.AgentTask{}, false
	}
	status := decoded.Task.Status
	if status == "" {
		status = agenttasks.StatusCompleted
	}
	finished := time.Time{}
	if status == agenttasks.StatusCompleted || status == agenttasks.StatusFailed || status == agenttasks.StatusCancelled {
		finished = time.Now().UTC()
	}
	return mysqlstore.AgentTask{
		ID:                 decoded.Task.ID,
		SubagentSessionKey: decoded.Task.SessionID,
		AgentName:          firstNonEmptyString(decoded.Task.AgentName, "general-purpose"),
		Description:        decoded.Task.Description,
		Status:             status,
		Model:              decoded.Task.Model,
		ResultJSON:         string(decoded.Result),
		StartedAt:          time.Now().UTC(),
		FinishedAt:         finished,
	}, true
}

func acknowledgedAgentTasksFromResumeEntries(entries []session.Entry) []uint64 {
	seen := map[uint64]bool{}
	var ids []uint64
	for _, entry := range entries {
		if !strings.EqualFold(entry.Type, "tool_result") || !strings.EqualFold(entry.ToolName, "AgentGet") {
			continue
		}
		task, ok := taskFromAgentGetResumeResult(entry.Content)
		if !ok || task.ID == 0 || seen[task.ID] || !agentResumeStatusIsTerminal(task.Status) {
			continue
		}
		seen[task.ID] = true
		ids = append(ids, task.ID)
	}
	return ids
}

func activeSkillMessagesFromResumeEntries(entries []session.Entry) []anthropic.MessageParam {
	var messages []anthropic.MessageParam
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Type, "message") || !strings.EqualFold(entry.Role, "user") {
			continue
		}
		content := strings.TrimSpace(entry.Content)
		if !looksLikeActiveSkillContext(content) || seen[content] {
			continue
		}
		seen[content] = true
		messages = append(messages, anthropic.MessageParam{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: content}},
		})
		if len(messages) > 8 {
			messages = messages[len(messages)-8:]
		}
	}
	return messages
}

func looksLikeActiveSkillContext(content string) bool {
	return strings.Contains(content, "<system-reminder>") &&
		strings.Contains(content, "Skill ") &&
		strings.Contains(content, " instructions are now active") &&
		strings.Contains(content, "</system-reminder>")
}

func taskFromAgentStopResumeResult(content string, descriptions map[uint64]string, reason string) (mysqlstore.AgentTask, bool) {
	var decoded struct {
		Cancelled bool   `json:"cancelled"`
		TaskID    uint64 `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil || decoded.TaskID == 0 || !decoded.Cancelled {
		return mysqlstore.AgentTask{}, false
	}
	result := "background agent was cancelled before completion"
	if reason = strings.TrimSpace(reason); reason != "" {
		result += ": " + reason
	}
	return mysqlstore.AgentTask{
		ID:          decoded.TaskID,
		Description: descriptions[decoded.TaskID],
		Status:      agenttasks.StatusCancelled,
		ResultJSON:  resumeAgentResultJSON("", "", "", "", result, "", agenttasks.StatusCancelled),
		StartedAt:   time.Now().UTC(),
		FinishedAt:  time.Now().UTC(),
	}, true
}

func resumeAgentResultJSON(agentName, model, sessionID, outputFile, content, transcriptPath, status string) string {
	payload := map[string]any{
		"agent_name":  firstNonEmptyString(agentName, "general-purpose"),
		"model":       model,
		"session_id":  sessionID,
		"output_file": outputFile,
		"content":     content,
	}
	if strings.TrimSpace(transcriptPath) != "" {
		payload["transcript_path"] = transcriptPath
	}
	if strings.TrimSpace(status) != "" {
		payload["status"] = strings.TrimSpace(status)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func tuiResumeHistoryLimit(cwd string) int {
	loaded := config.LoadSettings(cwd)
	if loaded.TUI == nil || loaded.TUI.ResumeHistoryLimit == nil {
		return defaultTUIResumeHistoryLimit
	}
	if *loaded.TUI.ResumeHistoryLimit < 0 {
		return 0
	}
	return *loaded.TUI.ResumeHistoryLimit
}

// tuiResumeInitialMessages turns low-level resume repair data into short,
// UI-only rows. The model context is still provided by loadResumeMessages.
func tuiResumeInitialMessages(store session.Store, opts options, historyLimit int) ([]tui.InitialMessage, error) {
	if strings.TrimSpace(opts.resume) == "" {
		return nil, nil
	}
	entries, err := loadResumeEntries(store, opts.resume, opts.resumeSessionAt)
	if err != nil {
		return nil, err
	}
	_, report := query.MessagesFromTranscriptWithReport(entries)
	label := strings.TrimSpace(opts.resume)
	if label == "latest" {
		label = "latest session"
	}
	var parts []string
	parts = append(parts, "Resumed "+label+".")
	if report.Interrupted {
		switch report.Kind {
		case query.ResumeInterruptedTurn:
			parts = append(parts, "Recovered an interrupted tool turn; missing tool results were repaired and the transcript is ready to continue.")
		case query.ResumeInterruptedPrompt:
			parts = append(parts, "Recovered a prompt that had no assistant response; the transcript includes a no-response sentinel.")
		default:
			parts = append(parts, "Recovered interrupted transcript state.")
		}
	}
	var details []string
	if report.SyntheticToolResults > 0 {
		details = append(details, fmt.Sprintf("%d synthetic tool_result", report.SyntheticToolResults))
	}
	if report.DroppedOrphanedToolResults > 0 {
		details = append(details, fmt.Sprintf("%d orphaned tool_result removed", report.DroppedOrphanedToolResults))
	}
	if report.DroppedThinking > 0 {
		details = append(details, fmt.Sprintf("%d unsafe thinking block removed", report.DroppedThinking))
	}
	if len(details) > 0 {
		parts = append(parts, "Recovery details: "+strings.Join(details, ", ")+".")
	}
	initial := []tui.InitialMessage{{Role: "status", Content: strings.Join(parts, " ")}}
	initial = append(initial, recentTranscriptUIMessages(entries, historyLimit)...)
	// Recap is session-wide (off the conversation chain in v2), so locate it from
	// the full transcript rather than the current-chain resume slice.
	if full, ferr := loadFullTranscript(store, opts.resume); ferr == nil {
		if latest, ok := recap.Latest(full); ok {
			initial = append(initial, tui.InitialMessage{Role: "recap", Content: latest.Content})
		}
	}
	return initial, nil
}

// loadFullTranscript resolves a session id (or "latest") to its whole transcript,
// unfiltered by the message-graph current chain. Used for session-wide artifacts
// like recap that are not conversation nodes.
func loadFullTranscript(store session.Store, sessionID string) ([]session.Entry, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}
	var path string
	if sessionID == "latest" {
		summaries, err := store.List()
		if err != nil {
			return nil, err
		}
		if len(summaries) == 0 {
			return nil, nil
		}
		path = summaries[0].Path
	} else {
		summary, ok, err := store.Find(sessionID)
		if err != nil || !ok {
			return nil, err
		}
		path = summary.Path
	}
	return session.Load(path)
}

func tuiResumeHistoryMessages(store session.Store, sessionID, resumeAt string, limit int) ([]tui.InitialMessage, error) {
	entries, err := loadResumeEntries(store, sessionID, resumeAt)
	if err != nil {
		return nil, err
	}
	return recentTranscriptUIMessages(entries, limit), nil
}

type projectedTUITurn struct {
	messages     []tui.InitialMessage
	thinking     []tui.ThinkingDetail
	primaryCount int
	hasAssistant bool
}

func projectTranscriptUITurns(entries []session.Entry) []projectedTUITurn {
	turns := make([]projectedTUITurn, 0)
	turn := 0
	phase := 0
	for _, entry := range entries {
		role := strings.ToLower(strings.TrimSpace(entry.Role))
		content := strings.TrimSpace(entry.Content)
		if entry.Type == "message" && role == "user" && content != "" {
			turn++
			phase = 0
			turns = append(turns, projectedTUITurn{})
		}
		if turn == 0 || content == "" {
			continue
		}
		current := &turns[len(turns)-1]
		switch {
		case entry.Type == "message" && (role == "user" || role == "assistant"):
			current.messages = append(current.messages, tui.InitialMessage{Role: role, Content: content, Turn: turn, CreatedAt: entry.Timestamp})
			current.primaryCount++
			if role == "assistant" {
				current.hasAssistant = true
			}
		case entry.Type == "thinking" && role == "assistant":
			phase++
			message := tui.InitialMessage{Role: "thinking", Content: content, Turn: turn, Phase: phase, CreatedAt: entry.Timestamp}
			current.messages = append(current.messages, message)
			current.thinking = append(current.thinking, tui.ThinkingDetail{Turn: turn, Phase: phase, Content: content, CreatedAt: entry.Timestamp})
		}
	}
	return turns
}

func transcriptThinkingDetails(entries []session.Entry) []tui.ThinkingDetail {
	var details []tui.ThinkingDetail
	for _, item := range projectTranscriptUITurns(entries) {
		if item.hasAssistant {
			details = append(details, item.thinking...)
		}
	}
	return details
}

func tuiResumeThinkingDetails(store session.Store, sessionID, resumeAt string) ([]tui.ThinkingDetail, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	entries, err := loadResumeEntries(store, sessionID, resumeAt)
	if err != nil {
		return nil, err
	}
	return transcriptThinkingDetails(entries), nil
}

func recentTranscriptUIMessages(entries []session.Entry, limit int) []tui.InitialMessage {
	if limit <= 0 {
		return nil
	}
	turns := projectTranscriptUITurns(entries)
	start := len(turns)
	primary := 0
	for start > 0 && primary < limit {
		start--
		primary += turns[start].primaryCount
	}
	messages := make([]tui.InitialMessage, 0, limit)
	for _, item := range turns[start:] {
		for _, message := range item.messages {
			if message.Role == "thinking" && !item.hasAssistant {
				continue
			}
			messages = append(messages, message)
		}
	}
	return messages
}

func latestRecapForResume(store session.Store, sessionID string) string {
	// Recap is session-wide (off the conversation chain in v2): scan the full file.
	entries, err := loadFullTranscript(store, sessionID)
	if err != nil {
		return ""
	}
	entry, ok := recap.Latest(entries)
	if !ok {
		return ""
	}
	return strings.TrimSpace(entry.Content)
}
