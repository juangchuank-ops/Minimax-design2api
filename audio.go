package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Audio: speech synthesis, voice catalogue, lyrics, music.
//
// Everything here lives on the media gateway (design.minimax.io) and is
// authorised by the same `token` header as the video and image endpoints.
//
//	POST {media}/api/v1/audio/tts              -> {audio_url, subtitle_url, base}   sync
//	GET  {media}/api/v1/audio/voices           -> {voices[], total, base}
//	POST {media}/api/v1/audio/lyrics/generate  -> {song_title, style_tags, lyrics}  sync, free
//	POST {media}/api/v2/audio/music/minimax    -> {task_id, status, base}           async
//	GET  {media}/api/v2/audio/music/minimax/tasks/{id} -> {status, audio_url, ...}
//
// TTS is served in the **OpenAI shape** (`POST /v1/audio/speech`) because that
// is what new-api's type-1 channel speaks, and its handler for that route is a
// pure byte pipe: whatever this service returns becomes the response body. So
// unlike every other endpoint here, the success response is *binary*, not JSON.
// ---------------------------------------------------------------------------

const (
	hubTTSPath        = "/api/v1/audio/tts"
	hubVoicesPath     = "/api/v1/audio/voices"
	hubLyricsPath     = "/api/v1/audio/lyrics/generate"
	hubMusicSubmit    = "/api/v2/audio/music/minimax"
	hubMusicQuery     = "/api/v2/audio/music/minimax/tasks/"
	hubMusicModel     = "music-3.0"
	speechDefaultTTS  = "speech-2.8-hd"
	speechMaxChars    = 10000
	speechMinSpeed    = 0.5
	speechMaxSpeed    = 2.0
	speechDefaultLang = "auto"
)

// ---------------------------------------------------------------------------
// TTS
// ---------------------------------------------------------------------------

// oaiSpeechReq is the OpenAI /v1/audio/speech body. `metadata` is the escape
// hatch new-api uses to forward non-OpenAI knobs, so MiniMax-only fields are
// accepted both nested under it and at the top level.
type oaiSpeechReq struct {
	Model          string          `json:"model"`
	Input          string          `json:"input"`
	Voice          string          `json:"voice"`
	Instructions   string          `json:"instructions"`
	ResponseFormat string          `json:"response_format"`
	Speed          *float64        `json:"speed"`
	StreamFormat   string          `json:"stream_format"`
	Metadata       json.RawMessage `json:"metadata"`

	// MiniMax passthroughs.
	LanguageBoost  string   `json:"language_boost"`
	Emotion        string   `json:"emotion"`
	Vol            *float64 `json:"vol"`
	Pitch          *float64 `json:"pitch"`
	SubtitleEnable *bool    `json:"subtitle_enable"`
}

// hubTTSReq mirrors the client's buildTTSBody() field for field.
type hubTTSReq struct {
	Model          string   `json:"model"`
	Text           string   `json:"text"`
	VoiceID        string   `json:"voice_id"`
	Speed          float64  `json:"speed"`
	LanguageBoost  string   `json:"language_boost,omitempty"`
	SubtitleEnable bool     `json:"subtitle_enable"`
	Emotion        string   `json:"emotion,omitempty"`
	Vol            *float64 `json:"vol,omitempty"`
	Pitch          *float64 `json:"pitch,omitempty"`
}

type hubTTSResp struct {
	AudioURL    string  `json:"audio_url"`
	SubtitleURL string  `json:"subtitle_url"`
	Duration    float64 `json:"duration"`
	Base        struct {
		Code        int    `json:"code"`
		Message     string `json:"message"`
		UserMessage string `json:"user_message"`
	} `json:"base"`
}

func (r *oaiSpeechReq) mergeMetadata() {
	if len(r.Metadata) == 0 {
		return
	}
	var meta struct {
		LanguageBoost  string   `json:"language_boost"`
		Emotion        string   `json:"emotion"`
		Vol            *float64 `json:"vol"`
		Pitch          *float64 `json:"pitch"`
		SubtitleEnable *bool    `json:"subtitle_enable"`
		VoiceID        string   `json:"voice_id"`
		Speed          *float64 `json:"speed"`
	}
	if json.Unmarshal(r.Metadata, &meta) != nil {
		return
	}
	if r.LanguageBoost == "" {
		r.LanguageBoost = meta.LanguageBoost
	}
	if r.Emotion == "" {
		r.Emotion = meta.Emotion
	}
	if r.Vol == nil {
		r.Vol = meta.Vol
	}
	if r.Pitch == nil {
		r.Pitch = meta.Pitch
	}
	if r.SubtitleEnable == nil {
		r.SubtitleEnable = meta.SubtitleEnable
	}
	if r.Voice == "" {
		r.Voice = meta.VoiceID
	}
	if r.Speed == nil {
		r.Speed = meta.Speed
	}
}

