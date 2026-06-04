package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
	"github.com/google/uuid"
)

type Executor struct {
	paths                 config.Paths
	store                 *session.Store
	maxOutputLines        int
	allowParallelShell    bool
	allowInteractiveShell bool
	readOnly              bool
	shells                *ShellManager
	subAgents             *SubAgentManager
	mu                    sync.Mutex
}

func NewExecutor(paths config.Paths, store *session.Store, maxOutputLines int, allowParallelShell, allowInteractiveShell bool) *Executor {
	if maxOutputLines <= 0 {
		maxOutputLines = 2000
	}
	if allowInteractiveShell {
		allowParallelShell = true
	}
	exec := &Executor{
		paths:                 paths,
		store:                 store,
		maxOutputLines:        maxOutputLines,
		allowParallelShell:    allowParallelShell,
		allowInteractiveShell: allowInteractiveShell,
	}
	exec.shells = NewShellManager(paths.Workplace, maxOutputLines)
	exec.subAgents = NewSubAgentManager(maxOutputLines)
	return exec
}

func NewReadOnlyExecutor(paths config.Paths, store *session.Store, maxOutputLines int) *Executor {
	exec := NewExecutor(paths, store, maxOutputLines, false, false)
	exec.readOnly = true
	return exec
}

func (e *Executor) SetSubAgentRunner(runner SubAgentRunner) {
	e.subAgents.SetRunner(runner)
}

func (e *Executor) SetAgentLimits(maxOutputLines int, allowParallelShell, allowInteractiveShell bool) {
	if maxOutputLines <= 0 {
		maxOutputLines = 2000
	}
	if allowInteractiveShell {
		allowParallelShell = true
	}
	e.mu.Lock()
	e.maxOutputLines = maxOutputLines
	e.allowParallelShell = allowParallelShell
	e.allowInteractiveShell = allowInteractiveShell
	e.mu.Unlock()
	e.shells.SetLimit(maxOutputLines)
	e.subAgents.SetLimit(maxOutputLines)
}

func (e *Executor) Schemas(forSubAgent bool) []types.ToolSchema {
	schemas := []types.ToolSchema{
		schema("read_file", "Read a file.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       prop("string", "Workspace-relative path."),
				"start_line": prop("integer", "First line, 1-based."),
				"end_line":   prop("integer", "Last line, 1-based."),
			},
			"required": []string{"path"},
		}),
		schema("view_dir", "List a directory.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": prop("string", "Workspace-relative path."),
			},
		}),
		schema("search_grep", "Search files with a regex.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":          prop("string", "Regex pattern."),
				"mode":           prop("string", "content or filename."),
				"case_sensitive": prop("boolean", "Default true. Set false for case-insensitive search."),
			},
			"required": []string{"query"},
		}),
		schema("read_skill", "Read a visible skill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": prop("string", "Skill name."),
			},
			"required": []string{"name"},
		}),
		schema("diff_file", "Edit files and manage recorded changes.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode":             prop("string", "apply, replace, write, delete, history, revert, or revert_many."),
				"dry_run":          prop("boolean", "Preview any write mode."),
				"path":             prop("string", "Workspace-relative path."),
				"content":          prop("string", "Full file content."),
				"unified_diff":     prop("string", "Unified diff."),
				"patches":          prop("array", "Array of unified diffs."),
				"old_text":         prop("string", "Text to replace."),
				"new_text":         prop("string", "Replacement text."),
				"replace_all":      prop("boolean", "Replace every match."),
				"change_id":        prop("string", "Recorded change ID."),
				"change_ids":       prop("array", "Recorded change IDs."),
				"limit":            prop("integer", "History entry limit."),
				"allow_create":     prop("boolean", "Allow file creation."),
				"expected_current": prop("string", "Expected current content."),
				"reverse_order":    prop("boolean", "Reverse change_ids for revert_many."),
			},
			"required": []string{"mode"},
		}),
	}
	if forSubAgent {
		return schemas
	}
	shellCWD := e.paths.Workplace
	if shellCWD == "" {
		shellCWD = "workplace"
	}
	schemas = append(schemas, schema("shell_run_sync", fmt.Sprintf("Run a shell command in %q.", shellCWD), map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":     prop("string", "Shell command."),
			"timeout_sec": prop("integer", "Timeout seconds."),
		},
		"required": []string{"command"},
	}))
	if !e.allowParallelShell {
		return append(schemas, subAgentSchemas()...)
	}
	schemas = append(schemas,
		schema("shell_run_async", fmt.Sprintf("Start a background shell command in %q.", shellCWD), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": prop("string", "Shell command."),
			},
			"required": []string{"command"},
		}),
		schema("shell_async_status", "Check a background shell command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Shell ID."),
			},
		}),
		schema("shell_async_kill", "Kill a background shell command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Shell ID."),
			},
			"required": []string{"shell_id"},
		}),
	)
	if e.allowInteractiveShell {
		schemas = append(schemas,
			schema("shell_async_write", "Send input to an interactive shell.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"shell_id": prop("string", "Shell ID."),
					"input":    prop("string", "Interactive input text."),
				},
				"required": []string{"shell_id", "input"},
			}))
	}
	return append(schemas, subAgentSchemas()...)
}

