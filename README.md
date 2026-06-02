# Asayn

Asayn means **agent skills are all you need**. It is a Go + Bubble Tea TUI agent scaffold inspired by Claude Code, designed to run on Ubuntu/Linux.

## What This MVP Includes

- Workplaces: running `asayn` in any directory treats that directory as the workplace.
- Global config: `~/.Asayn/`.
- Workplace config: `./Asayn/`.
- First-run setup: creates `Asayn/`, `Asayn/.sessions/`, initializes git when needed, and adds `Asayn/` to `.gitignore`.
- Config precedence: workplace files win over global files. Skills are visible from both `./Asayn/skills` and `~/.Asayn/skills`; duplicate skill names prefer the workplace skill.
- DeepSeek chat client using the OpenAI-compatible `/chat/completions` format.
- Thinking mode support with `reasoning_effort` and `thinking.type`.
- Session history and per-session file change chains.
- Bubble Tea TUI with slash commands.
- Built-in tool schemas only: file reading, grep, directory view, diff-based file changes, shell execution, and sub-agent delegation.
- Right sidebar sub-agent status; click a sub-agent row to print its current transcript/status.

DeepSeek reference behavior used here:

- Multi-round chat is stateless; every API call sends the accumulated `messages`.
- Thinking mode can use `reasoning_effort` plus `thinking`.
- When tool calls happen, assistant messages, including `reasoning_content`, are kept in the session history so they can be sent back on later turns.

## Layout

```text
~/.Asayn/
  api_config.toml
  root_agents/
    default.toml
  sub_agents/
    default.toml
  special_agents/
    default.toml
  skills/

<workplace>/Asayn/
  api_config.toml
  root_agents/
  sub_agents/
  special_agents/
  skills/
  .sessions/
```

## Build

```bash
go mod tidy
go build -o asayn ./cmd/asayn
```

Run from any project directory:

```bash
./asayn
```

For local development on non-Linux machines:

```bash
ASAYN_ALLOW_NON_LINUX=1 go run ./cmd/asayn
```

## API Config

`api_config.toml` defaults to:

```toml
url = "https://api.deepseek.com"
api_key = "env:DEEPSEEK_API_KEY"
model = "deepseek-v4-pro"
reasoning_effort = "max"
thinking_enabled = true
timeout_seconds = 120

[headers]
```

## Agent Config

`root_agents/default.toml` defaults to:

```toml
name = "default"
system_prompt = "You are a helpful assistant."
visible_skills = []
max_output_chars = 5000
allow_interactive_shell = false
```

## Slash Commands

- `/help`
- `/new [name]`
- `/resume [session]`
- `/rename [name]`
- `/fork [name]`
- `/root_agent [name]`
- `/skills`
- `/skills [name] on|off`
- `/compact [instructions]` reserved
- `/btw <question>` reserved
- `/exit`

## Sub-Agents

Root agents can start, inspect, continue, and stop sub-agents through the fixed `sub_agent_*` tools. A sub-agent uses `sub_agents/default.toml`, keeps its own session history, and cannot see `sub_agent_*`, `diff_file`, or shell tools. That keeps delegated work read-only by default, so a sub-agent can research and report without modifying the root agent's files.
