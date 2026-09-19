package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Account credit state.
//
// Credits and tokens are two independent clocks: a JWT lives ~40 days, but the
// free credits a new account starts with live 3 days, top-up credits a year,
// and subscription credits a month. A pool full of valid-looking tokens says
// nothing about whether any of them can actually generate anything, so the
// console needs to be able to ask.
//
// All three endpoints live on the cloud gateway (design.minimax.io); they are
// account endpoints, so they follow `gateway` rather than `video_base`, and are
// authorised by the same `token` header as everything else.
// ---------------------------------------------------------------------------

const (
	creditPathBalance = "/api/v1/credit/balance"
	creditPathWallet  = "/api/v1/credit/wallet"
	creditPathTrial   = "/api/v1/promotions/hailuo03-video-trial/status"
)

// CreditType values come from the desktop bundle's CreditType enum; the gateway
// only ever sends the integer.
const (
	creditTopUp      = 0
	creditMembership = 1
	creditBonus      = 2
	creditDefault    = 3
	creditCreator    = 4
	creditActivity   = 5
	creditLogin      = 6
	creditTransfer   = 7
)

// creditTypeName maps the wire enum to something readable. A switch rather than
// a map so an unknown value is visibly "未知(N)" instead of silently missing.
func creditTypeName(t int) string {
	switch t {
	case creditTopUp:
		return "充值"
	case creditMembership:
		return "订阅"
	case creditBonus:
		return "赠送"
	case creditDefault:
		return "默认"
	case creditCreator:
		return "创作者"
	case creditActivity:
		return "活动"
	case creditLogin:
		return "登录奖励"
	case creditTransfer:
		return "团队转入"
	}
	return "未知(" + strconv.Itoa(t) + ")"
}

// creditTypeLifetime documents how long each kind survives. This is why an
// account can be "valid but useless": bonus credits die in days, the token in
// weeks.
func creditTypeLifetime(t int) string {
	switch t {
	case creditTopUp:
		return "有效期一年"
	case creditMembership:
		return "有效期 1 个月，每月重置"
	case creditBonus:
		return "新用户登录奖励，有效期 3 天"
	case creditTransfer:
		return "随原有效期"
	}
	return ""
}

// --- wire shapes (only the fields we act on) --------------------------------

type gwBalance struct {
	TotalCredit string `json:"total_credit"`
}

type gwSubCredit struct {
	CreditType int    `json:"credit_type"`
	Credit     string `json:"credit"`
	EndTime    string `json:"end_time"` // milliseconds, as a string
}

type gwWallet struct {
	Source     int           `json:"source"`
	PlanName   string        `json:"plan_name"`
	SubCredits []gwSubCredit `json:"sub_credits"`
	URL        string        `json:"url"`
}

type gwWalletResp struct {
	Wallets []gwWallet `json:"wallets"`
}

type gwTrial struct {
	FreeCount int    `json:"free_count"`
	ClaimHint string `json:"claim_hint"`
}

// creditSnapshot is one account's billing state, flattened for the console.
//
// UserID is carried alongside Name because the console keys its cache on it:
// pool rows are addressed by index, which shifts every time a token is added or
// removed, while user_id is decoded from the JWT and does not move.
type creditSnapshot struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	UserID      string `json:"user_id,omitempty"`
	Balance     string `json:"balance,omitempty"`
	CreditType  int    `json:"credit_type"`
	CreditName  string `json:"credit_name,omitempty"`
	Credit      string `json:"credit,omitempty"`
	Lifetime    string `json:"lifetime,omitempty"`
	ExpireAt    int64  `json:"expire_at,omitempty"` // milliseconds, 0 when unknown
	FreeTrial   int    `json:"free_trial"`
	TrialHint   string `json:"trial_hint,omitempty"`
	SubscribeTo string `json:"subscribe_url,omitempty"`
	Error       string `json:"error,omitempty"`
}

// creditBase is the cloud gateway host for these account endpoints.
func (s *Server) creditBase() string {
	gateway, _, _, _, _ := s.cfg.settings()
	return gateway
}

