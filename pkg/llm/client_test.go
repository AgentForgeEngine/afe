package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spf13/pflag"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mycli/pkg/plugin"
)

type fakePlugin struct {
	name     string
	desc     string
	schema   map[string]any
	result   string
	calls    int
	lastArgs string
}

func (f *fakePlugin) Name() string        { return f.name }
func (f *fakePlugin) Description() string { return f.desc }
func (f *fakePlugin) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": f.schema,
	}
}
func (f *fakePlugin) IsEnabled() bool                 { return true }
func (f *fakePlugin) Init() error                     { return nil }
func (f *fakePlugin) RegisterFlags(fs *pflag.FlagSet) {}
func (f *fakePlugin) Execute(ctx context.Context, argsJSON string) (string, error) {
	f.calls++
	f.lastArgs = argsJSON
	return f.result, nil
}

func newMockServer(t *testing.T, responder func(req *chatRequest) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":%s}]}`, mustJSON(responder(&req)))
	}))
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestRunLoopDirectAnswer(t *testing.T) {
	srv := newMockServer(t, func(req *chatRequest) map[string]any {
		if len(req.Tools) != 1 {
			t.Errorf("expected 1 tool definition, got %d", len(req.Tools))
		}
		return map[string]any{
			"role":    "assistant",
			"content": "hello world",
		}
	})
	defer srv.Close()

	p := &fakePlugin{name: "echo", desc: "echoes", schema: map[string]any{}}
	c := NewClient(srv.URL, "test-model")
	got, err := c.Run(context.Background(), "say hi", []plugin.Plugin{p})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
	if p.calls != 0 {
		t.Fatalf("plugin should not have been called, calls=%d", p.calls)
	}
}

func TestRunLoopWithToolCall(t *testing.T) {
	requestCount := 0
	srv := newMockServer(t, func(req *chatRequest) map[string]any {
		requestCount++
		switch requestCount {
		case 1:
			// First call: model asks for the tool.
			return map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "echo",
						"arguments": `{"value":"ping"}`,
					},
				}},
			}
		default:
			// Second call: history must contain assistant tool call + tool result.
			foundToolMsg := false
			foundAssistantCall := false
			for _, m := range req.Messages {
				if m.Role == "tool" && m.ToolCallID == "call_1" && m.Content == "pong" {
					foundToolMsg = true
				}
				if m.Role == "assistant" && len(m.ToolCalls) > 0 {
					foundAssistantCall = true
				}
			}
			if !foundToolMsg || !foundAssistantCall {
				t.Errorf("history incomplete: assistantCall=%v toolMsg=%v", foundAssistantCall, foundToolMsg)
			}
			return map[string]any{
				"role":    "assistant",
				"content": "tool said pong",
			}
		}
	})
	defer srv.Close()

	p := &fakePlugin{name: "echo", desc: "echoes", result: "pong", schema: map[string]any{}}
	c := NewClient(srv.URL, "test-model")
	got, err := c.Run(context.Background(), "use the tool", []plugin.Plugin{p})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "tool said pong" {
		t.Fatalf("got %q, want %q", got, "tool said pong")
	}
	if p.calls != 1 {
		t.Fatalf("plugin calls = %d, want 1", p.calls)
	}
	if p.lastArgs != `{"value":"ping"}` {
		t.Fatalf("plugin args = %q", p.lastArgs)
	}
}

func TestRunLoopUnknownTool(t *testing.T) {
	requestCount := 0
	srv := newMockServer(t, func(req *chatRequest) map[string]any {
		requestCount++
		if requestCount == 1 {
			return map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "nonexistent",
						"arguments": `{}`,
					},
				}},
			}
		}
		// Verify the error was fed back.
		for _, m := range req.Messages {
			if m.Role == "tool" && !strings.Contains(m.Content, "unknown tool") {
				t.Errorf("expected unknown tool error in history, got %q", m.Content)
			}
		}
		return map[string]any{
			"role":    "assistant",
			"content": "recovered",
		}
	})
	defer srv.Close()

	p := &fakePlugin{name: "echo", desc: "echoes", schema: map[string]any{}}
	c := NewClient(srv.URL, "test-model")
	got, err := c.Run(context.Background(), "go", []plugin.Plugin{p})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "recovered" {
		t.Fatalf("got %q, want %q", got, "recovered")
	}
}

func TestRunLoopExceedsMaxIterations(t *testing.T) {
	srv := newMockServer(t, func(req *chatRequest) map[string]any {
		return map[string]any{
			"role":    "assistant",
			"content": "",
			"tool_calls": []map[string]any{{
				"id":   fmt.Sprintf("call_%d", len(req.Messages)),
				"type": "function",
				"function": map[string]any{
					"name":      "echo",
					"arguments": `{}`,
				},
			}},
		}
	})
	defer srv.Close()

	p := &fakePlugin{name: "echo", desc: "echoes", schema: map[string]any{}}
	c := NewClient(srv.URL, "test-model")
	_, err := c.Run(context.Background(), "loop forever", []plugin.Plugin{p})
	if err == nil || !strings.Contains(err.Error(), "tool loop aborted") {
		t.Fatalf("expected tool loop abort error, got %v", err)
	}
	if p.calls != MaxToolLoops {
		t.Fatalf("plugin calls = %d, want %d", p.calls, MaxToolLoops)
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://host:1234":                     "http://host:1234/chat/completions",
		"http://host:1234/":                    "http://host:1234/chat/completions",
		"http://host:1234/v1":                  "http://host:1234/v1/chat/completions",
		"http://host:1234/v1/chat":             "http://host:1234/v1/chat/completions",
		"http://host:1234/v1/chat/":            "http://host:1234/v1/chat/completions",
		"http://host:1234/v1/chat/completions": "http://host:1234/v1/chat/completions",
	}
	for in, want := range cases {
		if got := normalizeEndpoint(in); got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}
