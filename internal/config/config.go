package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	defaults "github.com/asayn/asayn"
	"github.com/pelletier/go-toml/v2"
)

const (
	RootAgentKind    = "root_agents"
	SubAgentKind     = "sub_agents"
	SpecialAgentKind = "special_agents"
)

type Paths struct {
	HomeDir      string
	WorkspaceDir string
	Workplace    string
}

type APIConfig struct {
	BaseURL         string            `toml:"url" json:"url"`
	APIKey          string            `toml:"api_key" json:"api_key"`
	ReasoningEffort string            `toml:"reasoning_effort" json:"reasoning_effort"`
	ThinkingEnabled bool              `toml:"thinking_enabled" json:"thinking_enabled"`
	TimeoutSeconds  int               `toml:"timeout_seconds" json:"timeout_seconds"`
	Headers         map[string]string `toml:"headers" json:"headers"`
}

type AgentConfig struct {
	Name                  string   `toml:"name" json:"name"`
	Model                 string   `toml:"model" json:"model"`
	Description           string   `toml:"description" json:"description"`
	SystemPrompt          string   `toml:"system_prompt" json:"system_prompt"`
	VisibleSkills         []string `toml:"visible_skills" json:"visible_skills"`
	MaxOutputLines        int      `toml:"max_output_lines" json:"max_output_lines"`
	ContextWindow         int      `toml:"context_window" json:"context_window"`
	MaxOutputTokens       int      `toml:"max_output_tokens" json:"max_output_tokens"`
	AllowParallelShell    bool     `toml:"allow_parallel_shell" json:"allow_parallel_shell"`
	AllowInteractiveShell bool     `toml:"allow_interactive_shell" json:"allow_interactive_shell"`
	ThinkingEnabled       bool     `toml:"thinking_enabled" json:"thinking_enabled"`
	ReasoningEffort       string   `toml:"reasoning_effort" json:"reasoning_effort"`
}

type Skill struct {
	Name        string
	Path        string
	Source      string
	Description string
	Metadata    map[string]string
	Body        string
}

type AgentInfo struct {
	Name        string
	Description string
	Source      string
}

func Bootstrap(cwd string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Paths{}, err
	}
	paths := Paths{
		HomeDir:      filepath.Join(home, ".Asayn"),
		WorkspaceDir: filepath.Join(abs, ".Asayn"),
		Workplace:    abs,
	}
	if err := ensureHome(paths); err != nil {
		return Paths{}, err
	}
	if err := ensureWorkspace(paths); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

func (p Paths) WorkspaceSessionsDir() string {
	return filepath.Join(p.WorkspaceDir, ".sessions")
}

func (p Paths) RootAgentSessionsDir() string {
	return filepath.Join(p.WorkspaceSessionsDir(), RootAgentKind)
}

func (p Paths) SubAgentSessionsDir() string {
	return filepath.Join(p.WorkspaceSessionsDir(), SubAgentKind)
}

func (p Paths) SpecialAgentSessionsDir() string {
	return filepath.Join(p.WorkspaceSessionsDir(), SpecialAgentKind)
}

func (p Paths) WorkspacePath(parts ...string) string {
	all := append([]string{p.WorkspaceDir}, parts...)
	return filepath.Join(all...)
}

func (p Paths) HomePath(parts ...string) string {
	all := append([]string{p.HomeDir}, parts...)
	return filepath.Join(all...)
}

func LoadAPIConfig(paths Paths) (APIConfig, error) {
	cfg := defaultAPIConfig()
	path := paths.HomePath("api_config.toml")
	if !exists(path) {
		return cfg, nil
	}
	if err := readTOML(path, &cfg); err != nil {
		return cfg, err
	}
	cfg.APIKey = resolveSecret(cfg.APIKey)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com"
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = "max"
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 120
	}
	return cfg, nil
}