// speechModelAliases folds the OpenAI TTS model names onto the single model the
// hub actually serves. `speech-2.8-hd` is the only supported version — the
// client's own catalogue comment says so ("当前唯一支持的版本").
var speechModelAliases = map[string]string{
	"tts-1":           speechDefaultTTS,
	"tts-1-hd":        speechDefaultTTS,
	"gpt-4o-mini-tts": speechDefaultTTS,
	"speech-2.8":      speechDefaultTTS,
	"speech-02":       speechDefaultTTS,
	"speech-2.6":      speechDefaultTTS,
}

// openAIVoiceAliases maps OpenAI's fixed voice names onto real MiniMax voice
// ids picked to match each one's character. Any MiniMax `voice_id` is also
// accepted verbatim, so the full 671-entry catalogue stays reachable.
//
// Every target MUST exist in `GET {gateway}/api/v1/audio/voices`. The hub
// answers an unknown id with 500 / `code=2054 voice id not exist`, so a typo
// here surfaces as a broken endpoint rather than a bad parameter.
//
// Don't hand-write plausible-looking ids ("Young_Female", "Deep_Male", …).
// The hub's internal naming is inconsistent — `English_CalmWoman` has no
// underscore before "Woman", `English_Gentle-voiced_man` has a hyphen — so
// guessing fails even for names that look canonical. Verify with
// `tools/check_voices.py`, which diffs this table against the live catalogue.
var openAIVoiceAliases = map[string]string{
	"alloy":   "English_FriendlyPerson",         // neutral, smooth/warm
	"echo":    "English_Trustworth_Man",         // male, warm/resonant/earthy
	"fable":   "English_expressive_narrator",    // storyteller, crisp/modulated
	"onyx":    "English_Deep-tonedMan",          // male, deep/gravelly
	"nova":    "English_Upbeat_Woman",           // female, bright/crisp
	"shimmer": "English_CalmWoman",              // female, smooth/warm
	"coral":   "English_radiant_girl",           // female, bright/energetic
	"ash":     "English_Gentle-voiced_man",      // male, warm/breathy/soothing
	"sage":    "English_Wiselady",               // female, velvety/sophisticated
	"ballad":  "English_CaptivatingStoryteller", // male, deep/resonant
	"verse":   "English_ThoughtfulMan",          // male, clear/smooth
}

// defaultVoice is used when the caller omits `voice`. Must also be in the
// catalogue — see the note on openAIVoiceAliases.
const defaultVoice = "English_FriendlyPerson"

// Do NOT validate voice ids against /api/v1/audio/voices before forwarding.
// The catalogue is not exhaustive: the hub still accepts legacy ids that it no
// longer lists (e.g. `Friendly_Person` synthesises fine but is absent from the
// 671 entries). Pre-validating would reject working voices to catch typos that
// the hub already reports clearly — `code=2054 voice id not exist`.

func resolveSpeechModel(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return speechDefaultTTS
	}
	if alias, ok := speechModelAliases[strings.ToLower(n)]; ok {
		return alias
	}
	// Accept a provider-qualified id like "minimax_tts/speech-2.8-hd".
	if _, tail, ok := strings.Cut(n, "/"); ok {
		if tail != "" {
			return tail
		}
	}
	return n
}

func resolveVoice(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return defaultVoice
	}
	if alias, ok := openAIVoiceAliases[strings.ToLower(n)]; ok {
		return alias
	}
	return n
}

func clampSpeed(v float64) float64 {
	switch {
	case v <= 0:
		return 1
	case v < speechMinSpeed:
		return speechMinSpeed
	case v > speechMaxSpeed:
		return speechMaxSpeed
	default:
		return v
	}
}

