package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Model catalog.
//
// MiniMax Design does not ship a model list in the client. At boot the desktop
// app calls GET {gateway}/api/v1/config with a `token` header and receives a
// provider matrix: every provider carries an npm package name (which tells us
// the wire protocol) and a baseURL (which tells us the host). We mirror that
// call so the model list stays in sync with whatever MiniMax is serving today.
//
// Protocol mapping observed on 3.0.16:
//   @ai-sdk/anthropic -> POST {base}/api/v1/messages
//   @ai-sdk/openai    -> POST {base}/api/v1/responses      (Responses API, not /chat/completions)
//   @ai-sdk/google    -> POST {base}/api/v1/{model}:generateContent   (path not confirmed)
// ---------------------------------------------------------------------------

const (
	protoAnthropic = "anthropic"
	protoResponses = "responses"
	protoGemini    = "gemini"
)

type ModelRoute struct {
	ID       string `json:"id"`       // public name clients use
	Provider string `json:"provider"` // minimaxHub / alpha / gamma / omega
	Upstream string `json:"upstream"` // model name sent upstream
	Protocol string `json:"protocol"` // anthropic | responses | gemini
	Display  string `json:"display"`  // human label from the catalog
	Context  int    `json:"context,omitempty"`
	MaxOut   int    `json:"max_output,omitempty"`
	Tools    bool   `json:"tools,omitempty"`
	Vision   bool   `json:"vision,omitempty"`
}

type Catalog struct {
	mu        sync.RWMutex
	routes    map[string]*ModelRoute // lowercased public id -> route
	order     []string
	source    string // "remote" | "builtin"
	fetchedAt time.Time
	err       error
}

func newCatalog() *Catalog {
	c := &Catalog{routes: map[string]*ModelRoute{}}
	c.apply(builtinCatalog(), "builtin")
	return c
}

