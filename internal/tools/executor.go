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
	maxOutputChars        int
	allowParallelShell    bool
	allowInteractiveShell bool
	readOnly              bool
	shells                *ShellManager
	subAgents             *SubAgentManager
	mu                    sync.Mutex
}

func NewExecutor(paths config.Paths, store *session.Store, maxOutputChars int, allowParallelShell, allowInteractiveShell bool) *Executor {
	if maxOutputChars <= 0 {
		maxOutputChars = 5000
	}
	if allowInteractiveShell {
		allowParallelShell = true
	}
	exec := &Executor{
		paths:                 paths,
		store:                 store,
		maxOutputChars:        maxOutputChars,
		allowParallelShell:    allowParallelShell,
		allowInteractiveShell: allowInteractiveShell,
	}
	exec.shells = NewShellManager(paths.Workplace, maxOutputChars)
	exec.subAgents = NewSubAgentManager(maxOutputChars)
	return exec
}

func NewReadOnlyExecutor(paths config.Paths, store *session.Store, maxOutputChars int) *Executor {
	exec := NewExecutor(paths, store, maxOutputChars, false, false)
	exec.readOnly = true
	return exec
}

func (e *Executor) SetSubAgentRunner(runner SubAgentRunner) {
	e.subAgents.SetRunner(runner)
}

func (e *Executor) SetAgentLimits(maxOutputChars int, allowParallelShell, allowInteractiveShell bool) {
	if maxOutputChars <= 0 {
		maxOutputChars = 5000
	}
	if allowInteractiveShell {
		allowParallelShell = true
	}
	e.mu.Lock()
	e.maxOutputChars = maxOutputChars
	e.allowParallelShell = allowParallelShell
	e.allowInteractiveShell = allowInteractiveShell
	e.mu.Unlock()
	e.shells.SetLimit(maxOutputChars)
	e.subAgents.SetLimit(maxOutputChars)
}

func (e *Executor) Schemas(forSubAgent bool) []types.ToolSchema {
	schemas := []types.ToolSchema{
		schema("read_file", "Read a workspace file with optional 1-based start_line/end_line and output truncation.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       prop("string", "Path under the workplace."),
				"start_line": prop("integer", "Optional 1-based start line."),
				"end_line":   prop("integer", "Optional 1-based end line."),
			},
			"required": []string{"path"},
		}),
		schema("view_dir", "List files and folders in a workspace directory.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": prop("string", "Directory path under the workplace."),
			},
		}),
		schema("search_grep", "Search file names or file contents in the workplace.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":          prop("string", "Search string or regular expression."),
				"mode":           prop("string", "filename or content."),
				"case_sensitive": prop("boolean", "Whether search is case sensitive."),
			},
			"required": []string{"query"},
		}),
		schema("read_skill", "Read the SKILL.md for a currently visible skill. Skill metadata and source are included in the result.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": prop("string", "Visible skill name."),
			},
			"required": []string{"name"},
		}),
		schema("diff_file", "Edit workspace files and record reversible change diffs. Prefer mode=replace with exact old_text/new_text for localized multi-line edits; use unified diff apply only when line context is certain.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode":             prop("string", "replace, preview, apply, write, delete, history, show, revert, or revert_many. Prefer replace for localized edits."),
				"path":             prop("string", "Path under the workplace."),
				"content":          prop("string", "Full new file content for write/preview. Use sparingly for new or small files."),
				"unified_diff":     prop("string", "Unified diff to apply. Can include one or more file patches."),
				"patches":          prop("array", "Optional list of unified diff strings."),
				"old_text":         prop("string", "Exact old text block for replace mode. Include enough surrounding lines to make it unique."),
				"new_text":         prop("string", "Replacement text block for replace mode."),
				"replace_all":      prop("boolean", "Replace every exact old_text occurrence. Default false requires exactly one match."),
				"find":             prop("string", "Deprecated alias for old_text."),
				"replace":          prop("string", "Deprecated alias for new_text."),
				"change_id":        prop("string", "Change ID to revert."),
				"change_ids":       prop("array", "Change IDs to show or revert in order."),
				"limit":            prop("integer", "Maximum history entries to show."),
				"allow_create":     prop("boolean", "Allow creating a new file on patch/write."),
				"expected_current": prop("string", "Optional guard: current content must equal this value."),
			},
			"required": []string{"mode"},
		}),
	}
	if forSubAgent {
		return schemas
	}
	shellCWD := e.paths.Workplace
	if shellCWD == "" {
		shellCWD = "the current workplace"
	}
	schemas = append(schemas, schema("shell_run_sync", fmt.Sprintf("Run a shell command synchronously in cwd %q. The command returns only command output; it is killed if it reaches timeout_sec.", shellCWD), map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":     prop("string", "Shell command to run."),
			"timeout_sec": prop("integer", "Maximum seconds to wait before killing the command, default 60."),
		},
		"required": []string{"command", "timeout_sec"},
	}))
	if !e.allowParallelShell {
		return append(schemas, subAgentSchemas()...)
	}
	schemas = append(schemas,
		schema("shell_run_async", fmt.Sprintf("Start a shell command asynchronously in cwd %q and return a shell_id. The command keeps running until it exits or is killed.", shellCWD), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": prop("string", "Shell command to run."),
			},
			"required": []string{"command"},
		}),
		schema("shell_async_status", "Check asynchronous shell commands. With shell_id, returns status and captured output for that command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Optional shell ID."),
			},
		}),
		schema("shell_async_kill", "Kill an asynchronous shell command by shell_id.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Shell ID."),
			},
			"required": []string{"shell_id"},
		}),
	)
	if e.allowInteractiveShell {
		schemas = append(schemas,
			schema("shell_async_write", "Write input to an asynchronous interactive shell command.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"shell_id": prop("string", "Shell ID."),
					"input":    prop("string", "Input to send, include newline when needed."),
				},
				"required": []string{"shell_id", "input"},
			}))
	}
	return append(schemas, subAgentSchemas()...)
}

