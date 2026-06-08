package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
	"github.com/asayn/asayn/internal/session"
)

type Executor struct {
	paths                 config.Paths
	store                 *session.Store
	maxOutputLines        int
	allowParallelShell    bool
	allowInteractiveShell bool
	basicOnly             bool
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
	exec.shells = NewShellManager(paths.WorkspaceRoot, maxOutputLines)
	exec.subAgents = NewSubAgentManager(maxOutputLines)
	return exec
}

func NewBasicExecutor(paths config.Paths, store *session.Store, maxOutputLines int) *Executor {
	exec := NewExecutor(paths, store, maxOutputLines, false, false)
	exec.basicOnly = true
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
		schema("file_read", "Read a file. Support paths inside workspace only. Binary files and files without extensions are considered risky and will only show a preview unless force_binary is set.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":         prop("string", "File path. Prefer a path relative to the workspace."),
				"start_line":   prop("integer", "First line, 1-based."),
				"end_line":     prop("integer", "Last line, 1-based."),
				"force_binary": prop("boolean", "Force reading a binary or extensionless file as text."),
			},
			"required": []string{"path"},
		}),
		schema("dir_view", "List a directory. Support paths inside workspace only.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": prop("string", "Directory path. Prefer a path relative to the workspace."),
			},
		}),
		schema("grep_search", "Search workspace files with a regex. .Asayn/ is skipped. Content mode skips known binary files.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":          prop("string", "Regex pattern."),
				"mode":           prop("string", "content or filename."),
				"case_sensitive": prop("boolean", "Default true. Set false for case-insensitive search."),
			},
			"required": []string{"query"},
		}),
		schema("skill_read", "Read a visible skill before applying it. Only skills listed as visible in the active session can be read.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": prop("string", "Skill name."),
			},
			"required": []string{"name"},
		}),

	}
	shellCWD := e.paths.WorkspaceRoot
	if shellCWD == "" {
		shellCWD = "workspace"
	}
	shellEnv := ShellEnvironmentName()
	schemas = append(schemas, schema("shell_run_sync", fmt.Sprintf("Run a blocking non-interactive %s command in %q(workspace).", shellEnv, shellCWD), map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":     prop("string", shellEnv+" command."),
			"timeout_sec": prop("integer", "Timeout seconds."),
		},
		"required": []string{"command"},
	}))
	if forSubAgent {
		return schemas
	}
	if !e.allowParallelShell {
		return append(schemas, subAgentSchemas()...)
	}
	schemas = append(schemas,
		schema("shell_run_async", fmt.Sprintf("Start a background %s command in %q(workspace).", shellEnv, shellCWD), map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": prop("string", shellEnv+" command. Commands run in the workspace root; check background commands with shell_async_check."),
			},
			"required": []string{"command"},
		}),
		schema("shell_async_check", "Check a background "+shellEnv+" command.", map[string]any{
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
			schema("shell_async_stdin", "Send stdin to an interactive background shell. Raw input is forwarded exactly; include \\n to press Enter.", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"shell_id": prop("string", "Shell ID."),
					"input":    prop("string", "Raw stdin text; include \\n to press Enter."),
				},
				"required": []string{"shell_id", "input"},
			}))
	}
	return append(schemas, subAgentSchemas()...)
}

