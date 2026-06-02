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
	paths          config.Paths
	store          *session.Store
	maxOutputChars int
	readOnly       bool
	shells         *ShellManager
	subAgents      *SubAgentManager
	mu             sync.Mutex
}

func NewExecutor(paths config.Paths, store *session.Store, maxOutputChars int) *Executor {
	if maxOutputChars <= 0 {
		maxOutputChars = 5000
	}
	exec := &Executor{
		paths:          paths,
		store:          store,
		maxOutputChars: maxOutputChars,
	}
	exec.shells = NewShellManager(paths.Workplace, maxOutputChars)
	exec.subAgents = NewSubAgentManager(maxOutputChars)
	return exec
}

func NewReadOnlyExecutor(paths config.Paths, store *session.Store, maxOutputChars int) *Executor {
	exec := NewExecutor(paths, store, maxOutputChars)
	exec.readOnly = true
	return exec
}

func (e *Executor) SetSubAgentRunner(runner SubAgentRunner) {
	e.subAgents.SetRunner(runner)
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
	}
	if forSubAgent {
		return schemas
	}
	schemas = append(schemas,
		schema("diff_file", "Create, modify, delete, patch, or revert files and record the full change chain in the active session.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode":             prop("string", "write, patch, delete, revert, or preview."),
				"path":             prop("string", "Path under the workplace."),
				"content":          prop("string", "Full new file content for write/preview."),
				"find":             prop("string", "Exact text to replace for patch."),
				"replace":          prop("string", "Replacement text for patch."),
				"change_id":        prop("string", "Change ID to revert."),
				"allow_create":     prop("boolean", "Allow creating a new file on patch/write."),
				"expected_current": prop("string", "Optional guard: current content must equal this value."),
			},
			"required": []string{"mode"},
		}),
		schema("shell_run", "Run a non-interactive shell command. Interactive commands require interactive=true and then use shell_read/shell_write/shell_kill.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":     prop("string", "Shell command to run."),
				"timeout_sec": prop("integer", "Wait time before returning partial output, default 60."),
				"interactive": prop("boolean", "Start in parallel and return a shell_id."),
			},
			"required": []string{"command"},
		}),
		schema("shell_read", "Read output from a running interactive shell command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Shell ID."),
			},
			"required": []string{"shell_id"},
		}),
		schema("shell_write", "Inject input into a running interactive shell command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Shell ID."),
				"input":    prop("string", "Input to send, include newline when needed."),
			},
			"required": []string{"shell_id", "input"},
		}),
		schema("shell_kill", "Kill a running interactive shell command.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"shell_id": prop("string", "Shell ID."),
			},
			"required": []string{"shell_id"},
		}),
	)
	if !forSubAgent {
		schemas = append(schemas,
			schema("sub_agent_start", "Start a parallel sub-agent task. Sub-agents cannot see sub_agent tools.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        prop("string", "Display name."),
					"instruction": prop("string", "Task for the sub-agent."),
				},
				"required": []string{"instruction"},
			}),
			schema("sub_agent_status", "List sub-agent status or get one sub-agent transcript/result.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sub_agent_id": prop("string", "Optional sub-agent ID."),
				},
			}),
			schema("sub_agent_send", "Send a follow-up instruction to a running or completed sub-agent context.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sub_agent_id": prop("string", "Sub-agent ID."),
					"instruction":  prop("string", "Follow-up instruction."),
				},
				"required": []string{"sub_agent_id", "instruction"},
			}),
			schema("sub_agent_stop", "Stop a sub-agent and its owned terminals.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sub_agent_id": prop("string", "Sub-agent ID."),
				},
				"required": []string{"sub_agent_id"},
			}),
		)
	}
	return schemas
}

func (e *Executor) Run(ctx context.Context, sess *session.Session, name string, args map[string]any) (string, error) {
	if e.readOnly && name != "read_file" && name != "view_dir" && name != "search_grep" {
		return "", fmt.Errorf("tool %q is not available to read-only sub-agents", name)
	}
	switch name {
	case "read_file":
		return e.readFile(args)
	case "view_dir":
		return e.viewDir(args)
	case "search_grep":
		return e.searchGrep(args)
	case "diff_file":
		return e.diffFile(sess, args)
	case "shell_run":
		return e.shells.Run(ctx, stringArg(args, "command"), intArg(args, "timeout_sec", 60), boolArg(args, "interactive", false))
	case "shell_read":
		return e.shells.Read(stringArg(args, "shell_id")), nil
	case "shell_write":
		return e.shells.Write(stringArg(args, "shell_id"), stringArg(args, "input"))
	case "shell_kill":
		return e.shells.Kill(stringArg(args, "shell_id"))
	case "sub_agent_start":
		return e.subAgents.Start(stringArg(args, "name"), stringArg(args, "instruction")), nil
	case "sub_agent_status":
		return e.subAgents.Status(stringArg(args, "sub_agent_id")), nil
	case "sub_agent_send":
		return e.subAgents.Send(stringArg(args, "sub_agent_id"), stringArg(args, "instruction")), nil
	case "sub_agent_stop":
		return e.subAgents.Stop(stringArg(args, "sub_agent_id")), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (e *Executor) SubAgentSummaries() []string {
	return e.subAgents.Summaries()
}

func (e *Executor) SubAgentSnapshots() []SubAgentSnapshot {
	return e.subAgents.Snapshots()
}

func (e *Executor) SubAgentStatus(id string) string {
	return e.subAgents.Status(id)
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
		if strings.HasPrefix(rel, "Asayn"+string(filepath.Separator)) || rel == "Asayn" {
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
	if mode == "revert" {
		return e.revertChange(sess, changeID)
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
		after = stringArg(args, "content")
		if !existed {
			action = "create"
		}
	case "patch":
		find := stringArg(args, "find")
		replace := stringArg(args, "replace")
		if !existed && !boolArg(args, "allow_create", false) {
			return "", fmt.Errorf("file does not exist; set allow_create=true or use write")
		}
		if find == "" {
			return "", fmt.Errorf("find is required for patch")
		}
		if !strings.Contains(before, find) {
			return "", fmt.Errorf("find text not present")
		}
		after = strings.Replace(before, find, replace, 1)
		if !existed {
			action = "create"
		}
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
		} else if err := os.WriteFile(path, []byte(ch.BeforeContent), 0o644); err != nil {
			return "", err
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