func subAgentSchemas() []types.ToolSchema {
	return []types.ToolSchema{
		schema("sub_agent_list", "List available sub-agents.", nil),
		schema("sub_agent_start_async", "Start a background sub-agent.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent":       prop("string", "Sub-agent name."),
				"name":        prop("string", "Task name."),
				"instruction": prop("string", "Instructions."),
			},
			"required": []string{"instruction"},
		}),
		schema("sub_agent_check", "Check a sub-agent.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
			},
			"required": []string{"sub_agent_id"},
		}),
		schema("sub_agent_wait_check", "Wait, then check a sub-agent.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
				"wait_seconds": prop("integer", "Wait seconds."),
			},
			"required": []string{"sub_agent_id", "wait_seconds"},
		}),
		schema("sub_agent_resume_async", "Resume a completed sub-agent.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
				"instruction":  prop("string", "Follow-up instructions."),
			},
			"required": []string{"sub_agent_id", "instruction"},
		}),
	}
}

func (e *Executor) Run(ctx context.Context, sess *session.Session, name string, args map[string]any) (string, error) {
	if e.readOnly && name != "read_file" && name != "view_dir" && name != "search_grep" && name != "read_skill" && name != "diff_file" {
		return "", fmt.Errorf("tool %q is not available to read-only sub-agents", name)
	}
	switch name {
	case "read_file":
		return e.readFile(args)
	case "view_dir":
		return e.viewDir(args)
	case "search_grep":
		return e.searchGrep(args)
	case "read_skill":
		return e.readSkill(args)
	case "diff_file":
		return e.diffFile(sess, args)
	case "shell_run_sync":
		return e.shells.RunBlocking(ctx, stringArg(args, "command"), intArg(args, "timeout_sec", 60))
	case "shell_run_async":
		if !e.allowParallelShell {
			return "", fmt.Errorf("shell_run_async is not available unless parallel shell is enabled")
		}
		return e.shells.StartAsync(stringArg(args, "command"), e.allowInteractiveShell)
	case "shell_async_status":
		if !e.allowParallelShell {
			return "", fmt.Errorf("shell_async_status is not available unless parallel shell is enabled")
		}
		return e.shells.Status(stringArg(args, "shell_id")), nil
	case "shell_async_kill":
		if !e.allowParallelShell {
			return "", fmt.Errorf("shell_async_kill is not available unless parallel shell is enabled")
		}
		return e.shells.Kill(stringArg(args, "shell_id"))
	case "shell_async_write":
		if !e.allowInteractiveShell {
			return "", fmt.Errorf("shell_async_write is not available unless interactive shell is enabled")
		}
		return e.shells.Write(stringArg(args, "shell_id"), stringArg(args, "input"))
	case "sub_agent_list":
		return e.subAgents.List(e.paths), nil
	case "sub_agent_start_async":
		return e.subAgents.Start(sess, e.store, stringArg(args, "agent"), stringArg(args, "name"), stringArg(args, "instruction")), nil
	case "sub_agent_check":
		return e.subAgents.Check(stringArg(args, "sub_agent_id")), nil
	case "sub_agent_wait_check":
		return e.subAgents.WaitCheck(ctx, stringArg(args, "sub_agent_id"), intArg(args, "wait_seconds", 0))
	case "sub_agent_resume_async":
		return e.subAgents.ResumeAsync(stringArg(args, "sub_agent_id"), stringArg(args, "instruction")), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (e *Executor) readSkill(args map[string]any) (string, error) {
	name := stringArg(args, "name")
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	visible := map[string]bool{}
	for _, item := range stringSliceArg(args, "_visible_skills") {
		visible[item] = true
	}
	if !visible[name] {
		return "", fmt.Errorf("skill %q is not visible in the active session", name)
	}
	skill, err := config.LoadSkill(e.paths, name)
	if err != nil {
		return "", err
	}
	meta := []string{}
	for k, v := range skill.Metadata {
		meta = append(meta, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Strings(meta)
	out := fmt.Sprintf("name: %s\nsource: %s\npath: %s\nmetadata: %s\n\n%s", skill.Name, skill.Source, skill.Path, strings.Join(meta, " "), strings.TrimSpace(skill.Body))
	return truncate(out, e.maxOutputLines), nil
}

func (e *Executor) SubAgentSnapshots() []SubAgentSnapshot {
	return e.subAgents.Snapshots()
}

func (e *Executor) ShellSnapshots() []ShellSnapshot {
	return e.shells.Snapshots()
}

func (e *Executor) RestoreSubAgents(parent *session.Session, refs []session.SubAgentRef, subStore *session.Store) {
	e.subAgents.Restore(parent, e.store, refs, subStore)
}

func (e *Executor) Shutdown() {
	e.subAgents.StopAll()
	e.shells.KillAll()
}

func (e *Executor) readFile(args map[string]any) (string, error) {
	path, err := e.resolveWorkplacePath(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	start := intArg(args, "start_line", 1)
	end := intArg(args, "end_line", len(lines))
	if start < 1 {
		start = 1
	}
	if end > len(lines) || end <= 0 {
		end = len(lines)
	}
	if start > end {
		return "", fmt.Errorf("start_line is after end_line")
	}
	out := strings.Join(lines[start-1:end], "\n")
	return truncate(out, e.maxOutputLines), nil
}

func (e *Executor) viewDir(args map[string]any) (string, error) {
	rel := stringArg(args, "path")
	if rel == "" {
		rel = "."
	}
	path, err := e.resolveWorkplacePath(rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	rows := []string{}
	for _, ent := range entries {
		suffix := ""
		if ent.IsDir() {
			suffix = "/"
		}
		rows = append(rows, ent.Name()+suffix)
	}
	sort.Strings(rows)
	return truncate(strings.Join(rows, "\n"), e.maxOutputLines), nil
}

func (e *Executor) searchGrep(args map[string]any) (string, error) {
	query := stringArg(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	mode := stringArg(args, "mode")
	if mode == "" {
		mode = "content"
	}
	caseSensitive := boolArg(args, "case_sensitive", true)
	re, err := compileSearchPattern(query, caseSensitive)
	if err != nil {
		return "", err
	}
	matches := []string{}
	err = filepath.WalkDir(e.paths.Workplace, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(e.paths.Workplace, path)
		if strings.HasPrefix(rel, ".Asayn"+string(filepath.Separator)) || rel == ".Asayn" {
			return nil
		}
		if mode == "filename" {
			if re.MatchString(filepath.ToSlash(rel)) {
				matches = append(matches, rel)
			}
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if isBinary(data) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, i+1, line))
			}
			if len(matches) >= 200 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "no matches", nil
	}
	return truncate(strings.Join(matches, "\n"), e.maxOutputLines), nil
}

func compileSearchPattern(query string, caseSensitive bool) (*regexp.Regexp, error) {
	pattern := query
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err == nil {
		return re, nil
	}
	quoted := regexp.QuoteMeta(query)
	if !caseSensitive {
		quoted = "(?i)" + quoted
	}
	return regexp.Compile(quoted)
}

func (e *Executor) diffFile(sess *session.Session, args map[string]any) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	mode := stringArg(args, "mode")
	changeID := stringArg(args, "change_id")
	switch mode {
	case "history":
		if changeID != "" || len(stringSliceArg(args, "change_ids")) > 0 {
			return e.showChanges(sess, changeID, stringSliceArg(args, "change_ids"))
		}
		return e.changeHistory(sess, stringArg(args, "path"), intArg(args, "limit", 20))
	case "revert_many":
		return e.revertChanges(sess, appendChangeIDs(changeID, stringSliceArg(args, "change_ids")), boolArg(args, "reverse_order", true))
	}
	if mode == "revert" {
		return e.revertChange(sess, changeID)
	}

	dryRun := boolArg(args, "dry_run", false)
	if mode == "apply" {
		return e.applyDiffs(sess, args, dryRun)
	}

	path, err := e.resolveWorkplacePath(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	beforeBytes, readErr := os.ReadFile(path)
	before := ""
	existed := readErr == nil
	if existed {
		before = string(beforeBytes)
	}
	if guard := stringArg(args, "expected_current"); guard != "" && guard != before {
		return "", fmt.Errorf("expected_current guard did not match")
	}

	after := before
	action := "modify"
	switch mode {
	case "write":
		after = stringArg(args, "content")
		if !existed {
			action = "create"
		}
	case "replace":
		if !existed {
			return "", fmt.Errorf("file does not exist; use write to create files")
		}
		next, err := replaceTextBlock(before, args)
		if err != nil {
			return "", err
		}
		after = next
	case "delete":
		after = ""
		action = "delete"
		if !existed {
			return "", fmt.Errorf("file does not exist")
		}
	default:
		return "", fmt.Errorf("unsupported diff_file mode %q", mode)
	}
	diff := unifiedDiff(filepath.ToSlash(stringArg(args, "path")), before, after)
	if dryRun {
		return truncate(diff, e.maxOutputLines), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if mode == "delete" {
		if err := os.Remove(path); err != nil {
			return "", err
		}
	} else if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return "", err
	}
	change := session.FileChange{
		ID:            uuid.NewString(),
		At:            time.Now(),
		Path:          filepath.ToSlash(stringArg(args, "path")),
		Action:        action,
		BeforeContent: before,
		AfterContent:  after,
		UnifiedDiff:   diff,
	}
	if err := e.store.AddChange(sess, change); err != nil {
		return "", err
	}
	return truncate(fmt.Sprintf("change_id=%s\n%s", change.ID, diff), e.maxOutputLines), nil
}

func replaceTextBlock(before string, args map[string]any) (string, error) {
	oldText := stringArg(args, "old_text")
	if oldText == "" {
		return "", fmt.Errorf("old_text is required for replace mode")
	}
	newText := stringArg(args, "new_text")
	count := strings.Count(before, oldText)
	if count == 0 {
		return "", fmt.Errorf("old_text not found")
	}
	if !boolArg(args, "replace_all", false) && count != 1 {
		return "", fmt.Errorf("old_text matched %d times; include more surrounding lines or set replace_all=true", count)
	}
	if boolArg(args, "replace_all", false) {
		return strings.ReplaceAll(before, oldText, newText), nil
	}
	return strings.Replace(before, oldText, newText, 1), nil
}

func (e *Executor) applyDiffs(sess *session.Session, args map[string]any, preview bool) (string, error) {
	diffs := stringSliceArg(args, "patches")
	if one := stringArg(args, "unified_diff"); one != "" {
		diffs = append([]string{one}, diffs...)
	}
	if len(diffs) == 0 {
		return "", fmt.Errorf("unified_diff or patches is required")
	}
	plans := []diffApplyPlan{}
	for _, raw := range diffs {
		parsed, err := parseUnifiedDiff(raw, filepath.ToSlash(stringArg(args, "path")))
		if err != nil {
			return "", err
		}
		plans = append(plans, parsed...)
	}
	if len(plans) == 0 {
		return "", fmt.Errorf("diff contained no file patches")
	}

	outputs := []string{}
	for _, plan := range plans {
		path, err := e.resolveWorkplacePath(plan.Path)
		if err != nil {
			return "", err
		}
		beforeBytes, readErr := os.ReadFile(path)
		before := ""
		existed := readErr == nil
		if existed {
			before = string(beforeBytes)
			if plan.Creates {
				return "", fmt.Errorf("%s already exists; /dev/null create patches require a missing file", plan.Path)
			}
		} else if !plan.Creates && !boolArg(args, "allow_create", false) {
			return "", fmt.Errorf("%s does not exist; set allow_create=true for creating files", plan.Path)
		}
		if guard := stringArg(args, "expected_current"); guard != "" && guard != before {
			return "", fmt.Errorf("expected_current guard did not match for %s", plan.Path)
		}
		after, err := applyParsedPatch(before, plan)
		if err != nil {
			return "", fmt.Errorf("%s: %w", plan.Path, err)
		}
		action := "modify"
		if !existed {
			action = "create"
		}
		if plan.Deletes {
			action = "delete"
		}
		diff := unifiedDiff(plan.Path, before, after)
		if preview {
			outputs = append(outputs, diff)
			continue
		}
		if action == "delete" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
				return "", err
			}
		}
		change := session.FileChange{
			ID:            uuid.NewString(),
			At:            time.Now(),
			Path:          plan.Path,
			Action:        action,
			BeforeContent: before,
			AfterContent:  after,
			UnifiedDiff:   diff,
		}
		if err := e.store.AddChange(sess, change); err != nil {
			return "", err
		}
		outputs = append(outputs, fmt.Sprintf("change_id=%s\n%s", change.ID, diff))
	}
	return truncate(strings.Join(outputs, "\n"), e.maxOutputLines), nil
}

func (e *Executor) revertChange(sess *session.Session, changeID string) (string, error) {
	if changeID == "" {
		return "", fmt.Errorf("change_id is required")
	}
	for i := len(sess.Changes) - 1; i >= 0; i-- {
		ch := sess.Changes[i]
		if ch.ID != changeID {
			continue
		}
		path, err := e.resolveWorkplacePath(ch.Path)
		if err != nil {
			return "", err
		}
		current, _ := os.ReadFile(path)
		diff := unifiedDiff(ch.Path, string(current), ch.BeforeContent)
		if ch.Action == "create" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(ch.BeforeContent), 0o644); err != nil {
				return "", err
			}
		}
		revert := session.FileChange{
			ID:            uuid.NewString(),
			At:            time.Now(),
			Path:          ch.Path,
			Action:        "revert:" + changeID,
			BeforeContent: string(current),
			AfterContent:  ch.BeforeContent,
			UnifiedDiff:   diff,
		}
		if err := e.store.AddChange(sess, revert); err != nil {
			return "", err
		}
		return truncate(fmt.Sprintf("reverted=%s\nchange_id=%s\n%s", changeID, revert.ID, diff), e.maxOutputLines), nil
	}
	return "", fmt.Errorf("change_id not found")
}

func (e *Executor) revertChanges(sess *session.Session, changeIDs []string, reverse bool) (string, error) {
	if len(changeIDs) == 0 {
		return "", fmt.Errorf("change_ids is required")
	}
	if reverse {
		for i, j := 0, len(changeIDs)-1; i < j; i, j = i+1, j-1 {
			changeIDs[i], changeIDs[j] = changeIDs[j], changeIDs[i]
		}
	}
	out := []string{}
	for _, id := range changeIDs {
		result, err := e.revertChange(sess, id)
		if err != nil {
			return strings.Join(out, "\n"), err
		}
		out = append(out, result)
	}
	return truncate(strings.Join(out, "\n"), e.maxOutputLines), nil
}

func (e *Executor) changeHistory(sess *session.Session, path string, limit int) (string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows := []string{}
	for i := len(sess.Changes) - 1; i >= 0; i-- {
		ch := sess.Changes[i]
		if path != "" && filepath.ToSlash(path) != ch.Path {
			continue
		}
		rows = append(rows, fmt.Sprintf("%s %s %s %s", ch.ID, ch.At.Format(time.RFC3339), ch.Action, ch.Path))
		if len(rows) >= limit {
			break
		}
	}
	if len(rows) == 0 {
		return "no changes", nil
	}
	return truncate(strings.Join(rows, "\n"), e.maxOutputLines), nil
}

func (e *Executor) showChanges(sess *session.Session, changeID string, changeIDs []string) (string, error) {
	ids := appendChangeIDs(changeID, changeIDs)
	if len(ids) == 0 {
		return "", fmt.Errorf("change_id or change_ids is required")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := []string{}
	for _, ch := range sess.Changes {
		if !wanted[ch.ID] {
			continue
		}
		out = append(out, fmt.Sprintf("change_id=%s\nat=%s\naction=%s\npath=%s\n%s", ch.ID, ch.At.Format(time.RFC3339), ch.Action, ch.Path, ch.UnifiedDiff))
	}
	if len(out) == 0 {
		return "change_id not found", nil
	}
	return truncate(strings.Join(out, "\n"), e.maxOutputLines), nil
}

func appendChangeIDs(first string, rest []string) []string {
	out := []string{}
	if first != "" {
		out = append(out, first)
	}
	out = append(out, rest...)
	return out
}

func (e *Executor) resolveWorkplacePath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	abs := filepath.Join(e.paths.Workplace, clean)
	resolved, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(e.paths.Workplace)
	if err != nil {
		return "", err
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workplace")
	}
	return resolved, nil
}

func schema(name, desc string, params map[string]any) types.ToolSchema {
	return types.ToolSchema{
		Type: "function",
		Function: types.FunctionSchema{
			Name:        name,
			Description: desc,
			Parameters:  params,
		},
	}
}

func prop(kind, desc string) map[string]any {
	return map[string]any{"type": kind, "description": desc}
}

func stringArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		n, _ := strconv.Atoi(t.String())
		return n
	case string:
		n, err := strconv.Atoi(t)
		if err == nil {
			return n
		}
	}
	return def
}