func subAgentSchemas() []types.ToolSchema {
	return []types.ToolSchema{
		schema("sub_agent_list", "List available sub-agents.", nil),
		schema("sub_agent_start_async", "Start a background sub-agent for isolated work. Do not delegate shell coordination.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        prop("string", "Sub-agent config name."),
				"task_name":   prop("string", "Task name."),
				"instruction": prop("string", "Instructions."),
			},
			"required": []string{"instruction"},
		}),
		schema("sub_agent_check", "Check sub-agent status. Sub-agent work may take a while, so usually do other useful work before check.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sub_agent_id": prop("string", "Sub-agent ID."),
			},
			"required": []string{"sub_agent_id"},
		}),
		schema("delay", "Delay execution. Avoid using unless necessary.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"seconds": prop("integer", "Delay seconds."),
			},
			"required": []string{"seconds"},
		}),
		schema("sub_agent_resume_async", "Resume a completed sub-agent with follow-up instructions.", map[string]any{
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
	if e.basicOnly && name != "file_read" && name != "dir_view" && name != "grep_search" && name != "skill_read" && name != "shell_run_sync" {
		return "", fmt.Errorf("tool %q is not available to basic-only agents", name)
	}
	switch name {
	case "file_read":
		return e.readFile(args)
	case "dir_view":
		return e.viewDir(args)
	case "grep_search":
		return e.searchGrep(args)
	case "skill_read":
		return e.readSkill(args)
	case "shell_run_sync":
		return e.shells.RunBlocking(ctx, stringArg(args, "command"), intArg(args, "timeout_sec", 60))
	case "shell_run_async":
		if !e.allowParallelShell {
			return "", fmt.Errorf("shell_run_async is not available unless parallel shell is enabled")
		}
		return e.shells.StartAsync(stringArg(args, "command"), e.allowInteractiveShell)
	case "shell_async_check":
		if !e.allowParallelShell {
			return "", fmt.Errorf("shell_async_check is not available unless parallel shell is enabled")
		}
		return e.shells.Status(stringArg(args, "shell_id")), nil
	case "shell_async_kill":
		if !e.allowParallelShell {
			return "", fmt.Errorf("shell_async_kill is not available unless parallel shell is enabled")
		}
		return e.shells.Kill(stringArg(args, "shell_id"))
	case "shell_async_stdin":
		if !e.allowInteractiveShell {
			return "", fmt.Errorf("shell_async_stdin is not available unless interactive shell is enabled")
		}
		return e.shells.Write(stringArg(args, "shell_id"), stringArg(args, "input"))
	case "sub_agent_list":
		return e.subAgents.List(e.paths), nil
	case "sub_agent_start_async":
		return e.subAgents.Start(sess, e.store, stringArg(args, "name"), stringArg(args, "task_name"), stringArg(args, "instruction")), nil
	case "sub_agent_check":
		return e.subAgents.Check(stringArg(args, "sub_agent_id")), nil
	case "delay":
		return e.subAgents.Delay(ctx, intArg(args, "seconds", 0))
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
	return truncate(skill.Content, e.maxOutputLines), nil
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

var riskyExtensions = map[string]bool{
	".7z": true, ".a": true, ".apk": true, ".app": true, ".ar": true, ".avi": true,
	".bin": true, ".bmp": true, ".br": true, ".bz2": true, ".class": true, ".dat": true,
	".db": true, ".dmg": true, ".dll": true, ".dylib": true, ".eot": true, ".exe": true,
	".flac": true, ".gif": true, ".gz": true, ".heic": true, ".heif": true, ".icns": true,
	".ico": true, ".iso": true, ".jar": true, ".jpg": true, ".jpeg": true, ".m4a": true,
	".m4v": true, ".mkv": true, ".mov": true, ".mp3": true, ".mp4": true, ".o": true,
	".obj": true, ".ogg": true, ".otf": true, ".pdf": true, ".png": true, ".pyc": true,
	".rar": true, ".rlib": true, ".so": true, ".sqlite": true, ".sqlite3": true,
	".tar": true, ".tgz": true, ".ttf": true, ".wasm": true, ".wav": true, ".webm": true,
	".webp": true, ".woff": true, ".woff2": true, ".xz": true, ".zip": true, ".zst": true,
}

const binaryProbeSize = 8192

func isRiskyFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return true
	}
	return riskyExtensions[ext]
}

func (e *Executor) readFile(args map[string]any) (string, error) {
	path, err := e.resolveWorkspaceRootPath(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	forceBinary := boolArg(args, "force_binary", false)
	if !forceBinary {
		preview, risky, err := riskyFilePreview(path)
		if err != nil {
			return "", err
		}
		if risky {
			return fmt.Sprintf("This file is likely a useless binary file. Preview (first %d chars):\n%s\n\nIf you are sure this is a text file, use force_binary=true.", len(preview), preview), nil
		}
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
	inputPath := stringArg(args, "path")
	if inputPath == "" {
		inputPath = "."
	}
	path, err := e.resolveWorkspaceRootPath(inputPath)
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
	err = filepath.WalkDir(e.paths.WorkspaceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(e.paths.WorkspaceRoot, path)
		if strings.HasPrefix(rel, ".Asayn"+string(filepath.Separator)) || rel == ".Asayn" {
			return nil
		}
		if mode == "filename" {
			if re.MatchString(filepath.ToSlash(rel)) {
				matches = append(matches, rel)
			}
			return nil
		}
		if isKnownBinaryFile(rel) {
			return nil
		}
		fileMatches, readErr := grepTextFile(path, rel, re, 200-len(matches))
		if readErr != nil {
			return nil
		}
		matches = append(matches, fileMatches...)
		if len(matches) >= 200 {
			return filepath.SkipAll
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

func isKnownBinaryFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext != "" && riskyExtensions[ext]
}

func riskyFilePreview(path string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()

	buf := make([]byte, binaryProbeSize)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", false, err
	}
	probe := buf[:n]
	risky := isRiskyFile(filepath.Base(path)) || isBinary(probe)
	if !risky {
		return "", false, nil
	}
	preview := safePreview(probe, 200)
	return preview, true, nil
}

func grepTextFile(path, rel string, re *regexp.Regexp, remaining int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	probe := make([]byte, binaryProbeSize)
	n, err := file.Read(probe)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if isBinary(probe[:n]) {
		return nil, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	matches := []string{}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if re.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, lineNo, line))
			if len(matches) >= remaining {
				return matches, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return matches, nil
	}
	return matches, nil
}

func (e *Executor) resolveWorkspaceRootPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	clean := filepath.Clean(path)
	candidate := clean
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(e.paths.WorkspaceRoot, clean)
	}
	resolved, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(e.paths.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return resolved, nil
}

func (e *Executor) workspaceDisplayPath(path string) (string, error) {
	resolved, err := e.resolveWorkspaceRootPath(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(e.paths.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return ".", nil
	}
	return filepath.ToSlash(rel), nil
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
	out.WriteString(fmt.Sprintf("\n\n--- [Output truncated: omitted lines %d to %d (total %d lines). Use grep_search/file_read for specific sections.] ---\n\n", truncStart, truncEnd, len(lines)))
	out.WriteString(strings.Join(lines[len(lines)-tail:], "\n"))
	return out.String()
}

func isBinary(data []byte) bool {
	limit := binaryProbeSize
	if len(data) < limit {
		limit = len(data)
	}
	if limit == 0 {
		return false
	}
	control := 0
	high := 0
	for i := 0; i < limit; i++ {
		b := data[i]
		if b == 0 {
			return true
		}
		if b >= 0x80 {
			high++
		}
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' && b != '\b' {
			control++
		}
	}
	probe := data[:limit]
	if control > limit/20 {
		return true
	}
	if high > limit/4 && !utf8.Valid(probe) {
		return true
	}
	return false
}

func safePreview(data []byte, limit int) string {
	if len(data) > limit {
		data = data[:limit]
	}
	text := strings.ToValidUTF8(string(data), "?")
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