// handleSpeech serves POST /v1/audio/speech.
//
// The success path writes raw audio bytes, not JSON: new-api's OpenaiTTSHandler
// copies the upstream body straight through to the caller, so anything else
// would arrive as a corrupt audio file.
func (s *Server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	var req oaiSpeechReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "body is not valid JSON: "+err.Error())
		return
	}
	req.mergeMetadata()

	text := strings.TrimSpace(req.Input)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "input is required")
		return
	}
	if len([]rune(text)) > speechMaxChars {
		writeErr(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("input exceeds %d characters", speechMaxChars))
		return
	}

	speed := 1.0
	if req.Speed != nil {
		speed = clampSpeed(*req.Speed)
	}
	// subtitle_enable makes the hub emit a timed transcript alongside the
	// audio. It costs nothing extra and the URL is handed back in a header.
	subtitle := req.SubtitleEnable == nil || *req.SubtitleEnable

	upReq := hubTTSReq{
		Model:          resolveSpeechModel(req.Model),
		Text:           text,
		VoiceID:        resolveVoice(req.Voice),
		Speed:          speed,
		LanguageBoost:  firstNonEmpty(req.LanguageBoost, speechDefaultLang),
		SubtitleEnable: subtitle,
		Emotion:        strings.TrimSpace(req.Emotion),
		Vol:            req.Vol,
		Pitch:          req.Pitch,
	}

	var upResp hubTTSResp
	if err := s.up.jsonCall(r.Context(), http.MethodPost, s.cfg.videoBase(),
		hubTTSPath, upReq, &upResp); err != nil {
		writeUpstreamError(w, err)
		return
	}
	if upResp.AudioURL == "" {
		msg := firstNonEmpty(upResp.Base.UserMessage, upResp.Base.Message)
		if msg == "" {
			msg = "upstream returned no audio_url"
		}
		writeErr(w, http.StatusBadGateway, "upstream_error", msg)
		return
	}

	audio, err := fetchMediaBytes(r.Context(), upResp.AudioURL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "upstream_error",
			"audio was generated but could not be downloaded: "+err.Error())
		return
	}

	h := w.Header()
	h.Set("Content-Type", audioContentType(upResp.AudioURL, req.ResponseFormat))
	h.Set("Content-Length", strconv.Itoa(len(audio)))
	// Non-standard extras: harmless to OpenAI clients, useful for debugging and
	// for callers that want the timed transcript.
	if upResp.SubtitleURL != "" {
		h.Set("X-Subtitle-Url", upResp.SubtitleURL)
	}
	h.Set("X-Audio-Source-Url", upResp.AudioURL)
	if upResp.Duration > 0 {
		h.Set("X-Audio-Duration", strconv.FormatFloat(upResp.Duration, 'f', -1, 64))
	}
	h.Set("X-Voice-Id", upReq.VoiceID)
	h.Set("X-Speech-Model", upReq.Model)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

// audioContentType prefers the real container implied by the CDN URL. The hub
// only ever produces mp3 today, so `response_format` is advisory — pretending
// otherwise would hand the caller a file whose extension lies.
func audioContentType(url, requested string) string {
	ext := strings.ToLower(path.Ext(url))
	switch ext {
	case ".mp3", ".mpeg", ".mpga":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".aac", ".m4a":
		return "audio/aac"
	}
	_ = requested
	return "audio/mpeg"
}

// ---------------------------------------------------------------------------
// Voice catalogue
// ---------------------------------------------------------------------------

