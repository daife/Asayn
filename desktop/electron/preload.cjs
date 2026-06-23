const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("asaynDesktop", {
  platform: "electron",
  startBridge: (workspace) => ipcRenderer.invoke("bridge:start", workspace),
  bridgeRequest: (request) => ipcRenderer.invoke("bridge:request", request),
  openDirectory: (title) => ipcRenderer.invoke("dialog:open-directory", title),
  minimize: () => ipcRenderer.invoke("window:minimize"),
  toggleMaximize: () => ipcRenderer.invoke("window:toggle-maximize"),
  close: () => ipcRenderer.invoke("window:close"),
  onBridgeEvent: (callback) => {
    const listener = (_event, payload) => callback(payload);
    ipcRenderer.on("bridge-event", listener);
    return () => ipcRenderer.removeListener("bridge-event", listener);
  },
});
