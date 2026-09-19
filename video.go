package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Video (MiniMax H3, Wan, Veo, Kling, Jimeng).
//
// The media API lives on the *cloud gateway* host (design.minimax.io), not the
// LLM hub, but it is authorised by the very same `token` header:
//
//	POST {videoBase}/api/v1/video/minimax-v3/generate    -> {task_id, base_resp}
//	GET  {videoBase}/api/v1/video/minimax-v3/tasks/{id}  -> {task_id, file_id, status, base_resp}
//	GET  {videoBase}/api/v1/video/minimax/files/{fid}    -> {file: {download_url}, base_resp}
//
// Rather than mirror that shape, this file serves the *public MiniMax platform*
// shape, because that is what new-api's MiniMax channel (channel type 35,
// adapter "hailuo") speaks:
//
//	POST {base}/v1/video_generation                Authorization: Bearer <key>
//	GET  {base}/v1/query/video_generation?task_id= Authorization: Bearer <key>
//	GET  {base}/v1/files/retrieve?file_id=         Authorization: Bearer <key>
//
// Point a type-35 channel at this service and its base_url, and every video
// model works through new-api with no further glue.
//
// This file owns the platform-facing surface only: request decoding, model
// routing, and response rendering. The per-vendor upstream protocols live in
// video_backends.go.
//
// Two details are easy to get wrong and are handled explicitly below:
//
//   - The hub *requires* an explicit `ratio` for pure text-to-video and rejects
//     "adaptive". The platform request has no ratio field, so we derive one.
//   - The hub's status strings are lowercase and differ from the platform's
//     capitalised Preparing/Queueing/Processing/Success/Fail vocabulary.
// ---------------------------------------------------------------------------

const (
	hubVideoFiles   = "/api/v1/video/minimax/files/"
	hubModelsConfig = "/api/v1/models/config"
)

// hubVideoModels are the model ids the hub actually accepts on
// /api/v1/video/minimax-v3/generate.
var hubVideoModels = map[string]bool{
	"MiniMax-H3":           true,
	"MiniMax-H3-Max":       true,
	"MiniMax-H3-Max-Turbo": true,
}

// hailuoAliases maps the public MiniMax platform model names — which is exactly
// what new-api's hailuo channel sends — onto the hub's H3 line. Models already
// in hubVideoModels pass through untouched.
var hailuoAliases = map[string]string{
	"MiniMax-Hailuo-2.3":      "MiniMax-H3",
	"MiniMax-Hailuo-2.3-Fast": "MiniMax-H3-Max-Turbo",
	"MiniMax-Hailuo-02":       "MiniMax-H3",
	"T2V-01-Director":         "MiniMax-H3",
	"T2V-01":                  "MiniMax-H3",
	"I2V-01-Director":         "MiniMax-H3",
	"I2V-01-live":             "MiniMax-H3",
	"I2V-01":                  "MiniMax-H3",
	"S2V-01":                  "MiniMax-H3",
}

// maxVideoModels only accept 480P/768P; everything else accepts 768P/2K.
var maxVideoModels = map[string]bool{
	"MiniMax-H3-Max":       true,
	"MiniMax-H3-Max-Turbo": true,
}

// hubRatios are the concrete values the hub accepts. "adaptive" is listed by the
// catalogue but rejected by the generator for text-to-video, so we never send it.
var hubRatios = []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"}

// ---------------------------------------------------------------------------
// Platform-facing wire shapes
// ---------------------------------------------------------------------------

type platBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type platVideoSubmitReq struct {
	Model           string          `json:"model"`
	Prompt          string          `json:"prompt"`
	Duration        int             `json:"duration"`
	Resolution      string          `json:"resolution"`
	Ratio           string          `json:"ratio"`
	AspectRatio     string          `json:"aspect_ratio"` // omni/veo spelling of ratio
	Size            string          `json:"size"`         // "1280x720"; used to infer ratio
	FirstFrameImage string          `json:"first_frame_image"`
	LastFrameImage  string          `json:"last_frame_image"`
	GenerateAudio   *bool           `json:"generate_audio"`
	PromptOptimizer *bool           `json:"prompt_optimizer"`
	CallbackURL     string          `json:"callback_url"`
	AigcWatermark   *bool           `json:"aigc_watermark"`
	Metadata        json.RawMessage `json:"metadata"`

	// Backend-specific inputs. They have no place in the public platform
	// shape, but the hub's other video backends need them and the platform
	// shape has no slot for them, so they ride along at the top level (or
	// inside `metadata`, which mergeMetadata flattens).
	Mode                 string `json:"mode"`                  // kling std/pro
	AudioURL             string `json:"sound_file"`            // kling avatar voice track
	VideoURL             string `json:"video_url"`             // kling/jimeng motion reference
	CharacterOrientation string `json:"character_orientation"` // kling motion-control
	KeepOriginalSound    string `json:"keep_original_sound"`   // kling motion-control
	ModelName            string `json:"model_name"`            // overrides the model→backend table
}

// KlingMode returns the kling quality tier, defaulting to the hub's own "std".
func (r platVideoSubmitReq) KlingMode() string {
	switch strings.ToLower(strings.TrimSpace(r.Mode)) {
	case "pro", "std":
		return strings.ToLower(strings.TrimSpace(r.Mode))
	default:
		return "std"
	}
}

type platVideoSubmitResp struct {
	TaskID   string       `json:"task_id"`
	BaseResp platBaseResp `json:"base_resp"`
}

type platVideoQueryResp struct {
	TaskID      string       `json:"task_id"`
	Status      string       `json:"status"`
	FileID      string       `json:"file_id,omitempty"`
	VideoWidth  int          `json:"video_width,omitempty"`
	VideoHeight int          `json:"video_height,omitempty"`
	BaseResp    platBaseResp `json:"base_resp"`
}

type platFileRetrieveResp struct {
	File struct {
		FileID      string `json:"file_id"`
		Bytes       int64  `json:"bytes,omitempty"`
		CreatedAt   int64  `json:"created_at,omitempty"`
		Filename    string `json:"filename,omitempty"`
		Purpose     string `json:"purpose,omitempty"`
		DownloadURL string `json:"download_url"`
	} `json:"file"`
	BaseResp platBaseResp `json:"base_resp"`
}

// ---------------------------------------------------------------------------
// Hub-facing wire shapes
// ---------------------------------------------------------------------------

type hubVideoSubmitReq struct {
	Model           string `json:"model"`
	Prompt          string `json:"prompt"`
	Ratio           string `json:"ratio,omitempty"`
	Duration        int    `json:"duration,omitempty"`
	Resolution      string `json:"resolution"`
	GenerateAudio   *bool  `json:"generate_audio,omitempty"`
	FirstFrameImage string `json:"first_frame_image,omitempty"`
	LastFrameImage  string `json:"last_frame_image,omitempty"`
}

