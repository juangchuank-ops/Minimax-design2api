package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// Anthropic Messages adapter (upstream: POST /api/v1/messages)
// Serves minimaxHub/MiniMax-M3, minimaxHub/MiniMax-M2.7, alpha, alpha_high.
// ---------------------------------------------------------------------------

func buildAnthropicRequest(r *OAIRequest, route *ModelRoute) (map[string]any, error) {
	maxTok := r.maxOut()
	if maxTok == 0 {
		maxTok = 8192
		if route.MaxOut > 0 && route.MaxOut < maxTok {
			maxTok = route.MaxOut
		}
	}

	var systemParts []string
	msgs := make([]any, 0, len(r.Messages))

	for _, m := range r.Messages {
		switch m.Role {
		case "system", "developer":
			text, _ := parseContent(m.Content)
			if text != "" {
				systemParts = append(systemParts, text)
			}

		case "user":
			blocks := contentToAnthBlocks(m.Content)
			if len(blocks) == 0 {
				continue
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": blocks})

		case "assistant":
			blocks := contentToAnthBlocks(m.Content)
			for _, tc := range m.ToolCalls {
				var input any = map[string]any{}
				if strings.TrimSpace(tc.Function.Arguments) != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = map[string]any{"_raw": tc.Function.Arguments}
					}
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})

		case "tool":
			text, _ := parseContent(m.Content)
			msgs = append(msgs, map[string]any{
				"role": "user",
				"content": []any{map[string]any{
					"type":        "tool_result",
					"tool_use_id": m.ToolCallID,
					"content":     text,
				}},
			})
		}
	}

	// Anthropic rejects an empty message list.
	if len(msgs) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}

	body := map[string]any{
		"model":      route.Upstream,
		"max_tokens": maxTok,
		"messages":   msgs,
		"stream":     true,
	}
	if len(systemParts) > 0 {
		body["system"] = strings.Join(systemParts, "\n\n")
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if stops := parseStop(r.Stop); len(stops) > 0 {
		body["stop_sequences"] = stops
	}
	if tools := anthropicTools(r.Tools); len(tools) > 0 {
		body["tools"] = tools
		if tc := anthropicToolChoice(r.ToolChoice); tc != nil {
			body["tool_choice"] = tc
		}
	}
	return body, nil
}

func contentToAnthBlocks(raw json.RawMessage) []any {
	text, parts := parseContent(raw)
	blocks := make([]any, 0, 2)
	if len(parts) == 0 {
		if text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
		return blocks
	}
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
			}
		case "image_url":
			if p.ImageURL == nil || p.ImageURL.URL == "" {
				continue
			}
			if src := anthropicImageSource(p.ImageURL.URL); src != nil {
				blocks = append(blocks, map[string]any{"type": "image", "source": src})
			}
		}
	}
	return blocks
}

func anthropicImageSource(u string) map[string]any {
	if strings.HasPrefix(u, "data:") {
		rest := u[len("data:"):]
		comma := strings.IndexByte(rest, ',')
		if comma < 0 {
			return nil
		}
		mediaType := rest[:comma]
		payload := rest[comma+1:]
		if strings.HasSuffix(mediaType, ";base64") {
			mediaType = strings.TrimSuffix(mediaType, ";base64")
		} else {
			// not base64 -> decode to base64 ourselves
			decoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				return nil
			}
			payload = base64.StdEncoding.EncodeToString(decoded)
		}
		return map[string]any{"type": "base64", "media_type": mediaType, "data": payload}
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return map[string]any{"type": "url", "url": u}
	}
	return nil
}

func anthropicTools(tools []OAITool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		if t.Type != "" && t.Type != "function" {
			continue
		}
		schema := t.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": json.RawMessage(schema),
		})
	}
	return out
}

func anthropicToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto":
			return map[string]any{"type": "auto"}
		case "required", "any":
			return map[string]any{"type": "any"}
		default:
			return nil
		}
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Function.Name != "" {
		return map[string]any{"type": "tool", "name": obj.Function.Name}
	}
	return nil
}

// --- stream parsing ---------------------------------------------------------

type anthStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID    string `json:"id"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			CacheRead    int `json:"cache_read_input_tokens"`
			CacheCreate  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Text  string          `json:"text"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthToolState tracks the OpenAI tool_call index assigned to each Anthropic
// content block index, because OpenAI numbers tool calls 0..n independently of
// where they appear in the content array.
type anthToolState struct {
	blockToTool map[int]int
	next        int
	usage       *OAIUsage
}

func runAnthropicStream(ctx context.Context, resp *http.Response, emit Emitter) error {
	st := &anthToolState{blockToTool: map[int]int{}, usage: &OAIUsage{}}

	err := readSSE(resp.Body, func(ev sseEvent) error {
		var e anthStreamEvent
		if err := decodeInto(ev.Data, &e); err != nil {
			return nil // ignore unparsable frames rather than killing the stream
		}
		switch e.Type {
		case "message_start":
			st.usage.PromptTokens = e.Message.Usage.InputTokens
			st.usage.CompletionTokens = e.Message.Usage.OutputTokens
			if e.Message.Usage.CacheRead > 0 || e.Message.Usage.CacheCreate > 0 {
				st.usage.PromptDetails = &struct {
					CachedTokens int `json:"cached_tokens"`
				}{CachedTokens: e.Message.Usage.CacheRead}
			}

		case "content_block_start":
			switch e.ContentBlock.Type {
			case "tool_use":
				idx := st.next
				st.next++
				st.blockToTool[e.Index] = idx
				emit.Delta(OAIDelta{ToolCalls: []OAIToolCall{{
					Index:    &idx,
					ID:       e.ContentBlock.ID,
					Type:     "function",
					Function: OAIFunctionArg{Name: e.ContentBlock.Name, Arguments: ""},
				}}})
			}

		case "content_block_delta":
			switch e.Delta.Type {
			case "text_delta":
				if e.Delta.Text != "" {
					emit.Delta(OAIDelta{Content: e.Delta.Text})
				}
			case "thinking_delta":
				if e.Delta.Thinking != "" {
					emit.Delta(OAIDelta{Reasoning: e.Delta.Thinking})
				}
			case "input_json_delta":
				if idx, ok := st.blockToTool[e.Index]; ok && e.Delta.PartialJSON != "" {
					i := idx
					emit.Delta(OAIDelta{ToolCalls: []OAIToolCall{{
						Index:    &i,
						Function: OAIFunctionArg{Arguments: e.Delta.PartialJSON},
					}}})
				}
			}

		case "message_delta":
			if e.Usage.OutputTokens > 0 {
				st.usage.CompletionTokens = e.Usage.OutputTokens
			}
			if e.Usage.InputTokens > 0 {
				st.usage.PromptTokens = e.Usage.InputTokens
			}
			if e.Delta.StopReason != "" {
				emit.Finish(anthStopReason(e.Delta.StopReason))
			}

		case "error":
			return fmt.Errorf("upstream error: %s", e.Error.Message)
		}
		return nil
	})
	if err != nil {
		return err
	}
	st.usage.TotalTokens = st.usage.PromptTokens + st.usage.CompletionTokens
	emit.Usage(st.usage)
	return nil
}

func anthStopReason(s string) string {
	switch s {
	case "end_turn", "stop_sequence", "pause_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	}
	return "stop"
}
