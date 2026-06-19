import { useEffect, useRef, useState } from "react";
import { Bot, BrainCircuit, ChevronDown, ChevronRight, CircleStop, Copy, ExternalLink, Folder, FolderOpen, GitFork, Menu, MessageSquarePlus, PanelLeftClose, Pencil, RotateCcw, Send, Settings2, TerminalSquare, Wrench, X } from "lucide-react";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { open } from "@tauri-apps/plugin-dialog";
import { connect, onAgentEvent, request } from "./bridge";
import Markdown from "./Markdown";
import type { AgentConfig, AgentEvent, Catalog, Message, Session, Snapshot, Workspace } from "./types";

type RunItem = { kind: "thinking" | "tool" | "error"; title: string; text: string; active?: boolean };
type TranscriptItem =
  | { kind: "user"; message: Message }
  | { kind: "assistant"; runItems: RunItem[]; content: string };

const compact = (n = 0) => n < 1_000 ? `${n}` : n < 1_000_000 ? `${(n / 1_000).toFixed(1)}K` : `${(n / 1_000_000).toFixed(1)}M`;
export function buildTranscript(messages?: Message[] | null): TranscriptItem[] {
  const transcript: TranscriptItem[] = [];
  let assistant: Extract<TranscriptItem, { kind: "assistant" }> | undefined;
  const toolItems = new Map<string, RunItem>();

  const ensureAssistant = () => {
    if (!assistant) {
      assistant = { kind: "assistant", runItems: [], content: "" };
      transcript.push(assistant);
    }
    return assistant;
  };

  for (const message of messages || []) {
    if (message.role === "system") continue;
    if (message.role === "user") {
      transcript.push({ kind: "user", message });
      assistant = undefined;
      toolItems.clear();
      continue;
    }
    if (message.role === "assistant") {
      const turn = ensureAssistant();
      if (message.reasoning_content) turn.runItems.push({ kind: "thinking", title: "Reasoning", text: message.reasoning_content });
      for (const call of message.tool_calls || []) {
        const item: RunItem = { kind: "tool", title: call.function.name, text: call.function.arguments };
        turn.runItems.push(item);
        toolItems.set(call.id, item);
      }
      if (message.content) turn.content += `${turn.content ? "\n\n" : ""}${message.content}`;
      continue;
    }
    if (message.role === "tool") {
      const matched = message.tool_call_id ? toolItems.get(message.tool_call_id) : undefined;
      if (matched) matched.text = message.content;
      else ensureAssistant().runItems.push({ kind: "tool", title: "Tool result", text: message.content });
    }
  }
  return transcript.filter((item) => item.kind === "user" || item.content || item.runItems.length > 0);
}
const normalizedPath = (path: string) => {
  const value = path.replace(/\\/g, "/").replace(/\/$/, "");
  return /^[a-z]:/i.test(value) ? value.toLowerCase() : value;
};
const samePath = (left: string, right: string) => normalizedPath(left) === normalizedPath(right);

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [catalog, setCatalog] = useState<Catalog>();
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [expandedWorkspaces, setExpandedWorkspaces] = useState<Set<string>>(new Set());
  const [prompt, setPrompt] = useState("");
  const [running, setRunning] = useState(false);
  const [stream, setStream] = useState("");
  const [runItems, setRunItems] = useState<RunItem[]>([]);
  const [queued, setQueued] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [sidebar, setSidebar] = useState(true);
  const [settings, setSettings] = useState(false);
  const [editing, setEditing] = useState<AgentConfig>();
  const [textDialog, setTextDialog] = useState<{ kind: "rename" | "fork"; title: string; label: string; value: string }>();
  const endRef = useRef<HTMLDivElement>(null);
  const conversationRef = useRef<HTMLElement>(null);
  const historyIndex = useRef(-1);
  const queueRef = useRef<string[]>([]);

  const refreshWorkspaceIndex = async (activePath?: string) => {
    const items = await request<Workspace[]>("workspace_index");
    setWorkspaces(items);
    if (activePath) setExpandedWorkspaces((current) => new Set([...current, activePath]));
  };

  const refresh = async () => {
    const next = await request<Snapshot>("snapshot");
    setSnapshot(next);
    await refreshWorkspaceIndex(next.workspace);
  };

  useEffect(() => {
    const off = onAgentEvent((event) => consumeEvent(event));
    connect().then(async () => {
      const next = await request<Snapshot>("initialize", { workspace: "" });
      setSnapshot(next);
      const [nextCatalog] = await Promise.all([request<Catalog>("catalog"), refreshWorkspaceIndex(next.workspace)]);
      setCatalog(nextCatalog);
    }).catch((e) => setError(String(e)));
    return off;
  }, []);

  useEffect(() => {
    const conversation = conversationRef.current;
    if (conversation) conversation.scrollTo({ top: conversation.scrollHeight, behavior: running ? "smooth" : "auto" });
  }, [snapshot?.session.messages, stream, runItems, running]);

  function consumeEvent(event: AgentEvent) {
    if (event.kind === "assistant_delta") setStream((text) => text + (event.text || ""));
    else if (event.kind === "thinking_start") setRunItems((items) => [...items, { kind: "thinking", title: "Reasoning", text: "", active: true }]);
    else if (event.kind === "thinking_delta" || event.kind === "thinking") setRunItems((items) => {
      const next = [...items]; const i = next.findLastIndex((x) => x.kind === "thinking");
      if (i >= 0) next[i] = { ...next[i], text: event.kind === "thinking_delta" ? next[i].text + (event.text || "") : event.text || "", active: false };
      else next.push({ kind: "thinking", title: "Reasoning", text: event.text || "" }); return next;
    });
    else if (event.kind === "tool_start") setRunItems((items) => [...items.map((x) => ({ ...x, active: false })), { kind: "tool", title: event.text || "Tool", text: "", active: true }]);
    else if (event.kind === "tool_result" || event.kind === "tool_error") setRunItems((items) => {
      const next = [...items]; const i = next.findLastIndex((x) => x.kind === "tool");
      if (i >= 0) next[i] = { ...next[i], kind: event.kind === "tool_error" ? "error" : "tool", text: event.text || "", active: false }; return next;
    });
    else if (event.kind === "auto_compact") { setStream(""); setRunItems([{ kind: "thinking", title: "Compressing context", text: "Preparing a continuation summary", active: true }]); }
    else if (event.kind === "done") {
      setRunning(false);
      if (event.error && !event.error.includes("context canceled")) setError(event.error);
      refresh().then(() => {
        setStream(""); setRunItems([]);
        const next = queueRef.current.shift(); setQueued([...queueRef.current]);
        if (next) launch(next);
      }).catch((e) => setError(String(e)));
    }
  }

  function launch(text: string, retry = false) {
    setError(""); setStream(""); setRunItems([]); setRunning(true);
    if (!retry) setSnapshot((current) => current ? ({ ...current, session: { ...current.session, messages: [...(current.session.messages || []), { role: "user", content: text }] } }) : current);
    request(retry ? "retry" : "ask", retry ? {} : { prompt: text }).catch((e) => { setRunning(false); setError(String(e)); });
  }

  async function send(retry = false) {
    let text = prompt.trim(); if ((!text && !retry) || !snapshot) return;
    if (running && !retry) { queueRef.current.push(text); setQueued([...queueRef.current]); setPrompt(""); return; }
    if (running) return;
    if (!retry && text.startsWith("/")) {
      const [command, ...rest] = text.split(/\s+/); const argument = rest.join(" ");
      if (command === "/new") { setPrompt(""); await action("new_session", { name: argument }); return; }
      if (command === "/resume") { if (argument) await action("load_session", { id: argument }); else setError("Choose a thread from the sidebar."); setPrompt(""); return; }
      if (command === "/rename") { if (argument) await action("rename_session", { name: argument }); else setError("Usage: /rename <name>"); setPrompt(""); return; }
      if (command === "/fork") { await action("fork_session", { name: argument || `${snapshot.session.name}-fork` }); setPrompt(""); return; }
      if (command === "/retry") { setPrompt(""); await send(true); return; }
      if (command === "/compact") { setPrompt(""); await compactNow(); return; }
      if (command === "/model_config") { setEditing({ ...snapshot.agent }); setSettings(true); setPrompt(""); return; }
      if (command === "/root_agent" || command === "/model") { if (argument) await selectAgent(argument); else setError("Choose an agent from the top bar."); setPrompt(""); return; }
      if (command === "/help") { setError("Commands: /new /resume /retry /rename /fork /root_agent /model /model_config /compact"); setPrompt(""); return; }
      if (command === "/btw") { setError("/btw is reserved for a future side-channel question flow."); setPrompt(""); return; }
      if (command === "/exit") { await getCurrentWindow().close(); return; }
      const named = command.slice(1);
      if (catalog?.skills.some((x) => x.Name === named)) text = `Recommend skill "${named}" for this request. ${argument}`;
      else if (catalog?.mcp.some((x) => x.Name === named)) text = `Recommend MCP server "${named}" for this request. ${argument}`;
    }
    if (!retry) setPrompt("");
    launch(text, retry);
  }

  async function action(name: string, payload?: unknown) {
    try {
      setError("");
      const next = await request<Snapshot>(name, payload);
      setSnapshot(next);
      await refreshWorkspaceIndex(next.workspace);
    }
    catch (e) { setError(String(e)); }
  }

  async function switchWorkspace(workspace: string, sessionId?: string) {
    if (running) { setError("Stop the current agent turn before switching workspaces."); return; }
    try {
      setError("");
      const next = await request<Snapshot>("switch_workspace", { workspace, session_id: sessionId || "" });
      setSnapshot(next);
      const [nextCatalog] = await Promise.all([request<Catalog>("catalog"), refreshWorkspaceIndex(next.workspace)]);
      setCatalog(nextCatalog);
    } catch (e) { setError(String(e)); }
  }

  async function chooseWorkspace() {
    const selected = await open({ directory: true, multiple: false, title: "Open an Asayn workspace" });
    if (typeof selected === "string") await switchWorkspace(selected);
  }

  async function openIndexedSession(workspace: Workspace, session: Session) {
    if (samePath(workspace.path, snapshot?.workspace || "")) await action("load_session", { id: session.id });
    else await switchWorkspace(workspace.path, session.id);
  }

  function toggleWorkspace(path: string) {
    setExpandedWorkspaces((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path); else next.add(path);
      return next;
    });
  }

  async function selectAgent(name: string) {
    await action("select_agent", { name });
    setCatalog(await request<Catalog>("catalog"));
  }

  async function compactNow() {
    if (running) return;
    setError(""); setStream(""); setRunItems([{ kind: "thinking", title: "Compressing context", text: "Creating a continuation summary", active: true }]); setRunning(true);
    try { await request("compact"); } catch (e) { setRunning(false); setError(String(e)); }
  }

  if (!snapshot) return <div className="boot"><div className="boot-mark" aria-label="Asayn"/><p>Starting the local agent engine…</p>{error && <pre>{error}</pre>}</div>;
  const transcript = buildTranscript(snapshot.session.messages);

  return <div className={`app ${sidebar ? "" : "sidebar-closed"}`}>
    <aside className="sidebar">
      <header className="brand"><div className="brand-mark" aria-label="Asayn"/><div><strong>ASAYN</strong><span>LOCAL AGENT WORKBENCH</span></div><button onClick={() => setSidebar(false)} title="Hide sidebar"><PanelLeftClose size={17}/></button></header>
      <div className="sidebar-actions">
        <button className="open-workspace" onClick={chooseWorkspace}><FolderOpen size={17}/> Open workspace</button>
        <button className="new-chat" onClick={() => action("new_session", {})}><MessageSquarePlus size={17}/> New thread</button>
      </div>
      <div className="session-label"><span>Workspaces</span><span>{workspaces.length}</span></div>
      <nav className="workspaces">{workspaces.map((workspace) => {
        const expanded = expandedWorkspaces.has(workspace.path);
        const activeWorkspace = samePath(workspace.path, snapshot.workspace);
        return <section className={`workspace-group ${activeWorkspace ? "current" : ""} ${workspace.available ? "" : "unavailable"}`} key={workspace.path}>
          <button className="workspace-row" disabled={!workspace.available} onClick={() => toggleWorkspace(workspace.path)} title={workspace.path}>
            {expanded ? <ChevronDown size={13}/> : <ChevronRight size={13}/>}<Folder size={14}/>
            <span>{workspace.name}</span><small>{workspace.sessions.length}</small>
          </button>
          {expanded && <div className="workspace-sessions">{workspace.sessions.length ? workspace.sessions.map((session) => <button key={session.id} className={activeWorkspace && session.id === snapshot.session.id ? "active" : ""} onClick={() => openIndexedSession(workspace, session)}>
            <span className="session-name">{session.name}</span><time>{relativeTime(session.updated_at)}</time>
          </button>) : <p>No saved threads</p>}</div>}
        </section>;
      })}</nav>
      <footer className="sidebar-footer"><div className="workspace-dot"/><div><span>Workspace</span><strong title={snapshot.workspace}>{snapshot.workspace.split(/[\\/]/).pop()}</strong></div><button onClick={() => { setEditing({ ...snapshot.agent }); setSettings(true); }}><Settings2 size={18}/></button></footer>
    </aside>

    <main>
      <header className="topbar">
        {!sidebar && <button className="icon-button" onClick={() => setSidebar(true)}><Menu size={19}/></button>}
        <div className="thread-title"><strong>{snapshot.session.name}</strong><span>{snapshot.session.id.slice(0, 8)}</span></div>
        <div className="top-actions">
          <label className="agent-select"><Bot size={15}/><select value={snapshot.agent.name} onChange={(e) => selectAgent(e.target.value)}>{catalog?.agents.map((a) => <option key={a.Name}>{a.Name}</option>)}</select><ChevronDown size={13}/></label>
          <button className="icon-button" title="Rename" onClick={() => setTextDialog({ kind: "rename", title: "Rename thread", label: "Thread name", value: snapshot.session.name })}><Pencil size={16}/></button>
          <button className="icon-button" title="Fork" onClick={() => setTextDialog({ kind: "fork", title: "Fork thread", label: "New thread name", value: `${snapshot.session.name}-fork` })}><GitFork size={16}/></button>
          <button className="icon-button" title="Compact context" disabled={running} onClick={compactNow}><BrainCircuit size={16}/></button>
          <button className="icon-button" title="Retry" disabled={running} onClick={() => send(true)}><RotateCcw size={16}/></button>
        </div>
      </header>

      <section className="conversation" ref={conversationRef}>
        {transcript.length === 0 && !running ? <EmptyState agent={snapshot.agent}/> : transcript.map((item, i) => item.kind === "user"
          ? <UserMessage key={`user-${i}`} message={item.message}/>
          : <AssistantTurn key={`assistant-${i}`} runItems={item.runItems} content={item.content}/>)}
        {(running || stream || runItems.length > 0) && <article className="turn assistant-turn live">
          <div className="speaker"><AsaynAvatar/><strong>Asayn</strong><span className="live-pill">LIVE</span></div>
          <div className="run-stack">{runItems.map((item, i) => <RunCard item={item} key={i}/>)}</div>
          {stream && <Markdown>{stream}</Markdown>}
          {!stream && runItems.length === 0 && <div className="thinking-line"><i/><i/><i/>Contacting {snapshot.agent.model}</div>}
        </article>}
        <div ref={endRef}/>
      </section>

      <section className="composer-wrap">
        {error && <div className="error-banner"><span>{error}</span><button onClick={() => setError("")}><X size={15}/></button></div>}
        <div className="composer">
          <textarea value={prompt} rows={1} placeholder={running ? "Add a message to the queue…" : `Ask ${snapshot.agent.name} to inspect, build, or explain…`} onChange={(e) => { historyIndex.current = -1; setPrompt(e.target.value); }} onKeyDown={(e) => {
            const history = snapshot.session.input_history || [];
            if (e.key === "Escape") { e.preventDefault(); if (queueRef.current.length) { queueRef.current.pop(); setQueued([...queueRef.current]); } else if (running) request("cancel"); }
            else if (e.key === "ArrowUp" && !prompt.includes("\n") && history.length) { e.preventDefault(); historyIndex.current = Math.min(history.length - 1, historyIndex.current < 0 ? history.length - 1 : historyIndex.current - 1); setPrompt(history[historyIndex.current]); }
            else if (e.key === "ArrowDown" && historyIndex.current >= 0) { e.preventDefault(); historyIndex.current += 1; if (historyIndex.current >= history.length) { historyIndex.current = -1; setPrompt(""); } else setPrompt(history[historyIndex.current]); }
            else if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
          }}/>
          <div className="composer-meta"><span><kbd>Enter</kbd> {running ? "queue" : "send"}</span><span><kbd>Shift Enter</kbd> newline</span>{queued.length > 0 && <span className="queue-count">{queued.length} queued · Esc removes last</span>}</div>
          {running ? <button className="send stop" onClick={() => request("cancel")} title="Stop"><CircleStop size={18}/></button> : <button className="send" disabled={!prompt.trim()} onClick={() => send()} title="Send"><Send size={18}/></button>}
        </div>
        <div className="statusbar"><span><i className="online"/> Local engine</span><span>{snapshot.agent.model}</span><span>{compact(snapshot.stats.SessionInput)} in / {compact(snapshot.stats.SessionOutput)} out</span><span>{compact(snapshot.session.last_total_tokens)} context</span></div>
      </section>
    </main>
    {settings && editing && catalog && <Settings config={editing} catalog={catalog} onClose={() => setSettings(false)} onSave={async (config) => { await action("save_agent_config", config); setCatalog(await request<Catalog>("catalog")); setSettings(false); }}/>}
    {textDialog && <TextDialog {...textDialog} onClose={() => setTextDialog(undefined)} onSubmit={async (value) => {
      await action(textDialog.kind === "rename" ? "rename_session" : "fork_session", { name: value });
      setTextDialog(undefined);
    }}/>}
  </div>;
}

