package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Multi-backend video support.
//
// The cloud gateway exposes one video backend per vendor. They share the host
// and the `token` header but nothing else: each has its own request body, its
// own status vocabulary and its own place to put the finished video.
//
//	backend                generate                                   query
//	minimax_v3  POST /api/v1/video/minimax-v3/generate   GET  .../minimax-v3/tasks/{id}
//	wan_i2v     POST /api/v1/video/wan3/generate         GET  .../wan3/tasks/{id}
//	veo3        POST /api/v1/video/veo3/generate?model=  GET  .../veo3/tasks/{id}
//	kling       POST /api/v1/video/kling/generate        GET  .../kling/tasks/{id}
//	kling_omni  POST /api/v1/video/kling-omni/generate   GET  .../kling-omni/tasks/{id}
//	kling_avatar         POST /api/v1/video/kling/avatar        GET  .../kling/avatar/{id}
//	kling_motion_control POST /api/v1/video/kling/motion-control GET .../kling/motion-control/{id}
//	jimeng_motion_control POST /api/v1/video/jimeng/generate     GET  .../jimeng/tasks/{id}
//
// Two structural facts drive the design below:
//
//  1. The platform shape this service serves has exactly one slot for the
//     artifact — `file_id`, which new-api then exchanges at
//     /v1/files/retrieve. Only minimax_v3 actually has a file id; every other
//     backend hands back a finished CDN URL. We therefore percent-encode the
//     URL into the `file_id` slot and teach /v1/files/retrieve to recognise an
//     absolute URL and echo it back. (Escaping matters: unescaped URLs would
//     be truncated at the first `&` by the caller's query parser.)
//
//  2. The query endpoint is chosen from the task id, but the platform's query
//     call carries no model or backend. Non-default backends therefore tag
//     their task ids as `{backend}~{upstream_id}`; the separator is RFC 3986
//     unreserved, so it survives every hop untouched.
//
// Some backends are gated behind a paid membership (see MembersOnly). That is
// a property of the account, not of this service, so we do not pre-empt the
// call — the hub's own 403 is far more informative than anything we could
// synthesise, and it passes through unchanged.
// ---------------------------------------------------------------------------

// videoTaskSep separates the backend tag from the upstream task id. It is
// unreserved in RFC 3986, so nothing along the way will re-encode it.
const videoTaskSep = "~"

// backend families — several catalogue backends share one implementation.
const (
	famMinimaxV3 = "minimax_v3"
	famWan3      = "wan3"
	famVeo3      = "veo3"
	famKling     = "kling"
	famKlingOmni = "kling_omni"
	famJimeng    = "jimeng"
)

// defaultVideoBackend is used when a caller names a bare H3 model, and is the
// backend implied by an untagged task id.
const defaultVideoBackend = "minimax_v3"

type videoBackendSpec struct {
	ID       string // backend id exactly as /api/v1/models/config spells it
	Generate string // upstream generate path
	Query    string // upstream task path; the task id is appended as /{id}
	Family   string
	// MembersOnly records backends the hub gates behind a paid subscription.
	// Purely informational: the hub decides, and its 403 is passed through.
	MembersOnly bool
}