type hubFileResp struct {
	File struct {
		// The hub returns this as a string, but the public platform API types it
		// as an int64 — accept either rather than failing to decode.
		FileID      json.RawMessage `json:"file_id"`
		Bytes       int64           `json:"bytes"`
		CreatedAt   int64           `json:"created_at"`
		Filename    string          `json:"filename"`
		Purpose     string          `json:"purpose"`
		DownloadURL string          `json:"download_url"`
	} `json:"file"`
	BaseResp platBaseResp `json:"base_resp"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleVideoGeneration serves POST /v1/video_generation (MiniMax platform shape).
func (s *Server) handleVideoGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	var req platVideoSubmitReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "body is not valid JSON: "+err.Error())
		return
	}
	// new-api folds `metadata` into the typed request before sending, but a
	// hand-rolled caller may nest the same fields under metadata instead.
	req.mergeMetadata()

	// `model_name` is the hub's own field name; accept it as an alias for
	// `model` so a caller who copied a hub payload by hand still works.
	if strings.TrimSpace(req.Model) == "" {
		req.Model = req.ModelName
	}
	target, err := resolveVideoTarget(req.Model)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" && req.FirstFrameImage == "" && req.VideoURL == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}
	if len(prompt) > 20000 {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "prompt exceeds 20000 characters")
		return
	}

	out, err := s.submitVideo(r.Context(), videoBackends[target.Backend], target.Model, &req, prompt)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	if out.Reason != "" {
		// The hub accepted the call and answered with a business error; pass it
		// through in the platform envelope rather than inventing an HTTP code.
		writeJSON(w, http.StatusOK, platVideoSubmitResp{
			BaseResp: platBaseResp{StatusCode: 1, StatusMsg: out.Reason},
		})
		return
	}
	if out.TaskID == "" {
		writeErr(w, http.StatusBadGateway, "upstream_error",
			"backend "+target.Backend+" accepted the request but returned no task_id")
		return
	}
	writeJSON(w, http.StatusOK, platVideoSubmitResp{
		TaskID:   encodeVideoTaskID(target.Backend, out.TaskID),
		BaseResp: platBaseResp{StatusCode: 0, StatusMsg: "success"},
	})
}

// handleVideoQuery serves GET /v1/query/video_generation?task_id=…
//
// The platform call carries no model, so the backend is recovered from the task
// id we handed out (see encodeVideoTaskID). Ids minted before multi-backend
// support are untagged and still resolve to minimax_v3.
func (s *Server) handleVideoQuery(w http.ResponseWriter, r *http.Request) {
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	backend, upstreamID := decodeVideoTaskID(taskID)
	state, err := s.queryVideoTask(r.Context(), backend, upstreamID)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}

	out := platVideoQueryResp{
		TaskID:   taskID,
		Status:   state.Status,
		BaseResp: platBaseResp{StatusCode: 0, StatusMsg: "success"},
	}
	if state.Status == "Fail" {
		out.BaseResp = platBaseResp{StatusCode: 1, StatusMsg: state.Reason}
	}
	if state.Status == "Success" {
		// minimax_v3 yields a file id to exchange; every other backend yields a
		// finished URL, which we percent-encode into the same slot so the
		// platform's /v1/files/retrieve handshake still works.
		out.FileID = firstNonEmpty(state.FileID, encodeMediaURL(state.VideoURL))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleVideoFile serves GET /v1/files/retrieve?file_id=…, resolving the hub's
// short-lived download URL. A file_id that is itself an absolute URL (see
// encodeMediaURL) is echoed straight back — those backends have no file to
// exchange.
func (s *Server) handleVideoFile(w http.ResponseWriter, r *http.Request) {
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	fileID := strings.TrimSpace(r.URL.Query().Get("file_id"))
	if fileID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "file_id is required")
		return
	}

	var out platFileRetrieveResp
	if direct, ok := decodeMediaURL(fileID); ok {
		out.File.FileID = fileID
		out.File.Purpose = "video"
		out.File.DownloadURL = direct
		out.BaseResp = platBaseResp{StatusCode: 0, StatusMsg: "success"}
		writeJSON(w, http.StatusOK, out)
		return
	}

	var hub hubFileResp
	if err := s.up.jsonCall(r.Context(), http.MethodGet, s.cfg.videoBase(),
		hubVideoFiles+fileID, nil, &hub); err != nil {
		writeUpstreamError(w, err)
		return
	}
	if hub.File.DownloadURL == "" {
		writeErr(w, http.StatusNotFound, "not_found_error", "video not ready")
		return
	}

	out.File.FileID = fileID
	out.File.Bytes = hub.File.Bytes
	out.File.CreatedAt = hub.File.CreatedAt
	out.File.Filename = hub.File.Filename
	out.File.Purpose = hub.File.Purpose
	out.File.DownloadURL = hub.File.DownloadURL
	out.BaseResp = platBaseResp{StatusCode: 0, StatusMsg: "success"}
	writeJSON(w, http.StatusOK, out)
}

// handleVideoModels serves GET /v1/video/models — the hub's video catalogue.
func (s *Server) handleVideoModels(w http.ResponseWriter, r *http.Request) {
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	models, err := s.videoCatalog(r.Context())
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": videoModelViews(models)})
}

// videoModelViews projects the hub's video catalogue into the shape callers
// see. It also surfaces the routing we will actually use, so a caller can tell
// which backend a model lands on without reading this source. Shared by the
// public endpoint and the admin console so the two cannot disagree.
func videoModelViews(models []videoModelEntry) []map[string]any {
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		entry := map[string]any{
			"id":         m.ID,
			"object":     "model",
			"created":    0,
			"owned_by":   m.Backend,
			"name":       m.Name,
			"hub_model":  m.ModelName,
			"resolution": m.params("resolution"),
			"ratio":      m.params("ratio"),
			"duration":   m.params("duration"),
		}
		if t, ok := videoModelTable[m.ID]; ok {
			spec := videoBackends[t.Backend]
			entry["backend"] = t.Backend
			entry["backend_model"] = t.Model
			entry["members_only"] = spec.MembersOnly
		} else {
			entry["backend"] = ""
			entry["members_only"] = false
		}
		data = append(data, entry)
	}
	return data
}

// ---------------------------------------------------------------------------
// Video catalogue (lazy, cached)
// ---------------------------------------------------------------------------

type videoModelEntry struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Backend   string                     `json:"backend"`
	ModelName string                     `json:"model_name"`
	Params    map[string]json.RawMessage `json:"params"`
}

// params returns the option list for a catalogue param, e.g. "resolution".
func (m videoModelEntry) params(key string) []string {
	raw, ok := m.Params[key]
	if !ok {
		return nil
	}
	var p struct {
		Default string   `json:"default"`
		Options []string `json:"options"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return p.Options
}