func stringSliceArg(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := []string{}
		for _, item := range t {
			if item == nil {
				continue
			}
			out = append(out, fmt.Sprint(item))
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}

func boolArg(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	default:
		return def
	}
}

func truncate(s string, limitLines int) string {
	if limitLines <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= limitLines {
		return s
	}
	head := 500
	tail := limitLines - head
	if limitLines < 1000 {
		head = limitLines / 3
		tail = limitLines - head
	}
	truncStart := head + 1
	truncEnd := len(lines) - tail

	var out strings.Builder
	out.WriteString(strings.Join(lines[:head], "\n"))
	out.WriteString(fmt.Sprintf("\n\n--- [Output truncated: omitted lines %d to %d (total %d lines). Use search_grep/read_file for specific sections.] ---\n\n", truncStart, truncEnd, len(lines)))
	out.WriteString(strings.Join(lines[len(lines)-tail:], "\n"))
	return out.String()
}

func unifiedDiff(path, before, after string) string {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")
	var b strings.Builder
	b.WriteString("--- a/" + path + "\n")
	b.WriteString("+++ b/" + path + "\n")
	b.WriteString("@@\n")
	max := len(beforeLines)
	if len(afterLines) > max {
		max = len(afterLines)
	}
	for i := 0; i < max; i++ {
		var old, neu string
		hasOld := i < len(beforeLines)
		hasNew := i < len(afterLines)
		if hasOld {
			old = beforeLines[i]
		}
		if hasNew {
			neu = afterLines[i]
		}
		switch {
		case hasOld && hasNew && old == neu:
			b.WriteString(" " + old + "\n")
		default:
			if hasOld {
				b.WriteString("-" + old + "\n")
			}
			if hasNew {
				b.WriteString("+" + neu + "\n")
			}
		}
	}
	return b.String()
}

