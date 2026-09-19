package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The admin console's whole dashboard hangs off these two functions, and they
// are the only place where a request is classified without the handler's
// cooperation. A silent regression here shows up as a plausible-looking but
// wrong dashboard, which is worse than an obvious crash.
func TestKindForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/chat/completions", "chat"},
		{"/v1/messages", "chat"},
		{"/v1/responses", "chat"},

		{"/v1/images/generations", "image"},
		{"/v1/image/models", "image"},

		{"/v1/video_generation", "video"},
		{"/v1/query/video_generation", "video"},
		{"/v1/video/models", "video"},
		{"/v1/files/retrieve", "video"},

		{"/v1/audio/speech", "audio"},
		{"/v1/audio/voices", "audio"},
		{"/v1/lyrics/generations", "audio"},
		{"/v1/music/generations", "audio"},
		{"/v1/query/music_generation", "audio"},

		{"/health", "other"},
	}

	for _, tc := range cases {
		if got := kindForPath(tc.path); got != tc.want {
			t.Errorf("kindForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// The body is tee'd, not buffered whole, so a large request arrives here
// truncated. The regex fallback is what makes the model still recoverable —
// and /v1/music_generation already showed that `model` is omitted on some
// routes, so an empty result must stay empty rather than becoming a guess.
func TestExtractModelPreference(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"well formed", `{"model":"MiniMax-M3","messages":[]}`, "MiniMax-M3"},
		{"model not first", `{"stream":true,"messages":[],"model":"gamma"}`, "gamma"},
		{"truncated before close", `{"model":"nano_banana_2_flash","prompt":"a c`, "nano_banana_2_flash"},
		{"no model field", `{"prompt":"x"}`, ""},
		{"empty body", ``, ""},
		{"model is not a string", `{"model":42}`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractModelPreference([]byte(tc.body)); got != tc.want {
				t.Fatalf("extractModelPreference(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestMetricsSummaryAndTail(t *testing.T) {
	m := newMetrics()

	m.record(RequestRecord{TimeMS: 1000, Path: "/v1/chat/completions", Kind: "chat", Model: "MiniMax-M3", Status: 200, MS: 120, OK: true})
	m.record(RequestRecord{TimeMS: 2000, Path: "/v1/chat/completions", Kind: "chat", Model: "MiniMax-M3", Status: 200, MS: 80, OK: true})
	m.record(RequestRecord{TimeMS: 3000, Path: "/v1/video_generation", Kind: "video", Status: 502, MS: 400, OK: false})

	s := m.summary()
	if s.Total != 3 || s.Success != 2 || s.Failed != 1 {
		t.Fatalf("counters = total %d success %d failed %d, want 3/2/1", s.Total, s.Success, s.Failed)
	}
	if s.ByKind["chat"] != 2 || s.ByKind["video"] != 1 {
		t.Fatalf("by_kind = %v, want chat 2 / video 1", s.ByKind)
	}
	if s.ByStatus["200"] != 2 || s.ByStatus["502"] != 1 {
		t.Fatalf("by_status = %v, want 200 x2 / 502 x1", s.ByStatus)
	}
	// A record without a model must not create an empty-string bucket.
	if _, ok := s.ByModel[""]; ok {
		t.Fatalf("by_model contains an empty key: %v", s.ByModel)
	}
	if s.AvgMS != 200 {
		t.Fatalf("avg_ms = %v, want 200", s.AvgMS)
	}
	if want := 2.0 / 3.0 * 100; s.SuccessRate < want-0.01 || s.SuccessRate > want+0.01 {
		t.Fatalf("success_rate = %v, want ~66.67", s.SuccessRate)
	}

	tail := m.tail()
	if len(tail) != 3 {
		t.Fatalf("tail length = %d, want 3", len(tail))
	}
	if tail[0].TimeMS != 3000 {
		t.Fatalf("tail[0] is not the newest record: %+v", tail[0])
	}
}

// The ring must stay bounded, otherwise a busy gateway grows the process
// without limit.
func TestMetricsTailIsBounded(t *testing.T) {
	m := newMetrics()
	for i := 0; i < recentLimit+50; i++ {
		m.record(RequestRecord{TimeMS: int64(i), Path: "/v1/chat/completions", Kind: "chat", Status: 200, OK: true})
	}
	if got := len(m.tail()); got != recentLimit {
		t.Fatalf("tail length = %d, want %d", got, recentLimit)
	}
	if got := m.summary().Total; got != int64(recentLimit+50) {
		t.Fatalf("total = %d, want %d (counters must not be trimmed with the ring)", got, recentLimit+50)
	}
}

// Streaming is the reason recWriter exists, and breaking it is silent: the
// handler's w.(http.Flusher) assertion would fail and every SSE response would
// degrade to "streaming_unsupported" with a 500.
func TestRecWriterPreservesFlusher(t *testing.T) {
	var rw http.ResponseWriter = &recWriter{ResponseWriter: httptest.NewRecorder(), status: 200}
	if _, ok := rw.(http.Flusher); !ok {
		t.Fatal("recWriter does not implement http.Flusher; SSE handlers will fail their assertion")
	}
}

// End-to-end through ServeHTTP: an SSE handler must come back recorded as a
// stream, with the status the handler chose.
func TestServeInstrumentedRecordsStreamingCall(t *testing.T) {
	s := &Server{
		cfg:     defaultConfig(),
		metrics: newMetrics(),
		mux:     http.NewServeMux(),
	}
	s.mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("handler lost the flusher")
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"a\":1}\n\n")
		flusher.Flush()
	})

	body := `{"model":"MiniMax-M3","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	tail := s.metrics.tail()
	if len(tail) != 1 {
		t.Fatalf("tail length = %d, want 1", len(tail))
	}
	got := tail[0]
	if !got.Stream {
		t.Error("record.Stream = false, want true")
	}
	if got.Kind != "chat" || got.Status != 200 || !got.OK {
		t.Errorf("record = %+v, want kind chat / status 200 / ok true", got)
	}
	if got.Model != "MiniMax-M3" {
		t.Errorf("record.Model = %q, want MiniMax-M3 (the tee failed)", got.Model)
	}
	if got.Path != "/v1/chat/completions" {
		t.Errorf("record.Path = %q", got.Path)
	}
}

// Non-/v1 traffic must stay out of the counters: the console polls itself and
// the health probe runs on a timer, and letting those in would make the
// dashboard describe the monitoring instead of the service.
func TestServeHTTPDoesNotCountAdminOrHealth(t *testing.T) {
	s := &Server{
		cfg:     defaultConfig(),
		metrics: newMetrics(),
		mux:     http.NewServeMux(),
	}
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	s.mux.HandleFunc("/admin/api/state", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{}") })

	for _, path := range []string{"/health", "/admin/api/state"} {
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if got := s.metrics.summary().Total; got != 0 {
		t.Fatalf("total = %d, want 0 — admin/health traffic leaked into the metrics", got)
	}
}

// The console is served from the binary, so a missing file is a build-time
// mistake that only shows up in the browser. Assert the bundle is complete.
func TestAdminBundleIsEmbedded(t *testing.T) {
	for _, name := range []string{"index.html", "app.css", "app.js", "pages.js", "playground.js"} {
		data, err := webFS.ReadFile("web/" + name)
		if err != nil {
			t.Fatalf("web/%s is not embedded: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("web/%s is empty", name)
		}
	}
}

func TestAdminAssetRouting(t *testing.T) {
	s := &Server{cfg: defaultConfig()}

	cases := []struct {
		path     string
		wantCode int
		wantType string
	}{
		{"/admin/", http.StatusOK, "text/html; charset=utf-8"},
		{"/admin/app.css", http.StatusOK, "text/css; charset=utf-8"},
		{"/admin/app.js", http.StatusOK, "application/javascript; charset=utf-8"},
		{"/admin/playground.js", http.StatusOK, "application/javascript; charset=utf-8"},
		// A traversal attempt must 404 rather than resolve against the
		// embedded filesystem, and an unknown name must not fall through to
		// index.html.
		{"/admin/../config.json", http.StatusNotFound, ""},
		{"/admin/..%2fconfig.json", http.StatusNotFound, ""},
		{"/admin/nope.js", http.StatusNotFound, ""},
	}

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.path == "/admin/" {
			s.handleAdminIndex(rec, req)
		} else {
			s.handleAdminAsset(rec, req)
		}
		if rec.Code != tc.wantCode {
			t.Errorf("%s -> status %d, want %d", tc.path, rec.Code, tc.wantCode)
			continue
		}
		if tc.wantType != "" {
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("%s -> content-type %q, want %q", tc.path, got, tc.wantType)
			}
		}
	}
}

// voiceViews is shared by the public endpoint and the admin console's
// playground mirror, so the filtering behaviour has to hold for both.
func TestVoiceViewsFilters(t *testing.T) {
	up := &hubVoicesResp{
		Voices: []hubVoice{
			{VoiceID: "English_CalmWoman", Name: "Calm Woman", Language: "English"},
			{VoiceID: "Chinese_wenrounvxing", Name: "温柔女性", Language: "Chinese"},
			{VoiceID: "English_Gentle-voiced_man", Name: "Gentle Man", Language: "English"},
		},
	}

	if got := len(voiceViews(up, "", "")); got != 3 {
		t.Fatalf("unfiltered = %d voices, want 3", got)
	}
	// `language` matches case-insensitively as a substring.
	if got := len(voiceViews(up, "english", "")); got != 2 {
		t.Fatalf("language=english = %d voices, want 2", got)
	}
	// `q` matches either the voice id or the display name.
	if got := len(voiceViews(up, "", "gentle")); got != 1 {
		t.Fatalf("q=gentle = %d voices, want 1", got)
	}
	if got := len(voiceViews(up, "", "温柔")); got != 1 {
		t.Fatalf("q=温柔 = %d voices, want 1", got)
	}
	if got := len(voiceViews(up, "english", "calm")); got != 1 {
		t.Fatalf("language+q = %d voices, want 1", got)
	}
	if got := len(voiceViews(up, "", "nothing-matches")); got != 0 {
		t.Fatalf("no match = %d voices, want 0", got)
	}

	view := voiceViews(up, "", "calm")[0]
	if view["id"] != "English_CalmWoman" || view["object"] != "voice" {
		t.Fatalf("projection lost fields: %v", view)
	}
	if _, err := json.Marshal(view); err != nil {
		t.Fatalf("projection is not JSON-serialisable: %v", err)
	}
}

// configView feeds the settings form; a field silently dropped here becomes an
// uneditable setting in the console.
func TestConfigViewHasEveryEditableField(t *testing.T) {
	s := &Server{cfg: defaultConfig()}
	view := s.configView()

	for _, key := range []string{
		"listen", "gateway", "upstream", "video_base", "version_code",
		"app_id", "device_platform", "default_model", "api_keys", "timeout_sec",
	} {
		if _, ok := view[key]; !ok {
			t.Errorf("configView is missing %q", key)
		}
	}
	// Tokens live on their own endpoints; exposing them here would put JWTs
	// into a settings form that is rendered on every page load.
	if _, ok := view["tokens"]; ok {
		t.Error("configView must not expose account tokens")
	}
}

// configView copies api_keys rather than aliasing the live slice, so a caller
// mutating the returned value cannot corrupt the running config.
func TestConfigViewCopiesAPIKeys(t *testing.T) {
	cfg := defaultConfig()
	cfg.APIKeys = []string{"sk-original"}
	s := &Server{cfg: cfg}

	view := s.configView()
	keys := view["api_keys"].([]string)
	keys[0] = "sk-mutated"

	if cfg.APIKeys[0] != "sk-original" {
		t.Fatalf("configView aliased the live api_keys slice: config is now %v", cfg.APIKeys)
	}
}

// An empty api_keys must serialise as [] and never as null. `append([]string(nil))`
// on an empty slice yields nil, and the console's `.length` check then throws on
// a perfectly valid config — the whole pool page rendered as an error because of
// this one byte.
func TestConfigViewEmptyAPIKeysMarshalsAsArray(t *testing.T) {
	s := &Server{cfg: defaultConfig()}

	encoded, err := json.Marshal(s.configView())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"api_keys":[]`) {
		t.Fatalf("api_keys is not an empty array: %s", encoded)
	}
}

// The metrics clock must reflect process uptime, not wall-clock weirdness.
func TestMetricsUptimeIsMonotonic(t *testing.T) {
	m := newMetrics()
	first := m.summary().UptimeSeconds
	time.Sleep(1100 * time.Millisecond)
	second := m.summary().UptimeSeconds
	if second < first {
		t.Fatalf("uptime went backwards: %d then %d", first, second)
	}
}