var videoBackends = map[string]videoBackendSpec{
	"minimax_v3": {
		ID: "minimax_v3", Family: famMinimaxV3,
		Generate: "/api/v1/video/minimax-v3/generate",
		Query:    "/api/v1/video/minimax-v3/tasks",
	},
	// Wan 3.0 reuses the wan_i2v backend id — the catalogue comment says a new
	// backend id would break older clients' catalogue parsing, so the local
	// gateway picks the implementation from the model id instead. We follow the
	// model id to the wan3 endpoints, exactly as the client does.
	"wan_i2v": {
		ID: "wan_i2v", Family: famWan3,
		Generate: "/api/v1/video/wan3/generate",
		Query:    "/api/v1/video/wan3/tasks",
	},
	"veo3": {
		ID: "veo3", Family: famVeo3, MembersOnly: true,
		Generate: "/api/v1/video/veo3/generate",
		Query:    "/api/v1/video/veo3/tasks",
	},
	"kling": {
		ID: "kling", Family: famKling, MembersOnly: true,
		Generate: "/api/v1/video/kling/generate",
		Query:    "/api/v1/video/kling/tasks",
	},
	// Kling 3.0 Omni is reached through a *different* pair of paths than the
	// plain kling model, even though the catalogue files it under backend
	// "kling" and distinguishes it by model_name. We give it its own spec so
	// the task-id tag alone determines where the poll goes.
	"kling_omni": {
		ID: "kling_omni", Family: famKlingOmni, MembersOnly: true,
		Generate: "/api/v1/video/kling-omni/generate",
		Query:    "/api/v1/video/kling-omni/tasks",
	},
	"kling_avatar": {
		ID: "kling_avatar", Family: famKling, MembersOnly: true,
		Generate: "/api/v1/video/kling/avatar",
		Query:    "/api/v1/video/kling/avatar",
	},
	"kling_motion_control": {
		ID: "kling_motion_control", Family: famKling, MembersOnly: true,
		Generate: "/api/v1/video/kling/motion-control",
		Query:    "/api/v1/video/kling/motion-control",
	},
	"jimeng_motion_control": {
		ID: "jimeng_motion_control", Family: famJimeng, MembersOnly: true,
		Generate: "/api/v1/video/jimeng/generate",
		Query:    "/api/v1/video/jimeng/tasks",
	},
}

// ---------------------------------------------------------------------------
// Model → (backend, upstream model) routing
// ---------------------------------------------------------------------------

// videoTarget is a resolved destination for one video request.
type videoTarget struct {
	Backend string // key into videoBackends
	Model   string // value the backend expects in its own model field
	Name    string // catalogue display name, for diagnostics
}

// videoModelTable is the single source of truth for video routing. The
// catalogue's own `backend` field is *not* sufficient: several backends share
// one implementation, and which one is used is decided from the model id — the
// desktop client does exactly the same lookup (`if (modelName === "kling-v3-omni")
// return this.generateOmniVideo(...)`), so we mirror it rather than trust
// `backend` alone.
//
// Keeping it static (instead of reading the live catalogue on every request)
// means routing still works when the catalogue endpoint is down, and a typo in
// a model name fails fast with a list of what *is* supported.
var videoModelTable = map[string]videoTarget{
	// MiniMax H3 line — the only backend with a real file_id artifact.
	"MiniMax-H3":           {Backend: "minimax_v3", Model: "MiniMax-H3", Name: "MiniMax H3"},
	"MiniMax-H3-Max":       {Backend: "minimax_v3", Model: "MiniMax-H3-Max", Name: "MiniMax H3 Max"},
	"MiniMax-H3-Max-Turbo": {Backend: "minimax_v3", Model: "MiniMax-H3-Max-Turbo", Name: "MiniMax H3 Max Turbo"},

	// Wan 3.0 reuses the catalogue's wan_i2v backend id but its own paths.
	"wan3.0-video":       {Backend: "wan_i2v", Model: "wan3.0-video", Name: "Wan 3.0"},
	"wan3.0-video-prime": {Backend: "wan_i2v", Model: "wan3.0-video-prime", Name: "Wan 3.0 Prime"},

	// Kling: three separate endpoints that the catalogue splits by model id.
	"kling-v3-omni-video":  {Backend: "kling_omni", Model: "kling-v3-omni", Name: "Kling 3.0 Omni"},
	"kling-avatar":         {Backend: "kling_avatar", Name: "Kling Avatar"},
	"kling-motion-control": {Backend: "kling_motion_control", Name: "Kling Motion Control"},

	"jimeng_motion_control": {Backend: "jimeng_motion_control", Name: "Jimeng Motion Control 2.0"},

	"veo-3.1-fast-generate-001": {Backend: "veo3", Model: "veo-3.1-fast-generate-001", Name: "Veo3.1 Fast"},
	"veo-3.1-generate-001":      {Backend: "veo3", Model: "veo-3.1-generate-001", Name: "Veo3.1"},
}

