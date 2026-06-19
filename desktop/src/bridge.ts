import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import type { AgentEvent } from "./types";

type Envelope = { type: "response" | "event"; id?: string; request_id?: string; ok?: boolean; data?: unknown; error?: string };
type Pending = { resolve: (value: unknown) => void; reject: (reason: Error) => void };
const pending = new Map<string, Pending>();
const subscribers = new Set<(event: AgentEvent) => void>();
let unlisten: UnlistenFn | undefined;
const isTauri = () => "__TAURI_INTERNALS__" in window;
const demo = {
  session: { id: "demo-7f31c2a8", name: "desktop-migration", root_agent: "default", created_at: new Date().toISOString(), updated_at: new Date().toISOString(), messages: [], input_history: [] },
  sessions: [{ id: "demo-7f31c2a8", name: "desktop-migration", root_agent: "default", created_at: new Date().toISOString(), updated_at: new Date().toISOString(), messages: [] }],
  agent: { name: "default", provider: "DeepSeek", model: "deepseek-v4-pro", description: "A workspace-aware coding agent with tools, skills, and sub-agents.", system_prompt: "You are a highly capable agent.", visible_skills: ["uv-python"], visible_mcp: ["codegraph"], max_output_lines: 2000, context_window: 1024000, max_output_tokens: 384000, auto_compact_threshold_percent: 70, real_time_context_control: true, allow_parallel_shell: true, allow_interactive_shell: true, thinking_enabled: true, reasoning_effort: "low" },
  stats: { TotalInput: 0, TotalOutput: 0, TotalCacheHit: 0, SessionInput: 0, SessionOutput: 0, SessionCacheHit: 0 }, workspace: "/workspace/Asayn"
};

export async function connect(workspace = "") {
  if (!isTauri()) return;
  if (!unlisten) {
    unlisten = await listen<Envelope>("bridge-event", ({ payload }) => {
      if (payload.type === "response" && payload.id) {
        const item = pending.get(payload.id);
        if (!item) return;
        pending.delete(payload.id);
        payload.ok ? item.resolve(payload.data) : item.reject(new Error(payload.error || "Bridge request failed"));
      } else if (payload.type === "event") {
        subscribers.forEach((fn) => fn(payload.data as AgentEvent));
      }
    });
  }
  await invoke("start_bridge", { workspace });
}

export function onAgentEvent(fn: (event: AgentEvent) => void) { subscribers.add(fn); return () => { subscribers.delete(fn); }; }

export async function request<T>(action: string, payload?: unknown): Promise<T> {
  if (!isTauri()) {
    if (action === "catalog") return { agents: [{ Name: "default", Description: "General-purpose root agent", Source: "global" }], skills: [{ Name: "uv-python", Description: "Python project workflows", Source: "global", Folder: "uv-python" }], mcp: [{ Name: "codegraph", Description: "Workspace code intelligence", Source: "global" }], providers: { DeepSeek: { url: "https://api.deepseek.com", allowed_models: ["deepseek-v4-pro", "deepseek-v4-flash"] } }, config: demo.agent } as T;
    if (action === "workspace_index") return [{ path: demo.workspace, name: "Asayn", last_session_id: demo.session.id, last_opened_at: demo.session.updated_at, available: true, sessions: demo.sessions }] as T;
    return demo as T;
  }
  const id = crypto.randomUUID();
  const promise = new Promise<T>((resolve, reject) => pending.set(id, { resolve: resolve as (v: unknown) => void, reject }));
  try { await invoke("bridge_request", { request: { id, action, payload } }); }
  catch (error) { pending.delete(id); throw error; }
  return promise;
}