// warmMediaCatalogs pre-fetches the media catalogues at boot so the first media
// picker does not stall on a cold cache. Called from a goroutine; it must never
// block startup or take the process down — the catalogue endpoints already
// degrade per-catalogue when upstream is unreachable.
func (s *Server) warmMediaCatalogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	started := time.Now()
	if _, err := s.videoCatalog(ctx); err != nil {
		log.Printf("[catalog] video warm-up failed: %v", err)
	}
	if _, err := s.imageCatalog(ctx); err != nil {
		log.Printf("[catalog] image warm-up failed: %v", err)
	}
	if _, err := s.voiceCatalog(ctx); err != nil {
		log.Printf("[catalog] voices warm-up failed: %v", err)
	} else {
		log.Printf("[catalog] media catalogues warmed in %s", time.Since(started).Round(time.Millisecond))
	}
}

// creditsFor queries one token's billing state. A failure on any single
// endpoint degrades to that field being absent rather than failing the whole
// row — an unreachable trial endpoint must not hide the balance.
func (s *Server) creditsFor(ctx context.Context, index int, t *Token) creditSnapshot {
	base := s.creditBase()
	out := creditSnapshot{Index: index, Name: t.display(), UserID: t.UserID}

	var balance gwBalance
	if err := s.up.jsonCall(ctx, http.MethodGet, base, creditPathBalance, nil, &balance); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Balance = balance.TotalCredit

	var wallet gwWalletResp
	if err := s.up.jsonCall(ctx, http.MethodGet, base, creditPathWallet, nil, &wallet); err == nil {
		// The product spends the earliest-expiring bucket first, so that is the
		// one that will actually run out — surface it, not the sum.
		var earliest *gwSubCredit
		for wi := range wallet.Wallets {
			if out.SubscribeTo == "" {
				out.SubscribeTo = wallet.Wallets[wi].URL
			}
			for ci := range wallet.Wallets[wi].SubCredits {
				sc := &wallet.Wallets[wi].SubCredits[ci]
				if earliest == nil || expiresBefore(sc.EndTime, earliest.EndTime) {
					earliest = sc
				}
			}
		}
		if earliest != nil {
			out.CreditType = earliest.CreditType
			out.CreditName = creditTypeName(earliest.CreditType)
			out.Lifetime = creditTypeLifetime(earliest.CreditType)
			out.Credit = earliest.Credit
			out.ExpireAt = creditEndTime(earliest.EndTime)
		}
	}

	var trial gwTrial
	if err := s.up.jsonCall(ctx, http.MethodGet, base, creditPathTrial, nil, &trial); err == nil {
		out.FreeTrial = trial.FreeCount
		out.TrialHint = trial.ClaimHint
	}
	return out
}

// creditEndTime parses the wallet's string millisecond timestamp. Unparseable
// or absent timestamps become 0 rather than an error: the balance is still
// worth showing.
func creditEndTime(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// expiresBefore compares two raw timestamps, treating 0 as "unknown". An
// unknown expiry must never win the "earliest" comparison — otherwise a bucket
// with no end date would be reported as the one that runs out first.
func expiresBefore(a, b string) bool {
	x, y := creditEndTime(a), creditEndTime(b)
	if x == 0 {
		return false
	}
	if y == 0 {
		return true
	}
	return x < y
}

// handleAdminCredits reports the billing state of every pooled token.
//
// On demand rather than on a timer: each row costs three upstream calls, so
// polling it would be rude to the gateway. The console only asks when the
// operator clicks.
func (s *Server) handleAdminCredits(w http.ResponseWriter, r *http.Request) {
	s.cfg.mu.RLock()
	tokens := append([]*Token(nil), s.cfg.Tokens...)
	s.cfg.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	// The balances are independent, so fan out — but bounded, because a large
	// pool should not open fifty sockets to the gateway at once.
	const parallelism = 4
	out := make([]creditSnapshot, len(tokens))
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup

	for i, t := range tokens {
		wg.Add(1)
		go func(i int, t *Token) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = s.creditsFor(ctx, i, t)
		}(i, t)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}
