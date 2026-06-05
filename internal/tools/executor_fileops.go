package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asayn/asayn/internal/session"
	"github.com/google/uuid"
)

// fileEdit is the unified file editing tool. Modes:
//   write         - write full file content (creates or overwrites)
//   delete_lines  - delete a line range
//   insert        - insert text after a given line
//   replace_lines - replace a line range with new text
//   find_replace  - find exact old_text and replace with new_text
//   view          - view change history or detail
//   rollback      - rollback recorded changes
func (e *Executor) fileEdit(sess *session.Session, args map[string]any) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	mode := stringArg(args, "mode")
	changeID := stringArg(args, "change_id")

	switch mode {
	case "view":
		if changeID != "" || len(stringSliceArg(args, "change_ids")) > 0 {
			return e.viewChanges(sess, changeID, stringSliceArg(args, "change_ids"))
		}
		return e.listChanges(sess, stringArg(args, "relative_path"), intArg(args, "limit", 0))
	case "rollback":
		return e.rollback(sess, appendIDs(changeID, stringSliceArg(args, "change_ids")))
	}

	dryRun := boolArg(args, "dry_run", false)
	path, err := e.resolveWorkplacePath(stringArg(args, "relative_path"))
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
		oldText := stringArg(args, "old_text")
		if oldText == "" {
			return "", fmt.Errorf("old_text is required for find_replace")
		}
		newText := stringArg(args, "new_text")
		count := strings.Count(before, oldText)
		if count == 0 {
			return "", fmt.Errorf("old_text not found in file")
		}
		replaceAll := boolArg(args, "replace_all", false)
		if !replaceAll && count > 1 {
			return "", fmt.Errorf("old_text matched %d times; use more surrounding context or set replace_all=true", count)
		}
		if replaceAll {
			after = strings.ReplaceAll(before, oldText, newText)
		} else {
			after = strings.Replace(before, oldText, newText, 1)
		}

	default:
		return "", fmt.Errorf("unsupported file_edit mode %q; valid: write, delete_lines, insert, replace_lines, find_replace, view, rollback", mode)
	}

	diff := e.computeDiff(stringArg(args, "relative_path"), before, after, mode)
	if dryRun {
		return truncate(diff, e.maxOutputLines), nil
	}

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
		Path:          filepath.ToSlash(stringArg(args, "relative_path")),
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

// computeDiff produces a focused diff with 2 lines of surrounding context.
func (e *Executor) computeDiff(relPath, before, after, mode string) string {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	if before == "" {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", filepath.ToSlash(relPath), len(afterLines)))
		for _, l := range afterLines {
			b.WriteString("+" + l + "\n")
		}
		return b.String()
	}

	firstDiff := -1
	lastDiff := -1
	maxLen := len(beforeLines)
	if len(afterLines) > maxLen {
		maxLen = len(afterLines)
	}
	for i := 0; i < maxLen; i++ {
		var bl, al string
		if i < len(beforeLines) {
			bl = beforeLines[i]
		}
		if i < len(afterLines) {
			al = afterLines[i]
		}
		if bl != al {
			if firstDiff < 0 {
				firstDiff = i
			}
			lastDiff = i
		}
	}
	if firstDiff < 0 {
		return "(no changes)"
	}

	ctx := 2
	displayStart := firstDiff - ctx
	if displayStart < 0 {
		displayStart = 0
	}
	displayEnd := lastDiff + ctx
	if displayEnd >= maxLen {
		displayEnd = maxLen - 1
	}

	oldStart := displayStart + 1
	oldCount := displayEnd - displayStart + 1
	if oldCount > len(beforeLines)-displayStart {
		oldCount = len(beforeLines) - displayStart
	}
	newStart := displayStart + 1
	newCount := displayEnd - displayStart + 1
	if newCount > len(afterLines)-displayStart {
		newCount = len(afterLines) - displayStart
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n",
		filepath.ToSlash(relPath), filepath.ToSlash(relPath),
		oldStart, oldCount, newStart, newCount))
	for i := displayStart; i <= displayEnd; i++ {
		var bl, al string
		if i < len(beforeLines) {
			bl = beforeLines[i]
		}
		if i < len(afterLines) {
			al = afterLines[i]
		}
		if bl == al {
			b.WriteString(" " + bl + "\n")
		} else {
			if i < len(beforeLines) {
				b.WriteString("-" + bl + "\n")
			}
			if i < len(afterLines) {
				b.WriteString("+" + al + "\n")
			}
		}
	}
	if lastDiff-firstDiff > 20 {
		b.WriteString(fmt.Sprintf("  ... (%d lines changed in %d-line context)\n",
			lastDiff-firstDiff+1, displayEnd-displayStart+1))
	}
	return b.String()
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
		path, err := e.resolveWorkplacePath(ch.Path)
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
