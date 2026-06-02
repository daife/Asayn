package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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
	APIKey         string            `toml:"api_key" json:"api_key"`
	Model          string            `toml:"model" json:"model"`
	ReasoningEffort string          `toml:"reasoning_effort" json:"reasoning_effort"`
	ThinkingEnabled bool             `toml:"thinking_enabled" json:"thinking_enabled"`
	TimeoutSeconds int               `toml:"timeout_seconds" json:"timeout_seconds"`
	Headers        map[string]string `toml:"headers" json:"headers"`
}

type AgentConfig struct {
	Name                  string   `toml:"name" json:"name"`
	SystemPrompt          string   `toml:"system_prompt" json:"system_prompt"`
	VisibleSkills         []string `toml:"visible_skills" json:"visible_skills"`
	MaxOutputChars        int      `toml:"max_output_chars" json:"max_output_chars"`
	AllowInteractiveShell bool     `toml:"allow_interactive_shell" json:"allow_interactive_shell"`
}

type Skill struct {
	Name   string
	Path   string
	Source string
	Body   string
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
		WorkspaceDir: filepath.Join(abs, "Asayn"),
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
	path := firstExisting(paths.WorkspacePath("api_config.toml"), paths.HomePath("api_config.toml"))
	if path == "" {
		return cfg, nil
	}
	if err := readTOML(path, &cfg); err != nil {
		return cfg, err
	}
	cfg.APIKey = resolveSecret(cfg.APIKey)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com"
	}
	if cfg.Model == "" {
		cfg.Model = "deepseek-v4-pro"
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
	cfg := defaultAgentConfig(name)
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
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "You are a helpful assistant."
	}
	if cfg.MaxOutputChars <= 0 {
		cfg.MaxOutputChars = 5000
	}
	return cfg, nil
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
		{paths.HomePath("skills"), "home"},
		{paths.WorkspacePath("skills"), "workplace"},
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
			body, _ := os.ReadFile(filepath.Join(base.root, ent.Name()))
			seen[name] = Skill{
				Name:   name,
				Path:   filepath.Join(base.root, ent.Name()),
				Source: base.source,
				Body:   string(body),
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

func ensureHome(paths Paths) error {
	for _, dir := range []string{
		paths.HomeDir,
		paths.HomePath(RootAgentKind),
		paths.HomePath(SubAgentKind),
		paths.HomePath(SpecialAgentKind),
		paths.HomePath("skills"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := writeTOMLIfMissing(paths.HomePath("api_config.toml"), defaultAPIConfig()); err != nil {
		return err
	}
	if err := writeTOMLIfMissing(paths.HomePath(RootAgentKind, "default.toml"), defaultAgentConfig("default")); err != nil {
		return err
	}
	if err := writeTOMLIfMissing(paths.HomePath(SubAgentKind, "default.toml"), defaultAgentConfig("default")); err != nil {
		return err
	}
	return writeTOMLIfMissing(paths.HomePath(SpecialAgentKind, "default.toml"), defaultAgentConfig("default"))
}

func ensureWorkspace(paths Paths) error {
	if err := os.MkdirAll(paths.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.WorkspaceSessionsDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.WorkspacePath("skills"), 0o755); err != nil {
		return err
	}
	for _, rel := range []string{"api_config.toml", RootAgentKind, SubAgentKind, SpecialAgentKind} {
		dst := paths.WorkspacePath(rel)
		if exists(dst) {
			continue
		}
		src := paths.HomePath(rel)
		if err := copyPath(src, dst); err != nil {
			return err
		}
	}
	if err := ensureGit(paths.Workplace); err != nil {
		return err
	}
	return ensureGitIgnore(paths.Workplace)
}

func ensureGit(workplace string) error {
	if exists(filepath.Join(workplace, ".git")) {
		return nil
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = workplace
	return cmd.Run()
}

func ensureGitIgnore(workplace string) error {
	path := filepath.Join(workplace, ".gitignore")
	var content string
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "Asayn/" {
			return nil
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "Asayn/\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func defaultAPIConfig() APIConfig {
	return APIConfig{
		BaseURL:          "https://api.deepseek.com",
		APIKey:          "env:DEEPSEEK_API_KEY",
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "max",
		ThinkingEnabled: true,
		TimeoutSeconds:  120,
		Headers:         map[string]string{},
	}
}

func defaultAgentConfig(name string) AgentConfig {
	return AgentConfig{
		Name:           name,
		SystemPrompt:   "You are a helpful assistant.",
		VisibleSkills:  []string{},
		MaxOutputChars: 5000,
	}
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

func writeTOMLIfMissing(path string, value any) error {
	if exists(path) {
		return nil
	}
	data, err := toml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
