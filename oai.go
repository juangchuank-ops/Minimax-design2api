package main

import (
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------------------
// OpenAI Chat Completions wire types (request + response).
// Only the subset that actually round-trips through the MiniMax Design gateway.
// ---------------------------------------------------------------------------

type OAIRequest struct {
	Model               string          `json:"model"`
	Messages            []OAIMessage    `json:"messages"`
	Stream              bool            `json:"stream"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"`
	Tools               []OAITool       `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	User                string          `json:"user,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func (r *OAIRequest) maxOut() int {
	if r.MaxCompletionTokens != nil && *r.MaxCompletionTokens > 0 {
		return *r.MaxCompletionTokens
	}
	if r.MaxTokens != nil && *r.MaxTokens > 0 {
		return *r.MaxTokens
	}
	return 0
}

type OAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []OAIToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Reasoning  string          `json:"reasoning_content,omitempty"`
}

type OAIToolCall struct {
	Index *int `json:"index,omitempty"`
	// id/type are only emitted on the first delta of a tool call; OpenAI omits
	// them on continuation frames, and some clients reject empty strings.
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function OAIFunctionArg `json:"function"`
}

type OAIFunctionArg struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type OAITool struct {
	Type     string      `json:"type"`
	Function OAIFunction `json:"function"`
}

type OAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type OAIResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   *OAIUsage   `json:"usage,omitempty"`
}

type OAIChoice struct {
	Index        int        `json:"index"`
	Message      OAIMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type OAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptDetails    *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

type OAIChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []OAIChunkRow `json:"choices"`
	Usage   *OAIUsage     `json:"usage,omitempty"`
}

type OAIChunkRow struct {
	Index        int      `json:"index"`
	Delta        OAIDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type OAIDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	Reasoning string        `json:"reasoning_content,omitempty"`
	ToolCalls []OAIToolCall `json:"tool_calls,omitempty"`
}

// contentParts flattens an OpenAI message content field, which may be a plain
// string or an array of typed parts.
type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL    string `json:"url"`
		Detail string `json:"detail"`
	} `json:"image_url,omitempty"`
}

func parseContent(raw json.RawMessage) (text string, parts []contentPart) {
	if len(raw) == 0 {
		return "", nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s, nil
		}
		return "", nil
	}
	if strings.HasPrefix(trimmed, "[") {
		_ = json.Unmarshal(raw, &parts)
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Text != "" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String(), parts
	}
	return "", nil
}

func parseStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return []string{s}
		}
		return nil
	}
	var list []string
	_ = json.Unmarshal(raw, &list)
	return list
}

func strPtr(s string) *string { return &s }
