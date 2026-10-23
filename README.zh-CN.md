# Asayn

<p align="center">
  <img src="assets/banner.png" alt="Asayn" width="100%"/>
</p>

中文 | [English](README.md)

**Asayn = agent skills are all you need.**

Asayn 是一个受 Claude Code 启发的 Go 终端智能体。它提供 Bubble Tea TUI、兼容 OpenAI Chat Completions 的模型接入、按工作区保存的会话、可配置的 root/sub agents、skills、MCP 工具、shell 执行和自动上下文压缩。

## 目录

- [安装](#安装)
- [快速开始](#快速开始)
- [目录与文件](#目录与文件)
- [配置](#配置)
- [命令](#命令)
- [工具能力](#工具能力)
- [从源码构建](#从源码构建)

## 安装

### 桌面 GUI

从 [GitHub Releases](https://github.com/daife/Asayn/releases/latest) 下载最新桌面安装包。GUI 已内置本地 Go Agent 引擎，不需要另外安装 CLI。

Asayn 发布两种桌面版本：

- **Tauri 桌面版**：默认推荐，安装包更小。它使用系统自带的 WebView/WebKit runtime，适合大多数用户。
- **Electron 桌面版**：文件名为 `Asayn-Electron-*` 的兜底版本。它内置 Chromium，因此体积更大，但可以避免部分设备缺少 WebView runtime、或不同 WebView 版本导致的不可预测问题。

如果 Tauri 版本出现黑屏、WebView 渲染异常，或者目标设备没有可靠的 WebView runtime，请改用 Electron 版本。

#### Windows x64

下载并运行以下任一文件：

- `Asayn_*_x64-setup.exe`：推荐使用的 NSIS 安装程序。
- `Asayn_*_x64_en-US.msi`：适合统一部署或手动安装的 MSI 安装包。
- `Asayn-Electron-*-windows-x64.exe` / `.msi`：Electron 兜底安装包，适合 WebView 有问题的机器。

安装后从开始菜单启动 **Asayn**，点击 **Open workspace** 选择项目目录。首次启动时，GUI 会和 CLI 一样创建 `~/.Asayn/` 全局默认配置以及工作区 `.Asayn/` 状态。需要在 `~/.Asayn/api_config.toml` 中配置 Provider 和 API key；也可以通过 GUI 的 Agent 设置打开该配置文件。设置面板中也提供和一键安装脚本一致的 Claude Code skills 与 MCP 迁移入口。

#### Linux x64

从最新 Release 中选择 Tauri 版 DEB 或 AppImage：

```bash
# Debian / Ubuntu
sudo apt install ./Asayn_*_amd64.deb

# 便携 AppImage
chmod +x Asayn_*_amd64.AppImage
./Asayn_*_amd64.AppImage
```

Electron 兜底包也会同时发布：`Asayn-Electron-*-linux-x86_64.AppImage` 和 `Asayn-Electron-*-linux-amd64.deb`。当系统 WebKit/WebView 缺失或表现不稳定时，请使用 Electron 版本。

桌面版目前同时提供 Tauri 与 Electron 的 Windows x64、Linux x64 安装包。macOS 用户可以使用 CLI release asset，或从源码构建桌面应用。

GUI 与 CLI 共用 `~/.Asayn/` 下的配置，包括 Providers、Agents、Skills、MCP servers、用量数据和工作区会话索引。

### 终端 CLI

#### Linux

```bash
curl -sSL https://raw.githubusercontent.com/daife/Asayn/main/install.sh | bash
```

Linux 安装脚本会下载最新 GitHub release，把 `asayn` 安装到 `~/.local/bin`，必要时更新 shell PATH，并可选择迁移 Claude Code 的 skills 和 MCP server 配置。

#### Windows PowerShell

```powershell
Invoke-WebRequest -Uri "https://raw.githubusercontent.com/daife/Asayn/main/install.ps1" -OutFile install.ps1 && .\install.ps1
```

也可以使用 batch 包装脚本：

```cmd
curl -o install.bat https://raw.githubusercontent.com/daife/Asayn/main/install.bat && install.bat
```

Windows 安装脚本会把 `asayn.exe` 安装到 `%USERPROFILE%\.local\bin`，更新用户 PATH，并可执行同样的 Claude Code 迁移流程。目前 release 提供 Windows amd64 二进制文件。

#### macOS

Release 中包含 `asayn-darwin-amd64` 和 `asayn-darwin-arm64`。当前 `install.sh` 实际下载 Linux 产物，因此 macOS 请手动下载对应 release asset，或从源码构建。

#### Claude Code 迁移

Linux 和 Windows 安装脚本可以扫描常见 Claude Code 位置中的 skills 和 MCP server 配置。脚本会把发现的每个 skill 或 MCP server 显示为独立编号；重复项会标记并跳过。选中的 skills 会复制到 `~/.Asayn/skills/`，选中的 MCP servers 会写成 `~/.Asayn/mcp/` 下的独立 JSON 文件。

## 快速开始

使用 GUI 时，启动 **Asayn**，点击 **Open workspace**，然后选择需要 Agent 操作的项目目录。

使用终端客户端时：

```bash
cd /path/to/your/project
asayn
```

首次运行时，Asayn 会在 `~/.Asayn/` 创建全局默认配置，并在 `<project>/.Asayn/` 创建工作区状态。先编辑 `~/.Asayn/api_config.toml`，填入所用 provider 的 API key，然后在 TUI 中开始使用。

## 目录与文件

全局配置：

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

工作区状态：

```text
<workspace>/.Asayn/
  .sessions/
    root_agents/
    sub_agents/
    special_agents/
```

Asayn 会把仓库中的 `default_Asayn/` 嵌入二进制文件。启动时，缺失的默认文件会复制到 `~/.Asayn/`，但不会覆盖已有文件。工作区 `.Asayn/` 用于保存会话；如果工作区已有 `.gitignore`，Asayn 会在其中追加 `.Asayn/`，已存在则不会重复添加。

## 配置

### API Provider

编辑 `~/.Asayn/api_config.toml`：

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

内置默认配置包含 DeepSeek、SiliconFlow 和 XiaomiMIMO 示例。每个 Provider 可通过 `api_type = "chat_completions"`（默认，兼容旧配置）或 `api_type = "responses"` 选择协议。URL 会按协议追加 `/chat/completions` 或 `/responses`，也接受完整接口地址；需要 `/v1` 的服务商请在 URL 中保留 `/v1`。

```toml
[providers.OpenAI]
url = "https://api.openai.com/v1"
api_key = "your_api_key"
api_type = "responses"
prompt_cache_key = "asayn"
timeout_seconds = 120
allowed_models = ["your-model"]
```

将 agent 的 `provider` 和 `model` 设置为对应服务商和实际模型即可，主 agent、子 agent 和压缩 agent 均支持两种协议。每个持久会话独立保存 `cache_session_id` 和 `prompt_cache_key`。Agent 请求使用会话自己的缓存 key（覆盖 Provider 固定值），Responses 请求同时发送稳定的 `session-id` Header。恢复或重试复用原标识；旧会话从已有 UUID 补齐。新会话、持久 fork 和独立子 agent 使用不同标识；压缩临时副本继承原会话标识。无会话的独立客户端仍使用 Provider 的 `prompt_cache_key`，Responses 未配置时回退到 `user`；Chat Completions 的 `user` 仍原样发送。订阅 OAuth 继续使用 `https://api.openai.com/v1/responses`，保持 `store: false` 和流式请求，与 Codex ChatGPT 后端区分。缓存命中仍取决于服务商支持、缓存有效期及相同提示前缀；稳定路由不保证缓存命中或物理缓存隔离。

Responses 使用 `store = false` 和独立的本地会话历史，保存并回传加密推理项以支持后续工具调用及恢复会话，不使用 `previous_response_id`。`thinking_enabled = true` 时发送 `reasoning.effort`，其中 `max` 映射为 `xhigh`；关闭时省略推理参数，由模型决定默认行为。参数支持情况取决于模型和兼容服务商。

### Agent

Agent 配置位置：

```text
~/.Asayn/root_agents/*.toml
~/.Asayn/sub_agents/*.toml
~/.Asayn/special_agents/*.toml
<workspace>/.Asayn/root_agents/*.toml
<workspace>/.Asayn/sub_agents/*.toml
<workspace>/.Asayn/special_agents/*.toml
```

Root agent 负责主会话。Sub-agent 用基础工具集执行委派任务。`special_agents/compact_agent.toml` 用于 `/compact` 和自动上下文压缩。

同名时，工作区 agent 配置优先于全局配置。如果 `/model_config` 保存的配置在工作区中尚不存在，则会写入全局配置位置。

常用字段包括 `provider`、`model`、`system_prompt`、`visible_skills`、`visible_mcp`、`max_output_lines`、`allow_parallel_shell`、`allow_interactive_shell`、`thinking_enabled`、`reasoning_effort` 和 `auto_compact_threshold_percent`。

### Skills

Skill 是包含 `SKILL.md` 的目录包：

```text
~/.Asayn/skills/<skill-name>/SKILL.md
<workspace>/.Asayn/skills/<skill-name>/SKILL.md
```

同名时，工作区 skill 会覆盖全局 skill。使用 `/model_config` 为每个 agent 选择可见 skills。

### MCP

MCP server 配置是 `~/.Asayn/mcp/` 或 `<workspace>/.Asayn/mcp/` 下的 JSON 文件。同名时，工作区 MCP 配置会覆盖全局配置。可在 `/model_config` 中按 agent 启用可见 MCP。

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

支持的 transport 名称包括 `stdio`、`streamable_http`，以及作为 streamable HTTP 别名的 `http`。

## 命令

在 TUI 中输入 `/` 可查看模糊匹配的命令建议。`Tab` 补全选中的命令。没有命令建议时，上下方向键用于浏览输入历史。

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示帮助 |
| `/new [name]` | 新建会话 |
| `/resume [session]` | 选择或恢复已保存会话 |
| `/retry` | 重试上一条用户请求 |
| `/rename [name]` | 重命名当前会话 |
| `/fork [name]` | 从当前点复制会话 |
| `/root_agent [name]` | 选择或设置 root agent |
| `/model [name]` | `/root_agent` 的别名 |
| `/model_config` | 配置模型、thinking、shell、skills、MCP |
| `/compact` | 使用 `compact_agent` 压缩上下文 |
| `/btw <question>` | 预留命令，当前未实现 |
| `/exit` | 退出 CLI |

Agent 正在工作时，按 Enter 会在当前批工具调用全部完成后插入输入的消息（如果没有工具调用，则在当前模型响应结束后插入）；按 Tab 会将消息排队到当前完整轮次结束后发送。按 Esc 会取消最后一条队列消息；如果队列为空，则中断当前回合。

TUI 中按 Enter 后会等待 0.5 秒再发送。若回车前后 0.5 秒内还有其他回车，或等待期间继续编辑，则取消待发送操作，避免终端把粘贴换行误识别为发送。粘贴结束后可检查输入，再按 Enter 发送。

在 Windows 上，`/model_config` 和 GUI 的 Agent 设置中会为 root agent 提供 **Git Bash** shell 选项。强烈建议开启 Git Bash——模型通常比 PowerShell 更擅长使用 bash shell 命令。开启时，Asayn 会检查 Git Bash 是否安装在默认路径 `C:\Program Files\Git\bin\bash.exe`；如果未找到，会拒绝开启并提示从 <https://git-scm.com/download/win> 使用默认设置安装 Git for Windows。开启成功后，暴露给模型的 shell 工具会描述为 Git Bash command，并通过 Git Bash 执行，而不是 PowerShell。

## 工具能力

内置 agent 工具包括：

- 工作区文件读取、目录浏览和正则搜索。
- `view_image`：读取工作区内的 PNG、JPEG、GIF、WebP 图片（单张最大 20 MiB），支持 `detail` 参数。
- 读取可见 skills。
- 同步 shell 命令。
- 在 agent 配置中启用后，可使用异步和交互式 shell 命令。
- Sub-agent 列表、异步启动、检查、恢复和 delay。
- 从已启用 MCP server 中发现的可见 MCP 工具。

Sub-agent 使用基础 executor：文件/搜索/skill/同步 shell 以及可见 MCP，不包含 root agent 的 sub-agent 编排工具。

可以直接告诉 agent「查看 `screenshots/page.png`，分析界面问题」，模型会调用 `view_image({"path":"screenshots/page.png","detail":"auto"})`。`detail` 可选 `auto`（默认）、`low`、`high`、`original`；具体支持取决于模型。图片按实际内容识别格式，路径不能通过 `..` 或符号链接逃出工作区。CLI、桌面端和 sub-agent 共用此工具。

图片由当前模型直接理解，需配置支持视觉输入的模型，例如 [DeepSeek 视觉文档](https://api-docs.deepseek.com/zh-cn/guides/vision/) 中的模型。Chat Completions 使用 `image_url` 内容块，Responses 使用 [`input_image`](https://developers.openai.com/api/docs/guides/images-vision?api-mode=responses)。[ChatGPT 订阅模式](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference) 沿用现有 OAuth 认证和 Responses 端点，保持 `store=false`、`stream=true`，无需额外 API Key；图像格式与 Responses 相同，账号及模型仍需支持视觉输入。

图像数据随工具结果保存在本地会话，恢复会话时无需重新读取原文件；界面只显示文件信息。启用实时上下文控制时，旧图像与对应工具结果一起隐藏。图片会增加会话体积及模型上下文消耗。

## 从源码构建

### 桌面应用（Tauri 2 与 Electron）

可选桌面客户端位于 `desktop/`。界面使用 React 和 TypeScript，Agent 引擎仍由打包后的 Go sidecar 提供，因此 CLI 与桌面端共用会话、工具、Skills、MCP 和模型配置实现。

Tauri 与 Electron 版本共用同一套 React UI，只是桌面 runtime 与打包方式不同。

```bash
cd desktop
npm install
npm run tauri dev
```

构建 Tauri 桌面安装包：

```bash
cd desktop
npm run tauri build
```

构建 Electron 兜底安装包：

```bash
cd desktop
npm run build:electron -- --win nsis msi --x64
npm run build:electron -- --linux AppImage deb --x64
```

平台依赖及发布构建方式见 `desktop/README.md`。

### 终端应用

要求：

- Go 1.24 或更新版本。
- Bubble Tea 支持的终端。
- Linux、macOS 或 Windows。其他平台需要设置 `ASAYN_ALLOW_NON_LINUX=1`。

构建：

```bash
go build -o asayn ./cmd/asayn
```

更小的 release 风格二进制文件：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o asayn ./cmd/asayn
```

交叉编译示例：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" -o asayn-linux-amd64 ./cmd/asayn
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -buildid=" -o asayn-darwin-arm64 ./cmd/asayn
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" -o asayn-windows-amd64.exe ./cmd/asayn
```

运行测试：

```bash
go test ./...
```

### 使用 ChatGPT 订阅额度（订阅 Responses）

新增第三种协议 `subscription_responses`，通过官方 Sign in with ChatGPT 授权使用符合条件的 ChatGPT 订阅额度。先在本机终端执行：

```sh
asayn subscription login personal
asayn subscription models personal
```

登录命令会打开系统浏览器（Continue with ChatGPT），等待本机回调，验证 ID Token 签名、issuer、audience、有效期、nonce 和授权 scope。`personal` 是本地账号标签；省略时为 `default`。不同账号或工作区请使用不同标签，重新登录同一标签会复用已签发的 client ID 和本机 host ID，并检查账号身份一致。

在 `~/.Asayn/api_config.toml` 中添加：

```toml
[providers.ChatGPT]
api_type = "subscription_responses"
subscription_account = "personal"
timeout_seconds = 120
allowed_models = ["替换为 models 命令返回的 slug"]
```

然后把主 agent、子 agent 或压缩 agent 的 `provider` 设置为 `ChatGPT`，`model` 设置为实际 slug。桌面版也读取同一配置和凭据：先用 CLI 登录，再在桌面版中选择对应的 Provider/模型。`models` 命令使用所选账号的实时模型列表，保留官方显示顺序；换账号后重新查询并更新 `allowed_models`。

不要配置 `api_key`。URL 可省略，订阅请求固定使用公开的 `https://api.openai.com/v1/responses`。与普通 Responses 的差异：强制 `store=false`、`stream=true`，系统提示转为 developer 消息，每次发送完整本地历史，不使用 `previous_response_id` 或不支持的采样字段。内部非流式调用也通过流式请求聚合结果，仅收到 `response.completed` 后返回成功；订阅额度错误、未完成和中断均返回错误，不会自动切换 API 付费。

凭据保存在 `~/.Asayn/subscription/<标签>.json`（0600，目录 0700），不会写入 API 配置或浏览器存储。请勿提交或分享该目录。接近过期时自动刷新并保存新的 refresh token；跨进程锁防止并发刷新。暂时性刷新失败保留凭据，确认 refresh token 不可用后清除 token 并要求重新登录。Windows 请确保用户目录 ACL 仅允许本人访问。

```sh
asayn subscription logout personal
```

退出会尝试撤销远端可续期会话并清除本地 token，保留注册信息以便重新登录；远端撤销未确认时会明确提示，可在 ChatGPT Settings 中断开应用。浏览器和 CLI 需运行在同一台机器；远程 VM 请按官方自托管指引转移受保护凭据，并保留 VM 自己的 host ID。

这项能力目前受官方预览限制、账号/工作区资格、地区和实际授权范围约束；Plus 的五小时额度在使用订阅的应用之间共享，不是每个应用单独一份。可在 ChatGPT Settings → Usage 查看额度和管理授权。

官方文档：[登录授权](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)、[会话与刷新](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions)、[模型调用](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)、[预览限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)。
