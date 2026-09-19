package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// --- classification ---------------------------------------------------------

// Billing rejection and credential failure are different things and must stay
// disjoint. Getting this wrong in either direction is expensive: a billing
// error read as a dead credential benches a perfectly good token for 30
// minutes, and a dead credential read as billing leaves it in rotation.
func TestBillingAndCredentialMarkersAreDisjoint(t *testing.T) {
	billing := []string{
		`{"base_resp":{"status_code":1008,"status_msg":"余额不足，请充值"}}`,
		`{"error":{"message":"insufficient balance"}}`,
		`{"message":"积分不足，本次生成无法开始"}`,
		`{"message":"quota exceeded"}`,
		`{"message":"Not enough credit to run this model"}`,
	}
	credential := []string{
		`{"message":"invalid token"}`,
		`{"message":"token expired"}`,
		`{"message":"未登录"}`,
		`{"message":"client version_code is required"}`,
	}

	for _, body := range billing {
		raw := []byte(body)
		if !isBillingExhaustion(http.StatusForbidden, raw) {
			t.Errorf("not classified as billing: %s", body)
		}
		if isCredentialFailure(http.StatusForbidden, raw) {
			t.Errorf("billing body classified as a credential failure: %s", body)
		}
	}
	for _, body := range credential {
		raw := []byte(body)
		if isBillingExhaustion(http.StatusForbidden, raw) {
			t.Errorf("credential body classified as billing: %s", body)
		}
	}

	// A 401 is always about the credential, never about money.
	if isBillingExhaustion(http.StatusUnauthorized, []byte("insufficient balance")) {
		t.Error("401 must never be classified as billing")
	}
	// "client version_code is required" is our own config bug: it must not
	// bench anything, and it must not look like a billing problem either.
	if isCredentialFailure(http.StatusForbidden, []byte("client version_code is required")) {
		t.Error("version_code must not be treated as a credential failure")
	}
}

// --- the fix: an empty account must not swallow the request -----------------

// twoTokenUpstream builds a pool with one broke and one funded account, behind
// a stub upstream that decides by token header.
func twoTokenUpstream(t *testing.T, brokeStatus int, brokeBody string, counter *int32) (*Config, *Pool, *Upstream) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if counter != nil {
			atomic.AddInt32(counter, 1)
		}
		switch r.Header.Get("token") {
		case "broke":
			w.WriteHeader(brokeStatus)
			_, _ = w.Write([]byte(brokeBody))
		case "rich":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"invalid token"}`))
		}
	}))
	t.Cleanup(srv.Close)

	cfg := defaultConfig()
	cfg.Upstream = srv.URL
	cfg.Tokens = []*Token{
		{Name: "broke", Token: "broke"},
		{Name: "rich", Token: "rich"},
	}
	cfg.normalize()

	pool := newPool(cfg)
	return cfg, pool, newUpstream(cfg, pool, newCatalog())
}

// The bug this covers: a billing 403 is a "generic 4xx", and the generic path
// returned immediately without trying the next token. One empty account in a
// healthy pool therefore broke requests it had no business touching.
func TestBillingErrorFailsOverToAnotherToken(t *testing.T) {
	_, pool, up := twoTokenUpstream(t, http.StatusForbidden,
		`{"base_resp":{"status_code":1008,"status_msg":"余额不足，请充值"}}`, nil)

	resp, tok, err := up.call(context.Background(), "/api/v1/messages", map[string]any{"model": "x"})
	if err != nil {
		t.Fatalf("call failed instead of failing over to the funded token: %v", err)
	}
	defer resp.Body.Close()
	defer pool.release(tok)

	if tok.Name != "rich" {
		t.Fatalf("request landed on %q, want it to move on to the funded token", tok.Name)
	}

	var broke *poolView
	for i := range pool.snapshot() {
		if v := pool.snapshot()[i]; v.Name == "broke" {
			broke = &v
		}
	}
	if broke == nil {
		t.Fatal("broke token vanished from the pool")
	}
	if !broke.Cooling {
		t.Error("the empty account was left looking healthy; it will keep being selected and keep failing")
	}
	if broke.CoolingReason != reasonBilling {
		t.Errorf("cooling reason = %q, want %q so the console can explain it", broke.CoolingReason, reasonBilling)
	}
	if broke.Fails != 0 {
		t.Errorf("fails = %d, want 0 — topping up must not be punished by an escalating penalty", broke.Fails)
	}
}

// A request-level 400 says nothing about the other tokens, so it must keep the
// old behaviour: no failover, no cooldown. Otherwise one unentitled model would
// take the whole pool offline.
func TestOrdinaryBadRequestDoesNotFailOverOrBench(t *testing.T) {
	var calls int32
	_, pool, up := twoTokenUpstream(t, http.StatusBadRequest, `{"message":"invalid parameter"}`, &calls)

	_, _, err := up.call(context.Background(), "/api/v1/messages", map[string]any{"model": "x"})
	if err == nil {
		t.Fatal("want an error")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("made %d upstream calls, want 1 — a request-level 400 must not be retried elsewhere", got)
	}
	for _, v := range pool.snapshot() {
		if v.Cooling {
			t.Errorf("%s was benched for a request-level error: %+v", v.Name, v)
		}
	}
}

// With a single token there is nothing to rotate to, so benching it would only
// swap the hub's own "余额不足" for a generic "no usable token in the pool" —
// strictly less informative. The error must survive intact.
func TestBillingErrorDoesNotBenchASoloToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1008,"status_msg":"余额不足，请充值"}}`))
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.Upstream = srv.URL
	cfg.Tokens = []*Token{{Name: "only", Token: "only"}}
	cfg.normalize()
	pool := newPool(cfg)
	up := newUpstream(cfg, pool, newCatalog())

	_, _, err := up.call(context.Background(), "/api/v1/messages", map[string]any{"model": "x"})
	if err == nil {
		t.Fatal("want an error")
	}
	var ue *upstreamError
	if !asUpstream(err, &ue) || !ue.NoCredit {
		t.Fatalf("err = %v, want a NoCredit upstreamError so the caller labels it", err)
	}
	if !strings.Contains(ue.Body, "余额不足") {
		t.Errorf("upstream body was not passed through: %q", ue.Body)
	}
	for _, v := range pool.snapshot() {
		if v.Cooling {
			t.Errorf("solo token was benched (%+v)", v)
		}
	}
}

