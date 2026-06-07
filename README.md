# Asayn

Asayn means **agent skills are all you need**. It is a Go + Bubble Tea TUI agent scaffold inspired by Claude Code, designed to run on Linux and Windows.

## Quick Install (Recommended)

### Linux/macOS
```bash
curl -sSL https://raw.githubusercontent.com/daife/Asayn/main/install.sh | bash
```

### Windows (PowerShell)
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

## Key Features

- **Workspaces**: Running `asayn` in any directory treats that directory as the workspace
- **Global config**: `~/.Asayn/` (API keys, agents, skills)
- **Workspace config**: `./.Asayn/` (project-specific settings)
- **OpenAI-compatible**: Works with DeepSeek, SiliconFlow, and other OpenAI-compatible APIs
- **Thinking mode**: Support for `reasoning_effort` and `thinking.type`
- **TUI**: Bubble Tea terminal interface with slash commands
- **Sub-agents**: Delegate tasks to specialized agents
- **Skills**: Directory-based skill packages

## Layout

```
~/.Asayn/
  api_config.toml
  root_agents/
  sub_agents/
  special_agents/
  skills/

<workspace>/.Asayn/
  root_agents/
  sub_agents/
  special_agents/
  skills/
```

## API Configuration

Edit `~/.Asayn/api_config.toml` to configure your API providers:

```toml
[providers.DeepSeek]
api_key = "your-api-key"
```

## Usage

```bash
cd /path/to/your/project
asayn
```

First run creates `~/.Asayn/` with global defaults and `<project>/.Asayn/` for the current workspace.

## Commands

- `/help` - Show help
- `/new [name]` - Start a new session
- `/resume [session]` - Resume a saved session
- `/retry` - Retry the last request
- `/rename` - Rename current session
- `/fork` - Fork from the current point
- `/root_agent` - Select root agent
- `/model` - Select root agent (alias for /root_agent)
- `/model_config` - Configure agent settings
- `/compact` - Compress conversation context
- `/btw` - Reserved side-channel question
- `/exit` - Exit CLI

## Build From Source

```bash
go build -o asayn ./cmd/asayn
```

For Windows:
```bash
GOOS=windows GOARCH=amd64 go build -o asayn.exe ./cmd/asayn
```
