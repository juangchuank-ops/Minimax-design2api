package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	cfg     *Config
	pool    *Pool
	catalog *Catalog
	up      *Upstream
	metrics *Metrics
	client  *http.Client
	mux     *http.ServeMux

	// startedAt is process boot time, surfaced on the dashboard next to the
	// metrics' own uptime so a metrics reset is distinguishable from a restart.
	startedAt time.Time

	refreshMu sync.Mutex
}

func newServer(cfg *Config) *Server {
	pool := newPool(cfg)
	catalog := newCatalog()
	s := &Server{
		cfg:       cfg,
		pool:      pool,
		catalog:   catalog,
		up:        newUpstream(cfg, pool, catalog),
		metrics:   newMetrics(),
		startedAt: time.Now(),
		client:    &http.Client{Timeout: 30 * time.Second},
	}
	s.up.client.Timeout = 0 // streaming: no global timeout, ctx governs
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/v1/models", s.handleModels)
	s.mux.HandleFunc("/v1/chat/completions", s.handleChat)
	s.mux.HandleFunc("/v1/messages", s.handleAnthropicNative)

	// Video. These three are deliberately the *MiniMax platform* shape so a
	// new-api MiniMax channel (type 35, "hailuo") can use this service as its
	// upstream with base_url pointed here.
	s.mux.HandleFunc("/v1/video_generation", s.handleVideoGeneration)
	s.mux.HandleFunc("/v1/query/video_generation", s.handleVideoQuery)
	s.mux.HandleFunc("/v1/files/retrieve", s.handleVideoFile)
	s.mux.HandleFunc("/v1/video/models", s.handleVideoModels)

	// Images. Unlike video, these speak *OpenAI's* shape, so a plain OpenAI
	// channel (type 1) in new-api can point its base_url here and use the
	// catalogue's image model ids directly.
	s.mux.HandleFunc("/v1/images/generations", s.handleImageGenerations)
	s.mux.HandleFunc("/v1/image/models", s.handleImageModels)

	// Audio. TTS speaks OpenAI's /v1/audio/speech because new-api's type-1
	// channel byte-pipes that response straight to the caller. The rest is our
	// own shape, since OpenAI has no equivalent endpoint.
	s.mux.HandleFunc("/v1/audio/speech", s.handleSpeech)
	s.mux.HandleFunc("/v1/audio/voices", s.handleVoices)
	s.mux.HandleFunc("/v1/lyrics/generations", s.handleLyrics)
	s.mux.HandleFunc("/v1/music/generations", s.handleMusic)
	s.mux.HandleFunc("/v1/query/music_generation", s.handleMusicQuery)

	s.mux.HandleFunc("/admin", s.handleAdminIndex)
	s.mux.HandleFunc("/admin/", s.handleAdminAsset)

	s.mux.HandleFunc("/admin/api/state", s.handleAdminState)
	s.mux.HandleFunc("/admin/api/metrics", s.handleAdminMetrics)
	s.mux.HandleFunc("/admin/api/catalogs", s.handleAdminCatalogs)
	s.mux.HandleFunc("/admin/api/credits", s.handleAdminCredits)
	s.mux.HandleFunc("/admin/api/config", s.handleAdminConfig)
	s.mux.HandleFunc("/admin/api/tokens", s.handleAdminTokens)
	s.mux.HandleFunc("/admin/api/tokens/", s.handleAdminTokenItem)
	s.mux.HandleFunc("/admin/api/reload", s.handleAdminReload)
	s.mux.HandleFunc("/admin/api/pool/reset", s.handleAdminPoolReset)
	s.mux.HandleFunc("/admin/api/import-local", s.handleAdminImportLocal)

	s.mux.HandleFunc("/", s.handleRoot)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key, anthropic-version")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		s.mux.ServeHTTP(w, r)
		return
	}
	s.serveInstrumented(w, r)
}

