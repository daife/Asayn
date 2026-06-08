package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	diffContextLines = 3
)

// fileSnapshot stores enough to detect content changes.
type fileSnapshot struct {
	Path    string
	Content string
	Missing bool
}

// snapFiles walks the workspace, skipping .Asayn/ and binary/risky files.
// hasAsaynComponent checks if any path component is .Asayn
func hasAsaynComponent(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		if p == ".Asayn" {
			return true
		}
	}
	return false
}

func snapFiles(root string) []fileSnapshot {
	var snaps []fileSnapshot
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if hasAsaynComponent(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isRiskyFile(filepath.Base(p)) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			snaps = append(snaps, fileSnapshot{Path: rel, Missing: true})
			return nil
		}
		// Quick binary probe
		probe := data
		if len(probe) > binaryProbeSize {
			probe = probe[:binaryProbeSize]
		}
		if isBinary(probe) {
			return nil
		}
		snaps = append(snaps, fileSnapshot{Path: rel, Content: string(data)})
		return nil
	})
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Path < snaps[j].Path })
	return snaps
}

// computeFileDiff returns a unified-diff-style string showing file changes.
func computeFileDiff(before, after []fileSnapshot) string {
	beforeMap := make(map[string]string)
	beforeExists := make(map[string]bool)
	for _, s := range before {
		beforeMap[s.Path] = s.Content
		beforeExists[s.Path] = !s.Missing
	}

	afterMap := make(map[string]string)
	afterExists := make(map[string]bool)
	for _, s := range after {
		afterMap[s.Path] = s.Content
		afterExists[s.Path] = !s.Missing
	}

	type change struct {
		Path   string
		Before string
		After  string
		New    bool
		Del    bool
	}
	var changes []change

	// Deleted and modified
	for path, oldContent := range beforeMap {
		newContent, ok := afterMap[path]
		if !ok {
			changes = append(changes, change{Path: path, Before: oldContent, Del: true})
		} else if oldContent != newContent {
			changes = append(changes, change{Path: path, Before: oldContent, After: newContent})
		}
	}
	// Added
	for path, newContent := range afterMap {
		if _, ok := beforeMap[path]; !ok {
			changes = append(changes, change{Path: path, After: newContent, New: true})
		}
	}

	if len(changes) == 0 {
		return ""
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })

	var out strings.Builder
	out.WriteString("\n---\nFile changes:\n")

	for _, ch := range changes {

		if ch.New {
			out.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", ch.Path, ch.Path))
			out.WriteString("new file\n")
			lines := strings.Split(ch.After, "\n")
			out.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
			for _, l := range lines {
				out.WriteString("+" + l + "\n")
			}
		} else if ch.Del {
			out.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", ch.Path, ch.Path))
			out.WriteString("deleted file\n")
			lines := strings.Split(ch.Before, "\n")
			out.WriteString(fmt.Sprintf("@@ -1,%d +0,0 @@\n", len(lines)))
			for _, l := range lines {
				out.WriteString("-" + l + "\n")
			}
		} else {
			diff := unifiedDiff(ch.Path, ch.Before, ch.After)
			out.WriteString(diff)
		}
	}
	return out.String()
}

// unifiedDiff produces a minimal unified diff between two text contents.
func unifiedDiff(path, a, b string) string {
	aLines := strings.Split(a, "\n")
	bLines := strings.Split(b, "\n")

	hunks := computeHunks(aLines, bLines, diffContextLines)
	if len(hunks) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", path, path))
	out.WriteString("--- a/" + path + "\n")
	out.WriteString("+++ b/" + path + "\n")

	for _, h := range hunks {
		out.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", h.AStart+1, h.ALen, h.BStart+1, h.BLen))
		for _, l := range h.Lines {
			out.WriteString(l + "\n")
		}
	}
	return out.String()
}

type hunk struct {
	AStart, ALen int
	BStart, BLen int
	Lines        []string
}

