# Asayn

Asayn means **agent skills are all you need**. It is a Go + Bubble Tea TUI agent scaffold inspired by Claude Code, designed to run on Linux and Windows.

## What This MVP Includes

- Workspaces: running `asayn` in any directory treats that directory as the workspace.
- Global config: `~/.Asayn/`.
- Workspace config: `./.Asayn/`.
- First-run setup: creates `.Asayn/` and `.Asayn/.sessions/`. If the workspace already has `.gitignore`, Asayn adds `.Asayn/` once; it does not run `git init` or create `.gitignore`.
- Config precedence: workspace files win over global files (except `api_config.toml` which is global-only). Skills are visible from both `./.Asayn/skills` when that folder exists and `~/.Asayn/skills`; duplicate skill names prefer the workspace skill. Workspace setup does not copy global skills.
- OpenAI-compatible `/chat/completions` client with DeepSeek and SiliconFlow defaults.
- Thinking mode support with `reasoning_effort` and `thinking.type`.
- Session history and per-session file change chains.
- Bubble Tea TUI with slash commands and a chat input that soft-wraps upward to four rows.
- Built-in tool schemas only: file reading, grep, directory view, diff-based file changes, shell execution, and sub-agent delegation. Fixed tool usage rules live in each tool description sent to the API.
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
    compact_agent.toml
  skills/
    example-skill/
      SKILL.md

<workspace>/.Asayn/
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

The first run writes `~/.Asayn/api_config.toml`. Before sending your first model request, edit the provider entry there, or edit the embedded default before building your own binary:

```toml
[providers.DeepSeek]
api_key = "your-api-key"
```

Then run `asayn` from any project directory:

```bash
cd /path/to/your/project
asayn
```

The first run creates `~/.Asayn/` with global defaults and creates `<project>/.Asayn/` for the current workspace. `api_config.toml` is stored exclusively in `~/.Asayn/` and is never copied to or read from the project directory.

## Windows Installation

If you receive an executable named `asayn.exe`, place it in a directory on your `PATH`.

### Option 1: User-local install (recommended)

Create a local bin directory and add it to your user `PATH`:

```powershell
mkdir -Force $env:USERPROFILE\.local\bin
Move-Item asayn.exe $env:USERPROFILE\.local\bin\asayn.exe
```

Then add it to your `PATH` via **Settings → System → About → Advanced system settings → Environment Variables**, or run:

```powershell
[Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";$env:USERPROFILE\.local\bin", "User")
```

After restarting your terminal, `asayn` is available from any directory.

### Option 2: Current-directory use

Simply run `asayn.exe` from any project directory:

```powershell
.\asayn.exe
```

### First run

The first run writes `~/.Asayn/api_config.toml` (which resolves to `$env:USERPROFILE\.Asayn\api_config.toml` on Windows). Before sending your first model request, edit the provider entry:

```toml
[providers.DeepSeek]
api_key = "your-api-key"
```

The first run also creates `<project>\.Asayn\` for the current workspace. `api_config.toml` is stored exclusively in `~/.Asayn/` and is never copied to or read from the project directory.

> **Note:** Shell tools on Windows run commands through PowerShell. The terminal environment is included in tool descriptions so the model can choose the right command syntax.

## Build From Source

```bash
go mod tidy
go build -o asayn ./cmd/asayn
```

Run the built binary from any project directory:

```bash
./asayn
```

For local development on unsupported platforms:

```bash
ASAYN_ALLOW_NON_LINUX=1 go run ./cmd/asayn
```

To build a Windows executable from source:

```bash
GOOS=windows GOARCH=amd64 go build -o asayn.exe ./cmd/asayn
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

[providers.SiliconFlow.model_limits."nex-agi/Nex-N2-Pro"]
context_window = 384000
max_output_tokens = 32768

[providers.SiliconFlow.model_limits."deepseek-ai/DeepSeek-V4-Flash"]
context_window = 1024000
max_output_tokens = 384000

[providers.SiliconFlow.model_limits."deepseek-ai/DeepSeek-V4-Pro"]
context_window = 1024000
max_output_tokens = 384000

