import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "../..");
const outputDir = path.join(root, "desktop", "electron-binaries");
mkdirSync(outputDir, { recursive: true });

const name = process.platform === "win32" ? "asayn-bridge.exe" : "asayn-bridge";
const output = path.join(outputDir, name);
const result = spawnSync("go", [
  "build",
  "-trimpath",
  "-ldflags=-s -w -buildid=",
  "-o",
  output,
  "./cmd/asayn-bridge",
], {
  cwd: root,
  stdio: "inherit",
  env: { ...process.env, CGO_ENABLED: "0" },
});

if (result.error) {
  console.error(`Failed to run go build for Electron sidecar: ${result.error.message}`);
  process.exit(1);
}
if (result.status !== 0) process.exit(result.status || 1);
