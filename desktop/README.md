# Asayn Desktop

Tauri 2 desktop client for the existing Go agent engine. React renders the UI; a newline-delimited JSON sidecar keeps sessions, tools, MCP, model access, sub-agents, and configuration on the canonical Go implementation.

## Development

Requirements: Go 1.24+, Node.js 20+, Rust stable, and the [Tauri 2 platform prerequisites](https://v2.tauri.app/start/prerequisites/).

```bash
cd desktop
npm install
npm run tauri dev
```

Build an installer and its platform-specific Go sidecar:

```bash
npm run tauri build
```

Set `ASAYN_BRIDGE_BIN=/absolute/path/to/asayn-bridge` to test a custom bridge binary. The desktop app uses the process working directory as its Asayn workspace, matching the CLI.
