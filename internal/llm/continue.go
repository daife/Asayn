package llm

// ContinuePrompt resumes from recorded progress rather than repeating the request.
const ContinuePrompt = "Continue the previous unfinished work from the last recorded state. Review interrupted tool calls and verify any partial or unknown effects before retrying them. Do not repeat completed work."