func computeHunks(a, b []string, ctx int) []hunk {
	// LCS-based diff; walk the diff ops once, emitting hunks with context.
	diffs := diffLines(a, b)
	if len(diffs) == 0 {
		return nil
	}

	var hunks []hunk
	var cur *hunk
	needCtx := ctx
	pos := 0

	for pos < len(diffs) {
		op, aIdx, bIdx, text := diffs[pos].op, diffs[pos].aIdx, diffs[pos].bIdx, diffs[pos].text

		if op == 0 { // equal
			if cur != nil {
				if needCtx > 0 {
					cur.Lines = append(cur.Lines, " "+text)
					needCtx--
					cur.ALen++
					cur.BLen++
				} else {
					hunks = append(hunks, *cur)
					cur = nil
					needCtx = ctx
				}
			}
			pos++
			continue
		}

		// Start a new hunk if needed
		if cur == nil {
			// Walk back to include leading context
			ctxStart := pos
			for c := 0; c < ctx && ctxStart > 0 && diffs[ctxStart-1].op == 0; c++ {
				ctxStart--
			}
			cur = &hunk{}
			for j := ctxStart; j < pos; j++ {
				e := diffs[j]
				if j == ctxStart {
					cur.AStart = e.aIdx
					cur.BStart = e.bIdx
				}
				cur.Lines = append(cur.Lines, " "+e.text)
				cur.ALen++
				cur.BLen++
			}
			if cur.ALen == 0 && cur.BLen == 0 {
				cur.AStart = aIdx
				cur.BStart = bIdx
			}
			needCtx = ctx
		}

		if op == -1 {
			cur.Lines = append(cur.Lines, "-"+text)
			cur.ALen++
		} else {
			cur.Lines = append(cur.Lines, "+"+text)
			cur.BLen++
		}
		pos++
	}

	if cur != nil {
		// Trailing context
		remaining := ctx
		for pos < len(diffs) && diffs[pos].op == 0 && remaining > 0 {
			e := diffs[pos]
			cur.Lines = append(cur.Lines, " "+e.text)
			cur.ALen++
			cur.BLen++
			remaining--
			pos++
		}
		hunks = append(hunks, *cur)
	}

	return hunks
}

type diffOp struct {
	op        int   // 0=equal, -1=delete, 1=add
	aIdx, bIdx int
	text      string
}

// simple LCS diff returning operations
func diffLines(a, b []string) []diffOp {
	m, n := len(a), len(b)
	// memoized LCS
	type pair struct{ i, j int }
	lcs := make(map[pair]int)
	var lcsFunc func(i, j int) int
	lcsFunc = func(i, j int) int {
		if i >= m || j >= n {
			return 0
		}
		key := pair{i, j}
		if v, ok := lcs[key]; ok {
			return v
		}
		if a[i] == b[j] {
			lcs[key] = 1 + lcsFunc(i+1, j+1)
		} else {
			l1 := lcsFunc(i+1, j)
			l2 := lcsFunc(i, j+1)
			if l1 >= l2 {
				lcs[key] = l1
			} else {
				lcs[key] = l2
			}
		}
		return lcs[key]
	}
	_ = lcsFunc(0, 0) // populate lcs

	// backtrack
	var ops []diffOp
	i, j := 0, 0
	for i < m || j < n {
		if i < m && j < n && a[i] == b[j] {
			ops = append(ops, diffOp{op: 0, aIdx: i, bIdx: j, text: a[i]})
			i++
			j++
		} else if j < n && (i >= m || lcs[pair{i + 1, j}] < lcs[pair{i, j + 1}]) {
			ops = append(ops, diffOp{op: 1, aIdx: i, bIdx: j, text: b[j]})
			j++
		} else if i < m {
			ops = append(ops, diffOp{op: -1, aIdx: i, bIdx: j, text: a[i]})
			i++
		} else if j < n {
			ops = append(ops, diffOp{op: 1, aIdx: i, bIdx: j, text: b[j]})
			j++
		}
	}
	return ops
}