func subAgentSchemas() []types.ToolSchema {
	return []types.ToolSchema{
		schema("sub_agent_list", "List configured sub-agent names and descriptions.", nil),
		schema("sub_agent_start_async", "Start a parallel sub-agent. Sub-agents have file tools but no shell or sub-agent tools.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent":       prop("string", "Optional sub-agent config name from sub_agent_list."),
				"name":        prop("string", "Display name."),
				"instruction": prop("string", "Task for the sub-agent."),
			},
			"required": []string{"instruction"},
		}),
		schema("sub_agent_check", "Check the status and result of a specific sub-agent. If the status was ready_for_check, it transitions to completed after this check.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
			},
			"required": []string{"sub_agent_id"},
		}),
		schema("sub_agent_wait_check", "Wait for a specified number of seconds, then check one sub-agent. Do not poll with this tool; use it only when the user explicitly asks to wait or there is truly no useful work to do before checking once.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
				"wait_seconds": prop("integer", "Seconds to wait before checking status."),
			},
			"required": []string{"sub_agent_id", "wait_seconds"},
		}),
		schema("sub_agent_resume_async", "Send a follow-up instruction to a completed sub-agent context.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
				"instruction":  prop("string", "Follow-up instruction."),
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
	return truncate(out, e.maxOutputChars), nil
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
	return truncate(out, e.maxOutputChars), nil
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
	return truncate(strings.Join(rows, "\n"), e.maxOutputChars), nil
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
	caseSensitive := boolArg(args, "case_sensitive", false)
	pattern := query
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(query))
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
			hay := rel
			if !caseSensitive {
				hay = strings.ToLower(hay)
			}
			needle := query
			if !caseSensitive {
				needle = strings.ToLower(needle)
			}
			if strings.Contains(hay, needle) {
				matches = append(matches, rel)
			}
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
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
	return truncate(strings.Join(matches, "\n"), e.maxOutputChars), nil
}

func (e *Executor) diffFile(sess *session.Session, args map[string]any) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	mode := stringArg(args, "mode")
	changeID := stringArg(args, "change_id")
	switch mode {
	case "history":
		return e.changeHistory(sess, stringArg(args, "path"), intArg(args, "limit", 20))
	case "show":
		return e.showChanges(sess, changeID, stringSliceArg(args, "change_ids"))
	case "revert_many":
		return e.revertChanges(sess, appendChangeIDs(changeID, stringSliceArg(args, "change_ids")))
	}
	if mode == "revert" {
		return e.revertChange(sess, changeID)
	}

	if mode == "apply" || (mode == "preview" && (stringArg(args, "unified_diff") != "" || len(stringSliceArg(args, "patches")) > 0)) {
		return e.applyDiffs(sess, args, mode == "preview")
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
	case "preview", "write":
		if mode == "preview" && (stringArg(args, "old_text") != "" || stringArg(args, "find") != "") {
			next, err := replaceTextBlock(before, args)
			if err != nil {
				return "", err
			}
			after = next
			break
		}
		after = stringArg(args, "content")
		if !existed {
			action = "create"
		}
	case "replace", "patch":
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
	if mode == "preview" {
		return truncate(diff, e.maxOutputChars), nil
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
	return truncate(fmt.Sprintf("change_id=%s\n%s", change.ID, diff), e.maxOutputChars), nil
}

func replaceTextBlock(before string, args map[string]any) (string, error) {
	oldText := stringArg(args, "old_text")
	if oldText == "" {
		oldText = stringArg(args, "find")
	}
	if oldText == "" {
		return "", fmt.Errorf("old_text is required for replace mode")
	}
	newText := stringArg(args, "new_text")
	if _, ok := args["new_text"]; !ok {
		newText = stringArg(args, "replace")
	}
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
		parsed, err := parseUnifiedDiff(raw)
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
	return truncate(strings.Join(outputs, "\n"), e.maxOutputChars), nil
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
		return truncate(fmt.Sprintf("reverted=%s\nchange_id=%s\n%s", changeID, revert.ID, diff), e.maxOutputChars), nil
	}
	return "", fmt.Errorf("change_id not found")
}

func (e *Executor) revertChanges(sess *session.Session, changeIDs []string) (string, error) {
	if len(changeIDs) == 0 {
		return "", fmt.Errorf("change_ids is required")
	}
	out := []string{}
	for _, id := range changeIDs {
		result, err := e.revertChange(sess, id)
		if err != nil {
			return strings.Join(out, "\n"), err
		}
		out = append(out, result)
	}
	return truncate(strings.Join(out, "\n"), e.maxOutputChars), nil
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
	return truncate(strings.Join(rows, "\n"), e.maxOutputChars), nil
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
	return truncate(strings.Join(out, "\n"), e.maxOutputChars), nil
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

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	head := 500
	tail := 1000
	if limit < 1800 {
		head = limit / 3
		tail = limit / 3
	}
	omitted := len(s) - head - tail
	return s[:head] + fmt.Sprintf("\n--- (输出过长，已省略中间 %d 字符；可以将输出保存到文件后用 search_grep/read_file 查看) ---\n", omitted) + s[len(s)-tail:]
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

func parseUnifiedDiff(raw string) ([]diffApplyPlan, error) {
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
			plans = append(plans, diffApplyPlan{
				Path:    path,
				Creates: oldPath == "/dev/null",
				Deletes: newPath == "/dev/null",
			})
			current = &plans[len(plans)-1]
			i++
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			if current == nil {
				return nil, fmt.Errorf("hunk before file header")
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
