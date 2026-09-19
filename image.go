package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Image generation.
//
// The media API exposes one endpoint pair per *backend* (vendor), not per model.
// Six backends are registered in the catalogue:
//
//	openai, qwen, seedream, nano_banana  -> /api/v2/image/<backend>/generate
//	                                        /api/v2/image/<backend>/tasks/<id>
//	kling, midjourney                    -> /api/v1/image/<backend>/generate
//	                                        /api/v1/image/<backend>/tasks/<id>
//
// All of them are asynchronous except the v1 nano_banana path, which answers
// synchronously with an image_url. This file always uses the v2 pair for
// nano_banana and falls back to the sync v1 call if v2 is unavailable.
//
// Unlike video, the image backends do not share a request body: each one was
// reverse-engineered separately from the desktop client's service classes, and
// the shapes differ enough that they are reproduced verbatim below.
//
// The facade speaks OpenAI's images API, because that is what new-api's type-1
// (OpenAI) channel sends — so pointing a plain OpenAI channel at this service
// with the catalogue's image model ids gets every backend for free:
//
//	POST /v1/images/generations   {model, prompt, n, size, ...}
//	GET  /v1/image/models         catalogue (backend + accepted params)
// ---------------------------------------------------------------------------

// imageBackendSpec is where one vendor's submit/query pair lives.
type imageBackendSpec struct {
	Generate string
	Query    string
}

var imageBackends = map[string]imageBackendSpec{
	"nano_banana": {Generate: "/api/v2/image/nano_banana/generate", Query: "/api/v2/image/nano_banana/tasks"},
	"openai":      {Generate: "/api/v2/image/openai/generate", Query: "/api/v2/image/openai/tasks"},
	"qwen":        {Generate: "/api/v2/image/qwen/generate", Query: "/api/v2/image/qwen/tasks"},
	"seedream":    {Generate: "/api/v2/image/seedream/generate", Query: "/api/v2/image/seedream/tasks"},
	"kling":       {Generate: "/api/v1/image/kling/generate", Query: "/api/v1/image/kling/tasks"},
	"midjourney":  {Generate: "/api/v1/image/midjourney/generate", Query: "/api/v1/image/midjourney/tasks"},
}

// nanoBananaSyncGenerate is the v1 endpoint, used only as a fallback.
const nanoBananaSyncGenerate = "/api/v1/image/nano_banana/generate"

// imagePollInterval / imagePollTimeout bound how long the facade waits for a
// render. The upstream estimate is ~30-60s; 5 minutes is generous but finite.
const (
	imagePollInterval = 3 * time.Second
	imagePollTimeout  = 5 * time.Minute
)

// ---------------------------------------------------------------------------
// Catalogue
// ---------------------------------------------------------------------------

type imageModelEntry struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Backend   string                     `json:"backend"`
	ModelName string                     `json:"model_name"`
	Params    map[string]json.RawMessage `json:"params"`
}