var (
	videoCatalogMu  sync.Mutex
	videoCatalogAt  time.Time
	videoCatalogVal []videoModelEntry
)

// videoCatalog fetches /api/v1/models/config once and caches it for 10 minutes.
func (s *Server) videoCatalog(ctx context.Context) ([]videoModelEntry, error) {
	videoCatalogMu.Lock()
	defer videoCatalogMu.Unlock()
	if len(videoCatalogVal) > 0 && time.Since(videoCatalogAt) < 10*time.Minute {
		return videoCatalogVal, nil
	}

	var resp struct {
		VideoModels []videoModelEntry `json:"videoModels"`
	}
	if err := s.up.jsonCall(ctx, http.MethodGet, s.cfg.videoBase(),
		hubModelsConfig, nil, &resp); err != nil {
		if len(videoCatalogVal) > 0 {
			return videoCatalogVal, nil // serve stale rather than fail
		}
		return nil, err
	}
	videoCatalogVal = resp.VideoModels
	videoCatalogAt = time.Now()
	return videoCatalogVal, nil
}

// ---------------------------------------------------------------------------
// Mapping helpers
// ---------------------------------------------------------------------------

// mapResolution folds the platform's 512P/720P/768P/1080P vocabulary onto the
// hub's 480P/768P/2K. Unknown values fall back to the model's ceiling-safe 768P.
func mapResolution(model, res string) string {
	res = strings.ToUpper(strings.TrimSpace(res))
	if res == "" {
		if maxVideoModels[model] {
			return "768P"
		}
		return "768P"
	}
	if maxVideoModels[model] {
		switch res {
		case "480P", "512P":
			return "480P"
		case "768P", "720P":
			return "768P"
		default:
			return "768P" // 1080P / 2K are beyond what Max renders
		}
	}
	switch res {
	case "2K", "1080P":
		return "2K"
	case "768P", "720P", "512P", "480P":
		return "768P"
	default:
		return "768P"
	}
}