type hubVoice struct {
	VoiceID     string `json:"voice_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Gender      string `json:"gender"`
	Age         string `json:"age"`
	Accent      string `json:"accent"`
	SampleAudio string `json:"sample_audio"`
}

type hubVoicesResp struct {
	Voices []hubVoice `json:"voices"`
	Total  int        `json:"total"`
	Base   struct {
		Message string `json:"message"`
	} `json:"base"`
}

// handleVoices serves GET /v1/audio/voices. The hub returns 671 voices across
// 60 language buckets; `language` / `q` narrow it server-side here so callers
// do not have to pull the whole table.
func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	up, err := s.voiceCatalog(r.Context())
	if err != nil {
		writeUpstreamError(w, err)
		return
	}

	data := voiceViews(up, r.URL.Query().Get("language"), r.URL.Query().Get("q"))
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
		"total":  len(data),
	})
}

var (
	voicesMu  sync.Mutex
	voicesVal *hubVoicesResp
	voicesAt  time.Time
)

// voiceCatalog fetches the hub's voice table, cached for 10 minutes.
//
// The table is 671 rows that do not change within a session, but the admin
// console filters it as the operator types — without this cache every
// keystroke would become an upstream request.
func (s *Server) voiceCatalog(ctx context.Context) (*hubVoicesResp, error) {
	voicesMu.Lock()
	defer voicesMu.Unlock()

	if voicesVal != nil && time.Since(voicesAt) < 10*time.Minute {
		return voicesVal, nil
	}
	var up hubVoicesResp
	if err := s.up.jsonCall(ctx, http.MethodGet, s.cfg.videoBase(),
		hubVoicesPath, nil, &up); err != nil {
		return nil, err
	}
	voicesVal = &up
	voicesAt = time.Now()
	return voicesVal, nil
}

// voiceViews projects the hub's voice table into the shape clients see and
// applies the optional `language` / `q` filters. Both the public endpoint and
// the admin console's playground use it, so a change to the projection cannot
// make the two disagree.
func voiceViews(up *hubVoicesResp, language, query string) []map[string]any {
	lang := strings.ToLower(strings.TrimSpace(language))
	q := strings.ToLower(strings.TrimSpace(query))
	data := make([]map[string]any, 0, len(up.Voices))
	for _, v := range up.Voices {
		if lang != "" && !strings.Contains(strings.ToLower(v.Language), lang) {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(v.VoiceID), q) &&
			!strings.Contains(strings.ToLower(v.Name), q) {
			continue
		}
		data = append(data, map[string]any{
			"id":          v.VoiceID,
			"object":      "voice",
			"name":        v.Name,
			"language":    v.Language,
			"gender":      v.Gender,
			"age":         v.Age,
			"accent":      v.Accent,
			"description": v.Description,
			"sample_url":  v.SampleAudio,
		})
	}
	return data
}

// ---------------------------------------------------------------------------
// Lyrics
// ---------------------------------------------------------------------------

type lyricsReq struct {
	Prompt string `json:"prompt"`
	Mode   string `json:"mode"`
}

type hubLyricsResp struct {
	SongTitle string `json:"song_title"`
	StyleTags string `json:"style_tags"`
	Lyrics    string `json:"lyrics"`
	Base      struct {
		Code        int    `json:"code"`
		Message     string `json:"message"`
		UserMessage string `json:"user_message"`
	} `json:"base"`
}

// handleLyrics serves POST /v1/lyrics/generations.
//
// Unlike everything else here this call is synchronous *and* free — it only
// writes text. Empty bodies are accepted by the hub (it invents a title), but
// a prompt is required to be useful, so it is enforced here.
func (s *Server) handleLyrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	var req lyricsReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "body is not valid JSON: "+err.Error())
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}

	var up hubLyricsResp
	if err := s.up.jsonCall(r.Context(), http.MethodPost, s.cfg.videoBase(),
		hubLyricsPath, map[string]any{
			"mode":   firstNonEmpty(req.Mode, "write_full_song"),
			"prompt": prompt,
		}, &up); err != nil {
		writeUpstreamError(w, err)
		return
	}
	if up.Lyrics == "" {
		writeErr(w, http.StatusBadGateway, "upstream_error",
			firstNonEmpty(up.Base.UserMessage, up.Base.Message, "upstream returned no lyrics"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object":     "lyrics",
		"title":      up.SongTitle,
		"style_tags": up.StyleTags,
		"lyrics":     up.Lyrics,
	})
}

// ---------------------------------------------------------------------------
// Music (async task)
// ---------------------------------------------------------------------------

type musicReq struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Lyrics         string `json:"lyrics"`
	IsInstrumental string `json:"is_instrumental"`
	AudioURL       string `json:"audio_url"`
	CallbackURL    string `json:"callback_url"`
}

type hubMusicSubmitReq struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model"`
	IsInstrumental bool   `json:"is_instrumental"`
	Lyrics         string `json:"lyrics,omitempty"`
	AudioURL       string `json:"audio_url,omitempty"`
}

type hubMusicSubmitResp struct {
	TaskID string `json:"task_id"`
	Status int    `json:"status"`
	Base   struct {
		Code        int    `json:"code"`
		Message     string `json:"message"`
		UserMessage string `json:"user_message"`
	} `json:"base"`
}

// hubTaskStatusResp is the shape shared by every v2 task endpoint: a lowercase
// status plus whatever payload the backend attaches on success.
type hubTaskStatusResp struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	AudioURL string `json:"audio_url"`
	Duration any    `json:"duration"`
	Lyrics   string `json:"lyrics"`
	Base     struct {
		Code        int    `json:"code"`
		Message     string `json:"message"`
		UserMessage string `json:"user_message"`
	} `json:"base"`
}

