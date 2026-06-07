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

## Quick Install (Recommended)

We provide simple installation scripts for both Linux and Windows that automatically download and install the latest release.

### Linux/macOS

```bash
curl -sSL https://raw.githubusercontent.com/daife/Asayn/main/install.sh | bash
```

Or download and run manually:

```bash
wget https://raw.githubusercontent.com/daife/Asayn/main/install.sh
chmod +x install.sh
./install.sh
```

### Windows (PowerShell)

Open PowerShell as Administrator and run:

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/daife/Asayn/main/install.ps1" -OutFile install.ps1
.\install.ps1
```

Or use the batch script:

```cmd
curl -o install.bat https://raw.githubusercontent.com/daife/Asayn/main/install.bat
install.bat
```

The scripts will:
1. Check for existing `~/.Asayn` folder and ask if you want to clean it
2. Download the latest release for your platform
3. Install to `~/.local/bin` (Linux) or `%USERPROFILE%\.local\bin` (Windows)
4. Add the install directory to your PATH
5. Show you where to edit the configuration file