// resolveRatio picks a concrete aspect ratio. The hub rejects "adaptive" for
// text-to-video, so a default is mandatory rather than optional. `aspect_ratio`
// is accepted as a synonym because the omni and veo backends spell it that way.
func resolveRatio(req *platVideoSubmitReq) string {
	r := firstNonEmpty(strings.TrimSpace(req.Ratio), strings.TrimSpace(req.AspectRatio))
	if r != "" && !strings.EqualFold(r, "adaptive") {
		for _, ok := range hubRatios {
			if strings.EqualFold(r, ok) {
				return ok
			}
		}
	}
	if ratio, ok := ratioFromSize(req.Size); ok {
		return ratio
	}
	return "16:9"
}

// ratioFromSize converts a "1280x720"-style size into the nearest catalogue
// ratio, so callers that only speak in pixels still get a sensible frame.
func ratioFromSize(size string) (string, bool) {
	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == 'X' || r == '*' || r == '×' })
	if len(parts) != 2 {
		return "", false
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return "", false
	}
	target := float64(w) / float64(h)
	best, bestDelta := "", 0.0
	for _, r := range hubRatios {
		a, b, ok := strings.Cut(r, ":")
		if !ok {
			continue
		}
		aw, errA := strconv.Atoi(a)
		ah, errB := strconv.Atoi(b)
		if errA != nil || errB != nil || ah == 0 {
			continue
		}
		delta := target - float64(aw)/float64(ah)
		if delta < 0 {
			delta = -delta
		}
		if best == "" || delta < bestDelta {
			best, bestDelta = r, delta
		}
	}
	return best, best != ""
}

// mapHubVideoStatus converts the hub's lowercase status into the platform's
// capitalised vocabulary that new-api switches on.
func mapHubVideoStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "success", "succeed", "succeeded", "completed", "complete", "done", "finished":
		return "Success"
	case "fail", "failed", "failure", "error", "cancelled", "canceled", "rejected":
		return "Fail"
	case "preparing", "prepare":
		return "Preparing"
	case "queueing", "queued", "pending", "waiting":
		return "Queueing"
	default:
		return "Processing"
	}
}

// mergeMetadata folds nested metadata fields into the top-level request. new-api
// already flattens these, but a direct caller may nest them.
func (r *platVideoSubmitReq) mergeMetadata() {
	if len(r.Metadata) == 0 {
		return
	}
	var meta struct {
		Ratio           string `json:"ratio"`
		AspectRatio     string `json:"aspect_ratio"`
		Resolution      string `json:"resolution"`
		Duration        int    `json:"duration"`
		Size            string `json:"size"`
		GenerateAudio   *bool  `json:"generate_audio"`
		PromptOptimizer *bool  `json:"prompt_optimizer"`
		FirstFrameImage string `json:"first_frame_image"`
		LastFrameImage  string `json:"last_frame_image"`
		Mode            string `json:"mode"`
		AudioURL        string `json:"sound_file"`
		VideoURL        string `json:"video_url"`
		ModelName       string `json:"model_name"`
	}
	if json.Unmarshal(r.Metadata, &meta) != nil {
		return
	}
	if r.Ratio == "" {
		r.Ratio = meta.Ratio
	}
	if r.AspectRatio == "" {
		r.AspectRatio = meta.AspectRatio
	}
	if r.Resolution == "" {
		r.Resolution = meta.Resolution
	}
	if r.Duration == 0 {
		r.Duration = meta.Duration
	}
	if r.Size == "" {
		r.Size = meta.Size
	}
	if r.GenerateAudio == nil {
		r.GenerateAudio = meta.GenerateAudio
	}
	if r.PromptOptimizer == nil {
		r.PromptOptimizer = meta.PromptOptimizer
	}
	if r.FirstFrameImage == "" {
		r.FirstFrameImage = meta.FirstFrameImage
	}
	if r.LastFrameImage == "" {
		r.LastFrameImage = meta.LastFrameImage
	}
	if r.Mode == "" {
		r.Mode = meta.Mode
	}
	if r.AudioURL == "" {
		r.AudioURL = meta.AudioURL
	}
	if r.VideoURL == "" {
		r.VideoURL = meta.VideoURL
	}
	if r.ModelName == "" {
		r.ModelName = meta.ModelName
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