func LoadAgent(paths Paths, kind, name string) (AgentConfig, error) {
	if name == "" {
		name = "default"
	}
	cfg := defaultAgentConfig(kind, name)
	path := firstExisting(
		paths.WorkspacePath(kind, name+".toml"),
		paths.HomePath(kind, name+".toml"),
	)
	if path == "" {
		if name == "default" {
			return cfg, nil
		}
		return cfg, fmt.Errorf("%s/%s.toml not found", kind, name)
	}
	if err := readTOML(path, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Name == "" {
		cfg.Name = name
	}
	if cfg.Model == "" {
		if kind == SubAgentKind {
			cfg.Model = "deepseek-v4-flash"
		} else {
			cfg.Model = "deepseek-v4-pro"
		}
	}
	if cfg.Description == "" {
		cfg.Description = defaultAgentDescription(kind, cfg.Name)
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "You are a helpful assistant."
	}
	if cfg.MaxOutputLines <= 0 {
		cfg.MaxOutputLines = 2000
	}
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = 1024000
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = 384000
	}
	cfg.NormalizeShellConfig()
	cfg.NormalizeThinkingConfig()
	return cfg, nil
}

func (c *AgentConfig) NormalizeShellConfig() {
	if c.AllowInteractiveShell {
		c.AllowParallelShell = true
	}
}

func (c *AgentConfig) NormalizeThinkingConfig() {
	c.ReasoningEffort = normalizeReasoningEffort(c.ReasoningEffort)
}

