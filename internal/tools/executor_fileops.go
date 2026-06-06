package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/session"
	"github.com/google/uuid"
)

// fileEdit is the unified file editing tool. Modes:
//
//	write         - write full file content (creates or overwrites)
//	delete_lines  - delete a line range
//	insert        - insert text after a given line
//	replace_lines - replace a line range with new text
//	find_replace  - find old_text as a regex and replace with new_text
//	rollback      - rollback recorded changes
func (e *Executor) fileEdit(sess *session.Session, args map[string]any) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	mode := stringArg(args, "mode")
	changeID := stringArg(args, "change_id")

	switch mode {
	case "rollback":
		return e.rollback(sess, appendIDs(changeID, stringSliceArg(args, "change_ids")))
	case "batch":
		return e.fileEditBatch(sess, args)
	}

	inputPath := stringArg(args, "path")
	displayPath, err := e.workspaceDisplayPath(inputPath)
	if err != nil {
		return "", err
	}
	path, err := e.resolveWorkspaceRootPath(inputPath)
	if err != nil {
		return "", err
	}

	beforeBytes, readErr := os.ReadFile(path)
	before := ""
	existed := readErr == nil
	if existed {
		before = string(beforeBytes)
	}

	var after string
	action := "modify"

	switch mode {
	case "write":
		after = stringArg(args, "content")
		if !existed {
			action = "create"
		}

	case "delete_lines":
		if !existed {
			return "", fmt.Errorf("file does not exist")
		}
		start := intArg(args, "start_line", 0)
		end := intArg(args, "end_line", 0)
		if start <= 0 || end <= 0 || start > end {
			return "", fmt.Errorf("start_line and end_line required, start_line <= end_line")
		}
		lines := strings.Split(before, "\n")
		if start > len(lines) || end > len(lines) {
			return "", fmt.Errorf("line range %d-%d outside file (has %d lines)", start, end, len(lines))
		}
		after = strings.Join(append(lines[:start-1], lines[end:]...), "\n")

	case "insert":
		if !existed {
			return "", fmt.Errorf("file does not exist; use write mode to create files")
		}
		insertAfter := intArg(args, "insert_after_line", 0)
		text := stringArg(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required for insert mode")
		}
		lines := strings.Split(before, "\n")
		if insertAfter < 0 || insertAfter > len(lines) {
			return "", fmt.Errorf("insert_after_line %d outside file (has %d lines)", insertAfter, len(lines))
		}
		if insertAfter == 0 {
			after = text + "\n" + before
		} else {
			after = strings.Join(lines[:insertAfter], "\n") + "\n" + text + "\n" + strings.Join(lines[insertAfter:], "\n")
		}
		after = strings.TrimPrefix(after, "\n")

	case "replace_lines":
		if !existed {
			return "", fmt.Errorf("file does not exist; use write mode to create files")
		}
		start := intArg(args, "start_line", 0)
		end := intArg(args, "end_line", 0)
		text := stringArg(args, "text")
		if start <= 0 || end <= 0 || start > end {
			return "", fmt.Errorf("start_line and end_line required, start_line <= end_line")
		}
		lines := strings.Split(before, "\n")
		if start > len(lines) || end > len(lines) {
			return "", fmt.Errorf("line range %d-%d outside file (has %d lines)", start, end, len(lines))
		}
		after = strings.Join(lines[:start-1], "\n")
		if text != "" {
			if after != "" {
				after += "\n"
			}
			after += text
		}
		tail := strings.Join(lines[end:], "\n")
		if tail != "" {
			if after != "" {
				after += "\n"
			}
			after += tail
		}

	case "find_replace":
		if !existed {
			return "", fmt.Errorf("file does not exist; use write mode to create files")
		}
		pattern := stringArg(args, "old_text")
		if pattern == "" {
			return "", fmt.Errorf("old_text is required for find_replace")
		}
		newText := stringArg(args, "new_text")
		re, err := compileSearchPattern(pattern, true)
		if err != nil {
			return "", err
		}
		matches := re.FindAllStringIndex(before, -1)
		if len(matches) == 0 {
			return "", fmt.Errorf("old_text pattern not found in file")
		}
		replaceAll := boolArg(args, "replace_all", false)
		if !replaceAll && len(matches) > 1 {
			return "", fmt.Errorf("old_text pattern matched %d times; use a more specific regex or set replace_all=true", len(matches))
		}
		if replaceAll {
			after = re.ReplaceAllString(before, newText)
		} else {
			after = replaceFirstRegexMatch(re, before, newText, matches[0])
		}

	default:
		return "", fmt.Errorf("unsupported file_edit mode %q; valid: write, delete_lines, insert, replace_lines, find_replace, batch, rollback", mode)
	}

	diff := e.computeDiff(displayPath, before, after, mode)

	if mode == "delete_lines" && after == "" {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		action = "delete"
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
		Path:          displayPath,
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

func replaceFirstRegexMatch(re *regexp.Regexp, src, repl string, match []int) string {
	var b strings.Builder
	b.WriteString(src[:match[0]])
	b.WriteString(re.ReplaceAllString(src[match[0]:match[1]], repl))
	b.WriteString(src[match[1]:])
	return b.String()
}

// fileEditBatch applies multiple line-based operations to the same file
// from bottom to top against the original content, so line numbers from the
// caller's perspective stay stable. It produces a single combined diff and one
// recorded change.
func (e *Executor) fileEditBatch(sess *session.Session, args map[string]any) (string, error) {
	rawOps, ok := args["batch"].([]any)
	if !ok || len(rawOps) == 0 {
		return "", fmt.Errorf("batch field must be a non-empty array of op objects for batch mode")
	}

	ops, inputPath, err := parseBatchOps(rawOps)
	if err != nil {
		return "", err
	}
	displayPath, err := e.workspaceDisplayPath(inputPath)
	if err != nil {
		return "", err
	}
	path, err := e.resolveWorkspaceRootPath(inputPath)
	if err != nil {
		return "", err
	}

	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file does not exist; batch mode requires an existing file")
	}
	before := string(beforeBytes)
	after := before

	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].anchor == ops[j].anchor {
			return ops[i].index > ops[j].index
		}
		return ops[i].anchor > ops[j].anchor
	})

	for _, op := range ops {
		var err error
		after, err = applyBatchLineOp(after, op)
		if err != nil {
			return "", err
		}
	}

	if after == before {
		return "(no changes)", nil
	}

	diff := e.computeDiff(displayPath, before, after, "batch")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return "", err
	}

	change := session.FileChange{
		ID:            uuid.NewString(),
		At:            time.Now(),
		Path:          displayPath,
		Action:        "modify",
		BeforeContent: before,
		AfterContent:  after,
		UnifiedDiff:   diff,
	}
	if err := e.store.AddChange(sess, change); err != nil {
		return "", err
	}
	return truncate(fmt.Sprintf("change_id=%s\n%s", change.ID, diff), e.maxOutputLines), nil
}

