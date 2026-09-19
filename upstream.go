package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Upstream transport.
//
// Confirmed on MiniMax Design 3.0.16 (overseas channel):
//
//	POST {upstream}/api/v1/messages    Anthropic Messages API   (minimaxHub, alpha)
//	POST {upstream}/api/v1/responses   OpenAI Responses API     (gamma)
//
// Required headers on every call:
//
//	token:        <MiniMax Design access JWT>   <- not "Authorization", not "x-api-key"
//	version_code: >= 1.0.0, the app sends its own version (3.0.16)
//
// Omitting `version_code` yields 403 "client version_code is required";
// omitting `token` yields 401 "token 为空".
//
// Because 403 is overloaded — the gateway uses it for missing client headers,
// for models the account is not entitled to, and for dead credentials — a 403
// must never be read as "this JWT is dead" without inspecting the body. Getting
// that wrong poisons the whole pool: one unentitled model would put every
// account into a long cooldown and surface as "no usable token in the pool".
// ---------------------------------------------------------------------------

type Upstream struct {
	cfg     *Config
	pool    *Pool
	catalog *Catalog
	client  *http.Client
}

func newUpstream(cfg *Config, pool *Pool, catalog *Catalog) *Upstream {
	return &Upstream{
		cfg:     cfg,
		pool:    pool,
		catalog: catalog,
		client: &http.Client{
			Timeout: cfg.timeout(),
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 32,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Emitter receives normalised deltas; the server renders them as OpenAI SSE.
type Emitter interface {
	Delta(d OAIDelta)
	Finish(reason string)
	Usage(u *OAIUsage)
}

type upstreamError struct {
	Status  int
	Body    string
	AuthBad bool
	// NoCredit marks an account-level billing rejection: the token is valid, the
	// account behind it is empty. Kept separate from AuthBad so the pool can
	// bench it briefly without treating it as a dead credential.
	NoCredit bool
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream HTTP %d: %s", e.Status, truncate(e.Body, 400))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// credentialMarkers are the phrases the MiniMax hub uses when the *token* is at
// fault rather than the request. Matched case-insensitively against the error
// body; keep them specific so a model-level 403 does not trip them. Note that
// "client version_code is required" is deliberately absent: that is a fault in
// our own config, and penalising the token for it would be misleading.
var credentialMarkers = []string{
	"invalid token",
	"token 为空",
	"token为空",
	"token is required",
	"token expired",
	"token 已过期",
	"token已过期",
	"unauthorized",
	"invalid api key",
	"authentication",
	"not logged in",
	"please log in",
	"未登录",
	"请登录",
	"登录已过期",
	"凭证",
}

// isCredentialFailure decides whether an upstream 4xx means the pooled JWT is
// unusable. 401 always does. 403 is ambiguous — the hub returns it for missing
// client headers, for unentitled models and for dead credentials alike — so it
// only counts when the body names a credential problem.
func isCredentialFailure(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	s := strings.ToLower(string(body))
	for _, m := range credentialMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// billingMarkers are the phrases that mean "this account has run out of
// credits" rather than "this token is bad" or "this request is malformed".
//
// The distinction matters more than it looks. Without it the response falls
// into the generic 4xx bucket, which returns immediately without trying another
// token — so one empty account silently swallows requests that a healthy
// account in the same pool could have served.
var billingMarkers = []string{
	"余额不足", "额度不足", "积分不足", "贝壳不足", "请充值", "充值后",
	"insufficient balance", "insufficient credit", "insufficient_quota",
	"insufficient quota", "no enough credit", "not enough credit",
	"out of credit", "credits exhausted", "quota exceeded", "balance not enough",
}

// isBillingExhaustion reports whether a 4xx is an account-billing rejection.
//
// Deliberately narrow: only the statuses the hub actually uses for this, and
// only phrases that name credits/quota/balance. A 401 can never land here — it
// is always a credential problem.
func isBillingExhaustion(status int, body []byte) bool {
	switch status {
	case http.StatusForbidden, http.StatusBadRequest, http.StatusPaymentRequired,
		http.StatusTooManyRequests:
	default:
		return false
	}
	s := strings.ToLower(string(body))
	for _, m := range billingMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// call performs one upstream POST with a pooled token, retrying once per
// remaining token on transport / 5xx failures.
func (u *Upstream) call(ctx context.Context, path string, payload any) (*http.Response, *Token, error) {
	_, upstream, _, _, _ := u.cfg.settings()
	return u.callMethod(ctx, http.MethodPost, upstream, path, payload)
}

// callMethod is the general form: it targets an explicit base URL so the same
// pool can serve both the LLM hub (hub.minimax.io) and the media gateway
// (design.minimax.io), which are different hosts behind the same token.
func (u *Upstream) callMethod(ctx context.Context, method, base, path string, payload any) (*http.Response, *Token, error) {
	_, _, versionCode, appID, devicePlat := u.cfg.settings()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}

	var lastErr error
	tried := map[*Token]bool{}
	for attempt := 0; attempt < 4; attempt++ {
		tok, err := u.pool.acquire()
		if err != nil {
			if lastErr != nil {
				return nil, nil, lastErr
			}
			return nil, nil, err
		}
		if tried[tok] {
			u.pool.release(tok)
			break
		}
		tried[tok] = true

		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		if err != nil {
			u.pool.release(tok)
			return nil, nil, err
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("token", tok.Token)
		req.Header.Set("version_code", versionCode)
		if appID != "" {
			req.Header.Set("app_id", appID)
		}
		if devicePlat != "" {
			req.Header.Set("device_platform", devicePlat)
		}

		resp, err := u.client.Do(req)
		if err != nil {
			u.pool.failTransient(tok)
			u.pool.release(tok)
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			u.pool.success(tok)
			// The token stays checked out until the body is fully consumed.
			return resp, tok, nil
		}

		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		status := resp.StatusCode

		authBad := isCredentialFailure(status, raw)

		// An empty account must fail the request over to the next token: the
		// pool exists precisely so one dead account does not take the service
		// with it. Bench the token briefly and try again.
		if !authBad && isBillingExhaustion(status, raw) {
			u.pool.failExhausted(tok)
			u.pool.release(tok)
			lastErr = &upstreamError{Status: status, Body: string(raw), NoCredit: true}
			continue
		}

		// A 4xx that is not about the credential says nothing about the token's
		// health, so it must not cost the token a cooldown. Otherwise a single
		// unentitled model would take the whole pool offline.
		if status < 500 && !authBad {
			u.pool.release(tok)
			return nil, nil, &upstreamError{Status: status, Body: string(raw), AuthBad: false}
		}

		if authBad {
			u.pool.failCredential(tok)
		} else {
			u.pool.failTransient(tok)
		}
		u.pool.release(tok)

		lastErr = &upstreamError{Status: status, Body: string(raw), AuthBad: authBad}
		if !authBad && status < 500 {
			return nil, nil, lastErr
		}
	}
	if lastErr == nil {
		lastErr = errNoToken
	}
	return nil, nil, lastErr
}

// ---------------------------------------------------------------------------
// JSON (non-streaming) helper
// ---------------------------------------------------------------------------

// jsonCall performs one upstream request and decodes the JSON body into out.
// The response body is fully read and the pooled token released before
// returning, so callers never have to manage the checkout.
func (u *Upstream) jsonCall(ctx context.Context, method, base, path string, payload, out any) error {
	resp, tok, err := u.callMethod(ctx, method, base, path, payload)
	if err != nil {
		return err
	}
	defer u.pool.release(tok)
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("upstream returned non-JSON (%d bytes): %w", len(raw), err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// SSE reader
// ---------------------------------------------------------------------------

type sseEvent struct {
	Name string
	Data string
}

// readSSE walks an event stream. Anthropic sends `event:`/`data:` pairs;
// the Responses API only sends `data:` lines.
func readSSE(r io.Reader, fn func(sseEvent) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	var name string
	var data []string
	flush := func() error {
		if len(data) == 0 && name == "" {
			return nil
		}
		ev := sseEvent{Name: name, Data: strings.Join(data, "\n")}
		name, data = "", nil
		return fn(ev)
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment / keepalive
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			v := line[len("data:"):]
			v = strings.TrimPrefix(v, " ")
			data = append(data, v)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

func decodeInto(data string, v any) error {
	if data == "" || data == "[DONE]" {
		return nil
	}
	return json.Unmarshal([]byte(data), v)
}