// resolveVideoTarget maps whatever the caller named onto a backend plus the
// model string that backend expects. It accepts, in order:
//
//  1. a catalogue model id ("wan3.0-video")
//  2. a provider-qualified id ("wan_i2v/wan3.0-video")
//  3. a public MiniMax platform name ("MiniMax-Hailuo-2.3") — what new-api's
//     hailuo channel sends, since it knows nothing about this hub's catalogue
//  4. a bare backend id ("veo3"), which lets the backend apply its own default
func resolveVideoTarget(name string) (videoTarget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return videoModelTable["MiniMax-H3"], nil
	}
	if t, ok := videoModelTable[name]; ok {
		return t, nil
	}
	// "backend/model" — resolve the tail, then keep the named backend.
	if head, tail, ok := strings.Cut(name, "/"); ok {
		if t, ok := videoModelTable[tail]; ok {
			if _, known := videoBackends[head]; known {
				t.Backend = head
			}
			return t, nil
		}
	}
	if alias, ok := hailuoAliases[name]; ok {
		return videoTarget{Backend: "minimax_v3", Model: alias, Name: name}, nil
	}
	if _, ok := videoBackends[name]; ok {
		return videoTarget{Backend: name, Name: name}, nil
	}
	return videoTarget{}, fmt.Errorf("unsupported video model %q; try one of %s",
		name, strings.Join(videoModelNames(), ", "))
}

