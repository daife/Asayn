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
	HomeDir       string
	WorkspaceDir  string
	WorkspaceRoot string
}

type ProviderConfig struct {
	BaseURL        string                 `toml:"url" json:"url"`
	APIKey         string                 `toml:"api_key" json:"api_key"`
	TimeoutSeconds int                    `toml:"timeout_seconds" json:"timeout_seconds"`
	AllowedModels  []string               `toml:"allowed_models" json:"allowed_models"`
	ModelLimits    map[string]ModelLimits `toml:"model_limits" json:"model_limits"`
}

type ModelLimits struct {
	ContextWindow   int `toml:"context_window" json:"context_window"`
	MaxOutputTokens int `toml:"max_output_tokens" json:"max_output_tokens"`
}

type APIConfig struct {
	Providers map[string]ProviderConfig `toml:"providers" json:"providers"`
}

type AgentConfig struct {
	Name                        string   `toml:"name" json:"name"`
	Provider                    string   `toml:"provider" json:"provider"`
	Model                       string   `toml:"model" json:"model"`
	Description                 string   `toml:"description" json:"description"`
	SystemPrompt                string   `toml:"system_prompt" json:"system_prompt"`
	VisibleSkills               []string `toml:"visible_skills" json:"visible_skills"`
	MaxOutputLines              int      `toml:"max_output_lines" json:"max_output_lines"`
	ContextWindow               int      `toml:"context_window" json:"context_window"`
	MaxOutputTokens             int      `toml:"max_output_tokens" json:"max_output_tokens"`
	AutoCompactThresholdPercent int      `toml:"auto_compact_threshold_percent" json:"auto_compact_threshold_percent"`
	RealTimeContextControl      bool     `toml:"real_time_context_control" json:"real_time_context_control"`
	AllowParallelShell          bool     `toml:"allow_parallel_shell" json:"allow_parallel_shell"`
	AllowInteractiveShell       bool     `toml:"allow_interactive_shell" json:"allow_interactive_shell"`
	ThinkingEnabled             bool     `toml:"thinking_enabled" json:"thinking_enabled"`
	ReasoningEffort             string   `toml:"reasoning_effort" json:"reasoning_effort"`
}

type Skill struct {
	Name        string
	Folder      string
	Path        string
	Source      string
	Description string
	Metadata    map[string]string
	Content     string
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
		HomeDir:       filepath.Join(home, ".Asayn"),
		WorkspaceDir:  filepath.Join(abs, ".Asayn"),
		WorkspaceRoot: abs,
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
	for k, p := range cfg.Providers {
		if p.TimeoutSeconds <= 0 {
			p.TimeoutSeconds = 120
		}
		cfg.Providers[k] = p
	}
	return cfg, nil
}

// ModelLimitsFor returns the context window and max output tokens for a given
// model/provider combination. Falls back to sensible defaults when not configured:
// DeepSeek models default to 1M context / 384k output,
// Nex-N2-Pro defaults to 384K context / 32k output.
func ModelLimitsFor(api APIConfig, provider, model string) ModelLimits {
	defaults := defaultModelLimits(model)
	if prov, ok := api.Providers[provider]; ok {
		if limits, ok := prov.ModelLimits[model]; ok {
			if limits.ContextWindow <= 0 {
				limits.ContextWindow = defaults.ContextWindow
			}
			if limits.MaxOutputTokens <= 0 {
				limits.MaxOutputTokens = defaults.MaxOutputTokens
			}
			return limits
		}
	}
	return defaults
}

