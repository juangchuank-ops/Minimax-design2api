package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config is the on-disk configuration (config.json next to the binary).
type Config struct {
	Listen       string   `json:"listen"`
	Gateway      string   `json:"gateway"`      // cloud gateway, serves /api/v1/config (token catalog)
	Upstream     string   `json:"upstream"`     // LLM gateway host, e.g. https://hub.minimax.io
	VideoBase    string   `json:"video_base"`   // media/video host, e.g. https://design.minimax.io (defaults to Gateway)
	VersionCode  string   `json:"version_code"` // sent as `version_code` header; upstream rejects anything below 1.0.0
	AppID        string   `json:"app_id"`       // sent as `app_id`
	DevicePlat   string   `json:"device_platform"`
	APIKeys      []string `json:"api_keys"` // keys clients must present; empty slice = no auth
	DefaultModel string   `json:"default_model"`
	TimeoutSec   int      `json:"request_timeout_sec"`
	Tokens       []*Token `json:"tokens"`

	mu   sync.RWMutex
	path string
}

// Token is one MiniMax Design account credential.
type Token struct {
	Name     string `json:"name,omitempty"`
	Token    string `json:"token"`
	Region   string `json:"region,omitempty"` // "overseas" | "domestic"; only affects which gateway is used
	Enabled  *bool  `json:"enabled,omitempty"`
	UserID   string `json:"user_id,omitempty"`   // decoded from the JWT, display only
	ExpireAt int64  `json:"expire_at,omitempty"` // unix seconds, decoded from the JWT
	Note     string `json:"note,omitempty"`

	// runtime state, not persisted
	fails      int
	cooldownT  time.Time
	coolReason string // see the reason* constants in pool.go
	inflight   int
}

func (t *Token) isEnabled() bool { return t.Enabled == nil || *t.Enabled }

func (t *Token) display() string {
	if t.Name != "" {
		return t.Name
	}
	if t.UserID != "" {
		return t.UserID
	}
	if len(t.Token) > 12 {
		return t.Token[:12] + "..."
	}
	return t.Token
}

func defaultConfig() *Config {
	return &Config{
		Listen:       ":8080",
		Gateway:      "https://design.minimax.io",
		Upstream:     "https://hub.minimax.io",
		VersionCode:  "3.0.16",
		AppID:        "3001",
		DevicePlat:   "desktop",
		APIKeys:      []string{},
		DefaultModel: "MiniMax-M3",
		TimeoutSec:   600,
		Tokens:       []*Token{},
	}
}

func configPath() string {
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "config.json")
	}
	return "config.json"
}

func loadConfig() (*Config, error) {
	p := configPath()
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c := defaultConfig()
			c.path = p
			_ = c.save()
			return c, nil
		}
		return nil, err
	}
	c := defaultConfig()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("config.json is not valid JSON: %w", err)
	}
	c.path = p
	c.normalize()
	return c, nil
}

func (c *Config) normalize() {
	d := defaultConfig()
	if c.Listen == "" {
		c.Listen = d.Listen
	}
	if c.Gateway == "" {
		c.Gateway = d.Gateway
	}
	if c.Upstream == "" {
		c.Upstream = d.Upstream
	}
	if c.VersionCode == "" {
		c.VersionCode = d.VersionCode
	}
	if c.AppID == "" {
		c.AppID = d.AppID
	}
	if c.DevicePlat == "" {
		c.DevicePlat = d.DevicePlat
	}
	if c.DefaultModel == "" {
		c.DefaultModel = d.DefaultModel
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = d.TimeoutSec
	}
	if c.APIKeys == nil {
		c.APIKeys = []string{}
	}
	if c.Tokens == nil {
		c.Tokens = []*Token{}
	}
	c.Gateway = strings.TrimRight(c.Gateway, "/")
	c.Upstream = strings.TrimRight(c.Upstream, "/")
	if c.VideoBase == "" {
		// Video lives on the cloud gateway host, not the LLM hub: the same
		// token authorises both, but the paths only exist on design.minimax.io.
		c.VideoBase = c.Gateway
	}
	c.VideoBase = strings.TrimRight(c.VideoBase, "/")
	for _, t := range c.Tokens {
		if t.Region == "" {
			t.Region = "overseas"
		}
		decodeJWT(t)
	}
}

func (c *Config) save() error {
	c.mu.RLock()
	snapshot := struct {
		Listen       string   `json:"listen"`
		Gateway      string   `json:"gateway"`
		Upstream     string   `json:"upstream"`
		VideoBase    string   `json:"video_base"`
		VersionCode  string   `json:"version_code"`
		AppID        string   `json:"app_id"`
		DevicePlat   string   `json:"device_platform"`
		APIKeys      []string `json:"api_keys"`
		DefaultModel string   `json:"default_model"`
		TimeoutSec   int      `json:"request_timeout_sec"`
		Tokens       []*Token `json:"tokens"`
	}{
		Listen: c.Listen, Gateway: c.Gateway, Upstream: c.Upstream,
		VideoBase:   c.VideoBase,
		VersionCode: c.VersionCode, AppID: c.AppID, DevicePlat: c.DevicePlat,
		APIKeys: c.APIKeys, DefaultModel: c.DefaultModel, TimeoutSec: c.TimeoutSec,
		Tokens: c.Tokens,
	}
	c.mu.RUnlock()

	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if c.path == "" {
		c.path = configPath()
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// filePath is the resolved config.json location, shown in the admin console so
// an operator can find the file they are editing without guessing the cwd.
func (c *Config) filePath() string {
	if c.path != "" {
		return c.path
	}
	return configPath()
}

func (c *Config) timeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Duration(c.TimeoutSec) * time.Second
}

func (c *Config) settings() (gateway, upstream, versionCode, appID, devicePlat string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Gateway, c.Upstream, c.VersionCode, c.AppID, c.DevicePlat
}

func (c *Config) videoBase() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.VideoBase != "" {
		return c.VideoBase
	}
	return c.Gateway
}