type diffApplyPlan struct {
	Path    string
	Hunks   []diffHunk
	Creates bool
	Deletes bool
}

type diffHunk struct {
	OldStart int
	OldCount int
	Lines    []string
}

func parseUnifiedDiff(raw, fallbackPath string) ([]diffApplyPlan, error) {
	fallbackPath = filepath.ToSlash(strings.TrimSpace(fallbackPath))
	lines := strings.Split(raw, "\n")
	plans := []diffApplyPlan{}
	var current *diffApplyPlan
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "--- ") {
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+++ ") {
				return nil, fmt.Errorf("diff header missing +++ line")
			}
			oldPath := cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
			newPath := cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(lines[i+1], "+++ ")))
			path := newPath
			if path == "/dev/null" || path == "" {
				path = oldPath
			}
			if path == "" || path == "/dev/null" {
				return nil, fmt.Errorf("diff file path missing")
			}
			if fallbackPath != "" && fallbackPath != path {
				return nil, fmt.Errorf("diff path %q conflicts with path %q", path, fallbackPath)
			}
			plans = append(plans, diffApplyPlan{
				Path:    path,
				Creates: oldPath == "/dev/null",
				Deletes: newPath == "/dev/null",
			})
			current = &plans[len(plans)-1]
			i++
			continue
		}
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "@@\t") {
			if current == nil {
				if fallbackPath == "" {
					return nil, fmt.Errorf("hunk before file header; path is required")
				}
				plans = append(plans, diffApplyPlan{Path: fallbackPath})
				current = &plans[len(plans)-1]
			}
			hunk, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(lines[i], "--- ") || strings.HasPrefix(lines[i], "@@ ") {
					i--
					break
				}
				if lines[i] == `\ No newline at end of file` {
					continue
				}
				if lines[i] == "" {
					continue
				}
				prefix := lines[i][0]
				if prefix != ' ' && prefix != '+' && prefix != '-' {
					return nil, fmt.Errorf("invalid hunk line %q", lines[i])
				}
				hunk.Lines = append(hunk.Lines, lines[i])
			}
			current.Hunks = append(current.Hunks, hunk)
		}
	}
	return plans, nil
}

