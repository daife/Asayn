package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asayn/asayn/internal/config"
	"github.com/asayn/asayn/internal/llm/types"
)

const mcpProtocolVersion = "2025-03-26"

var mcpToolNameRE = regexp.MustCompile(`[^A-Za-z0-9_]`)

type MCPManager struct {
	paths   config.Paths
	limit   int
	mu      sync.Mutex
	visible []string
	servers map[string]*mcpServerRuntime
	toolMap map[string]mcpToolRef
}

type mcpToolRef struct {
	Server string
	Tool   string
}

type mcpServerRuntime struct {
	info      config.MCPServer
	signature string
	client    *stdioMCPClient
	tools     []mcpTool
	err       string
}

type mcpTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpCallResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError"`
}

func NewMCPManager(paths config.Paths, limit int) *MCPManager {
	if limit <= 0 {
		limit = 2000
	}
	return &MCPManager{
		paths:   paths,
		limit:   limit,
		servers: map[string]*mcpServerRuntime{},
		toolMap: map[string]mcpToolRef{},
	}
}

func (m *MCPManager) SetLimit(limit int) {
	if limit <= 0 {
		limit = 2000
	}
	m.mu.Lock()
	m.limit = limit
	m.mu.Unlock()
}

func (m *MCPManager) SetVisible(names []string) {
	m.mu.Lock()
	m.visible = uniqueStringList(names)
	m.mu.Unlock()
	m.Reload()
}

func (m *MCPManager) Reload() {
	m.mu.Lock()
	visible := append([]string(nil), m.visible...)
	m.mu.Unlock()

	available, err := config.ListMCPServers(m.paths)
	byName := map[string]config.MCPServer{}
	if err == nil {
		for _, srv := range available {
			byName[srv.Name] = srv
		}
	}
	visibleSet := map[string]bool{}
	for _, name := range visible {
		visibleSet[name] = true
	}

	m.mu.Lock()
	for name, rt := range m.servers {
		info, exists := byName[name]
		if !visibleSet[name] || !exists || rt.signature != mcpServerSignature(info) {
			rt.stop()
			delete(m.servers, name)
		}
	}
	for _, name := range visible {
		if _, ok := m.servers[name]; ok {
			continue
		}
		info, ok := byName[name]
		if !ok {
			m.servers[name] = &mcpServerRuntime{err: fmt.Sprintf("mcp server %q not found", name)}
			continue
		}
		rt := &mcpServerRuntime{info: info, signature: mcpServerSignature(info)}
		m.servers[name] = rt
		m.mu.Unlock()
		rt.start(m.paths)
		m.mu.Lock()
	}
	m.rebuildToolMapLocked()
	m.mu.Unlock()
}

func (m *MCPManager) Schemas() []types.ToolSchema {
	m.Reload()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []types.ToolSchema{}
	servers := make([]string, 0, len(m.servers))
	for name := range m.servers {
		servers = append(servers, name)
	}
	sort.Strings(servers)
	for _, serverName := range servers {
		rt := m.servers[serverName]
		if rt == nil || rt.client == nil || rt.err != "" {
			continue
		}
		for _, tool := range rt.tools {
			publicName := publicMCPToolName(serverName, tool.Name)
			desc := strings.TrimSpace(tool.Description)
			if tool.Title != "" && !strings.Contains(desc, tool.Title) {
				if desc != "" {
					desc = tool.Title + ": " + desc
				} else {
					desc = tool.Title
				}
			}
			if desc == "" {
				desc = fmt.Sprintf("MCP tool %s from server %s.", tool.Name, serverName)
			}
			params := tool.InputSchema
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			out = append(out, schema(publicName, desc, params))
		}
	}
	return out
}

func (m *MCPManager) HasTool(publicName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.toolMap[publicName]
	return ok
}

func (m *MCPManager) Call(ctx context.Context, publicName string, args map[string]any) (string, error) {
	m.Reload()
	m.mu.Lock()
	ref, ok := m.toolMap[publicName]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("unknown MCP tool %q", publicName)
	}
	rt := m.servers[ref.Server]
	if rt == nil || rt.client == nil {
		err := "not running"
		if rt != nil && rt.err != "" {
			err = rt.err
		}
		m.mu.Unlock()
		return "", fmt.Errorf("mcp server %q is %s", ref.Server, err)
	}
	client := rt.client
	limit := m.limit
	m.mu.Unlock()

	result, err := client.callTool(ctx, ref.Tool, args)
	if err != nil {
		return "", err
	}
	return truncate(formatMCPCallResult(result), limit), nil
}

func (m *MCPManager) Shutdown() {
	m.mu.Lock()
	servers := make([]*mcpServerRuntime, 0, len(m.servers))
	for _, rt := range m.servers {
		servers = append(servers, rt)
	}
	m.servers = map[string]*mcpServerRuntime{}
	m.toolMap = map[string]mcpToolRef{}
	m.mu.Unlock()
	for _, rt := range servers {
		rt.stop()
	}
}

func (m *MCPManager) StatusLines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	lines := []string{}
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rt := m.servers[name]
		status := "running"
		if rt.err != "" {
			status = "error: " + rt.err
		} else if rt.client == nil {
			status = "not running"
		}
		lines = append(lines, fmt.Sprintf("- mcp %s: %s (%d tools)", name, status, len(rt.tools)))
	}
	return lines
}