type batchLineOp struct {
	index           int
	mode            string
	path            string
	startLine       int
	endLine         int
	insertAfterLine int
	text            string
	anchor          int
}

func parseBatchOps(rawOps []any) ([]batchLineOp, string, error) {
	ops := make([]batchLineOp, 0, len(rawOps))
	inputPath := ""
	for idx, raw := range rawOps {
		rawOp, ok := raw.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("batch op %d: each batch element must be an object", idx)
		}
		op := batchLineOp{
			index:           idx,
			mode:            stringArg(rawOp, "mode"),
			path:            stringArg(rawOp, "path"),
			startLine:       intArg(rawOp, "start_line", 0),
			endLine:         intArg(rawOp, "end_line", 0),
			insertAfterLine: intArg(rawOp, "insert_after_line", 0),
			text:            stringArg(rawOp, "text"),
		}
		if op.path != "" {
			if inputPath == "" {
				inputPath = op.path
			} else if op.path != inputPath {
				return nil, "", fmt.Errorf("batch op %d: path %q does not match first path %q", idx, op.path, inputPath)
			}
		}
		switch op.mode {
		case "delete_lines":
			if op.startLine <= 0 || op.endLine <= 0 || op.startLine > op.endLine {
				return nil, "", fmt.Errorf("batch op %d (delete_lines): start_line and end_line required, start_line <= end_line", idx)
			}
			op.anchor = op.startLine
		case "insert":
			if op.text == "" {
				return nil, "", fmt.Errorf("batch op %d (insert): text is required", idx)
			}
			if op.insertAfterLine < 0 {
				return nil, "", fmt.Errorf("batch op %d (insert): insert_after_line must be >= 0", idx)
			}
			op.anchor = op.insertAfterLine
		case "replace_lines":
			if op.startLine <= 0 || op.endLine <= 0 || op.startLine > op.endLine {
				return nil, "", fmt.Errorf("batch op %d (replace_lines): start_line and end_line required, start_line <= end_line", idx)
			}
			op.anchor = op.startLine
		default:
			return nil, "", fmt.Errorf("batch op %d: unsupported mode %q; batch only supports delete_lines, insert, replace_lines", idx, op.mode)
		}
		ops = append(ops, op)
	}
	if inputPath == "" {
		return nil, "", fmt.Errorf("batch mode requires a path on at least one operation")
	}
	return ops, inputPath, nil
}

