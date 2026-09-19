package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// OpenAI Responses adapter (upstream: POST /api/v1/responses)
// Serves gamma, gamma_high, gamma_mid, gpt-6-astra.
//
// The gamma provider is declared as @ai-sdk/openai with Responses-only options
// (`store`, `reasoningEffort`, `reasoningSummary`, `include`), so the plain
// /chat/completions route is not what the desktop app uses. /responses is.
// ---------------------------------------------------------------------------

func buildResponsesRequest(r *OAIRequest, route *ModelRoute) (map[string]any, error) {
	var instructions []string
	items := make([]any, 0, len(r.Messages))

	for _, m := range r.Messages {
		switch m.Role {
		case "system", "developer":
			text, _ := parseContent(m.Content)
			if text != "" {
				instructions = append(instructions, text)
			}

		case "user":
			content := responsesInputContent(m.Content)
			if len(content) == 0 {
				continue
			}
			items = append(items, map[string]any{
				"type": "message", "role": "user", "content": content,
			})

		case "assistant":
			text, _ := parseContent(m.Content)
			if text != "" {
				items = append(items, map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": text}},
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": args,
				})
			}

		case "tool":
			text, _ := parseContent(m.Content)
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  text,
			})
		}
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}

	maxOut := r.maxOut()
	if maxOut == 0 {
		maxOut = 8192
		if route.MaxOut > 0 && route.MaxOut < maxOut {
			maxOut = route.MaxOut
		}
	}

	body := map[string]any{
		"model":             route.Upstream,
		"input":             items,
		"stream":            true,
		"store":             false,
		"max_output_tokens": maxOut,
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if tools := responsesTools(r.Tools); len(tools) > 0 {
		body["tools"] = tools
		if tc := responsesToolChoice(r.ToolChoice); tc != nil {
			body["tool_choice"] = tc
		}
	}
	if r.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *r.ParallelToolCalls
	}
	// Gamma is a reasoning model; keep the summary channel on so reasoning
	// surfaces as `reasoning_content` instead of being silently dropped.
	reasoning := map[string]any{"summary": "auto"}
	if r.ReasoningEffort != "" {
		reasoning["effort"] = r.ReasoningEffort
	}
	body["reasoning"] = reasoning
	body["include"] = []string{"reasoning.encrypted_content"}
	return body, nil
}

func responsesInputContent(raw json.RawMessage) []any {
	text, parts := parseContent(raw)
	if len(parts) == 0 {
		if text == "" {
			return nil
		}
		return []any{map[string]any{"type": "input_text", "text": text}}
	}
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				out = append(out, map[string]any{"type": "input_text", "text": p.Text})
			}
		case "image_url":
			if p.ImageURL != nil && p.ImageURL.URL != "" {
				out = append(out, map[string]any{"type": "input_image", "image_url": p.ImageURL.URL})
			}
		}
	}
	return out
}

func responsesTools(tools []OAITool) []any {
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
			"type":        "function",
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"parameters":  json.RawMessage(schema),
		})
	}
	return out
}

func responsesToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto", "none", "required":
			return s
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
		return map[string]any{"type": "function", "name": obj.Function.Name}
	}
	return nil
}

// --- stream parsing ---------------------------------------------------------

type respStreamEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemID string `json:"item_id"`
	Item   struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Status    string `json:"status"`
	} `json:"item"`
	Response struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Usage  *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			InputDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type respToolState struct {
	itemToTool map[string]int
	next       int
	usage      *OAIUsage
}

func runResponsesStream(ctx context.Context, resp *http.Response, emit Emitter) error {
	st := &respToolState{itemToTool: map[string]int{}, usage: &OAIUsage{}}
	sawFinish := false

	err := readSSE(resp.Body, func(ev sseEvent) error {
		var e respStreamEvent
		if err := decodeInto(ev.Data, &e); err != nil {
			return nil
		}
		switch e.Type {
		case "response.output_item.added":
			if e.Item.Type == "function_call" {
				idx := st.next
				st.next++
				key := e.Item.ID
				if key == "" {
					key = e.Item.CallID
				}
				st.itemToTool[key] = idx
				emit.Delta(OAIDelta{ToolCalls: []OAIToolCall{{
					Index: &idx,
					ID:    e.Item.CallID,
					Type:  "function",
					Function: OAIFunctionArg{
						Name:      e.Item.Name,
						Arguments: "",
					},
				}}})
			}

		case "response.output_text.delta":
			if e.Delta != "" {
				emit.Delta(OAIDelta{Content: e.Delta})
			}

		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if e.Delta != "" {
				emit.Delta(OAIDelta{Reasoning: e.Delta})
			}

		case "response.function_call_arguments.delta":
			if idx, ok := st.itemToTool[e.ItemID]; ok && e.Delta != "" {
				i := idx
				emit.Delta(OAIDelta{ToolCalls: []OAIToolCall{{
					Index:    &i,
					Function: OAIFunctionArg{Arguments: e.Delta},
				}}})
			}

		case "response.completed":
			sawFinish = true
			if e.Response.Usage != nil {
				st.usage.PromptTokens = e.Response.Usage.InputTokens
				st.usage.CompletionTokens = e.Response.Usage.OutputTokens
				st.usage.TotalTokens = e.Response.Usage.TotalTokens
				if e.Response.Usage.InputDetails != nil && e.Response.Usage.InputDetails.CachedTokens > 0 {
					st.usage.PromptDetails = &struct {
						CachedTokens int `json:"cached_tokens"`
					}{CachedTokens: e.Response.Usage.InputDetails.CachedTokens}
				}
			}
			if st.next > 0 {
				emit.Finish("tool_calls")
			} else {
				emit.Finish("stop")
			}

		case "response.incomplete":
			sawFinish = true
			emit.Finish("length")

		case "response.failed":
			msg := "response failed"
			if e.Response.IncompleteDetails != nil {
				msg = e.Response.IncompleteDetails.Reason
			}
			return fmt.Errorf("upstream response failed: %s", msg)

		case "error":
			return fmt.Errorf("upstream error: %s", e.Error.Message)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !sawFinish {
		if st.next > 0 {
			emit.Finish("tool_calls")
		} else {
			emit.Finish("stop")
		}
	}
	if st.usage.TotalTokens == 0 {
		st.usage.TotalTokens = st.usage.PromptTokens + st.usage.CompletionTokens
	}
	emit.Usage(st.usage)
	return nil
}
