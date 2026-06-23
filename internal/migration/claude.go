package migration

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/asayn/asayn/internal/config"
)

type ClaudeItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Source    string         `json:"source"`
	Target    string         `json:"target,omitempty"`
	Duplicate bool           `json:"duplicate"`
	Reason    string         `json:"reason,omitempty"`
	Config    map[string]any `json:"-"`
}

type ClaudeResult struct {
	Migrated []string `json:"migrated"`
	Skipped  []string `json:"skipped"`
}

type mcpConfigFile struct {
	MCPServers map[string]map[string]any `json:"mcpServers"`
}

func DiscoverClaude(paths config.Paths) ([]ClaudeItem, error) {
	skills, err := discoverSkills(paths)
	if err != nil {
		return nil, err
	}
	mcps, err := discoverMCP(paths)
	if err != nil {
		return nil, err
	}
	items := append(skills, mcps...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind > items[j].Kind
		}
		return strings.ToLower(items[i].Name)+"\x00"+items[i].Source < strings.ToLower(items[j].Name)+"\x00"+items[j].Source
	})
	return items, nil
}

func MigrateClaude(paths config.Paths, ids []string) (ClaudeResult, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			selected[id] = true
		}
	}
	if len(selected) == 0 {
		return ClaudeResult{}, nil
	}
	items, err := DiscoverClaude(paths)
	if err != nil {
		return ClaudeResult{}, err
	}
	result := ClaudeResult{}
	for _, item := range items {
		if !selected[item.ID] {
			continue
		}
		if item.Duplicate {
			result.Skipped = append(result.Skipped, fmt.Sprintf("%s %s: %s", item.Kind, item.Name, item.Reason))
			continue
		}
		switch item.Kind {
		case "skill":
			if err := copyDir(item.Source, item.Target); err != nil {
				result.Skipped = append(result.Skipped, fmt.Sprintf("skill %s: %v", item.Name, err))
				continue
			}
			result.Migrated = append(result.Migrated, fmt.Sprintf("skill %s -> %s", item.Name, item.Target))
		case "mcp":
			target := item.Target
			if target == "" {
				target = filepath.Join(paths.HomePath(config.MCPConfigKind), safeFilename(item.Name)+".json")
			}
			if exists(target) {
				result.Skipped = append(result.Skipped, fmt.Sprintf("mcp %s: target already exists", item.Name))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				result.Skipped = append(result.Skipped, fmt.Sprintf("mcp %s: %v", item.Name, err))
				continue
			}
			data, err := json.MarshalIndent(mcpConfigFile{MCPServers: map[string]map[string]any{item.Name: item.Config}}, "", "  ")
			if err != nil {
				result.Skipped = append(result.Skipped, fmt.Sprintf("mcp %s: %v", item.Name, err))
				continue
			}
			if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
				result.Skipped = append(result.Skipped, fmt.Sprintf("mcp %s: %v", item.Name, err))
				continue
			}
			result.Migrated = append(result.Migrated, fmt.Sprintf("mcp %s -> %s", item.Name, target))
		}
	}
	sort.Strings(result.Migrated)
	sort.Strings(result.Skipped)
	return result, nil
}