func applyBatchLineOp(content string, op batchLineOp) (string, error) {
	lines := strings.Split(content, "\n")
	switch op.mode {
	case "delete_lines":
		if op.startLine > len(lines) || op.endLine > len(lines) {
			return "", fmt.Errorf("batch op %d (delete_lines): line range %d-%d outside file (has %d lines)", op.index, op.startLine, op.endLine, len(lines))
		}
		return strings.Join(append(lines[:op.startLine-1], lines[op.endLine:]...), "\n"), nil
	case "insert":
		if op.insertAfterLine > len(lines) {
			return "", fmt.Errorf("batch op %d (insert): insert_after_line %d outside file (has %d lines)", op.index, op.insertAfterLine, len(lines))
		}
		if op.insertAfterLine == 0 {
			return strings.TrimPrefix(op.text+"\n"+content, "\n"), nil
		}
		return strings.TrimPrefix(strings.Join(lines[:op.insertAfterLine], "\n")+"\n"+op.text+"\n"+strings.Join(lines[op.insertAfterLine:], "\n"), "\n"), nil
	case "replace_lines":
		if op.startLine > len(lines) || op.endLine > len(lines) {
			return "", fmt.Errorf("batch op %d (replace_lines): line range %d-%d outside file (has %d lines)", op.index, op.startLine, op.endLine, len(lines))
		}
		head := strings.Join(lines[:op.startLine-1], "\n")
		if op.text != "" {
			if head != "" {
				head += "\n"
			}
			head += op.text
		}
		tail := strings.Join(lines[op.endLine:], "\n")
		if tail != "" {
			if head != "" {
				head += "\n"
			}
			head += tail
		}
		return head, nil
	default:
		return "", fmt.Errorf("batch op %d: unsupported mode %q", op.index, op.mode)
	}
}

func (e *Executor) viewHistory(sess *session.Session, args map[string]any) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	changeID := stringArg(args, "change_id")
	changeIDs := stringSliceArg(args, "change_ids")
	if changeID != "" || len(changeIDs) > 0 {
		return e.viewChanges(sess, changeID, changeIDs)
	}
	pathFilter := stringArg(args, "path")
	if pathFilter != "" {
		displayPath, err := e.workspaceDisplayPath(pathFilter)
		if err != nil {
			return "", err
		}
		pathFilter = displayPath
	}
	return e.listChanges(sess, pathFilter, intArg(args, "limit", 0))
}

type diffOp struct {
	kind     byte
	text     string
	oldIndex int
	newIndex int
}

// computeDiff produces a standard unified diff with focused hunks.
func (e *Executor) computeDiff(relPath, before, after, mode string) string {
	beforeLines, beforeNoNewline := splitDiffLines(before)
	afterLines, afterNoNewline := splitDiffLines(after)
	if equalStringSlices(beforeLines, afterLines) && beforeNoNewline == afterNoNewline {
		return "(no changes)"
	}

	ops := buildLineDiff(beforeLines, afterLines)
	if !hasChangedOps(ops) {
		ops = newlineOnlyDiff(beforeLines, afterLines)
	}
	var b strings.Builder
	oldPath := "a/" + filepath.ToSlash(relPath)
	if before == "" {
		oldPath = "/dev/null"
	}
	b.WriteString(fmt.Sprintf("--- %s\n+++ b/%s\n", oldPath, filepath.ToSlash(relPath)))
	appendUnifiedHunks(&b, ops, len(beforeLines), len(afterLines), beforeNoNewline, afterNoNewline)
	return b.String()
}

func splitDiffLines(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		return lines[:len(lines)-1], false
	}
	return lines, true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasChangedOps(ops []diffOp) bool {
	for _, op := range ops {
		if op.kind != ' ' {
			return true
		}
	}
	return false
}

