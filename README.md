# Asayn

[中文](README.zh-CN.md) | English

**Asayn = agent skills are all you need.**

Asayn is a Go-based terminal agent inspired by Claude Code. It provides a Bubble Tea TUI, OpenAI-compatible model access, workspace-aware sessions, configurable root/sub agents, skill packages, MCP tools, shell execution, and automatic context compaction.

## Contents

- [Install](#install)
- [Quick Start](#quick-start)
- [What Asayn Creates](#what-asayn-creates)
- [Configuration](#configuration)
- [Commands](#commands)
- [Tools and Capabilities](#tools-and-capabilities)
- [Build From Source](#build-from-source)

## Install

### Linux

```bash
curl -sSL https://raw.githubusercontent.com/daife/Asayn/main/install.sh | bash
```

The Linux installer downloads the latest GitHub release, installs `asayn` to `~/.local/bin`, updates your shell PATH when needed, and can optionally migrate Claude Code skills and MCP server configs.

### Windows PowerShell

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/daife/Asayn/main/install.ps1" -OutFile install.ps1
.\install.ps1
```

Or use the batch wrapper:

```cmd
curl -o install.bat https://raw.githubusercontent.com/daife/Asayn/main/install.bat
install.bat
```

The Windows installer installs `asayn.exe` to `%USERPROFILE%\.local\bin`, updates the user PATH, and can run the same Claude Code migration flow. Current release assets provide Windows amd64 binaries.

### macOS

Release assets include `asayn-darwin-amd64` and `asayn-darwin-arm64`. The current `install.sh` downloads Linux assets, so on macOS use the release asset manually or build from source.

### Claude Code Migration

The Linux and Windows installers can scan common Claude Code locations for skills and MCP server configs. Each discovered skill or MCP server is shown as a numbered item; duplicates are marked and skipped. Selected skills are copied to `~/.Asayn/skills/`, and selected MCP servers are written as individual JSON files under `~/.Asayn/mcp/`.

## Quick Start

```bash
cd /path/to/your/project
asayn
```

On first run, Asayn creates global defaults under `~/.Asayn/` and workspace state under `<project>/.Asayn/`. Edit `~/.Asayn/api_config.toml`, set the API key for your provider, then start chatting in the TUI.

## What Asayn Creates

Global configuration:

```text
~/.Asayn/
  api_config.toml
  root_agents/
  sub_agents/
  special_agents/
  skills/
  mcp/
  usage.jsonl
```

Workspace state:

```text
<workspace>/.Asayn/
  .sessions/
    root_agents/
    sub_agents/
    special_agents/
```

Asayn embeds the `default_Asayn/` directory into the binary. On startup, missing default files are copied into `~/.Asayn/` without overwriting existing files. The workspace `.Asayn/` directory stores sessions; if the workspace already has a `.gitignore`, Asayn appends `.Asayn/` when it is not already listed.

## Configuration

### API Providers

Edit `~/.Asayn/api_config.toml`:

```toml
[providers.DeepSeek]
url = "https://api.deepseek.com"
api_key = "your_api_key"
timeout_seconds = 120
allowed_models = [
  "deepseek-v4-pro",
  "deepseek-v4-flash"
]
```

The bundled default config includes DeepSeek, SiliconFlow, and XiaomiMIMO examples. Provider URLs are treated as OpenAI-compatible Chat Completions endpoints; if the configured URL does not end with `/chat/completions`, Asayn appends it.

### Agents

Agent configs live in:

```text
~/.Asayn/root_agents/*.toml
~/.Asayn/sub_agents/*.toml
~/.Asayn/special_agents/*.toml
<workspace>/.Asayn/root_agents/*.toml
<workspace>/.Asayn/sub_agents/*.toml
<workspace>/.Asayn/special_agents/*.toml
```

Root agents drive the main conversation. Sub-agents run delegated tasks with a basic tool set. `special_agents/compact_agent.toml` is used by `/compact` and automatic compaction.

Workspace agent configs take precedence over global configs with the same name. If `/model_config` saves a config that does not already exist in the workspace, it writes to the global config location.

Important fields include `provider`, `model`, `system_prompt`, `visible_skills`, `visible_mcp`, `max_output_lines`, `allow_parallel_shell`, `allow_interactive_shell`, `thinking_enabled`, `reasoning_effort`, and `auto_compact_threshold_percent`.

### Skills

Skills are directory packages containing `SKILL.md`:

```text
~/.Asayn/skills/<skill-name>/SKILL.md
<workspace>/.Asayn/skills/<skill-name>/SKILL.md
```

Workspace skills override global skills with the same declared name. Use `/model_config` to choose which skills are visible to each agent.

### MCP

MCP server configs are JSON files under `~/.Asayn/mcp/` or `<workspace>/.Asayn/mcp/`. Workspace MCP entries override global entries with the same server name. Enable per-agent visibility from `/model_config`.

```json
{
  "mcpServers": {
    "codegraph": {
      "type": "stdio",
      "command": "codegraph",
      "args": ["serve", "--mcp"]
    },
    "remote-docs": {
      "type": "streamable_http",
      "url": "https://example.com/mcp",
      "headers": {
        "Authorization": "Bearer ${MCP_TOKEN}"
      }
    }
  }
}
```

Supported transport names are `stdio`, `streamable_http`, and `http` as an alias for streamable HTTP.

## Commands

Type `/` in the TUI to see fuzzy command suggestions. `Tab` completes a selected command. When no command suggestions are active, up/down browse input history.

| Command | Description |
| --- | --- |
| `/help` | Show help |
| `/new [name]` | Start a new session |
| `/resume [session]` | Pick or resume a saved session |
| `/retry` | Retry the last user request |
| `/rename [name]` | Rename the current session |
| `/fork [name]` | Fork from the current point |
| `/root_agent [name]` | Pick or set the root agent |
| `/model [name]` | Alias for `/root_agent` |
| `/model_config` | Configure model, thinking, shell, skills, and MCP |
| `/compact` | Compress prior context with `compact_agent` |
| `/btw <question>` | Reserved for future side-channel questions |
| `/exit` | Exit the CLI |

During a running agent turn, pressing Enter queues the typed message. Pressing Esc cancels the last queued message, or interrupts the current turn if the queue is empty.

## Tools and Capabilities

Built-in agent tools include:

- Workspace file reading, directory listing, and regex search.
- Visible skill reading.
- Synchronous shell commands.
- Optional asynchronous and interactive shell commands when enabled in agent config.
- Sub-agent listing, async start/check/resume, and delay.
- Visible MCP tools discovered from enabled MCP servers.

Sub-agents use a basic executor: file/search/skill/synchronous shell plus visible MCP, without root-agent sub-agent orchestration.

## Build From Source

Requirements:

- Go 1.24 or newer.
- A terminal supported by Bubble Tea.
- Linux, macOS, or Windows. Other platforms require `ASAYN_ALLOW_NON_LINUX=1`.

Build:

```bash
go build -o asayn ./cmd/asayn
```

Smaller release-style binary:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o asayn ./cmd/asayn
```

Cross-compile examples:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" -o asayn-linux-amd64 ./cmd/asayn
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -buildid=" -o asayn-darwin-arm64 ./cmd/asayn
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" -o asayn-windows-amd64.exe ./cmd/asayn
```

Run tests:

```bash
go test ./...
```