func discoverSkills(paths config.Paths) ([]ClaudeItem, error) {
	home, _ := os.UserHomeDir()
	roots := []string{}
	if cfg := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); cfg != "" {
		roots = append(roots, filepath.Join(cfg, "skills"))
	}
	roots = append(roots,
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".config", "claude-code", "skills"),
	)
	if appdata := strings.TrimSpace(os.Getenv("APPDATA")); appdata != "" {
		roots = append(roots, filepath.Join(appdata, "Claude", "skills"))
	}
	roots = append(roots, filepath.Join(paths.WorkspaceRoot, ".claude", "skills"))

	existing := map[string]bool{}
	if skills, err := config.ListSkills(paths); err == nil {
		for _, skill := range skills {
			existing[strings.ToLower(skill.Name)] = true
			existing[strings.ToLower(filepath.Base(skill.Path))] = true
		}
	}
	for _, dir := range []string{paths.HomePath("skills"), paths.WorkspacePath("skills")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				existing[strings.ToLower(ent.Name())] = true
			}
		}
	}

	var items []ClaudeItem
	seen := map[string]bool{}
	for _, root := range uniqueExistingPaths(roots) {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && isPluginPath(path) {
				return filepath.SkipDir
			}
			if d.IsDir() || d.Name() != "SKILL.md" {
				return nil
			}
			folder := filepath.Dir(path)
			key := canonical(folder)
			if seen[key] {
				return nil
			}
			seen[key] = true
			name := parseSkillName(path)
			if name == "" {
				name = filepath.Base(folder)
			}
			target := paths.HomePath("skills", filepath.Base(folder))
			duplicate := exists(target) || existing[strings.ToLower(name)] || existing[strings.ToLower(filepath.Base(folder))]
			reason := ""
			if duplicate {
				reason = "already exists in Asayn skills"
			}
			items = append(items, ClaudeItem{
				ID:        itemID("skill", folder, name),
				Kind:      "skill",
				Name:      name,
				Source:    folder,
				Target:    target,
				Duplicate: duplicate,
				Reason:    reason,
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name)+"\x00"+items[i].Source < strings.ToLower(items[j].Name)+"\x00"+items[j].Source
	})
	return items, nil
}

func discoverMCP(paths config.Paths) ([]ClaudeItem, error) {
	home, _ := os.UserHomeDir()
	roots := []string{}
	if cfg := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); cfg != "" {
		roots = append(roots, cfg)
	}
	roots = append(roots,
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".config", "claude"),
		filepath.Join(home, ".config", "claude-code"),
	)
	if appdata := strings.TrimSpace(os.Getenv("APPDATA")); appdata != "" {
		roots = append(roots, filepath.Join(appdata, "Claude"))
	}
	if localappdata := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localappdata != "" {
		roots = append(roots, filepath.Join(localappdata, "Claude"))
	}
	roots = append(roots,
		filepath.Join(paths.WorkspaceRoot, ".mcp.json"),
		filepath.Join(paths.WorkspaceRoot, ".claude"),
	)

	var files []string
	seenFiles := map[string]bool{}
	for _, root := range uniqueExistingPaths(roots) {
		info, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if strings.EqualFold(filepath.Ext(root), ".json") && !isPluginPath(root) {
				key := canonical(root)
				if !seenFiles[key] {
					seenFiles[key] = true
					files = append(files, root)
				}
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && isPluginPath(path) {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".json") {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > 5*1024*1024 {
				return nil
			}
			key := canonical(path)
			if !seenFiles[key] {
				seenFiles[key] = true
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	existing := map[string]bool{}
	if servers, err := config.ListMCPServers(paths); err == nil {
		for _, srv := range servers {
			existing[strings.ToLower(srv.Name)] = true
		}
	}

	byName := map[string]ClaudeItem{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var parsed mcpConfigFile
		if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.MCPServers) == 0 {
			continue
		}
		for name, cfg := range parsed.MCPServers {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := byName[name]; ok {
				continue
			}
			target := paths.HomePath(config.MCPConfigKind, safeFilename(name)+".json")
			duplicate := exists(target) || existing[strings.ToLower(name)]
			reason := ""
			if duplicate {
				reason = "already exists in Asayn MCP servers"
			}
			byName[name] = ClaudeItem{
				ID:        itemID("mcp", file, name),
				Kind:      "mcp",
				Name:      name,
				Source:    file,
				Target:    target,
				Duplicate: duplicate,
				Reason:    reason,
				Config:    cfg,
			}
		}
	}
	items := make([]ClaudeItem, 0, len(byName))
	for _, item := range byName {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	return items, nil
}

func parseSkillName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return ""
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "name" {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	if exists(dst) {
		return fmt.Errorf("%s already exists", dst)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func uniqueExistingPaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		key := canonical(path)
		if !seen[key] {
			seen[key] = true
			out = append(out, path)
		}
	}
	return out
}

func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func isPluginPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "plugins" {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var safeNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeFilename(name string) string {
	safe := strings.Trim(safeNameRE.ReplaceAllString(name, "_"), "._-")
	if safe == "" {
		return "mcp_server"
	}
	return safe
}

func itemID(kind, source, name string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(canonical(source)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%s-%x", kind, h.Sum64())
}
