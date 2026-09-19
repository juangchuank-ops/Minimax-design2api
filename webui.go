package main

import (
	"embed"
	"net/http"
	"strings"
)

// The admin console is a plain HTML/CSS/JS bundle with no build step: the
// files in web/ are exactly what the binary serves. Keeping it that way is a
// deliberate project constraint — a Go toolchain and `go build` must be the
// only things needed to produce a working binary.
//
//go:embed web
var webFS embed.FS

func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" && r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	serveAdminAsset(w, r, "index.html")
}

// handleAdminAsset serves the console's stylesheet and script out of the
// binary.
//
// This is deliberately a flat, name-by-name lookup rather than a file server:
// there is no directory listing worth publishing, and a path containing a
// separator or a traversal segment should 404 rather than be resolved against
// the embedded filesystem.
func (s *Server) handleAdminAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/admin/")
	if name == "" {
		name = "index.html"
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	serveAdminAsset(w, r, name)
}

func serveAdminAsset(w http.ResponseWriter, r *http.Request, name string) {
	data, err := webFS.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", adminContentType(name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func adminContentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	default:
		return "text/html; charset=utf-8"
	}
}