function EmptyState({ agent }: { agent: AgentConfig }) {
  return <div className="empty"><div className="empty-orbit"><div className="empty-brand"/></div><p className="eyebrow">READY IN THIS WORKSPACE</p><h1>What should we<br/><em>make happen?</em></h1><p>{agent.description || "A workspace-aware coding agent with tools, skills, and sub-agents."}</p><div className="starter-grid"><span><TerminalSquare size={15}/> Inspect this codebase</span><span><Wrench size={15}/> Fix a failing test</span></div></div>;
}

function UserMessage({ message }: { message: Message }) {
  return <article className="turn user-turn"><div className="speaker"><div className="speaker-icon user">Y</div><strong>You</strong></div><div className="user-copy">{message.content}</div></article>;
}

function AsaynAvatar() { return <div className="speaker-icon asayn" aria-label="Asayn"/>; }

function AssistantTurn({ runItems, content }: { runItems: RunItem[]; content: string }) {
  const [copied, setCopied] = useState(false);
  return <article className="turn assistant-turn"><div className="speaker"><AsaynAvatar/><strong>Asayn</strong></div>
    {runItems.length > 0 && <div className="run-stack">{runItems.map((item, i) => <RunCard item={item} key={i}/>)}</div>}
    {content && <Markdown>{content}</Markdown>}
    {content && <button className="copy" onClick={() => { navigator.clipboard.writeText(content); setCopied(true); setTimeout(() => setCopied(false), 1200); }}><Copy size={13}/>{copied ? "Copied" : "Copy"}</button>}
  </article>;
}

