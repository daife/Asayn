export type ToolCall = { id: string; type: string; function: { name: string; arguments: string } };
export type Message = { role: "system" | "user" | "assistant" | "tool"; content: string; reasoning_content?: string; tool_calls?: ToolCall[]; tool_call_id?: string };
export type Session = { id: string; name: string; root_agent: string; created_at: string; updated_at: string; messages: Message[]; input_history?: string[]; last_total_tokens?: number };
export type AgentConfig = {
  name: string; provider: string; model: string; description: string; system_prompt: string;
  visible_skills: string[]; visible_mcp: string[]; max_output_lines: number; context_window: number;
  max_output_tokens: number; auto_compact_threshold_percent: number; real_time_context_control: boolean;
  allow_parallel_shell: boolean; allow_interactive_shell: boolean; thinking_enabled: boolean; reasoning_effort: string;
};
export type Stats = { TotalInput: number; TotalOutput: number; TotalCacheHit: number; SessionInput: number; SessionOutput: number; SessionCacheHit: number };
export type Snapshot = { session: Session; sessions: Session[]; agent: AgentConfig; stats: Stats; workspace: string };
export type CatalogItem = { Name: string; Description: string; Source: string };
export type Skill = CatalogItem & { Folder: string };
export type Provider = { url: string; allowed_models: string[] };
export type Catalog = { agents: CatalogItem[]; skills: Skill[]; mcp: CatalogItem[]; providers: Record<string, Provider>; config: AgentConfig };
export type AgentEvent = { kind: string; text?: string; usage?: unknown; answer?: string; error?: string };
