package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mycli/pkg/plugin"
)

// MaxToolLoops caps how many tool-execution iterations the loop may
// perform before aborting, guarding against local models that repeatedly
// emit malformed or endless tool calls.
const MaxToolLoops = 5

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Message is a single chat message in OpenAI format.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolDefinition is an OpenAI function tool definition sent with the
// completion request.
type ToolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model      string           `json:"model"`
	Messages   []Message        `json:"messages"`
	Tools      []ToolDefinition `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Client is an OpenAI-compatible chat completions client pointed at a
// local LLM endpoint.
type Client struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewClient builds a client for the given endpoint URL and model name.
// The URL may be a bare host (e.g. http://localhost:8080) or already end
// in /chat/completions; either form is accepted.
func NewClient(baseURL, model string) *Client {
	return &Client{
		baseURL:    normalizeEndpoint(baseURL),
		model:      model,
		httpClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

func normalizeEndpoint(base string) string {
	base = strings.TrimRight(base, "/")
	switch {
	case strings.HasSuffix(base, "/chat/completions"):
		return base
	case strings.HasSuffix(base, "/chat"):
		return base + "/completions"
	default:
		return base + "/chat/completions"
	}
}

// Tools converts active plugins into OpenAI tool definitions. Only
// enabled plugins are passed, keeping the prompt context small for
// local models.
func Tools(plugins []plugin.Plugin) []ToolDefinition {
	if len(plugins) == 0 {
		return nil
	}

	defs := make([]ToolDefinition, 0, len(plugins))
	for _, p := range plugins {
		var def ToolDefinition
		def.Type = "function"
		def.Function.Name = p.Name()
		def.Function.Description = p.Description()
		def.Function.Parameters = p.Schema()
		defs = append(defs, def)
	}
	return defs
}

// Run executes the single-shot completion loop: send the user prompt,
// dispatch any tool calls to the active plugins, append tool results,
// and repeat until the model produces a final answer or the iteration
// guard is hit. It returns the model's final text content.
func (c *Client) Run(ctx context.Context, prompt string, plugins []plugin.Plugin) (string, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt()},
		{Role: "user", Content: prompt},
	}
	toolDefs := Tools(plugins)

	for iteration := 0; ; iteration++ {
		if iteration >= MaxToolLoops {
			return "", fmt.Errorf("tool loop aborted after %d iterations without a final answer", MaxToolLoops)
		}

		resp, err := c.complete(ctx, messages, toolDefs)
		if err != nil {
			return "", err
		}

		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Record the assistant's tool-call message verbatim so the model
		// sees its own request in history.
		messages = append(messages, resp)

		for _, call := range resp.ToolCalls {
			result, execErr := c.executeTool(ctx, plugins, call)
			if execErr != nil {
				// Feed the error back to the model so it can recover
				// instead of aborting the whole run.
				result = fmt.Sprintf("Error: %v", execErr)
			}
			messages = append(messages, Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
			})
		}
	}
}

func (c *Client) executeTool(ctx context.Context, plugins []plugin.Plugin, call ToolCall) (string, error) {
	name := call.Function.Name

	for _, p := range plugins {
		if p.Name() == name {
			return p.Execute(ctx, call.Function.Arguments)
		}
	}
	return "", fmt.Errorf("unknown tool %q; available tools are %v", name, activeNames(plugins))
}

func activeNames(plugins []plugin.Plugin) []string {
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.Name())
	}
	return names
}

func (c *Client) complete(ctx context.Context, messages []Message, toolDefs []ToolDefinition) (Message, error) {
	reqBody := chatRequest{
		Model:      c.model,
		Messages:   messages,
		Tools:      toolDefs,
		ToolChoice: "auto",
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return Message{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return Message{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer localhost")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("request to %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Message{}, fmt.Errorf("endpoint returned %s: %s", resp.Status, truncate(string(body), 500))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Message{}, fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(body), 300))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, fmt.Errorf("endpoint error (%s): %s", parsed.Error.Type, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, fmt.Errorf("endpoint returned no choices: %s", truncate(string(body), 300))
	}

	msg := parsed.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		for i := range msg.ToolCalls {
			if msg.ToolCalls[i].Type == "" {
				msg.ToolCalls[i].Type = "function"
			}
		}
	}
	return msg, nil
}

func systemPrompt() string {
	return "You are a precise command-line agent with access to local tools. " +
		"Use the available tools to complete the user's task, calling them with valid JSON arguments. " +
		"Inspect results before answering. " +
		"When the task is complete, respond with a clear, self-contained final answer and do not call further tools."
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