function RunCard({ item }: { item: RunItem }) {
  return <details className={`run-card ${item.kind}`} open={item.active || item.kind === "error"}><summary>{item.kind === "thinking" ? <BrainCircuit size={15}/> : <TerminalSquare size={15}/>}<span>{item.title}</span>{item.active && <i/>}<ChevronDown size={14}/></summary>{item.text && <pre>{item.text}</pre>}</details>;
}

function TextDialog({ title, label, value, onClose, onSubmit }: { kind: "rename" | "fork"; title: string; label: string; value: string; onClose: () => void; onSubmit: (value: string) => Promise<void> }) {
  const [draft, setDraft] = useState(value); const [saving, setSaving] = useState(false);
  return <div className="modal-layer" onMouseDown={(e) => e.target === e.currentTarget && onClose()}><form className="text-dialog" onSubmit={async (e) => { e.preventDefault(); const next = draft.trim(); if (!next) return; setSaving(true); try { await onSubmit(next); } finally { setSaving(false); } }}>
    <header><div className="dialog-mark"><Pencil size={17}/></div><div><span>THREAD ACTION</span><h2>{title}</h2></div><button type="button" className="icon-button" onClick={onClose}><X size={18}/></button></header>
    <label>{label}<input autoFocus value={draft} onChange={(e) => setDraft(e.target.value)} onFocus={(e) => e.currentTarget.select()}/></label>
    <footer><button type="button" className="secondary" onClick={onClose}>Cancel</button><button type="submit" disabled={saving || !draft.trim()}>{saving ? "Saving…" : "Confirm"}</button></footer>
  </form></div>;
}

