use std::{
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, ChildStdin, Command, Stdio},
    sync::Mutex,
};
use tauri::{AppHandle, Emitter, Manager, State};

#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x08000000;

#[cfg(target_os = "linux")]
#[link(name = "gbm")]
extern "C" { fn gbm_bo_create_with_modifiers2(); }

#[cfg(target_os = "linux")]
fn retain_gbm_link() {
    // Keep libgbm under --as-needed; Ubuntu's arm64 WebKitGTK references this
    // symbol without declaring GBM in its pkg-config link list.
    std::hint::black_box(gbm_bo_create_with_modifiers2 as *const ());
}

struct BridgeState {
    child: Mutex<Option<Child>>,
    stdin: Mutex<Option<ChildStdin>>,
}

#[tauri::command]
fn start_bridge(app: AppHandle, state: State<BridgeState>, workspace: String) -> Result<(), String> {
    if state.child.lock().map_err(|e| e.to_string())?.is_some() { return Ok(()); }
    let explicit = std::env::var("ASAYN_BRIDGE_BIN").ok();
    let name = if cfg!(windows) { "asayn-bridge.exe" } else { "asayn-bridge" };
    let beside_app = std::env::current_exe().ok().and_then(|p| p.parent().map(|p| p.join(name)));
    let bundled = app.path().resource_dir().ok().map(|p| p.join(name));
    let candidate = explicit.map(PathBuf::from)
        .or_else(|| beside_app.filter(|p| p.exists()))
        .or_else(|| bundled.filter(|p| p.exists()));
    let mut command = if let Some(binary) = candidate {
        Command::new(binary)
    } else {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..");
        let mut cmd = Command::new("go");
        cmd.args(["run", "./cmd/asayn-bridge"]).current_dir(root);
        cmd
    };
    command.stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped());
    #[cfg(windows)]
    command.creation_flags(CREATE_NO_WINDOW);
    if !workspace.trim().is_empty() { command.current_dir(&workspace); }
    let mut child = command.spawn().map_err(|e| format!("start Go bridge: {e}"))?;
    let stdin = child.stdin.take().ok_or("bridge stdin unavailable")?;
    let stdout = child.stdout.take().ok_or("bridge stdout unavailable")?;
    let stderr = child.stderr.take().ok_or("bridge stderr unavailable")?;
    let events = app.clone();
    std::thread::spawn(move || {
        for line in BufReader::new(stdout).lines().map_while(Result::ok) {
            if let Ok(value) = serde_json::from_str::<serde_json::Value>(&line) { let _ = events.emit("bridge-event", value); }
        }
    });
    let errors = app.clone();
    std::thread::spawn(move || {
        for line in BufReader::new(stderr).lines().map_while(Result::ok) { let _ = errors.emit("bridge-error", line); }
    });
    *state.stdin.lock().map_err(|e| e.to_string())? = Some(stdin);
    *state.child.lock().map_err(|e| e.to_string())? = Some(child);
    Ok(())
}

#[tauri::command]
fn bridge_request(state: State<BridgeState>, request: serde_json::Value) -> Result<(), String> {
    let mut guard = state.stdin.lock().map_err(|e| e.to_string())?;
    let stdin = guard.as_mut().ok_or("bridge is not running")?;
    serde_json::to_writer(&mut *stdin, &request).map_err(|e| e.to_string())?;
    stdin.write_all(b"\n").and_then(|_| stdin.flush()).map_err(|e| e.to_string())
}

pub fn run() {
    #[cfg(target_os = "linux")]
    retain_gbm_link();
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .manage(BridgeState { child: Mutex::new(None), stdin: Mutex::new(None) })
        .invoke_handler(tauri::generate_handler![start_bridge, bridge_request])
        .on_window_event(|window, event| if let tauri::WindowEvent::Destroyed = event {
            if let Some(state) = window.try_state::<BridgeState>() {
                if let Ok(mut child) = state.child.lock() { if let Some(child) = child.as_mut() { let _ = child.kill(); } }
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running Asayn desktop");
}
