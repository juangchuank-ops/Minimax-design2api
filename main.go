package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// buildVersion is shown in the admin console's sidebar and about page. It is a
// plain variable so a release build can stamp it:
//
//	go build -ldflags "-X main.buildVersion=1.2.3"
var buildVersion = "1.1.0"

func main() {
	var (
		listen   = flag.String("listen", "", "override listen address, e.g. :8080")
		importTk = flag.Bool("import-token", false, "read the access token out of the locally installed MiniMax Design app and add it to the pool, then exit")
		showCfg  = flag.Bool("show", false, "print the resolved configuration and the model catalog, then exit")
		noFetch  = flag.Bool("no-catalog-fetch", false, "skip the startup /api/v1/config call")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	if *importTk {
		if err := importLocalToken(cfg); err != nil {
			log.Fatalf("import-token: %v", err)
		}
		if err := cfg.save(); err != nil {
			log.Fatalf("save config: %v", err)
		}
		for i, t := range cfg.Tokens {
			fmt.Printf("[%d] %-16s user=%s exp=%s\n", i, t.display(), t.UserID, expString(t.ExpireAt))
		}
		return
	}

	srv := newServer(cfg)

	if *showCfg {
		if !*noFetch {
			srv.refreshCatalog()
		}
		routes, src, at := srv.catalog.list()
		fmt.Printf("listen=%s gateway=%s upstream=%s source=%s at=%s\n",
			cfg.Listen, cfg.Gateway, cfg.Upstream, src, at.Format(time.RFC3339))
		for _, r := range routes {
			fmt.Printf("  %-28s proto=%-9s upstream=%-16s display=%s\n", r.ID, r.Protocol, r.Upstream, r.Display)
		}
		return
	}

	if !*noFetch {
		srv.refreshCatalog()
	}

	// Warm the media catalogues in the background. They are fetched from the
	// cloud gateway, and the voice table alone is 671 rows — tens of seconds on
	// a slow link. Without this, the first person to open a media picker waits
	// for that fetch; with it, the cost is paid once at boot where nobody is
	// looking. Best effort: a failure here is exactly what the per-catalogue
	// error handling in /admin/api/catalogs already reports.
	if len(cfg.Tokens) > 0 {
		go srv.warmMediaCatalogs()
	}

	log.Printf("minimax-design2api listening on %s", cfg.Listen)
	log.Printf("  gateway : %s", cfg.Gateway)
	log.Printf("  upstream: %s", cfg.Upstream)
	log.Printf("  tokens  : %d", len(cfg.Tokens))
	if len(cfg.APIKeys) == 0 {
		log.Printf("  auth    : OPEN (no api_keys configured)")
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv,
		ReadHeaderTimeout: 20 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func expString(ts int64) string {
	if ts == 0 {
		return "unknown"
	}
	t := time.Unix(ts, 0)
	d := time.Until(t)
	if d < 0 {
		return t.Format("2006-01-02") + " (EXPIRED)"
	}
	return fmt.Sprintf("%s (in %dd)", t.Format("2006-01-02"), int(d.Hours()/24))
}

// ---------------------------------------------------------------------------
// Local credential import.
//
// MiniMax Design keeps its access token in hub-config-global.json, encrypted as
//
//	v2enc:<iv b64>:<gcm tag b64>:<ciphertext b64>
//
// with a raw 32-byte AES-256-GCM key stored next to it in `.token-key`.
// Both files live under the Electron userData directory.
// ---------------------------------------------------------------------------

func candidateUserDataDirs() []string {
	var out []string
	switch runtime.GOOS {
	case "windows":
		roaming := os.Getenv("APPDATA")
		if roaming == "" {
			// Some shells (Git Bash, stripped service environments) do not
			// export APPDATA; derive it from the profile instead.
			if home, err := os.UserHomeDir(); err == nil {
				roaming = filepath.Join(home, "AppData", "Roaming")
			}
		}
		if roaming != "" {
			out = append(out,
				filepath.Join(roaming, "@hilo", "MiniMax Hub Global"),
				filepath.Join(roaming, "@hilo", "MiniMax Hub"),
				filepath.Join(roaming, "MiniMax Hub Global"),
			)
		}
		// The desktop app lets the user relocate its data directory; honour the
		// install record when it points somewhere else.
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			out = append(out, filepath.Join(local, "MiniMax"))
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			base := filepath.Join(home, "Library", "Application Support")
			out = append(out,
				filepath.Join(base, "@hilo", "MiniMax Hub Global"),
				filepath.Join(base, "MiniMax Hub Global"),
			)
		}
	default:
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out,
				filepath.Join(home, ".config", "@hilo", "MiniMax Hub Global"),
				filepath.Join(home, ".config", "MiniMax Hub Global"),
			)
		}
	}
	return out
}

func importLocalToken(cfg *Config) error {
	var dir string
	for _, d := range candidateUserDataDirs() {
		if _, err := os.Stat(filepath.Join(d, "hub-config-global.json")); err == nil {
			dir = d
			break
		}
	}
	if dir == "" {
		return fmt.Errorf("could not find hub-config-global.json; looked in:\n  %s", strings.Join(candidateUserDataDirs(), "\n  "))
	}

	keyPath := filepath.Join(dir, ".token-key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", keyPath, err)
	}
	if len(key) != 32 {
		return fmt.Errorf("%s holds %d bytes, expected a 32-byte AES-256 key", keyPath, len(key))
	}

	raw, err := os.ReadFile(filepath.Join(dir, "hub-config-global.json"))
	if err != nil {
		return err
	}
	var store struct {
		Tokens struct {
			AccessToken string `json:"accessToken"`
		} `json:"tokens"`
		LkgProviderConfig string `json:"lkgProviderConfig"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return fmt.Errorf("parse hub-config-global.json: %w", err)
	}
	if store.Tokens.AccessToken == "" {
		return fmt.Errorf("no accessToken stored — sign in to MiniMax Design first")
	}

	token, err := decryptV2Enc(key, store.Tokens.AccessToken)
	if err != nil {
		return fmt.Errorf("decrypt accessToken: %w", err)
	}

	t := &Token{Token: token, Region: "overseas", Name: "local-app"}
	decodeJWT(t)
	if t.UserID != "" {
		t.Name = "local-" + t.UserID
	}

	// de-duplicate
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	for _, existing := range cfg.Tokens {
		if existing.Token == token {
			existing.Name = t.Name
			fmt.Printf("token already present (index %d), refreshed name\n", indexOf(cfg.Tokens, existing))
			return nil
		}
	}
	cfg.Tokens = append(cfg.Tokens, t)
	fmt.Printf("imported token from %s\n", dir)
	return nil
}

func indexOf(list []*Token, want *Token) int {
	for i, t := range list {
		if t == want {
			return i
		}
	}
	return -1
}

func decryptV2Enc(key []byte, stored string) (string, error) {
	const prefix = "v2enc:"
	if !strings.HasPrefix(stored, prefix) {
		// Legacy / plaintext values are returned as-is by the desktop app.
		return stored, nil
	}
	parts := strings.Split(stored[len(prefix):], ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("expected 3 parts after %q, got %d", prefix, len(parts))
	}
	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("iv: %w", err)
	}
	tag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("tag: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	// Node's createCipheriv("aes-256-gcm", ...) is fed a 16-byte IV, while Go's
	// default GCM insists on a 12-byte nonce. Nonce size is not part of the GCM
	// security contract here (the IV is random per record), so widen it.
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, append(ct, tag...), nil)
	if err != nil {
		return "", fmt.Errorf("AES-GCM open failed (key rotated?): %w", err)
	}
	return string(plain), nil
}