// serveInstrumented measures one /v1 call.
//
// Only inference traffic is counted. The admin console polls itself several
// times a minute and the health probe hits /health on a timer; folding those
// into the totals would make the dashboard's request count and success rate
// describe the monitoring rather than the service.
func (s *Server) serveInstrumented(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := &recWriter{ResponseWriter: w, status: http.StatusOK}

	// Tee the body so the model name can be recovered after the handler has
	// consumed it. Handlers stay unaware this is happening.
	var body *bytes.Buffer
	if r.Method == http.MethodPost && r.Body != nil {
		body = &bytes.Buffer{}
		r.Body = &peekBody{rc: r.Body, buf: body}
	}

	s.mux.ServeHTTP(rw, r)

	// The tee only fills as the handler reads. A handler that ignores the body
	// entirely would leave the buffer empty, so drain a little ourselves —
	// otherwise the model silently disappears from the dashboard for exactly
	// those routes.
	if body != nil && body.Len() == 0 {
		_, _ = io.ReadAll(io.LimitReader(r.Body, peekBodyLimit))
	}

	rec := RequestRecord{
		TimeMS: start.UnixMilli(),
		Path:   r.URL.Path,
		Kind:   kindForPath(r.URL.Path),
		Status: rw.status,
		MS:     time.Since(start).Milliseconds(),
		Stream: rw.stream,
		OK:     rw.status >= 200 && rw.status < 300,
	}
	if body != nil {
		rec.Model = extractModelPreference(body.Bytes())
	}
	s.metrics.record(rec)
}

// --- auth -------------------------------------------------------------------

func (s *Server) clientAuthorized(r *http.Request) bool {
	s.cfg.mu.RLock()
	keys := append([]string(nil), s.cfg.APIKeys...)
	s.cfg.mu.RUnlock()
	if len(keys) == 0 {
		return true
	}
	got := bearer(r)
	if got == "" {
		got = r.Header.Get("x-api-key")
	}
	for _, k := range keys {
		if k != "" && k == got {
			return true
		}
	}
	return false
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, typ, msg string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    typ,
			"code":    typ,
		},
	})
}