// paramDefault returns a catalogue param's declared default, if any.
func (m imageModelEntry) paramDefault(key string) string {
	raw, ok := m.Params[key]
	if !ok {
		return ""
	}
	var p struct {
		Default any `json:"default"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Default == nil {
		return ""
	}
	switch v := p.Default.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func (m imageModelEntry) paramOptions(key string) []string {
	raw, ok := m.Params[key]
	if !ok {
		return nil
	}
	var p struct {
		Options []string `json:"options"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return p.Options
}

var (
	imageCatalogMu  sync.Mutex
	imageCatalogAt  time.Time
	imageCatalogVal []imageModelEntry
)

// imageCatalog fetches /api/v1/models/config once and caches the image half.
func (s *Server) imageCatalog(ctx context.Context) ([]imageModelEntry, error) {
	imageCatalogMu.Lock()
	defer imageCatalogMu.Unlock()
	if len(imageCatalogVal) > 0 && time.Since(imageCatalogAt) < 10*time.Minute {
		return imageCatalogVal, nil
	}

	var resp struct {
		ImageModels []imageModelEntry `json:"imageModels"`
	}
	if err := s.up.jsonCall(ctx, http.MethodGet, s.cfg.videoBase(),
		hubModelsConfig, nil, &resp); err != nil {
		if len(imageCatalogVal) > 0 {
			return imageCatalogVal, nil // serve stale rather than fail
		}
		return nil, err
	}
	imageCatalogVal = resp.ImageModels
	imageCatalogAt = time.Now()
	return imageCatalogVal, nil
}

// resolveImageModel maps a caller-supplied model onto a catalogue entry.
// A bare backend name ("seedream") is accepted too and resolves to that
// backend's first catalogue entry.
func (s *Server) resolveImageModel(ctx context.Context, name string) (imageModelEntry, error) {
	name = strings.TrimSpace(name)
	models, err := s.imageCatalog(ctx)
	if err != nil {
		return imageModelEntry{}, err
	}
	if len(models) == 0 {
		return imageModelEntry{}, fmt.Errorf("image catalogue is empty")
	}

	if name != "" {
		for _, m := range models {
			if strings.EqualFold(m.ID, name) {
				return m, nil
			}
		}
		// provider-qualified id, e.g. "seedream/doubao-seedream-4-5-251128"
		if _, tail, ok := strings.Cut(name, "/"); ok {
			for _, m := range models {
				if strings.EqualFold(m.ID, tail) || strings.EqualFold(m.ModelName, tail) {
					return m, nil
				}
			}
		}
		// bare backend name
		for _, m := range models {
			if strings.EqualFold(m.Backend, name) {
				return m, nil
			}
		}
		// A backend can exist upstream without any catalogue entry — qwen and
		// kling are both live on the hub but are not offered by this client
		// build. Let the caller reach them by naming the backend directly and
		// rely on the upstream defaults.
		if _, ok := imageBackends[strings.ToLower(name)]; ok {
			return imageModelEntry{
				ID:      strings.ToLower(name),
				Name:    name,
				Backend: strings.ToLower(name),
			}, nil
		}
		return imageModelEntry{}, fmt.Errorf("unsupported image model %q; try one of %s (or a backend name: %s)",
			name, strings.Join(imageModelIDs(models), ", "), strings.Join(imageBackendNames(), ", "))
	}

	// No model named: prefer the cheapest/fastest vendor that is always on.
	for _, m := range models {
		if m.Backend == "nano_banana" {
			return m, nil
		}
	}
	return models[0], nil
}

func imageModelIDs(models []imageModelEntry) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func imageBackendNames() []string {
	out := make([]string, 0, len(imageBackends))
	for k := range imageBackends {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

// oaiImageReq is OpenAI's images request plus the extensions this facade
// understands. Anything the caller does not set falls back to the catalogue
// default for that model.
type oaiImageReq struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	ResponseFormat string `json:"response_format"`
	Background     string `json:"background"`

	AspectRatio   string   `json:"aspect_ratio"`
	Resolution    string   `json:"resolution"`
	ImagePaths    []string `json:"image_paths"`
	Image         string   `json:"image"` // single reference image, convenience alias
	Seed          *int     `json:"seed"`
	GuidanceScale *float64 `json:"guidance_scale"`
	Stylize       *int     `json:"stylize"`
	Chaos         *int     `json:"chaos"`
	Weird         *int     `json:"weird"`
	Version       string   `json:"version"`
	ReferenceType string   `json:"reference_type"`
	ModelName     string   `json:"model_name"` // override the backend's model_name
}

// referencePaths merges the two ways a caller can hand us reference images.
func (r *oaiImageReq) referencePaths() []string {
	out := make([]string, 0, len(r.ImagePaths)+1)
	for _, p := range r.ImagePaths {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	if strings.TrimSpace(r.Image) != "" {
		out = append(out, strings.TrimSpace(r.Image))
	}
	return out
}

// imageSubmitResp covers every submit shape in one struct: v2 answers
// {task_id}, kling nests it under data, and the v1 nano_banana sync path
// answers with the finished image directly.
type imageSubmitResp struct {
	TaskID   string `json:"task_id"`
	ImageURL string `json:"image_url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Code     *int   `json:"code"`
	Data     *struct {
		TaskID string `json:"task_id"`
	} `json:"data"`
	Base  imageBase  `json:"base"`
	Error *imageBase `json:"error"`
}

func (r imageSubmitResp) taskID() string {
	if r.TaskID != "" {
		return r.TaskID
	}
	if r.Data != nil {
		return r.Data.TaskID
	}
	return ""
}

// imageBase is the shared failure envelope: v2 puts a human message in
// base.message, v1 errors put it under error.message.
type imageBase struct {
	Message      string `json:"message"`
	UserMessage  string `json:"user_message"`
	RefundStatus string `json:"refund_status"`
}

// imageQueryResp is the poll shape. `status` is a string here even though the
// submit response sends a number, so it is decoded loosely.
type imageQueryResp struct {
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"`
	ImageURL  string    `json:"image_url"`
	ImageURLs []string  `json:"image_urls"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Base      imageBase `json:"base"`
}

// urls flattens the single- and multi-image response shapes.
func (r imageQueryResp) urls() []string {
	if len(r.ImageURLs) > 0 {
		return r.ImageURLs
	}
	if r.ImageURL != "" {
		return []string{r.ImageURL}
	}
	return nil
}

// imageFailed reports whether a poll status is terminal-failure.
func imageFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "fail", "cancelled", "canceled", "error", "rejected":
		return true
	}
	return false
}

func imageSucceeded(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeed", "succeeded", "completed", "complete", "done", "finished":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleImageModels serves GET /v1/image/models.
func (s *Server) handleImageModels(w http.ResponseWriter, r *http.Request) {
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	models, err := s.imageCatalog(r.Context())
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": imageModelViews(models)})
}

// imageModelViews projects the hub's image catalogue into the shape callers
// see. Shared by the public endpoint and the admin console's mirror so a change
// to the projection cannot make the two disagree.
func imageModelViews(models []imageModelEntry) []map[string]any {
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id":             m.ID,
			"object":         "model",
			"created":        0,
			"owned_by":       m.Backend,
			"name":           m.Name,
			"backend_model":  m.ModelName,
			"aspect_ratio":   m.paramOptions("aspect_ratio"),
			"resolution":     m.paramOptions("resolution"),
			"quality":        m.paramOptions("quality"),
			"size":           m.paramOptions("size"),
			"supported_size": m.paramOptions("resolution"),
		})
	}
	return data
}

// handleImageGenerations serves POST /v1/images/generations (OpenAI shape).
func (s *Server) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	var req oaiImageReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "body is not valid JSON: "+err.Error())
		return
	}

	entry, err := s.resolveImageModel(r.Context(), req.Model)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	spec, ok := imageBackends[entry.Backend]
	if !ok {
		writeErr(w, http.StatusNotImplemented, "unsupported_backend",
			"backend "+entry.Backend+" has no known endpoint pair")
		return
	}

	refs := req.referencePaths()
	if strings.TrimSpace(req.Prompt) == "" && len(refs) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}
	if n := req.N; n < 0 || n > 4 {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "n must be between 1 and 4")
		return
	}

	body := buildImageBody(entry, req, refs)

	urls, err := s.runImageTask(r.Context(), entry, spec, body)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	if len(urls) == 0 {
		writeErr(w, http.StatusBadGateway, "upstream_error", "generation finished without an image URL")
		return
	}

	format := strings.ToLower(strings.TrimSpace(req.ResponseFormat))
	out := make([]oaiImageDatum, 0, len(urls))
	for _, u := range urls {
		d := oaiImageDatum{}
		if format == "b64_json" {
			b64, err := fetchAsBase64(r.Context(), u)
			if err != nil {
				writeErr(w, http.StatusBadGateway, "upstream_error",
					"could not download the generated image: "+err.Error())
				return
			}
			d.B64JSON = b64
		} else {
			d.URL = u
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, oaiImageResp{Created: time.Now().Unix(), Data: out})
}

type oaiImageResp struct {
	Created int64           `json:"created"`
	Data    []oaiImageDatum `json:"data"`
}

type oaiImageDatum struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ---------------------------------------------------------------------------
// Request building — one branch per vendor, reproducing the client's shapes
// ---------------------------------------------------------------------------

func buildImageBody(entry imageModelEntry, req oaiImageReq, refs []string) map[string]any {
	upstreamModel := firstNonEmpty(req.ModelName, entry.ModelName)
	aspect := firstNonEmpty(req.AspectRatio, aspectFromSize(req.Size), entry.paramDefault("aspect_ratio"))

	switch entry.Backend {
	case "nano_banana":
		return map[string]any{
			"prompt":       req.Prompt,
			"model_name":   upstreamModel,
			"image_paths":  refs,
			"aspect_ratio": firstNonEmpty(aspect, "auto"),
			"resolution":   firstNonEmpty(req.Resolution, resolutionFromSize(req.Size), entry.paramDefault("resolution"), "1K"),
		}

	case "openai":
		// The client resolves `size` from the model's allowed set; we pass the
		// caller's value through and let the hub reject anything unsupported.
		n := req.N
		if n <= 0 {
			n = 1
		}
		body := map[string]any{
			"prompt":      req.Prompt,
			"model":       firstNonEmpty(upstreamModel, "gpt-image-2"),
			"image_paths": refs,
			"quality":     firstNonEmpty(req.Quality, entry.paramDefault("quality"), "medium"),
			"n":           n,
		}
		if size := firstNonEmpty(req.Size, req.Resolution); size != "" {
			body["size"] = size
		}
		// background=auto is the documented default and is omitted on purpose:
		// the hub rejects an explicit "auto" for some models.
		if bg := strings.ToLower(strings.TrimSpace(req.Background)); bg != "" && bg != "auto" {
			body["background"] = bg
		}
		return body

	case "qwen":
		return map[string]any{
			"prompt":       req.Prompt,
			"image_paths":  refs,
			"aspect_ratio": firstNonEmpty(aspect, "1:1"),
		}

	case "seedream":
		model := firstNonEmpty(upstreamModel, "doubao-seedream-5-0-pro-260628")
		body := map[string]any{
			"prompt":       req.Prompt,
			"image_paths":  refs,
			"aspect_ratio": firstNonEmpty(aspect, "1:1"),
			"model":        model,
		}
		if size := firstNonEmpty(req.Size, req.Resolution); size != "" {
			body["size"] = size
		}
		// Seedream 5 dropped seed/guidance_scale; sending them is an error.
		if !strings.Contains(model, "5-0") {
			seed := -1
			if req.Seed != nil {
				seed = *req.Seed
			}
			guidance := 5.0
			if req.GuidanceScale != nil {
				guidance = *req.GuidanceScale
			}
			body["seed"] = seed
			body["guidance_scale"] = guidance
		}
		return body

	case "kling":
		body := map[string]any{
			"prompt":       req.Prompt,
			"model_name":   firstNonEmpty(upstreamModel, "kling-v2-1"),
			"aspect_ratio": firstNonEmpty(aspect, "1:1"),
			"n":            1,
		}
		if len(refs) > 0 {
			body["image"] = refs[0]
			body["image_reference"] = firstNonEmpty(req.ReferenceType, "subject")
		}
		return body

	case "midjourney":
		// Midjourney has no structured params worth forwarding — everything
		// rides on prompt flags, which the client appends itself. The version
		// suffix comes from the catalogue id, so it is resolved here rather
		// than left to the caller.
		return map[string]any{
			"prompt": appendMidjourneyVersion(appendMidjourneyFlags(req), entry.ID),
			"params": map[string]any{},
		}
	}

	// Unknown backend: send the least surprising shape and let the hub decide.
	return map[string]any{
		"prompt":      req.Prompt,
		"model_name":  upstreamModel,
		"image_paths": refs,
	}
}

// appendMidjourneyFlags mirrors the client's flag appending, including the
// version suffix derived from the catalogue id.
func appendMidjourneyFlags(req oaiImageReq) string {
	prompt := strings.TrimSpace(req.Prompt)

	if ar := strings.TrimSpace(req.AspectRatio); ar != "" && ar != "auto" && !strings.Contains(prompt, "--ar ") {
		prompt += " --ar " + ar
	}
	if req.Stylize != nil && *req.Stylize != 100 && !strings.Contains(prompt, "--stylize ") {
		prompt += fmt.Sprintf(" --stylize %d", *req.Stylize)
	}
	if req.Chaos != nil && *req.Chaos != 0 && !hasFlag(prompt, "chaos", "c") {
		prompt += fmt.Sprintf(" --chaos %d", *req.Chaos)
	}
	if req.Weird != nil && *req.Weird != 0 && !hasFlag(prompt, "weird", "w") {
		prompt += fmt.Sprintf(" --weird %d", *req.Weird)
	}
	if !hasFlag(prompt, "v", "version", "niji") {
		switch strings.ToLower(strings.TrimSpace(req.Version)) {
		case "niji7":
			prompt += " --niji 7"
		case "7":
			prompt += " --v 7"
		case "":
			// fall through to the model-derived default below
		default:
			prompt += " --v " + req.Version
		}
	}
	return prompt
}

// hasFlag reports whether the prompt already carries a --name flag.
func hasFlag(prompt string, names ...string) bool {
	fields := strings.Fields(prompt)
	for _, f := range fields {
		if !strings.HasPrefix(f, "--") {
			continue
		}
		body := strings.TrimPrefix(f, "--")
		if i := strings.IndexByte(body, '='); i >= 0 {
			body = body[:i]
		}
		for _, n := range names {
			if strings.EqualFold(body, n) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Submit + poll
// ---------------------------------------------------------------------------

// runImageTask submits one generation and waits for its image URLs. The
// catalogue model id is needed for Midjourney's version flag, so it is passed
// through the entry rather than folded into the body.
func (s *Server) runImageTask(ctx context.Context, entry imageModelEntry, spec imageBackendSpec, body map[string]any) ([]string, error) {
	// Midjourney resolves its version from the catalogue id, which the client
	// appends before submitting; do the same here.
	if entry.Backend == "midjourney" {
		if p, ok := body["prompt"].(string); ok {
			body["prompt"] = appendMidjourneyVersion(p, entry.ID)
		}
	}

	var submit imageSubmitResp
	if err := s.up.jsonCall(ctx, http.MethodPost, s.cfg.videoBase(),
		spec.Generate, body, &submit); err != nil {
		// nano_banana's v2 pair may not exist on older deployments; fall back
		// to the synchronous v1 endpoint before giving up.
		if entry.Backend == "nano_banana" {
			if ue, ok := err.(*upstreamError); ok && ue.Status == http.StatusNotFound {
				return s.runNanoBananaSync(ctx, body)
			}
		}
		return nil, err
	}

	if msg := submitFailure(submit); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	// The v1 sync path (and any backend that answers inline) is already done.
	if submit.ImageURL != "" {
		return []string{submit.ImageURL}, nil
	}

	taskID := submit.taskID()
	if taskID == "" {
		return nil, fmt.Errorf("upstream accepted the request but returned no task_id")
	}
	return s.pollImageTask(ctx, spec, taskID)
}

// runNanoBananaSync drives the legacy synchronous nano_banana endpoint.
func (s *Server) runNanoBananaSync(ctx context.Context, body map[string]any) ([]string, error) {
	var resp imageSubmitResp
	if err := s.up.jsonCall(ctx, http.MethodPost, s.cfg.videoBase(),
		nanoBananaSyncGenerate, body, &resp); err != nil {
		return nil, err
	}
	if msg := submitFailure(resp); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	if resp.ImageURL == "" {
		return nil, fmt.Errorf("nano_banana returned no image_url")
	}
	return []string{resp.ImageURL}, nil
}

// submitFailure extracts a human-readable failure from a submit response, or
// "" when the submit was accepted.
func submitFailure(r imageSubmitResp) string {
	if r.Error != nil && r.Error.Message != "" {
		return firstNonEmpty(r.Error.UserMessage, r.Error.Message)
	}
	if r.Code != nil && *r.Code != 0 {
		return firstNonEmpty(r.Base.UserMessage, r.Base.Message,
			fmt.Sprintf("upstream returned code %d", *r.Code))
	}
	if r.Base.Message != "" && !strings.EqualFold(r.Base.Message, "success") && r.taskID() == "" {
		return firstNonEmpty(r.Base.UserMessage, r.Base.Message)
	}
	return ""
}

// pollImageTask waits for a submitted task, returning its image URLs.
func (s *Server) pollImageTask(ctx context.Context, spec imageBackendSpec, taskID string) ([]string, error) {
	deadline := time.Now().Add(imagePollTimeout)
	for {
		var q imageQueryResp
		err := s.up.jsonCall(ctx, http.MethodGet, s.cfg.videoBase(),
			spec.Query+"/"+taskID, nil, &q)
		if err != nil {
			// A 404 usually means the task is not visible yet.
			if ue, ok := err.(*upstreamError); ok && ue.Status == http.StatusNotFound {
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("timed out waiting for image task %s", taskID)
				}
				if err := sleepCtx(ctx, imagePollInterval); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}

		switch {
		case imageSucceeded(q.Status):
			urls := q.urls()
			if len(urls) == 0 {
				// Transient: SUCCESS with an empty URL has been observed while
				// the asset is still being published.
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("task %s succeeded but returned no image URL", taskID)
				}
			} else {
				return urls, nil
			}
		case imageFailed(q.Status):
			msg := firstNonEmpty(q.Base.UserMessage, q.Base.Message, q.Status)
			if q.Base.RefundStatus != "" {
				msg += " (credits " + q.Base.RefundStatus + ")"
			}
			return nil, fmt.Errorf("image task failed: %s", msg)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for image task %s", taskID)
		}
		if err := sleepCtx(ctx, imagePollInterval); err != nil {
			return nil, err
		}
	}
}

// sleepCtx sleeps unless the caller goes away first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------------

// appendMidjourneyVersion mirrors resolveMidjourneyVersion + appendVersion.
func appendMidjourneyVersion(prompt, modelID string) string {
	if hasFlag(prompt, "v", "version", "niji") {
		return prompt
	}
	switch modelID {
	case "midjourney-niji7":
		return prompt + " --niji 7"
	case "midjourney-7":
		return prompt + " --v 7"
	case "midjourney-8.1", "midjourney":
		return prompt + " --v 8.1"
	case "midjourney-8.2":
		return prompt + " --v 8.2"
	}
	return prompt + " --v 8.2"
}

// aspectFromSize converts "1024x1024" into the nearest common ratio. Image
// backends take ratios rather than pixel pairs.
func aspectFromSize(size string) string {
	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == 'X' || r == '*' || r == '×' })
	if len(parts) != 2 {
		return ""
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return ""
	}
	type ratio struct {
		name string
		val  float64
	}
	candidates := []ratio{
		{"1:1", 1}, {"4:3", 4.0 / 3}, {"3:4", 3.0 / 4},
		{"16:9", 16.0 / 9}, {"9:16", 9.0 / 16}, {"3:2", 1.5},
		{"2:3", 2.0 / 3}, {"21:9", 21.0 / 9},
	}
	target := float64(w) / float64(h)
	best, bestDelta := "", 0.0
	for _, c := range candidates {
		d := target - c.val
		if d < 0 {
			d = -d
		}
		if best == "" || d < bestDelta {
			best, bestDelta = c.name, d
		}
	}
	return best
}

// resolutionFromSize maps a pixel size onto the 1K/2K/4K vocabulary the
// image backends use.
func resolutionFromSize(size string) string {
	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == 'X' || r == '*' || r == '×' })
	if len(parts) != 2 {
		return ""
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil {
		return ""
	}
	longest := w
	if h > longest {
		longest = h
	}
	switch {
	case longest >= 3000:
		return "4K"
	case longest >= 1500:
		return "2K"
	case longest > 0:
		return "1K"
	}
	return ""
}

// fetchAsBase64 downloads a generated asset for response_format=b64_json.
// The CDN URL is pre-signed, so no credential is attached.
func fetchAsBase64(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