func (m *MCPManager) rebuildToolMapLocked() {
	m.toolMap = map[string]mcpToolRef{}
	for serverName, rt := range m.servers {
		if rt == nil || rt.client == nil || rt.err != "" {
			continue
		}
		for _, tool := range rt.tools {
			m.toolMap[publicMCPToolName(serverName, tool.Name)] = mcpToolRef{Server: serverName, Tool: tool.Name}
		}
	}
}

func (rt *mcpServerRuntime) start(paths config.Paths) {
	if strings.ToLower(rt.info.Config.Type) != "" && strings.ToLower(rt.info.Config.Type) != "stdio" {
		rt.err = fmt.Sprintf("transport %q is not supported yet", rt.info.Config.Type)
		return
	}
	if strings.TrimSpace(rt.info.Config.Command) == "" {
		rt.err = "stdio command is empty"
		return
	}
	client := newStdioMCPClient(rt.info, paths.WorkspaceRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.start(ctx); err != nil {
		rt.err = err.Error()
		client.stop()
		return
	}
	tools, err := client.listTools(ctx)
	if err != nil {
		rt.err = err.Error()
		client.stop()
		return
	}
	rt.client = client
	rt.tools = tools
	rt.err = ""
}

func (rt *mcpServerRuntime) stop() {
	if rt != nil && rt.client != nil {
		rt.client.stop()
		rt.client = nil
	}
}

func mcpServerSignature(info config.MCPServer) string {
	b, _ := json.Marshal(info.Config)
	return info.Path + ":" + string(b)
}

func publicMCPToolName(server, tool string) string {
	server = strings.Trim(mcpToolNameRE.ReplaceAllString(server, "_"), "_")
	tool = strings.Trim(mcpToolNameRE.ReplaceAllString(tool, "_"), "_")
	if server == "" {
		server = "server"
	}
	if tool == "" {
		tool = "tool"
	}
	return "mcp__" + server + "__" + tool
}

func uniqueStringList(items []string) []string {
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			seen[item] = true
		}
	}
	out := make([]string, 0, len(seen))
	for item := range seen {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func formatMCPCallResult(result mcpCallResult) string {
	parts := []string{}
	for _, item := range result.Content {
		if typ, _ := item["type"].(string); typ == "text" {
			if text, _ := item["text"].(string); text != "" {
				parts = append(parts, text)
				continue
			}
		}
		b, _ := json.MarshalIndent(item, "", "  ")
		parts = append(parts, string(b))
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if out == "" {
		out = "ok"
	}
	if result.IsError {
		out = "mcp tool error: " + out
	}
	return out
}

type stdioMCPClient struct {
	info      config.MCPServer
	workdir   string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stderr    bytes.Buffer
	pending   map[int64]chan mcpRPCResponse
	mu        sync.Mutex
	nextID    int64
	closed    bool
	readDone  chan struct{}
	writeLock sync.Mutex
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *mcpRPCError    `json:"error"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newStdioMCPClient(info config.MCPServer, workdir string) *stdioMCPClient {
	return &stdioMCPClient{info: info, workdir: workdir, pending: map[int64]chan mcpRPCResponse{}, readDone: make(chan struct{})}
}

func (c *stdioMCPClient) start(ctx context.Context) error {
	cmd := exec.Command(c.info.Config.Command, c.info.Config.Args...)
	cmd.Dir = c.workdir
	cmd.Env = os.Environ()
	for k, v := range c.info.Config.Env {
		cmd.Env = append(cmd.Env, k+"="+expandMCPEnv(v, c.workdir))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.stdin = stdin
	go c.readLoop(stdout)
	go func() { _ = cmd.Wait(); c.closePending("mcp server exited") }()
	if err := c.initialize(ctx); err != nil {
		return err
	}
	return nil
}

func (c *stdioMCPClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "asayn",
			"version": "0.3.4",
		},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

func (c *stdioMCPClient) listTools(ctx context.Context) ([]mcpTool, error) {
	data, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result mcpToolsListResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *stdioMCPClient) callTool(ctx context.Context, name string, args map[string]any) (mcpCallResult, error) {
	data, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return mcpCallResult{}, err
	}
	var result mcpCallResult
	if err := json.Unmarshal(data, &result); err != nil {
		return mcpCallResult{}, err
	}
	return result, nil
}

func (c *stdioMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan mcpRPCResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp server is closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.writeMessage(msg); err != nil {
		c.removePending(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *stdioMCPClient) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.writeMessage(msg)
}

func (c *stdioMCPClient) writeMessage(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeLock.Lock()
	defer c.writeLock.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("mcp stdin is closed")
	}
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (c *stdioMCPClient) readLoop(stdout io.Reader) {
	defer close(c.readDone)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024*16)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var resp mcpRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *stdioMCPClient) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *stdioMCPClient) closePending(reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = map[int64]chan mcpRPCResponse{}
	stderr := strings.TrimSpace(c.stderr.String())
	c.mu.Unlock()
	if stderr != "" {
		reason += ": " + stderr
	}
	for _, ch := range pending {
		ch <- mcpRPCResponse{Error: &mcpRPCError{Code: -32000, Message: reason}}
	}
}

func (c *stdioMCPClient) stop() {
	c.mu.Lock()
	cmd := c.cmd
	stdin := c.stdin
	c.closed = true
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	c.closePending("mcp server stopped")
}

func expandMCPEnv(value, workspace string) string {
	value = strings.ReplaceAll(value, "${PROJECT_ROOT}", workspace)
	value = strings.ReplaceAll(value, "${WORKSPACE_ROOT}", workspace)
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	return value
}