func newlineOnlyDiff(beforeLines, afterLines []string) []diffOp {
	if len(beforeLines) == 0 && len(afterLines) == 0 {
		return nil
	}
	line := ""
	if len(beforeLines) > 0 {
		line = beforeLines[len(beforeLines)-1]
	}
	prefix := make([]diffOp, 0, len(beforeLines)+1)
	for i := 0; i < len(beforeLines)-1; i++ {
		prefix = append(prefix, diffOp{kind: ' ', text: beforeLines[i], oldIndex: i, newIndex: i})
	}
	prefix = append(prefix, diffOp{kind: '-', text: line, oldIndex: len(beforeLines) - 1, newIndex: -1})
	if len(afterLines) > 0 {
		prefix = append(prefix, diffOp{kind: '+', text: afterLines[len(afterLines)-1], oldIndex: -1, newIndex: len(afterLines) - 1})
	}
	return prefix
}

func buildLineDiff(beforeLines, afterLines []string) []diffOp {
	prefix := 0
	for prefix < len(beforeLines) && prefix < len(afterLines) && beforeLines[prefix] == afterLines[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(beforeLines)-prefix && suffix < len(afterLines)-prefix &&
		beforeLines[len(beforeLines)-1-suffix] == afterLines[len(afterLines)-1-suffix] {
		suffix++
	}

	ops := make([]diffOp, 0, len(beforeLines)+len(afterLines))
	for i := 0; i < prefix; i++ {
		ops = append(ops, diffOp{kind: ' ', text: beforeLines[i], oldIndex: i, newIndex: i})
	}

	oldMid := beforeLines[prefix : len(beforeLines)-suffix]
	newMid := afterLines[prefix : len(afterLines)-suffix]
	for _, op := range diffMiddle(oldMid, newMid) {
		if op.oldIndex >= 0 {
			op.oldIndex += prefix
		}
		if op.newIndex >= 0 {
			op.newIndex += prefix
		}
		ops = append(ops, op)
	}

	for i := suffix; i > 0; i-- {
		oldIndex := len(beforeLines) - i
		newIndex := len(afterLines) - i
		ops = append(ops, diffOp{kind: ' ', text: beforeLines[oldIndex], oldIndex: oldIndex, newIndex: newIndex})
	}
	return ops
}

func diffMiddle(oldLines, newLines []string) []diffOp {
	switch {
	case len(oldLines) == 0:
		ops := make([]diffOp, 0, len(newLines))
		for i, line := range newLines {
			ops = append(ops, diffOp{kind: '+', text: line, oldIndex: -1, newIndex: i})
		}
		return ops
	case len(newLines) == 0:
		ops := make([]diffOp, 0, len(oldLines))
		for i, line := range oldLines {
			ops = append(ops, diffOp{kind: '-', text: line, oldIndex: i, newIndex: -1})
		}
		return ops
	case len(oldLines)*len(newLines) > 4_000_000:
		ops := make([]diffOp, 0, len(oldLines)+len(newLines))
		for i, line := range oldLines {
			ops = append(ops, diffOp{kind: '-', text: line, oldIndex: i, newIndex: -1})
		}
		for i, line := range newLines {
			ops = append(ops, diffOp{kind: '+', text: line, oldIndex: -1, newIndex: i})
		}
		return ops
	}

	dp := make([][]int, len(oldLines)+1)
	for i := range dp {
		dp[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]diffOp, 0, len(oldLines)+len(newLines))
	for i, j := 0, 0; i < len(oldLines) || j < len(newLines); {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			ops = append(ops, diffOp{kind: ' ', text: oldLines[i], oldIndex: i, newIndex: j})
			i++
			j++
		} else if j >= len(newLines) || (i < len(oldLines) && dp[i+1][j] >= dp[i][j+1]) {
			ops = append(ops, diffOp{kind: '-', text: oldLines[i], oldIndex: i, newIndex: -1})
			i++
		} else {
			ops = append(ops, diffOp{kind: '+', text: newLines[j], oldIndex: -1, newIndex: j})
			j++
		}
	}
	return ops
}