// providerConfig mirrors the subset of /api/v1/config we care about.
type providerConfig struct {
	Model             string   `json:"model"`
	EnabledProviders  []string `json:"enabled_providers"`
	AgentModelDisplay map[string]struct {
		Description any `json:"description"`
	} `json:"agent_model_display"`
	Provider map[string]struct {
		Name    string `json:"name"`
		Npm     string `json:"npm"`
		Options struct {
			BaseURL string `json:"baseURL"`
		} `json:"options"`
		Models map[string]struct {
			Name       string `json:"name"`
			ToolCall   bool   `json:"tool_call"`
			Attachment bool   `json:"attachment"`
			Reasoning  bool   `json:"reasoning"`
			Modalities struct {
				Input []string `json:"input"`
			} `json:"modalities"`
			Limit struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	} `json:"provider"`
}

func protocolForNpm(npm string) string {
	switch npm {
	case "@ai-sdk/anthropic":
		return protoAnthropic
	case "@ai-sdk/openai":
		return protoResponses
	case "@ai-sdk/google":
		return protoGemini
	}
	return ""
}

func builtinCatalog() []*ModelRoute {
	return []*ModelRoute{
		{ID: "MiniMax-M3", Provider: "minimaxHub", Upstream: "MiniMax-M3", Protocol: protoAnthropic, Display: "MiniMax M3", Context: 196608, MaxOut: 128000, Tools: true, Vision: true},
		{ID: "MiniMax-M2.7", Provider: "minimaxHub", Upstream: "MiniMax-M2.7", Protocol: protoAnthropic, Display: "MiniMax M2.7", Context: 196608, MaxOut: 128000, Tools: true, Vision: true},
		{ID: "alpha", Provider: "alpha", Upstream: "alpha", Protocol: protoAnthropic, Display: "Alpha", Context: 200000, MaxOut: 64000, Tools: true, Vision: true},
		{ID: "alpha_high", Provider: "alpha", Upstream: "alpha_high", Protocol: protoAnthropic, Display: "Alpha High", Context: 200000, MaxOut: 64000, Tools: true, Vision: true},
		{ID: "gamma", Provider: "gamma", Upstream: "gamma", Protocol: protoResponses, Display: "Gamma", Context: 262000, MaxOut: 128000, Tools: true, Vision: true},
		{ID: "gamma_high", Provider: "gamma", Upstream: "gamma_high", Protocol: protoResponses, Display: "Gamma High", Context: 262000, MaxOut: 128000, Tools: true, Vision: true},
		{ID: "gamma_mid", Provider: "gamma", Upstream: "gamma_mid", Protocol: protoResponses, Display: "Gamma Mid", Context: 262000, MaxOut: 128000, Tools: true, Vision: true},
		{ID: "gpt-6-astra", Provider: "gamma", Upstream: "gpt-6-astra", Protocol: protoResponses, Display: "GPT-6-Astra", Context: 262000, MaxOut: 128000, Tools: true, Vision: true},
		{ID: "omega-3.1-pro", Provider: "omega", Upstream: "omega-3.1-pro", Protocol: protoGemini, Display: "Gemini 3.1 Pro Preview", Context: 200000, MaxOut: 64000, Tools: true, Vision: true},
	}
}

func (c *Catalog) apply(routes []*ModelRoute, source string) {
	m := make(map[string]*ModelRoute, len(routes)*2)
	order := make([]string, 0, len(routes)*2)
	for _, r := range routes {
		// Two public spellings per model, matching what the desktop app uses:
		//   "MiniMax-M3"          (bare)
		//   "minimaxHub/MiniMax-M3" (provider-qualified, as in agent_model)
		for _, id := range []string{r.ID, r.Provider + "/" + r.ID} {
			key := strings.ToLower(id)
			if _, dup := m[key]; dup {
				continue
			}
			clone := *r
			clone.ID = id
			m[key] = &clone
			order = append(order, id)
		}
	}
	sort.Strings(order)

	c.mu.Lock()
	c.routes = m
	c.order = order
	c.source = source
	c.fetchedAt = time.Now()
	c.mu.Unlock()
}

func (c *Catalog) lookup(name string) (*ModelRoute, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.routes[strings.ToLower(strings.TrimSpace(name))]
	return r, ok
}

func (c *Catalog) list() ([]*ModelRoute, string, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*ModelRoute, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.routes[strings.ToLower(id)])
	}
	return out, c.source, c.fetchedAt
}

func (c *Catalog) status() (string, time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.source, c.fetchedAt, c.err
}

// refresh pulls the provider matrix from the cloud gateway using any live token.
func (c *Catalog) refresh(ctx context.Context, cfg *Config, client *http.Client, token string) error {
	gateway, _, versionCode, _, _ := cfg.settings()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway+"/api/v1/config", nil)
	if err != nil {
		return err
	}
	req.Header.Set("token", token)
	req.Header.Set("version_code", versionCode)

	resp, err := client.Do(req)
	if err != nil {
		c.setErr(err)
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("gateway /api/v1/config -> HTTP %d", resp.StatusCode)
		c.setErr(err)
		return err
	}

	var pc providerConfig
	if err := json.Unmarshal(body, &pc); err != nil {
		c.setErr(err)
		return err
	}
	routes := routesFromProviderConfig(&pc)
	if len(routes) == 0 {
		err := fmt.Errorf("gateway returned an empty provider matrix")
		c.setErr(err)
		return err
	}
	c.apply(routes, "remote")
	return nil
}

func (c *Catalog) setErr(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func routesFromProviderConfig(pc *providerConfig) []*ModelRoute {
	var out []*ModelRoute
	for pid, p := range pc.Provider {
		proto := protocolForNpm(p.Npm)
		if proto == "" {
			continue
		}
		for mid, m := range p.Models {
			r := &ModelRoute{
				ID:       mid,
				Provider: pid,
				Upstream: mid,
				Protocol: proto,
				Display:  m.Name,
				Context:  m.Limit.Context,
				MaxOut:   m.Limit.Output,
				Tools:    m.ToolCall,
			}
			if r.Display == "" {
				r.Display = mid
			}
			for _, in := range m.Modalities.Input {
				if in == "image" {
					r.Vision = true
				}
			}
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out
}
