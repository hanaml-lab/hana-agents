package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Func adapts a Go function to a Tool.
type Func struct {
	ToolName string
	Fn       func(ctx context.Context, key string, args map[string]any) (any, error)
}

func (f Func) Name() string { return f.ToolName }

func (f Func) Call(ctx context.Context, key string, args map[string]any) (json.RawMessage, error) {
	v, err := f.Fn(ctx, key, args)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// HTTPTool calls a tool served by the Python SDK (agent_runtime.ToolServer) or anything that
// speaks the same protocol: POST {base}/tools/{name} with {"key", "args"}, reply {"result"} or
// {"error"}. The server deduplicates on the key.
type HTTPTool struct {
	ToolName string
	BaseURL  string
	Client   *http.Client
}

func (h HTTPTool) Name() string { return h.ToolName }

func (h HTTPTool) Call(ctx context.Context, key string, args map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"key": key, "args": args})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.BaseURL, "/")+"/tools/"+h.ToolName, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("%s: bad reply: %w", h.ToolName, err)
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("%s: %s", h.ToolName, reply.Error)
	}
	return reply.Result, nil
}

// Discover lists the tools a tool server offers (GET {base}/tools) as HTTPTools.
func Discover(ctx context.Context, baseURL string) ([]Tool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/tools", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var names []string
	if err := json.NewDecoder(resp.Body).Decode(&names); err != nil {
		return nil, err
	}
	out := make([]Tool, len(names))
	for i, n := range names {
		out[i] = HTTPTool{ToolName: n, BaseURL: baseURL}
	}
	return out, nil
}