func SaveAgentVisibleSkills(paths Paths, kind, name string, visibleSkills []string) (AgentConfig, error) {
	if name == "" {
		name = "default"
	}
	cfg, err := LoadAgent(paths, kind, name)
	if err != nil {
		return cfg, err
	}
	cfg.VisibleSkills = uniqueSorted(visibleSkills)
	if cfg.Name == "" {
		cfg.Name = name
	}
	path := paths.WorkspacePath(kind, name+".toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return cfg, err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, os.WriteFile(path, data, 0o644)
}

func SaveRootAgentShellConfig(paths Paths, name string, allowParallel, allowInteractive bool) (AgentConfig, error) {
	if name == "" {
		name = "default"
	}
	cfg, err := LoadAgent(paths, RootAgentKind, name)
	if err != nil {
		return cfg, err
	}
	cfg.AllowParallelShell = allowParallel
	cfg.AllowInteractiveShell = allowInteractive && allowParallel
	cfg.NormalizeShellConfig()
	if cfg.Name == "" {
		cfg.Name = name
	}
	path := paths.WorkspacePath(RootAgentKind, name+".toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return cfg, err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, os.WriteFile(path, data, 0o644)
}

func SaveAgentThinkingConfig(paths Paths, kind, name string, thinkingEnabled bool, reasoningEffort string) (AgentConfig, error) {
	if name == "" {
		name = "default"
	}
	cfg, err := LoadAgent(paths, kind, name)
	if err != nil {
		return cfg, err
	}
	cfg.ThinkingEnabled = thinkingEnabled
	cfg.ReasoningEffort = normalizeReasoningEffort(reasoningEffort)
	if cfg.Name == "" {
		cfg.Name = name
	}
	path := paths.WorkspacePath(kind, name+".toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return cfg, err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, os.WriteFile(path, data, 0o644)
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "max", "xhigh":
		return "max"
	default:
		return "high"
	}
}

func uniqueSorted(items []string) []string {
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			seen[item] = true
		}
	}
	out := make([]string, 0, len(seen))
	for item := range seen {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func ListAgentInfos(paths Paths, kind string) ([]AgentInfo, error) {
	names := map[string]AgentInfo{}
	for _, base := range []struct {
		root, source string
	}{
		{paths.HomePath(kind), "home"},
		{paths.WorkspacePath(kind), "workplace"},
	} {
		entries, err := os.ReadDir(base.root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, ent := range entries {
			if ent.IsDir() || filepath.Ext(ent.Name()) != ".toml" {
				continue
			}
			name := strings.TrimSuffix(ent.Name(), ".toml")
			cfg := defaultAgentConfig(kind, name)
			_ = readTOML(filepath.Join(base.root, ent.Name()), &cfg)
			if cfg.Name == "" {
				cfg.Name = name
			}
			if cfg.Description == "" {
				cfg.Description = defaultAgentDescription(kind, cfg.Name)
			}
			names[name] = AgentInfo{Name: cfg.Name, Description: cfg.Description, Source: base.source}
		}
	}
	out := make([]AgentInfo, 0, len(names))
	for _, info := range names {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func ListAgents(paths Paths, kind string) ([]string, error) {
	names := map[string]string{}
	for _, base := range []struct {
		root, source string
	}{
		{paths.HomePath(kind), "home"},
		{paths.WorkspacePath(kind), "workplace"},
	} {
		entries, err := os.ReadDir(base.root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, ent := range entries {
			if ent.IsDir() || filepath.Ext(ent.Name()) != ".toml" {
				continue
			}
			name := strings.TrimSuffix(ent.Name(), ".toml")
			names[name] = base.source
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func ListSkills(paths Paths) ([]Skill, error) {
	seen := map[string]Skill{}
	for _, base := range []struct {
		root, source string
	}{
		{paths.HomePath("skills"), "~/.Asayn/skills"},
		{paths.WorkspacePath("skills"), "[workplace]/.Asayn/skills"},
	} {
		entries, err := os.ReadDir(base.root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			skillPath := filepath.Join(base.root, ent.Name(), "SKILL.md")
			body, err := os.ReadFile(skillPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			meta, markdown := parseSkillMarkdown(string(body))
			name := meta["name"]
			if name == "" {
				name = ent.Name()
			}
			seen[name] = Skill{
				Name:        name,
				Path:        skillPath,
				Source:      base.source,
				Description: meta["description"],
				Metadata:    meta,
				Body:        markdown,
			}
		}
	}
	out := make([]Skill, 0, len(seen))
	for _, skill := range seen {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func LoadSkill(paths Paths, name string) (Skill, error) {
	if name == "" {
		return Skill{}, fmt.Errorf("skill name is required")
	}
	skills, err := ListSkills(paths)
	if err != nil {
		return Skill{}, err
	}
	for _, skill := range skills {
		if skill.Name == name {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("skill %q not found", name)
}

func parseSkillMarkdown(content string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(content, "---\n") {
		return meta, content
	}
	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, content
	}
	front := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			meta[key] = value
		}
	}
	return meta, body
}

func ensureHome(paths Paths) error {
	if err := os.MkdirAll(paths.HomeDir, 0o755); err != nil {
		return err
	}
	return copyEmbeddedDefaults(paths.HomeDir)
}

func copyEmbeddedDefaults(dstRoot string) error {
	return fs.WalkDir(defaults.FS, "default_Asayn", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("default_Asayn", path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if exists(dst) {
			return nil
		}
		data, err := defaults.FS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func ensureWorkspace(paths Paths) error {
	if err := os.MkdirAll(paths.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.WorkspaceSessionsDir(), 0o755); err != nil {
		return err
	}
	for _, dir := range []string{
		paths.RootAgentSessionsDir(),
		paths.SubAgentSessionsDir(),
		paths.SpecialAgentSessionsDir(),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, rel := range []string{RootAgentKind, SubAgentKind, SpecialAgentKind} {
		dst := paths.WorkspacePath(rel)
		if exists(dst) {
			continue
		}
		src := paths.HomePath(rel)
		if err := copyPath(src, dst); err != nil {
			return err
		}
	}
	return ensureGitIgnore(paths.Workplace)
}

func ensureGitIgnore(workplace string) error {
	path := filepath.Join(workplace, ".gitignore")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(b)
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == ".Asayn/" {
			return nil
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += ".Asayn/\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func defaultAPIConfig() APIConfig {
	return APIConfig{
		BaseURL:         "https://api.deepseek.com",
		APIKey:          "env:DEEPSEEK_API_KEY",
		ReasoningEffort: "max",
		ThinkingEnabled: true,
		TimeoutSeconds:  120,
		Headers:         map[string]string{},
	}
}

func defaultAgentConfig(kind, name string) AgentConfig {
	model := "deepseek-v4-pro"
	if kind == SubAgentKind {
		model = "deepseek-v4-flash"
	}
	return AgentConfig{
		Name:            name,
		Model:           model,
		Description:     defaultAgentDescription(kind, name),
		SystemPrompt:    "You are a helpful assistant.",
		VisibleSkills:   []string{},
		MaxOutputLines:  2000,
		ContextWindow:   1024000,
		MaxOutputTokens: 384000,
		ThinkingEnabled: true,
		ReasoningEffort: "max",
	}
}

func defaultAgentDescription(kind, name string) string {
	if kind == SubAgentKind {
		return "General-purpose sub-agent."
	}
	if name == "default" {
		return "General-purpose agent."
	}
	return ""
}

func resolveSecret(value string) string {
	if strings.HasPrefix(value, "env:") {
		return os.Getenv(strings.TrimPrefix(value, "env:"))
	}
	return value
}

func readTOML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return toml.Unmarshal(data, out)
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if exists(path) {
			return path
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
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
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if exists(dst) {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