func appendUnifiedHunks(b *strings.Builder, ops []diffOp, oldLen, newLen int, oldNoNewline, newNoNewline bool) {
	const context = 3

	for i := 0; i < len(ops); {
		for i < len(ops) && ops[i].kind == ' ' {
			i++
		}
		if i >= len(ops) {
			break
		}

		start := i - context
		if start < 0 {
			start = 0
		}

		lastChange := i
		for j := i + 1; j < len(ops); j++ {
			if ops[j].kind == ' ' {
				continue
			}
			if j-lastChange > context*2 {
				break
			}
			lastChange = j
		}
		end := lastChange + context
		if end >= len(ops) {
			end = len(ops) - 1
		}

		oldBefore, newBefore := countLinesBefore(ops[:start])
		oldCount, newCount := countLinesInHunk(ops[start : end+1])
		oldStart := oldBefore + 1
		if oldCount == 0 {
			oldStart = oldBefore
		}
		newStart := newBefore + 1
		if newCount == 0 {
			newStart = newBefore
		}
		if oldLen == 0 {
			oldStart = 0
		}
		if newLen == 0 {
			newStart = 0
		}

		b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))
		for _, op := range ops[start : end+1] {
			b.WriteByte(op.kind)
			b.WriteString(op.text)
			b.WriteByte('\n')
			if op.kind != '+' && oldNoNewline && op.oldIndex == oldLen-1 {
				b.WriteString("\\ No newline at end of file\n")
			}
			if op.kind != '-' && newNoNewline && op.newIndex == newLen-1 {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
		i = end + 1
	}
}

func countLinesBefore(ops []diffOp) (int, int) {
	oldCount, newCount := 0, 0
	for _, op := range ops {
		if op.kind != '+' {
			oldCount++
		}
		if op.kind != '-' {
			newCount++
		}
	}
	return oldCount, newCount
}

func countLinesInHunk(ops []diffOp) (int, int) {
	return countLinesBefore(ops)
}

func (e *Executor) listChanges(sess *session.Session, path string, limit int) (string, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}
	rows := make([]string, 0, limit)
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

func (e *Executor) viewChanges(sess *session.Session, changeID string, changeIDs []string) (string, error) {
	ids := appendIDs(changeID, changeIDs)
	if len(ids) == 0 {
		return "", fmt.Errorf("change_id or change_ids is required")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	out := make([]string, 0, len(ids))
	for _, ch := range sess.Changes {
		if !wanted[ch.ID] {
			continue
		}
		out = append(out, fmt.Sprintf("change_id=%s\nat=%s\naction=%s\npath=%s\n%s",
			ch.ID, ch.At.Format(time.RFC3339), ch.Action, ch.Path, ch.UnifiedDiff))
	}
	if len(out) == 0 {
		return "change_id not found", nil
	}
	return truncate(strings.Join(out, "\n"), e.maxOutputLines), nil
}

func (e *Executor) rollback(sess *session.Session, changeIDs []string) (string, error) {
	if len(changeIDs) == 0 {
		return "", fmt.Errorf("change_ids is required for rollback")
	}
	selected := map[string]bool{}
	for _, id := range changeIDs {
		selected[id] = true
	}
	indexes := make([]int, 0, len(changeIDs))
	for i, ch := range sess.Changes {
		if selected[ch.ID] {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) != len(selected) {
		return "", fmt.Errorf("some change_ids not found")
	}
	for _, idx := range indexes {
		ch := sess.Changes[idx]
		for later := idx + 1; later < len(sess.Changes); later++ {
			if sess.Changes[later].Path == ch.Path && !selected[sess.Changes[later].ID] {
				return "", fmt.Errorf("cannot rollback %s; later change %s also modified %s", ch.ID, sess.Changes[later].ID, ch.Path)
			}
		}
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] > indexes[j] })

	out := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		ch := sess.Changes[idx]
		path, err := e.resolveWorkspaceRootPath(ch.Path)
		if err != nil {
			return strings.Join(out, "\n"), err
		}
		current, _ := os.ReadFile(path)
		diff := e.computeDiff(ch.Path, string(current), ch.BeforeContent, "rollback")
		if ch.Action == "create" {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return strings.Join(out, "\n"), err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return strings.Join(out, "\n"), err
			}
			if err := os.WriteFile(path, []byte(ch.BeforeContent), 0o644); err != nil {
				return strings.Join(out, "\n"), err
			}
		}
		sess.Changes = append(sess.Changes[:idx], sess.Changes[idx+1:]...)
		out = append(out, fmt.Sprintf("rolled_back=%s\n%s", ch.ID, diff))
	}
	if e.store != nil {
		if err := e.store.Save(sess); err != nil {
			return strings.Join(out, "\n"), err
		}
	}
	return truncate(strings.Join(out, "\n"), e.maxOutputLines), nil
}

func appendIDs(first string, rest []string) []string {
	out := make([]string, 0, 1+len(rest))
	if first != "" {
		out = append(out, first)
	}
	out = append(out, rest...)
	return out
}
