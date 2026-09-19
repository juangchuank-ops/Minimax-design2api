package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Request metrics.
//
// The admin console needs numbers that describe what this process actually
// served: how many calls, how many failed, how slow, and a live tail of recent
// requests. Everything here is in-memory and process-local — a handful of
// counters plus a fixed-size ring, behind one mutex. Nothing is persisted, so
// a restart legitimately resets the dashboard.
// ---------------------------------------------------------------------------

const (
	recentLimit   = 120
	peekBodyLimit = 8 << 10
)

// RequestRecord is one finished request, as shown in the dashboard's tail.
type RequestRecord struct {
	TimeMS int64  `json:"time_ms"`
	Path   string `json:"path"`
	Kind   string `json:"kind"` // chat | image | video | audio | other
	Model  string `json:"model,omitempty"`
	Status int    `json:"status"`
	MS     int64  `json:"ms"`
	Stream bool   `json:"stream"`
	OK     bool   `json:"ok"`
}

// MetricsSummary is the aggregate view handed to the admin console.
type MetricsSummary struct {
	UptimeSeconds int64            `json:"uptime_seconds"`
	Total         int64            `json:"total"`
	Success       int64            `json:"success"`
	Failed        int64            `json:"failed"`
	SuccessRate   float64          `json:"success_rate"`
	AvgMS         float64          `json:"avg_ms"`
	ByKind        map[string]int64 `json:"by_kind"`
	ByStatus      map[string]int64 `json:"by_status"`
	ByModel       map[string]int64 `json:"by_model"`
}

type Metrics struct {
	mu       sync.Mutex
	started  time.Time
	total    int64
	success  int64
	failed   int64
	sumMS    int64
	byKind   map[string]int64
	byStatus map[int]int64
	byModel  map[string]int64
	recent   []RequestRecord // newest first
}

func newMetrics() *Metrics {
	return &Metrics{
		started:  time.Now(),
		byKind:   map[string]int64{},
		byStatus: map[int]int64{},
		byModel:  map[string]int64{},
	}
}

func (m *Metrics) record(rec RequestRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.total++
	m.sumMS += rec.MS
	if rec.OK {
		m.success++
	} else {
		m.failed++
	}
	m.byKind[rec.Kind]++
	m.byStatus[rec.Status]++
	if rec.Model != "" {
		m.byModel[rec.Model]++
	}

	m.recent = append([]RequestRecord{rec}, m.recent...)
	if len(m.recent) > recentLimit {
		m.recent = m.recent[:recentLimit]
	}
}

func (m *Metrics) summary() MetricsSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := MetricsSummary{
		UptimeSeconds: int64(time.Since(m.started).Seconds()),
		Total:         m.total,
		Success:       m.success,
		Failed:        m.failed,
		ByKind:        make(map[string]int64, len(m.byKind)),
		ByStatus:      make(map[string]int64, len(m.byStatus)),
		ByModel:       make(map[string]int64, len(m.byModel)),
	}
	if m.total > 0 {
		out.SuccessRate = float64(m.success) / float64(m.total) * 100
		out.AvgMS = float64(m.sumMS) / float64(m.total)
	}
	for k, v := range m.byKind {
		out.ByKind[k] = v
	}
	for k, v := range m.byStatus {
		out.ByStatus[strconv.Itoa(k)] = v
	}
	for k, v := range m.byModel {
		out.ByModel[k] = v
	}
	return out
}

func (m *Metrics) tail() []RequestRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RequestRecord, len(m.recent))
	copy(out, m.recent)
	return out
}

// --- instrumentation --------------------------------------------------------

// recWriter captures the status code and whether the response was an SSE
// stream, so a streaming call can be classified after the fact.
//
// It deliberately re-implements Flush: handlers assert w.(http.Flusher) to
// stream, and wrapping the writer would otherwise break SSE entirely.
type recWriter struct {
	http.ResponseWriter
	status int
	stream bool
	wrote  bool
}

func (w *recWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		w.status = code
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
			w.stream = true
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		w.stream = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *recWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// peekBody tees the first few KB of a request body so the middleware can read
// the `model` field without the handlers having to cooperate.
type peekBody struct {
	rc  io.ReadCloser
	buf *bytes.Buffer
}

func (p *peekBody) Read(b []byte) (int, error) {
	n, err := p.rc.Read(b)
	if n > 0 && p.buf.Len() < peekBodyLimit {
		room := peekBodyLimit - p.buf.Len()
		if n < room {
			room = n
		}
		p.buf.Write(b[:room])
	}
	return n, err
}

func (p *peekBody) Close() error { return p.rc.Close() }

var modelKeyRe = regexp.MustCompile(`"model"\s*:\s*"([^"]{1,160})"`)

// extractModelPreference reads the model from a (possibly truncated) body. The
// JSON path is exact; the regex is the fallback for bodies cut mid-object,
// where unmarshalling cannot succeed.
func extractModelPreference(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Model != "" {
		return probe.Model
	}
	if m := modelKeyRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// kindForPath buckets a request into the dashboard's capability rows. The
// substring tests are ordered: "images" must be checked before the looser
// "video"/"audio" tests, and "music"/"lyrics" both live under "audio".
func kindForPath(p string) string {
	switch {
	case p == "/v1/chat/completions" || p == "/v1/messages" || p == "/v1/responses":
		return "chat"
	case strings.Contains(p, "images") || strings.Contains(p, "image/"):
		return "image"
	case strings.Contains(p, "video") || strings.Contains(p, "files/retrieve"):
		return "video"
	case strings.Contains(p, "audio"), strings.Contains(p, "lyrics"), strings.Contains(p, "music"):
		return "audio"
	}
	return "other"
}