// handleMusic serves POST /v1/music/generations.
//
// Instrumental tracks need no lyrics; a vocal track without lyrics makes the
// hub run its lyric writer first, which is exactly what the desktop client
// does. That fallback is reproduced here so callers can pass a bare prompt.
func (s *Server) handleMusic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}

	var req musicReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "body is not valid JSON: "+err.Error())
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "prompt is required")
		return
	}

	instrumental := strings.EqualFold(strings.TrimSpace(req.IsInstrumental), "instrumental") ||
		strings.EqualFold(strings.TrimSpace(req.IsInstrumental), "true")

	body := hubMusicSubmitReq{
		Prompt:         prompt,
		Model:          firstNonEmpty(strings.TrimSpace(req.Model), hubMusicModel),
		IsInstrumental: instrumental,
	}
	if !instrumental {
		lyrics := strings.TrimSpace(req.Lyrics)
		if lyrics == "" {
			var lyr hubLyricsResp
			if err := s.up.jsonCall(r.Context(), http.MethodPost, s.cfg.videoBase(),
				hubLyricsPath, map[string]any{
					"mode":   "write_full_song",
					"prompt": prompt,
				}, &lyr); err != nil {
				writeUpstreamError(w, err)
				return
			}
			if lyr.Lyrics == "" {
				writeErr(w, http.StatusBadGateway, "upstream_error",
					"could not write lyrics for this prompt and none were supplied")
				return
			}
			lyrics = lyr.Lyrics
		}
		body.Lyrics = lyrics
	}

	var up hubMusicSubmitResp
	if err := s.up.jsonCall(r.Context(), http.MethodPost, s.cfg.videoBase(),
		hubMusicSubmit, body, &up); err != nil {
		writeUpstreamError(w, err)
		return
	}
	if up.TaskID == "" {
		writeErr(w, http.StatusBadGateway, "upstream_error",
			firstNonEmpty(up.Base.UserMessage, up.Base.Message, "hub accepted the request but returned no task_id"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      up.TaskID,
		"task_id": up.TaskID,
		"object":  "music.generation",
		"status":  "queued",
		"model":   body.Model,
	})
}

// handleMusicQuery serves GET /v1/query/music_generation?task_id=…
func (s *Server) handleMusicQuery(w http.ResponseWriter, r *http.Request) {
	if !s.clientAuthorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid_api_key", "invalid API key")
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	var up hubTaskStatusResp
	if err := s.up.jsonCall(r.Context(), http.MethodGet, s.cfg.videoBase(),
		hubMusicQuery+taskID, nil, &up); err != nil {
		if ue, ok := err.(*upstreamError); ok && ue.Status == http.StatusNotFound {
			// The hub 404s while a task is still unknown to it; report that as
			// "in progress" so pollers keep waiting instead of giving up.
			writeJSON(w, http.StatusOK, map[string]any{
				"task_id": taskID,
				"status":  "Processing",
			})
			return
		}
		writeUpstreamError(w, err)
		return
	}

	out := map[string]any{
		"task_id": firstNonEmpty(up.TaskID, taskID),
		"status":  mapHubTaskStatus(up.Status),
	}
	if up.AudioURL != "" {
		out["audio_url"] = up.AudioURL
	}
	if up.Lyrics != "" {
		out["lyrics"] = up.Lyrics
	}
	if d, ok := numberFromAny(up.Duration); ok {
		out["duration"] = d
	}
	if up.Status == "failed" || up.Status == "cancelled" {
		out["fail_reason"] = firstNonEmpty(up.Base.UserMessage, up.Base.Message, up.Status)
	}
	writeJSON(w, http.StatusOK, out)
}

// mapHubTaskStatus folds the lowercase v2 task vocabulary onto the capitalised
// one the MiniMax platform uses, so both facades speak the same words.
func mapHubTaskStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "success", "succeeded", "completed":
		return "Success"
	case "failed", "fail":
		return "Fail"
	case "cancelled", "canceled":
		return "Fail"
	case "processing", "running":
		return "Processing"
	case "pending", "queued", "queueing":
		return "Queueing"
	case "preparing":
		return "Preparing"
	case "":
		return "Processing"
	default:
		return s
	}
}

func numberFromAny(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Media fetch
// ---------------------------------------------------------------------------

const maxMediaBytes = 256 << 20 // 256 MiB; a 20-minute track is well under this

// fetchMediaBytes downloads a CDN artifact. The hub hands back short-lived
// signed URLs on cdn.hailuoai.video; they need no auth header.
func fetchMediaBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", truncate(url, 120), resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaBytes))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("fetch %s: empty body", truncate(url, 120))
	}
	return raw, nil
}
