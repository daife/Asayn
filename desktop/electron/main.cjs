const { app, BrowserWindow, dialog, ipcMain } = require("electron");
const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const readline = require("node:readline");

const isWindows = process.platform === "win32";
const bridgeName = isWindows ? "asayn-bridge.exe" : "asayn-bridge";
let mainWindow;
let bridgeChild;
let bridgeStdin;

function firstExisting(paths) {
  return paths.find((item) => item && fs.existsSync(item));
}

function repoRoot() {
  return path.resolve(__dirname, "..", "..");
}

function resolveBridgeBinary() {
  if (process.env.ASAYN_BRIDGE_BIN) return process.env.ASAYN_BRIDGE_BIN;
  return firstExisting([
    path.join(process.resourcesPath || "", bridgeName),
    path.join(process.resourcesPath || "", "electron-binaries", bridgeName),
    path.join(app.getAppPath(), "electron-binaries", bridgeName),
    path.join(repoRoot(), "desktop", "electron-binaries", bridgeName),
    path.join(repoRoot(), bridgeName),
  ]);
}

function sendToRenderer(channel, payload) {
  if (!mainWindow || mainWindow.isDestroyed()) return;
  mainWindow.webContents.send(channel, payload);
}

function startBridge(workspace = "") {
  if (bridgeChild) return;
  const binary = resolveBridgeBinary();
  const command = binary || "go";
  const args = binary ? [] : ["run", "./cmd/asayn-bridge"];
  const cwd = workspace && workspace.trim() ? workspace : repoRoot();
  bridgeChild = spawn(command, args, {
    cwd,
    stdio: ["pipe", "pipe", "pipe"],
    windowsHide: true,
    env: { ...process.env },
  });
  bridgeStdin = bridgeChild.stdin;

  readline.createInterface({ input: bridgeChild.stdout }).on("line", (line) => {
    try { sendToRenderer("bridge-event", JSON.parse(line)); }
    catch { /* The bridge protocol is JSON lines; ignore non-JSON noise. */ }
  });
  readline.createInterface({ input: bridgeChild.stderr }).on("line", (line) => {
    sendToRenderer("bridge-error", line);
  });
  bridgeChild.once("exit", (code, signal) => {
    sendToRenderer("bridge-error", `bridge exited${code === null ? "" : ` with code ${code}`}${signal ? ` (${signal})` : ""}`);
    bridgeChild = undefined;
    bridgeStdin = undefined;
  });
  bridgeChild.once("error", (error) => {
    sendToRenderer("bridge-error", `start Go bridge: ${error.message}`);
    bridgeChild = undefined;
    bridgeStdin = undefined;
  });
}

function stopBridge() {
  if (!bridgeChild) return;
  bridgeChild.kill();
  bridgeChild = undefined;
  bridgeStdin = undefined;
}

function createWindow() {
  mainWindow = new BrowserWindow({
    title: "Asayn",
    width: 1360,
    height: 860,
    minWidth: 940,
    minHeight: 640,
    frame: false,
    transparent: false,
    backgroundColor: "#070b12",
    show: false,
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });
  mainWindow.once("ready-to-show", () => mainWindow.show());
  mainWindow.on("closed", () => {
    mainWindow = undefined;
    stopBridge();
  });

  const devUrl = process.env.ELECTRON_RENDERER_URL || process.env.VITE_DEV_SERVER_URL;
  if (devUrl) mainWindow.loadURL(devUrl);
  else mainWindow.loadFile(path.join(__dirname, "..", "dist", "index.html"));
}

app.whenReady().then(createWindow);
app.on("window-all-closed", () => {
  stopBridge();
  if (process.platform !== "darwin") app.quit();
});
app.on("before-quit", stopBridge);
app.on("activate", () => {
  if (BrowserWindow.getAllWindows().length === 0) createWindow();
});

ipcMain.handle("bridge:start", (_event, workspace) => startBridge(workspace));
ipcMain.handle("bridge:request", (_event, request) => {
  if (!bridgeStdin) throw new Error("bridge is not running");
  bridgeStdin.write(`${JSON.stringify(request)}\n`);
});
ipcMain.handle("dialog:open-directory", async (_event, title) => {
  const result = await dialog.showOpenDialog(mainWindow, {
    title,
    properties: ["openDirectory"],
  });
  return result.canceled ? undefined : result.filePaths[0];
});
ipcMain.handle("window:minimize", () => mainWindow?.minimize());
ipcMain.handle("window:toggle-maximize", () => {
  if (!mainWindow) return;
  if (mainWindow.isMaximized()) mainWindow.unmaximize();
  else mainWindow.maximize();
});
ipcMain.handle("window:close", () => mainWindow?.close());