// The error type is what a caller greps for when generation stops working.
func TestUpstreamErrorTypeLabelsBilling(t *testing.T) {
	rec := httptest.NewRecorder()
	writeUpstreamError(rec, &upstreamError{
		Status: http.StatusForbidden, Body: `{"status_msg":"余额不足"}`, NoCredit: true,
	})
	if !strings.Contains(rec.Body.String(), `"insufficient_credit"`) {
		t.Fatalf("body = %s, want the insufficient_credit type", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	writeUpstreamError(rec, &upstreamError{Status: http.StatusForbidden, Body: `{"status_msg":"no permission"}`})
	if strings.Contains(rec.Body.String(), "insufficient_credit") {
		t.Fatalf("an ordinary 403 was mislabelled as a billing failure: %s", rec.Body.String())
	}
}

// --- credit decoding --------------------------------------------------------

func TestCreditTypeNaming(t *testing.T) {
	want := map[int]string{
		0: "充值", 1: "订阅", 2: "赠送", 3: "默认", 4: "创作者", 5: "活动", 6: "登录奖励", 7: "团队转入",
	}
	for v, name := range want {
		if got := creditTypeName(v); got != name {
			t.Errorf("creditTypeName(%d) = %q, want %q", v, got, name)
		}
	}
	if got := creditTypeName(99); !strings.Contains(got, "99") {
		t.Errorf("unknown types must stay visible, got %q", got)
	}
	// The whole point of surfacing the type is this one: bonus credits are the
	// 3-day kind, and that is why a "valid" account can be useless.
	if got := creditTypeLifetime(creditBonus); !strings.Contains(got, "3 天") {
		t.Errorf("bonus lifetime = %q, want it to mention 3 天", got)
	}
	if got := creditTypeLifetime(creditTopUp); !strings.Contains(got, "一年") {
		t.Errorf("top-up lifetime = %q, want it to mention 一年", got)
	}
}

func TestCreditEndTimeParsing(t *testing.T) {
	if got := creditEndTime("1790013469697"); got != 1790013469697 {
		t.Errorf("creditEndTime = %d", got)
	}
	for _, bad := range []string{"", "  ", "0", "not-a-number", "-"} {
		if got := creditEndTime(bad); got != 0 {
			t.Errorf("creditEndTime(%q) = %d, want 0", bad, got)
		}
	}
}

// An unknown expiry must never be reported as "the one that runs out first" —
// that would point the operator at the wrong credit bucket.
func TestExpiresBeforeTreatsUnknownAsLast(t *testing.T) {
	if expiresBefore("", "1790013469697") {
		t.Error("an unknown expiry won the earliest comparison")
	}
	if !expiresBefore("1790013469697", "") {
		t.Error("a known expiry should beat an unknown one")
	}
	if expiresBefore("", "") {
		t.Error("two unknowns should not compare as ordered")
	}
	if !expiresBefore("1000", "2000") {
		t.Error("1000 should expire before 2000")
	}
	if expiresBefore("2000", "1000") {
		t.Error("2000 should not expire before 1000")
	}
}

// creditBase must follow the cloud gateway, not the media host: these are
// account endpoints, and an operator who repointed video_base at a CDN would
// otherwise lose the credit panel.
func TestCreditBaseFollowsGatewayNotVideoBase(t *testing.T) {
	cfg := defaultConfig()
	cfg.Gateway = "https://gateway.example"
	cfg.VideoBase = "https://media.example"
	s := &Server{cfg: cfg}

	if got := s.creditBase(); got != "https://gateway.example" {
		t.Fatalf("creditBase = %q, want the gateway host", got)
	}
}