func defaultModelLimits(model string) ModelLimits {
	l := strings.ToLower(model)
	if strings.Contains(l, "nex-n2") {
		return ModelLimits{ContextWindow: 384000, MaxOutputTokens: 32768}
	}
	// deepseek-class defaults
	return ModelLimits{ContextWindow: 1024000, MaxOutputTokens: 384000}
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
	if cfg.Provider == "" {
		cfg.Provider = "DeepSeek"
	}
	if cfg.Model == "" {
		if kind == SubAgentKind || kind == SpecialAgentKind {
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
	if cfg.AutoCompactThresholdPercent <= 0 {
		cfg.AutoCompactThresholdPercent = 80
	}
	cfg.AutoCompactThresholdPercent = clampPercent(cfg.AutoCompactThresholdPercent)
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

func SaveAgent(paths Paths, kind, name string, update func(*AgentConfig)) (AgentConfig, error) {
	if name == "" {
		name = "default"
	}
	cfg, err := LoadAgent(paths, kind, name)
	if err != nil {
		return cfg, err
	}
	update(&cfg)
	cfg.AutoCompactThresholdPercent = clampPercent(cfg.AutoCompactThresholdPercent)
	cfg.NormalizeShellConfig()
	cfg.NormalizeThinkingConfig()
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

func SaveAgentVisibleSkills(paths Paths, kind, name string, visibleSkills []string) (AgentConfig, error) {
	return SaveAgent(paths, kind, name, func(c *AgentConfig) {
		c.VisibleSkills = uniqueSorted(visibleSkills)
	})
}

func SaveRootAgentShellConfig(paths Paths, name string, allowParallel, allowInteractive bool) (AgentConfig, error) {
	return SaveAgent(paths, RootAgentKind, name, func(c *AgentConfig) {
		c.AllowParallelShell = allowParallel
		c.AllowInteractiveShell = allowInteractive && allowParallel
	})
}

func SaveAgentThinkingConfig(paths Paths, kind, name string, thinkingEnabled bool, reasoningEffort string) (AgentConfig, error) {
	return SaveAgent(paths, kind, name, func(c *AgentConfig) {
		c.ThinkingEnabled = thinkingEnabled
		c.ReasoningEffort = normalizeReasoningEffort(reasoningEffort)
	})
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	case "max":
		return "max"
	default:
		return "high"
	}
}

func clampPercent(value int) int {
	if value < 5 {
		return 5
	}
	if value > 95 {
		return 95
	}
	return value
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
		{paths.WorkspacePath(kind), "workspace"},
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
		{paths.WorkspacePath(kind), "workspace"},
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
		{paths.WorkspacePath("skills"), "[workspace]/.Asayn/skills"},
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
				Folder:      filepath.ToSlash(filepath.Join(base.source, ent.Name())),
				Path:        skillPath,
				Source:      base.source,
				Description: meta["description"],
				Metadata:    meta,
				Content:     string(body),
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
	return ensureGitIgnore(paths.WorkspaceRoot)
}

func ensureGitIgnore(workspace string) error {
	path := filepath.Join(workspace, ".gitignore")
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
		Providers: map[string]ProviderConfig{
			"SiliconFlow": {
				BaseURL:        "https://api.siliconflow.cn/v1",
				APIKey:         "your_api_key",
				TimeoutSeconds: 120,
				AllowedModels: []string{
					"nex-agi/Nex-N2-Pro",
					"deepseek-ai/DeepSeek-V4-Pro",
					"deepseek-ai/DeepSeek-V4-Flash",
				},
				ModelLimits: map[string]ModelLimits{
					"nex-agi/Nex-N2-Pro":            {ContextWindow: 384000, MaxOutputTokens: 32768},
					"deepseek-ai/DeepSeek-V4-Pro":   {ContextWindow: 1024000, MaxOutputTokens: 384000},
					"deepseek-ai/DeepSeek-V4-Flash": {ContextWindow: 1024000, MaxOutputTokens: 384000},
				},
			},
			"DeepSeek": {
				BaseURL:        "https://api.deepseek.com",
				APIKey:         "your_api_key",
				TimeoutSeconds: 120,
				AllowedModels: []string{
					"deepseek-v4-pro",
					"deepseek-v4-flash",
				},
				ModelLimits: map[string]ModelLimits{
					"deepseek-v4-pro":   {ContextWindow: 1024000, MaxOutputTokens: 384000},
					"deepseek-v4-flash": {ContextWindow: 1024000, MaxOutputTokens: 384000},
				},
			},
		},
	}
}

func defaultAgentConfig(kind, name string) AgentConfig {
	model := "deepseek-v4-pro"
	provider := "DeepSeek"
	if kind == SubAgentKind {
		model = "deepseek-v4-flash"
	}
	if kind == SpecialAgentKind {
		model = "deepseek-v4-flash"
	}
	return AgentConfig{
		Name:                        name,
		Provider:                    provider,
		Model:                       model,
		Description:                 defaultAgentDescription(kind, name),
		SystemPrompt:                "You are a helpful assistant.",
		VisibleSkills:               []string{},
		MaxOutputLines:              2000,
		ContextWindow:               1024000,
		MaxOutputTokens:             384000,
		AutoCompactThresholdPercent: 80,
		RealTimeContextControl:      false,
		ThinkingEnabled:             false,
		ReasoningEffort:             "max",
	}
}

func defaultAgentDescription(kind, name string) string {
	if kind == SubAgentKind {
		return "General-purpose sub-agent."
	}
	if kind == SpecialAgentKind && name == "compact_agent" {
		return "Summarizes prior context into a compact continuation state."
	}
	if kind == SpecialAgentKind {
		return "Special-purpose agent."
	}
	if name == "default" {
		return "General-purpose agent."
	}
	return ""
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
