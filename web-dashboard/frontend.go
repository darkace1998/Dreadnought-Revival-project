package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/sirupsen/logrus"
)

//go:embed web
var webFS embed.FS

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// frontendHandler serves the embedded static UI. SPA-style fallback: unknown
// non-/api paths return index.html so deep links work.
func frontendHandler(log *logrus.Logger) http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.WithError(err).Warn("embed web dir")
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "dashboard UI missing", http.StatusInternalServerError)
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(sub, trimLeadingSlash(r.URL.Path)); err == nil {
				files.ServeHTTP(w, r)
				return
			}
			// Unknown asset path under /api already routed elsewhere; fall
			// through to index.html for client-side tabs (#/...).
			r2 := *r
			r2.URL.Path = "/"
			files.ServeHTTP(w, &r2)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func trimLeadingSlash(p string) string {
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	return p
}
