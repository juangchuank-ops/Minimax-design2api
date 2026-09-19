package main

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Token pool.
//
// Every upstream call carries a raw `token` header holding a MiniMax Design
// access JWT. Tokens are account-scoped: the same JWT that reads /api/v1/config
// is the one that authorises /api/v1/messages. So a pool is just a list of
// JWTs with health tracking — no signing, no exchange.
// ---------------------------------------------------------------------------

var (
	errNoToken      = errors.New("no usable token in the pool")
	errTokenExpired = errors.New("token expired")
)

const (
	cooldownBase       = 5 * time.Second
	cooldownMax        = 10 * time.Minute
	credentialCooldown = 30 * time.Minute
	// billingCooldown benches an account that has run out of credits. Credits do
	// not come back on their own in seconds — a transient 5s penalty would just
	// mean re-selecting a token that cannot possibly succeed. It is deliberately
	// shorter than credentialCooldown: the fix is a top-up, not a new token.
	billingCooldown = 15 * time.Minute
)

// Cooldown reasons, surfaced to the admin console so "冷却中" is not the end of
// the story when you are trying to work out why the pool is unhappy.
const (
	reasonCredential = "credential" // the JWT itself is dead
	reasonTransient  = "transient"  // upstream hiccup
	reasonBilling    = "billing"    // token is fine, the account is empty
)

type Pool struct {
	cfg *Config
	mu  sync.Mutex
}

func newPool(cfg *Config) *Pool { return &Pool{cfg: cfg} }

// acquire returns the least-loaded healthy token.
func (p *Pool) acquire() (*Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var cands []*Token
	for _, t := range p.cfg.Tokens {
		if !t.isEnabled() || t.Token == "" {
			continue
		}
		if t.expired() {
			continue
		}
		if now.Before(t.cooldownT) {
			continue
		}
		cands = append(cands, t)
	}
	if len(cands) == 0 {
		// Say *why* the pool is empty. "no usable token" alone sends the
		// operator hunting for a config problem when the real answer is often
		// "everything is in cooldown for another 20 minutes".
		var soonest time.Time
		for _, t := range p.cfg.Tokens {
			if !t.isEnabled() || t.Token == "" || t.expired() {
				continue
			}
			if now.Before(t.cooldownT) && (soonest.IsZero() || t.cooldownT.Before(soonest)) {
				soonest = t.cooldownT
			}
		}
		if !soonest.IsZero() {
			wait := int(soonest.Sub(now).Seconds())
			return nil, fmt.Errorf("%w (all tokens cooling down, soonest ready in %ds; POST /admin/api/pool/reset to clear)", errNoToken, wait)
		}
		return nil, errNoToken
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].inflight < cands[j].inflight })
	cands[0].inflight++
	return cands[0], nil
}

func (p *Pool) release(t *Token) {
	if t == nil {
		return
	}
	p.mu.Lock()
	if t.inflight > 0 {
		t.inflight--
	}
	p.mu.Unlock()
}

// success clears any accumulated penalty.
func (p *Pool) success(t *Token) {
	if t == nil {
		return
	}
	p.mu.Lock()
	t.fails = 0
	t.cooldownT = time.Time{}
	t.coolReason = ""
	p.mu.Unlock()
}

// resetCooldowns clears every token's failure penalty and cooldown. Exposed to
// the admin UI because a single unentitled model or a transient upstream hiccup
// can otherwise bench the whole pool for tens of minutes with no way back
// short of restarting the process.
func (p *Pool) resetCooldowns() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.cfg.Tokens {
		t.fails = 0
		t.cooldownT = time.Time{}
		t.coolReason = ""
	}
}

// failCredential benches a token whose *credential* is bad. A 401/403 from the
// gateway means the JWT itself is dead, so we push the cooldown far out rather
// than retrying — and we let the penalty accumulate, because repeated
// credential failures are a real signal.
func (p *Pool) failCredential(t *Token) {
	if t == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t.fails++
	d := cooldownBase << uint(min(t.fails-1, 10))
	if d > cooldownMax {
		d = cooldownMax
	}
	if d < credentialCooldown {
		d = credentialCooldown
	}
	t.cooldownT = time.Now().Add(d)
	t.coolReason = reasonCredential
}

// failTransient records an upstream hiccup (transport error or 5xx).
//
// Two deliberate choices here, both learned the hard way:
//
//   - The penalty does not escalate. Most 5xx responses from the media gateway
//     are deterministic request-level rejections (an unsupported model, a bad
//     parameter combination), not congestion. Escalating on those turns a
//     single bad request into minutes of self-inflicted downtime.
//   - With only one usable token there is nothing to rotate to, so benching it
//     buys nothing and costs availability. Skip the cooldown entirely.
func (p *Pool) failTransient(t *Token) {
	if t == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.usableLocked() <= 1 {
		return
	}
	t.cooldownT = time.Now().Add(cooldownBase)
	t.coolReason = reasonTransient
}

// failExhausted benches a token whose *account* is out of credits.
//
// This is a third failure class on purpose. It is not a credential failure —
// the JWT is perfectly good and will work again the moment it is topped up — so
// it must not accumulate a penalty: topping up and then waiting out an
// escalating 30-minute bench would be actively unhelpful. It is not transient
// either: credits do not reappear in five seconds, so the short penalty would
// simply mean re-selecting a token that cannot possibly succeed.
//
// The penalty does not escalate, and a lone token is left alone for the same
// reason as failTransient: benching it would replace the hub's own
// "insufficient balance" message with a generic "no usable token in the pool",
// which is strictly less informative. Note that the caller still fails the
// *request* over to the next token — that is where an empty account does its
// damage, not in the cooldown.
func (p *Pool) failExhausted(t *Token) {
	if t == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.usableLocked() <= 1 {
		return
	}
	t.cooldownT = time.Now().Add(billingCooldown)
	t.coolReason = reasonBilling
}

// usableLocked counts tokens that could serve a request right now.
func (p *Pool) usableLocked() int {
	now := time.Now()
	n := 0
	for _, t := range p.cfg.Tokens {
		if !t.isEnabled() || t.Token == "" || t.expired() {
			continue
		}
		if now.Before(t.cooldownT) {
			continue
		}
		n++
	}
	return n
}

type poolView struct {
	Name          string `json:"name"`
	UserID        string `json:"user_id,omitempty"`
	Region        string `json:"region"`
	Enabled       bool   `json:"enabled"`
	ExpireAt      int64  `json:"expire_at,omitempty"`
	Expired       bool   `json:"expired"`
	Fails         int    `json:"fails"`
	Inflight      int    `json:"inflight"`
	Cooling       bool   `json:"cooling"`
	CooldownS     int64  `json:"cooldown_seconds"`
	CoolingReason string `json:"cooling_reason,omitempty"`
	Note          string `json:"note,omitempty"`
}

func (p *Pool) snapshot() []poolView {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]poolView, 0, len(p.cfg.Tokens))
	for _, t := range p.cfg.Tokens {
		v := poolView{
			Name:     t.display(),
			UserID:   t.UserID,
			Region:   t.Region,
			Enabled:  t.isEnabled(),
			ExpireAt: t.ExpireAt,
			Expired:  t.expired(),
			Fails:    t.fails,
			Inflight: t.inflight,
			Note:     t.Note,
		}
		if now.Before(t.cooldownT) {
			v.Cooling = true
			v.CooldownS = int64(t.cooldownT.Sub(now).Seconds())
			v.CoolingReason = t.coolReason
		}
		out = append(out, v)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