// videoModelNames lists the catalogue model ids, for error messages.
func videoModelNames() []string {
	out := make([]string, 0, len(videoModelTable))
	for k := range videoModelTable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Artifact slot encoding
// ---------------------------------------------------------------------------
//
// The platform shape carries exactly one artifact slot: `file_id`, which the
// caller exchanges at /v1/files/retrieve. minimax_v3 has a genuine file id;
// every other backend hands back a finished CDN URL. We percent-encode the URL
// into that slot and teach /v1/files/retrieve to recognise an absolute URL.
//
// Escaping is load-bearing: an unescaped URL would be cut at the first `&` by
// the caller's query parser, silently truncating signed CDN links.
// ---------------------------------------------------------------------------

// encodeMediaURL puts a finished media URL into the file_id slot.
func encodeMediaURL(u string) string { return url.QueryEscape(u) }

// decodeMediaURL recognises an encoded absolute URL in the file_id slot and
// returns it. The second result is false for a genuine file id.
func decodeMediaURL(fileID string) (string, bool) {
	if strings.HasPrefix(fileID, "http://") || strings.HasPrefix(fileID, "https://") {
		return fileID, true
	}
	// A file id minted by an older build of this service was not escaped, so
	// a raw URL may still arrive. Treat that as a URL too.
	if unescaped, err := url.QueryUnescape(fileID); err == nil && unescaped != fileID {
		if strings.HasPrefix(unescaped, "http://") || strings.HasPrefix(unescaped, "https://") {
			return unescaped, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Task id tagging
// ---------------------------------------------------------------------------

// encodeVideoTaskID tags a non-default backend's task id so the query call —
// which carries no backend — can find its way home.
func encodeVideoTaskID(backend, upstreamID string) string {
	if backend == "" || backend == defaultVideoBackend || upstreamID == "" {
		return upstreamID
	}
	return backend + videoTaskSep + upstreamID
}

// decodeVideoTaskID splits a tagged task id. Untagged ids belong to the
// default backend, which keeps ids minted by earlier versions working.
func decodeVideoTaskID(id string) (backend, upstreamID string) {
	if head, tail, ok := strings.Cut(id, videoTaskSep); ok && tail != "" {
		if _, known := videoBackends[head]; known {
			return head, tail
		}
	}
	return defaultVideoBackend, id
}

// ---------------------------------------------------------------------------
// Normalised result
// ---------------------------------------------------------------------------

// videoTaskState is one backend's answer, flattened into the vocabulary the
// platform shape uses. Exactly one of FileID / VideoURL is set.
type videoTaskState struct {
	Status   string // platform vocabulary: Success / Fail / Preparing / Queueing / Processing
	Reason   string
	FileID   string // minimax_v3 only — exchanged at /v1/files/retrieve
	VideoURL string // every other backend — a finished CDN URL
}

// videoSubmitOutcome is what a backend returns after accepting a task.
type videoSubmitOutcome struct {
	TaskID string
	Reason string
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

// submitVideo posts a generation request to the right backend and returns the
// upstream task id.
func (s *Server) submitVideo(ctx context.Context, spec videoBackendSpec, model string,
	req *platVideoSubmitReq, prompt string) (videoSubmitOutcome, error) {

	path := spec.Generate
	var body any

	switch spec.Family {
	case famMinimaxV3:
		h3 := hubVideoSubmitReq{
			Model:           model,
			Prompt:          prompt,
			Duration:        clampVideoDuration(spec.Family, model, req.Duration),
			Resolution:      mapResolution(model, req.Resolution),
			GenerateAudio:   req.GenerateAudio,
			FirstFrameImage: req.FirstFrameImage,
			LastFrameImage:  req.LastFrameImage,
		}
		// first-last-frame disables every concrete ratio in the catalogue, so
		// only send one for the text/reference path — which is also the path
		// that hard-requires it.
		if req.FirstFrameImage == "" && req.LastFrameImage == "" {
			h3.Ratio = resolveRatio(req)
		}
		body = h3

	case famWan3:
		w := hubWan3SubmitReq{
			Model:           model,
			Prompt:          prompt,
			Resolution:      mapWan3Resolution(req.Resolution),
			Duration:        clampVideoDuration(spec.Family, model, req.Duration),
			Audio:           req.GenerateAudio,
			FirstFrameImage: req.FirstFrameImage,
			LastFrameImage:  req.LastFrameImage,
		}
		if req.FirstFrameImage == "" && req.LastFrameImage == "" {
			w.Ratio = resolveRatio(req)
		}
		body = w

	case famVeo3:
		// Veo3 takes a Google-style envelope and picks the model from the query
		// string rather than the body.
		v := hubVeo3SubmitReq{Instances: []hubVeo3Instance{{Prompt: prompt}}}
		v.Parameters.DurationSeconds = clampVideoDuration(spec.Family, model, req.Duration)
		v.Parameters.AspectRatio = resolveRatio(req)
		v.Parameters.Resolution = mapVeo3Resolution(req.Resolution)
		v.Parameters.GenerateAudio = true
		v.Parameters.PersonGeneration = "allow_all"
		body = v
		path += "?model=" + url.QueryEscape(model)

	case famKling:
		k := map[string]any{
			"mode": req.KlingMode(),
		}
		switch spec.ID {
		case "kling_avatar":
			// Avatar needs a portrait plus a voice track; both are plain URLs.
			k["image"] = req.FirstFrameImage
			k["sound_file"] = req.AudioURL
			k["prompt"] = prompt
		case "kling_motion_control":
			k["image_url"] = req.FirstFrameImage
			k["video_url"] = req.VideoURL
			k["character_orientation"] = firstNonEmpty(req.CharacterOrientation, "video")
			k["keep_original_sound"] = firstNonEmpty(req.KeepOriginalSound, "yes")
			k["prompt"] = prompt
		default:
			// The plain kling model inlines the first frame as raw base64, not
			// as a URL. Callers of this service speak URLs, so fetch and encode.
			k["model_name"] = model
			k["prompt"] = prompt
			k["duration"] = strconv.Itoa(klingDuration(spec.Family, model, req.Duration))
			if img := strings.TrimSpace(req.FirstFrameImage); img != "" {
				if b64, err := inlineImage(ctx, img); err == nil {
					k["image"] = b64
				}
			}
			if req.KlingMode() == "pro" && req.GenerateAudio != nil {
				k["sound"] = map[bool]string{true: "on", false: "off"}[*req.GenerateAudio]
			}
		}
		body = k

	case famKlingOmni:
		// Omni takes reference images/videos as lists and plans its own shots.
		k := map[string]any{
			"model_name":   model,
			"prompt":       prompt,
			"mode":         req.KlingMode(),
			"aspect_ratio": resolveRatio(req),
			"duration":     strconv.Itoa(klingDuration(spec.Family, model, req.Duration)),
		}
		// kling-video-o1 silently drops native audio; only the omni model keeps it.
		if model == "kling-v3-omni" && req.GenerateAudio != nil && *req.GenerateAudio {
			k["sound"] = "on"
		} else {
			k["sound"] = "off"
		}
		if img := strings.TrimSpace(req.FirstFrameImage); img != "" {
			k["image_list"] = []map[string]any{{"image_url": img}}
		}
		if vid := strings.TrimSpace(req.VideoURL); vid != "" {
			k["video_list"] = []map[string]any{{"video_url": vid}}
		}
		body = k

	case famJimeng:
		// Jimeng is motion control only: a character image plus a reference
		// video whose motion gets transferred. The image list is always sent,
		// even empty, exactly as the client does.
		images := []string{}
		if img := strings.TrimSpace(req.FirstFrameImage); img != "" {
			images = append(images, img)
		}
		body = map[string]any{
			"image_urls": images,
			"video_url":  req.VideoURL,
		}

	default:
		return videoSubmitOutcome{}, fmt.Errorf("backend %q has no submit implementation", spec.ID)
	}

	// Every backend answers with a different envelope, so decode into a generic
	// map and dig the fields out rather than guessing a struct per vendor.
	var raw map[string]any
	if err := s.up.jsonCall(ctx, http.MethodPost, s.cfg.videoBase(), path, body, &raw); err != nil {
		return videoSubmitOutcome{}, err
	}

	if msg := digError(raw); msg != "" {
		return videoSubmitOutcome{Reason: msg}, nil
	}
	taskID := digString(raw, "task_id", "output.task_id", "data.task_id", "id")
	if taskID == "" {
		return videoSubmitOutcome{}, fmt.Errorf(
			"backend %q accepted the request but returned no task id: %s", spec.ID, truncateJSON(raw, 300))
	}
	return videoSubmitOutcome{TaskID: taskID}, nil
}

// ---------------------------------------------------------------------------
// Query
// ---------------------------------------------------------------------------

// queryVideoTask polls one backend and normalises the answer.
func (s *Server) queryVideoTask(ctx context.Context, backend, upstreamID string) (videoTaskState, error) {
	spec, ok := videoBackends[backend]
	if !ok {
		spec = videoBackends[defaultVideoBackend]
	}

	var raw map[string]any
	err := s.up.jsonCall(ctx, http.MethodGet, s.cfg.videoBase(), spec.Query+"/"+upstreamID, nil, &raw)
	if err != nil {
		if ue, isUpstream := err.(*upstreamError); isUpstream && ue.Status == http.StatusNotFound {
			// The hub 404s while a task is still unknown to it. Report that as
			// in-flight rather than an error so pollers keep waiting.
			return videoTaskState{Status: "Processing"}, nil
		}
		return videoTaskState{}, err
	}

	// Status first, vendor code only as a fallback.
	//
	// Kling and Jimeng stamp a vendor code (0 / 10000 = success) on their
	// responses, but a query answer that carries a perfectly good status does
	// not need the code interpreted — and treating a non-zero code as fatal
	// would risk declaring a running task dead. So the code is consulted only
	// when there is no status to read at all.
	status := firstNonEmpty(
		digString(raw, "output.task_status", "data.task_status", "data.status", "status"),
	)
	if status == "" {
		if msg := digError(raw); msg != "" {
			return videoTaskState{Status: "Fail", Reason: msg}, nil
		}
	}

	out := videoTaskState{
		Status: mapHubVideoStatus(status),
		Reason: firstNonEmpty(digString(raw, "message", "user_message", "data.task_status_msg", "error_message")),
	}

	if out.Status == "Success" {
		out.VideoURL = digString(raw,
			"output.video_url", "data.video_url", "video_url",
			"data.task_result.videos.0.url")
		out.FileID = digString(raw, "file_id", "data.file_id")
		if out.VideoURL == "" && out.FileID == "" {
			return videoTaskState{Status: "Fail",
				Reason: fmt.Sprintf("backend %q reported success but returned no artifact: %s",
					spec.ID, truncateJSON(raw, 300))}, nil
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

type hubWan3SubmitReq struct {
	Model           string `json:"model"`
	Prompt          string `json:"prompt"`
	Resolution      string `json:"resolution,omitempty"`
	Ratio           string `json:"ratio,omitempty"`
	Duration        int    `json:"duration,omitempty"`
	Audio           *bool  `json:"audio,omitempty"`
	FirstFrameImage string `json:"first_frame_image,omitempty"`
	LastFrameImage  string `json:"last_frame_image,omitempty"`
}

type hubVeo3Instance struct {
	Prompt string `json:"prompt"`
}

type hubVeo3SubmitReq struct {
	Instances  []hubVeo3Instance `json:"instances"`
	Parameters struct {
		DurationSeconds  int    `json:"duration_seconds"`
		AspectRatio      string `json:"aspect_ratio"`
		Resolution       string `json:"resolution"`
		GenerateAudio    bool   `json:"generate_audio"`
		PersonGeneration string `json:"person_generation"`
	} `json:"parameters"`
}

// ---------------------------------------------------------------------------
// Resolution / ratio vocabularies per backend
// ---------------------------------------------------------------------------

// mapWan3Resolution folds the platform's pixel vocabulary onto Wan 3.0's
// 480P / 720P / 1080P ladder.
func mapWan3Resolution(res string) string {
	switch strings.ToUpper(strings.TrimSpace(res)) {
	case "480P", "512P":
		return "480P"
	case "1080P", "2K":
		return "1080P"
	case "720P", "768P":
		return "720P"
	default:
		return "" // let the hub apply its own default (1080P)
	}
}

// mapVeo3Resolution returns the lowercase 720p / 1080p spelling Veo3 wants.
func mapVeo3Resolution(res string) string {
	switch strings.ToUpper(strings.TrimSpace(res)) {
	case "1080P", "2K":
		return "1080p"
	default:
		return "720p"
	}
}

// durationBounds returns the inclusive [min, max] seconds a backend+model
// accepts. A (0, 0) result means the backend has no duration field at all.
//
// These come from `params.duration.options` in /api/v1/models/config. They are
// duplicated here rather than read from the live catalogue so that a malformed
// request fails fast, with a value we know is legal, even when the catalogue
// endpoint is unreachable.
func durationBounds(family, model string) (int, int) {
	switch family {
	case famMinimaxV3:
		// The H3 line shares one ladder, but Max and Turbo start two seconds
		// higher than the base model.
		//
		// Match on "-Max", not "Max": the vendor prefix is "MiniMax", so a
		// substring test would classify every H3 model as a Max variant.
		if strings.Contains(model, "-Max") {
			return 5, 15
		}
		return 4, 15
	case famWan3:
		return 2, 30
	case famVeo3:
		// Veo3.1 accepts exactly one length. Not a range — a single value.
		return 8, 8
	case famKling, famKlingOmni:
		return 3, 15
	}
	return 0, 0
}

// clampVideoDuration folds a caller-supplied duration onto what the backend
// accepts. Zero means "no preference" and is passed through as zero so the
// backend applies its own default.
//
// This matters more than it looks. new-api's hailuo channel has no config entry
// for any of the non-Hailuo models, so it fills in its fallback of 6 seconds;
// Veo3.1 accepts only 8. Without the clamp an ordinary request dies upstream
// with an opaque parameter error that looks like a broken channel.
func clampVideoDuration(family, model string, d int) int {
	lo, hi := durationBounds(family, model)
	if lo == 0 && hi == 0 {
		return 0 // backend has no duration concept
	}
	if lo == hi {
		return lo // fixed-length model
	}
	if d <= 0 {
		return 0 // let the backend pick
	}
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// ---------------------------------------------------------------------------
// Generic JSON digging
//
// Every backend names the same three things differently (task id, status,
// artifact) and nests them at a different depth. Rather than model seven
// envelopes, we walk a dotted path with array indices.
// ---------------------------------------------------------------------------

// dig walks a dotted path such as "data.task_result.videos.0.url". Numeric
// segments index into arrays. It returns nil as soon as the path leaves the
// document.
func dig(doc map[string]any, path string) any {
	var cur any = doc
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[seg]
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

// digString returns the first non-empty string at any of the given paths.
func digString(doc map[string]any, paths ...string) string {
	for _, p := range paths {
		switch v := dig(doc, p).(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case json.Number:
			return v.String()
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

// digError extracts a human-readable failure message from whichever envelope
// the backend used, but only when it actually signals failure. Returning ""
// means "no error reported".
func digError(doc map[string]any) string {
	// Kling and Jimeng stamp a vendor code on every response; 0 and 10000 are
	// their respective success values.
	for _, p := range []string{"code", "data.code"} {
		if code := dig(doc, p); code != nil {
			if n, ok := asInt(code); ok && n != 0 && n != 10000 {
				return firstNonEmpty(
					digString(doc, "message", "user_message", "data.task_status_msg", "msg"),
					fmt.Sprintf("upstream returned code %d", n))
			}
		}
	}
	// minimax_v3 uses the platform's own base_resp.
	if br, ok := dig(doc, "base_resp").(map[string]any); ok {
		if n, ok := asInt(br["status_code"]); ok && n != 0 {
			msg, _ := br["status_msg"].(string)
			return firstNonEmpty(strings.TrimSpace(msg), fmt.Sprintf("base_resp status_code %d", n))
		}
	}
	// A top-level `error` object (or string) is the hub's generic shape.
	switch e := dig(doc, "error").(type) {
	case map[string]any:
		return digString(e, "message", "type")
	case string:
		if strings.TrimSpace(e) != "" {
			return strings.TrimSpace(e)
		}
	}
	return ""
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	}
	return 0, false
}

func truncateJSON(doc map[string]any, n int) string {
	b, err := json.Marshal(doc)
	if err != nil {
		return "(unprintable)"
	}
	return truncate(string(b), n)
}

// klingDuration clamps onto the kling ladder and substitutes the catalogue's
// default of 5s when the caller expressed no preference. Unlike the other
// backends, kling's `duration` is a required string field and can never be
// omitted, so a zero here has to become a real number rather than a dropped
// key.
func klingDuration(family, model string, d int) int {
	if v := clampVideoDuration(family, model, d); v > 0 {
		return v
	}
	return 5
}

// ---------------------------------------------------------------------------
// Media inlining
// ---------------------------------------------------------------------------

// maxInlineImage bounds what we are willing to fetch and base64-encode. The
// plain kling model wants the first frame inline rather than by URL, so a
// caller handing us a URL forces a fetch; cap it rather than let a huge or
// hostile URL exhaust memory.
const maxInlineImage = 12 << 20

// inlineImage turns an image reference into the raw base64 the plain kling
// model expects. Values that are already base64 (or a data URI) pass through;
// http(s) URLs are fetched. Base64 inflates by 4/3, so the caller-side limit is
// deliberately below maxInlineImage.
func inlineImage(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty image reference")
	}
	// data:image/png;base64,AAAA -> AAAA
	if _, tail, ok := strings.Cut(ref, ";base64,"); ok {
		return tail, nil
	}
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		return ref, nil // already raw base64
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: HTTP %d", ref, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxInlineImage+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxInlineImage {
		return "", fmt.Errorf("image at %s exceeds the %d byte inline limit", ref, maxInlineImage)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
