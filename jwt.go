package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// The MiniMax Design access token is a plain HS256 JWT. We never verify the
// signature (we cannot — the secret lives on MiniMax's side); we only read the
// `user.id` and `exp` claims for display and expiry handling.

type jwtClaims struct {
	Exp  int64 `json:"exp"`
	User struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Avatar   string `json:"avatar"`
		DeviceID string `json:"deviceID"`
		IsAnon   bool   `json:"isAnonymous"`
	} `json:"user"`
}

func decodeJWT(t *Token) {
	if t == nil || t.Token == "" {
		return
	}
	parts := strings.Split(t.Token, ".")
	if len(parts) != 3 {
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// some encoders emit standard base64 with padding
		raw, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return
		}
	}
	var c jwtClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return
	}
	if c.User.ID != "" {
		t.UserID = c.User.ID
	}
	if c.Exp > 0 {
		t.ExpireAt = c.Exp
	}
}

func (t *Token) expired() bool {
	if t.ExpireAt == 0 {
		return false
	}
	return time.Now().Unix() >= t.ExpireAt
}