// --- handlers ---------------------------------------------------------------

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "minimax-design2api",
		"docs":    "/v1/models",
		"admin":   "/admin/",
		"message": "OpenAI-compatible gateway for MiniMax Design desktop models",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	src, at, err := s.catalog.status()
	models, _, _ := s.catalog.list()
	body := map[string]any{
		"status":         "ok",
		"catalog_source": src,
		"catalog_age_s":  int(time.Since(at).Seconds()),
		"model_count":    len(models),
		"pool":           s.pool.snapshot(),
	}
	if err != nil {
		body["catalog_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	routes, _, _ := s.catalog.list()
	data := make([]map[string]any, 0, len(routes))
	for _, rt := range routes {
		data = append(data, map[string]any{
			"id":       rt.ID,
			"object":   "model",
			"created":  0,
			"owned_by": rt.Provider,
			"meta": map[string]any{
				"protocol":   rt.Protocol,
				"display":    rt.Display,
				"context":    rt.Context,
				"max_output": rt.MaxOut,
				"tools":      rt.Tools,
				"vision":     rt.Vision,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var req OAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "request body is not valid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		s.cfg.mu.RLock()
		req.Model = s.cfg.DefaultModel
		s.cfg.mu.RUnlock()
	}

	route, ok := s.catalog.lookup(req.Model)
	if !ok {
		writeErr(w, http.StatusNotFound, "model_not_found",
			fmt.Sprintf("model %q is not in the MiniMax Design catalog; GET /v1/models for the current list", req.Model))
		return
	}
	if route.Protocol == protoGemini {
		writeErr(w, http.StatusNotImplemented, "unsupported_protocol",
			fmt.Sprintf("model %q speaks the Gemini protocol; its upstream route has not been mapped yet", route.ID))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.timeout())
	defer cancel()

	id := newID("chatcmpl")
	created := time.Now().Unix()

	if req.Stream {
		s.streamChat(ctx, w, r, &req, route, id, created)
		return
	}
	s.blockingChat(ctx, w, &req, route, id, created)
}

func (s *Server) dispatch(ctx context.Context, req *OAIRequest, route *ModelRoute, emit Emitter) error {
	switch route.Protocol {
	case protoAnthropic:
		payload, err := buildAnthropicRequest(req, route)
		if err != nil {
			return err
		}
		resp, tok, err := s.up.call(ctx, "/api/v1/messages", payload)
		if err != nil {
			return err
		}
		defer s.pool.release(tok)
		defer resp.Body.Close()
		return runAnthropicStream(ctx, resp, emit)

	case protoResponses:
		payload, err := buildResponsesRequest(req, route)
		if err != nil {
			return err
		}
		resp, tok, err := s.up.call(ctx, "/api/v1/responses", payload)
		if err != nil {
			return err
		}
		defer s.pool.release(tok)
		defer resp.Body.Close()
		return runResponsesStream(ctx, resp, emit)

	default:
		return fmt.Errorf("unsupported protocol %q", route.Protocol)
	}
}

func (s *Server) blockingChat(ctx context.Context, w http.ResponseWriter, req *OAIRequest, route *ModelRoute, id string, created int64) {
	c := &collector{}
	if err := s.dispatch(ctx, req, route, c); err != nil {
		writeUpstreamError(w, err)
		return
	}
	msg := OAIMessage{Role: "assistant"}
	switch {
	case c.text != "":
		msg.Content = mustJSON(c.text)
	case len(c.toolCalls) > 0:
		// OpenAI returns null (not "") when a turn is purely tool calls.
		msg.Content = json.RawMessage("null")
	default:
		msg.Content = mustJSON("")
	}
	if c.reasoning != "" {
		msg.Reasoning = c.reasoning
	}
	if len(c.toolCalls) > 0 {
		msg.ToolCalls = c.toolCalls
	}
	writeJSON(w, http.StatusOK, OAIResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   req.Model,
		Choices: []OAIChoice{{Index: 0, Message: msg, FinishReason: c.finish}},
		Usage:   c.usage,
	})
}

func (s *Server) streamChat(ctx context.Context, w http.ResponseWriter, r *http.Request, req *OAIRequest, route *ModelRoute, id string, created int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming_unsupported", "response writer does not support streaming")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage

	send := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := w.Write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Opening chunk carries the assistant role, matching OpenAI's shape.
	if !send(OAIChunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
		Choices: []OAIChunkRow{{Index: 0, Delta: OAIDelta{Role: "assistant"}}},
	}) {
		return
	}

	em := &sseEmitter{
		send: func(d OAIDelta) bool {
			return send(OAIChunk{
				ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
				Choices: []OAIChunkRow{{Index: 0, Delta: d}},
			})
		},
		finish: func(reason string) bool {
			return send(OAIChunk{
				ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
				Choices: []OAIChunkRow{{Index: 0, Delta: OAIDelta{}, FinishReason: strPtr(reason)}},
			})
		},
	}

	err := s.dispatch(ctx, req, route, em)
	if err != nil {
		// Headers are already out; surface the failure as a final chunk.
		send(map[string]any{"error": map[string]any{
			"message": err.Error(),
			"type":    "upstream_error",
		}})
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	if em.pendingFinish != "" {
		em.finish(em.pendingFinish)
	}
	if includeUsage && em.usage != nil {
		send(OAIChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: req.Model,
			Choices: []OAIChunkRow{}, Usage: em.usage,
		})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeUpstreamError(w http.ResponseWriter, err error) {
	var ue *upstreamError
	if ok := asUpstream(err, &ue); ok {
		code := http.StatusBadGateway
		switch {
		case ue.Status == 429:
			code = http.StatusTooManyRequests
		case ue.AuthBad:
			code = http.StatusUnauthorized
		case ue.Status >= 400 && ue.Status < 500:
			code = http.StatusBadRequest
		}
		// The upstream body is passed through verbatim — it is more specific
		// than anything we could synthesise. Only the error *type* is made
		// explicit, because "余额不足" arriving as a generic upstream_error is
		// exactly the kind of thing that sends you looking at routing and auth.
		typ := "upstream_error"
		if ue.NoCredit {
			typ = "insufficient_credit"
		}
		writeErr(w, code, typ, ue.Error())
		return
	}
	writeErr(w, http.StatusBadGateway, "upstream_error", err.Error())
}

func asUpstream(err error, target **upstreamError) bool {
	for err != nil {
		if ue, ok := err.(*upstreamError); ok {
			*target = ue
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// --- emitters ---------------------------------------------------------------

// sseEmitter forwards deltas straight to the wire.
type sseEmitter struct {
	send          func(OAIDelta) bool
	finish        func(string) bool
	pendingFinish string
	usage         *OAIUsage
}

func (e *sseEmitter) Delta(d OAIDelta)  { e.send(d) }
func (e *sseEmitter) Finish(r string)   { e.pendingFinish = r }
func (e *sseEmitter) Usage(u *OAIUsage) { e.usage = u }

// collector accumulates a full response for non-streaming callers.
type collector struct {
	text      string
	reasoning string
	toolCalls []OAIToolCall
	finish    string
	usage     *OAIUsage
	byIndex   map[int]int
}

func (c *collector) Delta(d OAIDelta) {
	if d.Content != "" {
		c.text += d.Content
	}
	if d.Reasoning != "" {
		c.reasoning += d.Reasoning
	}
	for _, tc := range d.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		if c.byIndex == nil {
			c.byIndex = map[int]int{}
		}
		pos, ok := c.byIndex[idx]
		if !ok {
			c.toolCalls = append(c.toolCalls, OAIToolCall{
				ID: tc.ID, Type: "function",
				Function: OAIFunctionArg{Name: tc.Function.Name},
			})
			pos = len(c.toolCalls) - 1
			c.byIndex[idx] = pos
		}
		slot := &c.toolCalls[pos]
		if slot.ID == "" {
			slot.ID = tc.ID
		}
		if slot.Type == "" {
			slot.Type = "function"
		}
		if slot.Function.Name == "" {
			slot.Function.Name = tc.Function.Name
		}
		slot.Function.Arguments += tc.Function.Arguments
	}
}

func (c *collector) Finish(r string) {
	if r != "" {
		c.finish = r
	}
}

func (c *collector) Usage(u *OAIUsage) {
	if u != nil {
		c.usage = u
	}
}

// --- admin ------------------------------------------------------------------

// configView is the editable slice of Config the admin console may read and
// write back. Account tokens are deliberately absent — they have their own
// endpoints, and a settings form is the wrong place to leak JWTs.
func (s *Server) configView() map[string]any {
	s.cfg.mu.RLock()
	defer s.cfg.mu.RUnlock()

	// Copy rather than alias, and keep it non-nil: `append([]string(nil), …)`
	// on an empty slice returns nil, which marshals to `null` and makes the
	// console's `.length` checks blow up on a perfectly valid config.
	keys := make([]string, len(s.cfg.APIKeys))
	copy(keys, s.cfg.APIKeys)

	return map[string]any{
		"listen":          s.cfg.Listen,
		"gateway":         s.cfg.Gateway,
		"upstream":        s.cfg.Upstream,
		"video_base":      s.cfg.VideoBase,
		"version_code":    s.cfg.VersionCode,
		"app_id":          s.cfg.AppID,
		"device_platform": s.cfg.DevicePlat,
		"default_model":   s.cfg.DefaultModel,
		"api_keys":        keys,
		"timeout_sec":     s.cfg.TimeoutSec,
	}
}

func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request) {
	routes, src, at := s.catalog.list()
	_, _, cerr := s.catalog.status()

	pool := s.pool.snapshot()
	usable := 0
	for _, t := range pool {
		if t.Enabled && !t.Expired && !t.Cooling {
			usable++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":        buildVersion,
		"started_at":     s.startedAt.Unix(),
		"config":         s.configView(),
		"config_path":    s.cfg.filePath(),
		"pool":           pool,
		"pool_usable":    usable,
		"models":         routes,
		"catalog_source": src,
		"catalog_at":     at.Unix(),
		"catalog_error":  errString(cerr),
		"metrics":        s.metrics.summary(),
	})
}

// handleAdminMetrics returns the recent-request tail. The aggregate counters
// ride along with /admin/api/state; this endpoint exists so the dashboard can
// poll the tail alone instead of re-sending the whole catalogue every few
// seconds.
func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": s.metrics.summary(),
		"recent":  s.metrics.tail(),
	})
}

// handleAdminCatalogs exposes the media catalogues to the console.
//
// The public /v1/image/models, /v1/video/models and /v1/audio/voices endpoints
// sit behind the *client* API key — which the admin console has no reason to
// know, and which is empty by default. These mirrors reuse the same builders
// under admin access, so the model page and the playground can populate
// before the operator has pasted a key anywhere.
//
// Failures are per-catalogue and non-fatal: an unreachable voice table must not
// blank out the video picker.
func (s *Server) handleAdminCatalogs(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}

	if models, err := s.videoCatalog(r.Context()); err != nil {
		out["video_error"] = err.Error()
	} else {
		out["video"] = videoModelViews(models)
	}
	if models, err := s.imageCatalog(r.Context()); err != nil {
		out["image_error"] = err.Error()
	} else {
		out["image"] = imageModelViews(models)
	}
	if got, err := s.voiceCatalog(r.Context()); err != nil {
		out["voices_error"] = err.Error()
	} else {
		out["voices"] = voiceViews(got, r.URL.Query().Get("language"), r.URL.Query().Get("q"))
	}

	writeJSON(w, http.StatusOK, out)
}

// handleAdminConfig writes back the whitelisted config fields.
//
// `listen` is accepted but only takes effect on the next restart, and the
// console says so rather than implying the change is live. Everything else
// applies to the running process immediately; the file is rewritten so the
// change survives a restart too.
func (s *Server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT")
		return
	}
	var in struct {
		Listen         *string   `json:"listen"`
		Gateway        *string   `json:"gateway"`
		Upstream       *string   `json:"upstream"`
		VideoBase      *string   `json:"video_base"`
		VersionCode    *string   `json:"version_code"`
		AppID          *string   `json:"app_id"`
		DevicePlatform *string   `json:"device_platform"`
		DefaultModel   *string   `json:"default_model"`
		APIKeys        *[]string `json:"api_keys"`
		TimeoutSec     *int      `json:"timeout_sec"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	s.cfg.mu.Lock()
	if in.Listen != nil {
		s.cfg.Listen = strings.TrimSpace(*in.Listen)
	}
	if in.Gateway != nil {
		s.cfg.Gateway = strings.TrimRight(strings.TrimSpace(*in.Gateway), "/")
	}
	if in.Upstream != nil {
		s.cfg.Upstream = strings.TrimRight(strings.TrimSpace(*in.Upstream), "/")
	}
	if in.VideoBase != nil {
		s.cfg.VideoBase = strings.TrimRight(strings.TrimSpace(*in.VideoBase), "/")
	}
	if in.VersionCode != nil {
		s.cfg.VersionCode = strings.TrimSpace(*in.VersionCode)
	}
	if in.AppID != nil {
		s.cfg.AppID = strings.TrimSpace(*in.AppID)
	}
	if in.DevicePlatform != nil {
		s.cfg.DevicePlat = strings.TrimSpace(*in.DevicePlatform)
	}
	if in.DefaultModel != nil {
		s.cfg.DefaultModel = strings.TrimSpace(*in.DefaultModel)
	}
	if in.TimeoutSec != nil && *in.TimeoutSec > 0 {
		s.cfg.TimeoutSec = *in.TimeoutSec
	}
	if in.APIKeys != nil {
		keys := make([]string, 0, len(*in.APIKeys))
		for _, k := range *in.APIKeys {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		s.cfg.APIKeys = keys
	}
	s.cfg.normalize()
	s.cfg.mu.Unlock()

	if err := s.cfg.save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"config": s.configView(),
	})
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

type tokenInput struct {
	Tokens  []string `json:"tokens"`
	Name    string   `json:"name,omitempty"`
	Region  string   `json:"region,omitempty"`
	Replace bool     `json:"replace,omitempty"`
}

func (s *Server) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	var in tokenInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	s.cfg.mu.Lock()
	if in.Replace {
		s.cfg.Tokens = nil
	}
	added := 0
	for i, raw := range in.Tokens {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name := in.Name
		if name != "" && len(in.Tokens) > 1 {
			name = fmt.Sprintf("%s-%d", name, i+1)
		}
		t := &Token{Token: raw, Region: in.Region, Name: name}
		if t.Region == "" {
			t.Region = "overseas"
		}
		decodeJWT(t)
		s.cfg.Tokens = append(s.cfg.Tokens, t)
		added++
	}
	s.cfg.mu.Unlock()

	if err := s.cfg.save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	go s.refreshCatalog()
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "pool": s.pool.snapshot()})
}

func (s *Server) handleAdminTokenItem(w http.ResponseWriter, r *http.Request) {
	idxStr := strings.TrimPrefix(r.URL.Path, "/admin/api/tokens/")
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "token index must be an integer")
		return
	}

	s.cfg.mu.Lock()
	if idx < 0 || idx >= len(s.cfg.Tokens) {
		s.cfg.mu.Unlock()
		writeErr(w, http.StatusNotFound, "not_found", "token index out of range")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		s.cfg.Tokens = append(s.cfg.Tokens[:idx], s.cfg.Tokens[idx+1:]...)
	case http.MethodPut:
		var in struct {
			Enabled *bool  `json:"enabled"`
			Name    string `json:"name"`
			Token   string `json:"token"`
			Region  string `json:"region"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			s.cfg.mu.Unlock()
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		t := s.cfg.Tokens[idx]
		if in.Enabled != nil {
			t.Enabled = in.Enabled
		}
		if in.Name != "" {
			t.Name = in.Name
		}
		if in.Region != "" {
			t.Region = in.Region
		}
		if in.Token != "" {
			t.Token = in.Token
			decodeJWT(t)
		}
	default:
		s.cfg.mu.Unlock()
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT or DELETE")
		return
	}
	s.cfg.mu.Unlock()

	if err := s.cfg.save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pool": s.pool.snapshot()})
}

