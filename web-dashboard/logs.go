package main

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// logSources is the allowlist of tail-able log files. Names map to paths
// resolved against RUN_DIR (run/*.log) plus the two special cases documented
// in AGENTS.md: mmogbrain's frame log lives at the repo root, and battle logs
// live per-instance under run/battle-logs/.
//
// No request-supplied path is ever opened: the "name" parameter only selects
// from this table, so directory traversal cannot escape it.
func (s *server) logSources() map[string]string {
	run := s.cfg.runDir
	candidates := []string{run, filepath.Join("..", run), "."}
	find := func(names ...string) string {
		for _, dir := range candidates {
			for _, n := range names {
				p := filepath.Join(dir, n)
				if _, err := os.Stat(p); err == nil {
					abs, err := filepath.Abs(p)
					if err == nil {
						return abs
					}
					return p
				}
			}
		}
		abs, _ := filepath.Abs(filepath.Join(run, names[0]))
		return abs
	}
	// mmogbrain's detailed frame log: repo-root mmogbrain.log, NOT run/.
	frame := ""
	for _, c := range []string{"mmogbrain.log", filepath.Join("..", "mmogbrain.log")} {
		if _, err := os.Stat(c); err == nil {
			abs, err := filepath.Abs(c)
			if err == nil {
				frame = abs
			} else {
				frame = c
			}
			break
		}
	}
	return map[string]string{
		"auth-server":   find("auth-server.log"),
		"legacy-api":    find("legacy-api.log"),
		"master-server": find("master-server.log"),
		"game-manager":  find("game-manager.log"),
		"dn-dedicated":  find("dn-dedicated.log"),
		"gateway":       find("gateway.log"),
		"mmogbrain":     find("mmogbrain.log"),
		"mmog-frames":   frame,
		"web-dashboard": find("web-dashboard.log"),
	}
}

// apiLogs tails one allowlisted log: GET /api/logs?name=<source>&lines=200.
// Battle logs are listed separately via ?name=battle-logs (directory listing)
// and read via ?name=battle-logs&file=<basename>.
func (s *server) apiLogs(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		names := make([]string, 0)
		for n := range s.logSources() {
			names = append(names, n)
		}
		names = append(names, "battle-logs")
		sort.Strings(names)
		writeJSON(w, http.StatusOK, map[string]any{"sources": names})
		return
	}
	if name == "battle-logs" {
		s.apiBattleLogs(w, r)
		return
	}
	sources := s.logSources()
	path, ok := sources[name]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown log source")
		return
	}
	if path == "" {
		// Known source, but the file does not exist (yet) anywhere we look.
		writeJSON(w, http.StatusOK, map[string]any{
			"name": name, "path": "", "lines": []string{},
			"note": "no log yet (service never started or log rotated away)",
		})
		return
	}
	lines := clampLines(r.URL.Query().Get("lines"))
	content, truncated, err := tailFile(path, lines)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"name": name, "path": path, "lines": []string{},
			"note": "no log yet (service never started or log rotated away)",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "path": path, "lines": content, "truncated": truncated,
	})
}

// apiBattleLogs lists run/battle-logs/ (newest first) or tails one file by
// basename. The basename must not contain a path separator.
func (s *server) apiBattleLogs(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.cfg.runDir, "battle-logs")
	for _, c := range []string{dir, filepath.Join("..", dir)} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			dir = c
			break
		}
	}
	file := r.URL.Query().Get("file")
	if file == "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"files": []string{}})
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
				names = append(names, e.Name())
			}
		}
		// ReadDir sorts by filename; battle logs are timestamped, so reverse
		// for newest-first.
		for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
			names[i], names[j] = names[j], names[i]
		}
		if len(names) > 50 {
			names = names[:50]
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": names})
		return
	}
	if strings.ContainsAny(file, `/\`) || !strings.HasSuffix(file, ".log") {
		writeError(w, http.StatusBadRequest, "invalid file")
		return
	}
	content, truncated, err := tailFile(filepath.Join(dir, file), clampLines(r.URL.Query().Get("lines")))
	if err != nil {
		writeError(w, http.StatusNotFound, "battle log not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "lines": content, "truncated": truncated})
}

func clampLines(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 200
	}
	if n > 2000 {
		return 2000
	}
	return n
}

// tailFile returns the last n lines of a file without loading all of it into
// a single string join. Simple and bounded: lines are capped at 2000.
func tailFile(path string, n int) ([]string, bool, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]string, 0, n)
	sc := bufio.NewScanner(f)
	const maxLine = 64 * 1024
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	total := 0
	for sc.Scan() {
		total++
		if len(buf) < n {
			buf = append(buf, sc.Text())
		} else {
			copy(buf, buf[1:])
			buf[n-1] = sc.Text()
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	return buf, total > n, nil
}

func loggingMiddleware(log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/status" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			log.WithFields(logrus.Fields{"method": r.Method, "path": r.URL.Path}).Info("request")
			next.ServeHTTP(w, r)
		})
	}
}