func parseHunkHeader(header string) (diffHunk, error) {
	re := regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)
	m := re.FindStringSubmatch(header)
	if m == nil {
		return diffHunk{}, fmt.Errorf("invalid hunk header %q", header)
	}
	oldStart, _ := strconv.Atoi(m[1])
	oldCount := 1
	if m[2] != "" {
		oldCount, _ = strconv.Atoi(m[2])
	}
	return diffHunk{OldStart: oldStart, OldCount: oldCount}, nil
}

func cleanDiffPath(path string) string {
	path = strings.Trim(path, "\"")
	if path == "/dev/null" {
		return path
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return filepath.ToSlash(path)
}

func isBinary(data []byte) bool {
	limit := 1024
	if len(data) < limit {
		limit = len(data)
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func applyParsedPatch(before string, plan diffApplyPlan) (string, error) {
	if len(plan.Hunks) == 0 {
		return before, nil
	}
	lines := strings.Split(before, "\n")
	out := []string{}
	cursor := 0
	for _, hunk := range plan.Hunks {
		start := hunk.OldStart - 1
		if hunk.OldStart == 0 {
			start = 0
		}
		if start < cursor || start > len(lines) {
			return "", fmt.Errorf("hunk start is outside file")
		}
		out = append(out, lines[cursor:start]...)
		pos := start
		for _, hline := range hunk.Lines {
			if hline == "" {
				continue
			}
			prefix := hline[0]
			text := hline[1:]
			switch prefix {
			case ' ':
				if pos >= len(lines) || lines[pos] != text {
					return "", fmt.Errorf("context mismatch near line %d", pos+1)
				}
				out = append(out, text)
				pos++
			case '-':
				if pos >= len(lines) || lines[pos] != text {
					return "", fmt.Errorf("delete mismatch near line %d", pos+1)
				}
				pos++
			case '+':
				out = append(out, text)
			}
		}
		cursor = pos
	}
	out = append(out, lines[cursor:]...)
	return strings.Join(out, "\n"), nil
}