[providers.DeepSeek]
url = "https://api.deepseek.com"
api_key = "your_api_key"
timeout_seconds = 120
allowed_models = [
  "deepseek-v4-pro",
  "deepseek-v4-flash"
]

[providers.DeepSeek.model_limits.deepseek-v4-pro]
context_window = 1024000
max_output_tokens = 384000

[providers.DeepSeek.model_limits.deepseek-v4-flash]
context_window = 1024000
max_output_tokens = 384000
```

The optional `model_limits` table sets per-model context window and max output tokens. DeepSeek-class models default to 1 M context / 384 k output when not configured. The sidebar context-window bar reads these limits from the current provider/model combination in real time.

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
thinking_enabled = false
reasoning_effort = "max"
allow_parallel_shell = false
allow_interactive_shell = false
```

Shell tools use the platform terminal environment: Windows runs commands through PowerShell, while Linux and other non-Windows builds use `sh`. The active terminal environment is included in shell tool descriptions so the model can choose the right command syntax. Synchronous shell commands are non-interactive. When interactive shell is enabled, asynchronous PowerShell sessions omit `-NonInteractive` and expose stdin.

## Slash Commands

- `/help`
- `/new [name]`
- `/resume [session]`
- `/retry` retries the last saved user request, useful after an idle timeout.
- `/rename [name]`
- `/fork [name]`
- `/copy_answer` copies the latest assistant answer and writes Markdown/HTML preview files under `.Asayn/`.
- `/root_agent [name]` (alias: `/model`)
- `/model_config` opens the interactive model, thinking, shell, and skill picker for root, sub, and special agents.
- `/compact`
- `/btw <question>` reserved
- `/exit`

## Sub-Agents

Root agents can list, start, check, wait-check, and resume sub-agents through the fixed `sub_agent_*` tools. `sub_agent_list` lists configured `sub_agents/*.toml` names and descriptions. `sub_agent_start_async` accepts a `name` from that list; workspace configs override global configs. A sub-agent keeps its own session history and only sees the basic file, search, skill, and history tools; it cannot see `sub_agent_*` or shell tools.

## Context Compression

`/compact` runs `special_agents/compact_agent.toml` to summarize the current effective conversation into a compact continuation state. The compact agent receives detailed compression instructions as that temporary user turn, but the retained conversation replaces that long prompt with `Recall what we worked on before.` so later root-agent calls are not polluted by compression instructions. The visible transcript remains in the TUI, but subsequent model calls only send the root system prompt plus the new compact summary round and newer messages. Repeated compression only summarizes the current effective context; history hidden by an earlier compression is not sent to `compact_agent` again. Asayn also starts compression automatically when the latest context-window usage reaches 80%; if an agent turn is still running, it is canceled first and the compact agent summarizes the partial chain that had already been recorded. Compression token usage is logged against the same session. Special agents appear in `/model_config`; they use the same basic tool exposure as sub-agents and only see skills selected for that special agent.

## Skills

Skills are directory-based packages. Asayn discovers only directories that contain `SKILL.md`:

```text
~/.Asayn/skills/<skill-name>/SKILL.md
<workspace>/.Asayn/skills/<skill-name>/SKILL.md
```

`SKILL.md` must start with YAML frontmatter metadata, commonly `name` and `description`, followed by Markdown instructions. At startup and prompt refresh, Asayn exposes only each visible skill's folder and frontmatter metadata to the model; the full raw `SKILL.md` file is loaded only through the `skill_read` tool after the skill is enabled with `/model_config` or listed in the active agent config.

## File Edit Tool

`file_edit` records reversible file changes and returns a focused unified diff for the change:

- `mode="write"` creates or overwrites a file.
- `mode="delete_lines"`, `mode="insert"`, and `mode="replace_lines"` edit by 1-based line numbers.
- `mode="find_replace"` treats `old_text` as a `grep_search`-style regex and replaces it with `new_text`; use `replace_all=true` when multiple matches are intended.
- `mode="rollback"` restores one or more recorded changes and removes those change records from history.

Use `view_history` to list recent change summaries or view the recorded diff for `change_id` / `change_ids`. Use `file_read` for file contents.
