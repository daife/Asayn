# Asayn

Asayn means **agent skills are all you need**. It is a Go + Bubble Tea TUI agent scaffold inspired by Claude Code, designed to run on Ubuntu/Linux.

## What This MVP Includes

- Workplaces: running `asayn` in any directory treats that directory as the workplace.
- Global config: `~/.Asayn/`.
- Workplace config: `./.Asayn/`.
- First-run setup: creates `.Asayn/` and `.Asayn/.sessions/`. If the workplace already has `.gitignore`, Asayn adds `.Asayn/` once; it does not run `git init` or create `.gitignore`.
- Config precedence: workplace files win over global files (except `api_config.toml` which is global-only). Skills are visible from both `./.Asayn/skills` when that folder exists and `~/.Asayn/skills`; duplicate skill names prefer the workplace skill. Workspace setup does not copy global skills.
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
default_Asayn/        # repository defaults embedded into the binary
  api_config.toml
  root_agents/
  sub_agents/
  special_agents/
  skills/

~/.Asayn/
  api_config.toml
  root_agents/
    default.toml
  sub_agents/
    default.toml
  special_agents/
    default.toml
  skills/
    example-skill/
      SKILL.md

<workplace>/.Asayn/
  root_agents/
  sub_agents/
  special_agents/
  skills/        # optional local skill directories; not created or copied by default
  .sessions/
    root_agents/
    sub_agents/
    special_agents/
```

On first use, Asayn copies missing files from the defaults embedded in the executable into `~/.Asayn/`. Existing user files are not overwritten.

## Binary Distribution

Asayn is distributed as a single executable file. No repository checkout or `default_Asayn/` directory is needed at runtime because the default config files are embedded into the binary.

If you receive an executable named `asayn`, install it somewhere on your `PATH`:

```bash
chmod +x asayn
install -D -m 0755 asayn ~/.local/bin/asayn
```

Make sure `~/.local/bin` is on your `PATH`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Configure your API key before first use:

```bash
export DEEPSEEK_API_KEY="your-api-key"
```

Then run `asayn` from any project directory:

```bash
cd /path/to/your/project
asayn
```

The first run creates `~/.Asayn/` with global defaults and creates `<project>/.Asayn/` for the current workplace. `api_config.toml` is stored exclusively in `~/.Asayn/` and is never copied to or read from the project directory.

## Build From Source

```bash
go mod tidy
go build -o asayn ./cmd/asayn
```

Run the built binary from any project directory:

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
[providers.SiliconFlow]
url = "https://api.siliconflow.cn/v1"
api_key = "your_api_key"
timeout_seconds = 120
allowed_models = [
  "deepseek-ai/DeepSeek-V4-Flash",
  "deepseek-ai/DeepSeek-V4-Pro",
  "nex-agi/Nex-N2-Pro"
]

[providers.DeepSeek]
url = "https://api.deepseek.com"
api_key = "your_api_key"
timeout_seconds = 120
allowed_models = [
  "deepseek-v4-pro",
  "deepseek-v4-flash"
]
```

## Agent Config

`root_agents/default.toml` defaults to:

```toml
name = "default"
provider = "DeepSeek"
model = "deepseek-v4-pro"
description = "General-purpose agent."
system_prompt = "You are a highly competent agent."
visible_skills = []
max_output_lines = 2000
context_window = 1024000
max_output_tokens = 384000
thinking_enabled = false
reasoning_effort = "max"
allow_parallel_shell = false
allow_interactive_shell = false
```

## Slash Commands

- `/help`
- `/new [name]`
- `/resume [session]`
- `/rename [name]`
- `/fork [name]`
- `/root_agent [name]` (alias: `/model`)
- `/model_config`
- `/compact [instructions]` reserved
- `/btw <question>` reserved
- `/exit`

## Sub-Agents

Root agents can list, start, check, wait-check, and resume sub-agents through the fixed `sub_agent_*` tools. `sub_agent_list` lists configured `sub_agents/*.toml` names and descriptions. `sub_agent_start_async` accepts an `agent` name from that list; workplace configs override global configs. A sub-agent keeps its own session history and cannot see `sub_agent_*`, `diff_file`, or shell tools. That keeps delegated work read-only by default, so a sub-agent can research and report without modifying the root agent's files.

## Skills

Skills are directory-based packages. Asayn discovers only directories that contain `SKILL.md`:

```text
~/.Asayn/skills/<skill-name>/SKILL.md
<workplace>/.Asayn/skills/<skill-name>/SKILL.md
```

`SKILL.md` should include YAML frontmatter metadata, commonly `name` and `description`, followed by Markdown instructions. Visible skill metadata and source are exposed to the model; the full `SKILL.md` body is loaded only through the `read_skill` tool after the skill is enabled with `/model_config` or listed in the active agent config.

## Diff Tool

`diff_file` records reversible file changes and returns a verification diff:

- `mode="replace"` edits a localized exact `old_text` block into `new_text`, records a change ID, and returns the resulting diff. This is preferred for multi-line edits because it does not rely on line numbers.
- Add `dry_run=true` to `replace`, `write`, `delete`, or `apply` to preview without writing.
- `mode="apply"` with `unified_diff` or `patches` applies one or more unified diff file patches and records change IDs. Patch paths come from diff headers; headerless patches use `path` as the fallback. If both are provided and disagree, the tool errors.
- `mode="history"` lists recorded changes, optionally filtered by `path`. With `change_id` or `change_ids`, it shows the recorded diff for those changes.
- `mode="revert"` reverts one recorded change.
- `mode="revert_many"` reverts multiple changes in reverse order by default; set `reverse=false` to use the provided order.

Full-file `write` is still available for new or small files. Unified diffs remain useful when context is certain; `replace` is safer when editing JSON tails, comma-sensitive blocks, or other multi-line regions where line numbers are easy to get wrong.