function Settings({ config, catalog, onClose, onSave }: { config: AgentConfig; catalog: Catalog; onClose: () => void; onSave: (c: AgentConfig) => Promise<void> }) {
  const [draft, setDraft] = useState(config); const [saving, setSaving] = useState(false);
  const models = catalog.providers[draft.provider]?.allowed_models || [];
  const toggle = (field: "visible_skills" | "visible_mcp", value: string) => setDraft({ ...draft, [field]: draft[field].includes(value) ? draft[field].filter((x) => x !== value) : [...draft[field], value] });
  return <div className="modal-layer" onMouseDown={(e) => e.target === e.currentTarget && onClose()}><section className="settings">
    <header><div><span>AGENT PROFILE</span><h2>{draft.name}</h2></div><button className="icon-button" onClick={onClose}><X size={19}/></button></header>
    <div className="settings-body"><div className="field-row"><label>Provider<select value={draft.provider} onChange={(e) => setDraft({ ...draft, provider: e.target.value, model: catalog.providers[e.target.value]?.allowed_models?.[0] || "" })}>{Object.keys(catalog.providers).map((x) => <option key={x}>{x}</option>)}</select></label><label>Model<select value={draft.model} onChange={(e) => setDraft({ ...draft, model: e.target.value })}>{models.map((x) => <option key={x}>{x}</option>)}</select></label></div>
      <label>System prompt<textarea rows={5} value={draft.system_prompt} onChange={(e) => setDraft({ ...draft, system_prompt: e.target.value })}/></label>
      <div className="field-row"><label>Reasoning effort<select value={draft.reasoning_effort} onChange={(e) => setDraft({ ...draft, reasoning_effort: e.target.value })}>{["none", "low", "medium", "high", "max"].map((x) => <option key={x}>{x}</option>)}</select></label><label>Compact at<input type="number" min="10" max="100" value={draft.auto_compact_threshold_percent} onChange={(e) => setDraft({ ...draft, auto_compact_threshold_percent: Number(e.target.value) })}/></label></div>
      <div className="switches"><Switch label="Thinking stream" value={draft.thinking_enabled} set={(v) => setDraft({ ...draft, thinking_enabled: v })}/><Switch label="Parallel shell" value={draft.allow_parallel_shell} set={(v) => setDraft({ ...draft, allow_parallel_shell: v, allow_interactive_shell: v && draft.allow_interactive_shell })}/><Switch label="Interactive shell" value={draft.allow_interactive_shell} set={(v) => setDraft({ ...draft, allow_interactive_shell: v, allow_parallel_shell: v || draft.allow_parallel_shell })}/></div>
      <Picker title="Visible skills" items={catalog.skills} selected={draft.visible_skills} toggle={(x) => toggle("visible_skills", x)}/><Picker title="Visible MCP servers" items={catalog.mcp} selected={draft.visible_mcp} toggle={(x) => toggle("visible_mcp", x)}/>
    </div><footer><button className="secondary api-config" onClick={() => request("open_path", { path: catalog.api_config_path })} title="Open API config file"><ExternalLink size={13}/>API config</button><button className="secondary" onClick={onClose}>Cancel</button><button disabled={saving} onClick={async () => { setSaving(true); await onSave(draft); }}>{saving ? "Saving…" : "Save profile"}</button></footer>
  </section></div>;
}

function Switch({ label, value, set }: { label: string; value: boolean; set: (v: boolean) => void }) { return <label className="switch"><span>{label}</span><input type="checkbox" checked={value} onChange={(e) => set(e.target.checked)}/><i/></label>; }
function Picker({ title, items, selected, toggle }: { title: string; items: { Name: string; Description: string }[]; selected: string[]; toggle: (v: string) => void }) { return <div className="picker"><label>{title}<span>{selected.length} enabled</span></label><div>{items.map((item) => <button className={selected.includes(item.Name) ? "selected" : ""} key={item.Name} onClick={() => toggle(item.Name)}><i/>{item.Name}<small>{item.Description}</small></button>)}</div></div>; }
function relativeTime(value: string) { const seconds = Math.max(0, (Date.now() - new Date(value).getTime()) / 1000); if (seconds < 60) return "now"; if (seconds < 3600) return `${Math.floor(seconds / 60)}m`; if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`; return `${Math.floor(seconds / 86400)}d`; }