func (s *Server) handleAdminReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	s.refreshCatalog()
	src, at, err := s.catalog.status()
	writeJSON(w, http.StatusOK, map[string]any{
		"catalog_source": src,
		"catalog_at":     at.Unix(),
		"error":          errString(err),
	})
}

// handleAdminPoolReset clears every token's failure count and cooldown.
func (s *Server) handleAdminPoolReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	s.pool.resetCooldowns()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"pool": s.pool.snapshot(),
	})
}

// handleAdminImportLocal pulls the access token straight out of the locally
// installed MiniMax Design app and appends it to the pool.
func (s *Server) handleAdminImportLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if err := importLocalToken(s.cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "import_failed", err.Error())
		return
	}
	if err := s.cfg.save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	go s.refreshCatalog()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pool": s.pool.snapshot()})
}

// refreshCatalog pulls /api/v1/config using a live token.
func (s *Server) refreshCatalog() {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	tok, err := s.pool.acquire()
	if err != nil {
		s.catalog.setErr(fmt.Errorf("no token available to fetch the model catalog: %w", err))
		return
	}
	defer s.pool.release(tok)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.catalog.refresh(ctx, s.cfg, s.client, tok.Token); err != nil {
		log.Printf("[catalog] refresh failed: %v", err)
		return
	}
	routes, _, _ := s.catalog.list()
	log.Printf("[catalog] refreshed from gateway: %d model ids", len(routes))
}

// --- Anthropic-native passthrough ------------------------------------------

// handleAnthropicNative lets Anthropic SDK clients talk to the gateway with the
// same credentials, without translating through OpenAI shapes.
func (s *Server) handleAnthropicNative(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	route, ok := s.catalog.lookup(probe.Model)
	if !ok || route.Protocol != protoAnthropic {
		writeErr(w, http.StatusNotFound, "model_not_found",
			fmt.Sprintf("model %q is not an Anthropic-protocol model in the MiniMax Design catalog", probe.Model))
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	payload["model"] = route.Upstream

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.timeout())
	defer cancel()

	resp, tok, err := s.up.call(ctx, "/api/v1/messages", payload)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	defer s.pool.release(tok)
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	if !probe.Stream {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

// --- misc -------------------------------------------------------------------

func newID(prefix string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 24)
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = alphabet[uint64(seed>>33)%uint64(len(alphabet))]
	}
	return prefix + "-" + string(b)
}
