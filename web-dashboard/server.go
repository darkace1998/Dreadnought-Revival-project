package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// server bundles dependencies for the HTTP handlers.
type server struct {
	cfg      config
	log      *logrus.Logger
	sessions *sessions
	http     *http.Client
}

const (
	sessionCookie = "dn_dash"
	fieldStatus   = "status"
)

// ---------------------------------------------------------------- auth

// loginRequest is the only place the admin key crosses the browser boundary:
// the operator types it once, the server verifies it and mints a random
// session token. The key itself is never stored in the browser.
type loginRequest struct {
	AdminKey string `json:"admin_key"`
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !checkAdminKey(req.AdminKey, s.cfg.adminKey) {
		s.log.Warn("dashboard login failed")
		s.audit("login-failed", r.RemoteAddr)
		writeError(w, http.StatusUnauthorized, "invalid admin key")
		return
	}
	token, err := s.sessions.mint()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((sessionLifetime).Seconds()),
	})
	s.log.Info("dashboard login ok")
	s.audit("login", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok"})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok"})
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok"})
}

func (s *server) authed(r *http.Request) bool {
	// Header path: curl / admin-cli style usage.
	if checkAdminKey(r.Header.Get("X-Admin-Key"), s.cfg.adminKey) {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return s.sessions.valid(c.Value)
}

func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- health

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok", "service": "web-dashboard"})
}

// ---------------------------------------------------------------- upstream helpers

// upstreamGet performs an authenticated GET against another stack service and
// decodes the JSON body into a generic map. Non-JSON bodies are returned raw
// under "raw" so the dashboard shows something instead of failing silently.
func (s *server) upstreamGet(base, path, key string, query map[string]string) (int, map[string]any) {
	url := strings.TrimSuffix(base, "/") + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
		req.Header.Set("X-Internal-Key", key)
	}
	if len(query) > 0 {
		q := req.URL.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, map[string]any{"error": "read response: " + err.Error()}
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return resp.StatusCode, map[string]any{"raw": string(body)}
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return resp.StatusCode, decoded
}

// upstreamPostJSON POSTs a JSON payload with the admin key, used for the
// action endpoints (grant, ban, unban, stop-instance).
func (s *server) upstreamPostJSON(base, path, key string, payload any) (int, map[string]any) {
	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			return 0, map[string]any{"error": err.Error()}
		}
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(base, "/")+path, &buf)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
		req.Header.Set("X-Internal-Key", key)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, map[string]any{"error": "read response: " + err.Error()}
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil || decoded == nil {
		return resp.StatusCode, map[string]any{"raw": string(body)}
	}
	return resp.StatusCode, decoded
}

func (s *server) upstreamDelete(base, path, key string) (int, map[string]any) {
	req, err := http.NewRequest(http.MethodDelete, strings.TrimSuffix(base, "/")+path, nil)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
		req.Header.Set("X-Internal-Key", key)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, map[string]any{"error": "read response: " + err.Error()}
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil || decoded == nil {
		return resp.StatusCode, map[string]any{"raw": string(body)}
	}
	return resp.StatusCode, decoded
}

// ---------------------------------------------------------------- status aggregation

type serviceTarget struct {
	name string
	url  string
}

func (s *server) serviceTargets() []serviceTarget {
	return []serviceTarget{
		{"auth-server", s.cfg.authURL},
		{"legacy-api", s.cfg.legacyURL},
		{"mmogbrain", s.cfg.mmogURL},
		{"master-server", s.cfg.masterURL},
		{"game-manager", s.cfg.gameMgrURL},
	}
}

// apiStatus fans out to every service's /health in parallel and returns one
// combined document. The frontend polls this to render the overview grid and
// to feed its local time-series graphs.
func (s *server) apiStatus(w http.ResponseWriter, _ *http.Request) {
	targets := s.serviceTargets()
	type result struct {
		name string
		doc  map[string]any
	}
	ch := make(chan result, len(targets))
	for _, t := range targets {
		go func(t serviceTarget) {
			start := time.Now()
			code, doc := s.upstreamGet(t.url, "/health", "", nil)
			doc["http_code"] = code
			doc["latency_ms"] = time.Since(start).Milliseconds()
			doc["up"] = code == http.StatusOK
			ch <- result{name: t.name, doc: doc}
		}(t)
	}
	services := make(map[string]any, len(targets))
	up := 0
	for range targets {
		res := <-ch
		services[res.name] = res.doc
		if ok, _ := res.doc["up"].(bool); ok {
			up++
		}
	}
	// Best-effort extras for the hero cards (failures leave zero values).
	var queued, active, instances, servers, online int
	if code, doc := s.upstreamGet(s.cfg.mmogURL, "/health", "", nil); code == http.StatusOK {
		queued = int(jsonNumber(doc["queued_players"]))
		active = int(jsonNumber(doc["active_matches"]))
	}
	if code, doc := s.upstreamGet(s.cfg.gameMgrURL, "/instances", "", nil); code == http.StatusOK {
		instances = int(jsonNumber(doc["count"]))
	}
	if code, doc := s.upstreamGet(s.cfg.masterURL, "/servers", "", nil); code == http.StatusOK {
		servers = int(jsonNumber(doc["count"]))
	}
	if code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/online", s.cfg.adminKey, nil); code == http.StatusOK {
		online = int(jsonNumber(doc["count"]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"services":       services,
		"up":             up,
		"total":          len(targets),
		"queued_players": queued,
		"active_matches": active,
		"instances":      instances,
		"servers":        servers,
		"online":         online,
		"time":           time.Now().UTC().Format(time.RFC3339),
	})
}

func jsonNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

// ---------------------------------------------------------------- read endpoints (proxied)

func (s *server) apiQueue(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/queue", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiPlayers(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/players", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

// normJoinID folds the two spellings one account takes across services: the
// auth users.id (dashed UUID) and the mmog player_state user_id (the same
// UUID with hyphens stripped). Same folding as the matchmaker's
// normalizeUserID, so both spellings join.
func normJoinID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

// apiAccounts merges every REGISTERED account (auth-server) with its live
// balances (mmogbrain), so the dashboard shows players that never got past
// the launcher as well as veterans. mmog rows without an auth account (dev
// pids, legacy rows) are appended with an empty account side.
func (s *server) apiAccounts(w http.ResponseWriter, _ *http.Request) {
	_, usersDoc := s.upstreamGet(s.cfg.authURL, "/admin/users", s.cfg.adminKey, nil)
	_, playersDoc := s.upstreamGet(s.cfg.mmogURL, "/admin/players", s.cfg.adminKey, nil)

	balances := map[string]map[string]any{}
	if list, ok := playersDoc["players"].([]any); ok {
		for _, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := m["user_id"].(string); id != "" {
				balances[normJoinID(id)] = m
			}
		}
	}
	type account struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		Email         string `json:"email"`
		Banned        bool   `json:"banned"`
		BanReason     string `json:"ban_reason"`
		PlayerID      string `json:"player_id"`
		HasPlayerData bool   `json:"has_player_data"`
		Credits       int64  `json:"credits"`
		Premium       int64  `json:"premium"`
		FreeXP        int64  `json:"free_xp"`
		Rank          int    `json:"rank"`
	}
	out := []account{}
	seen := map[string]bool{}
	strField := func(m map[string]any, key string) string {
		v, _ := m[key].(string)
		return v
	}
	if list, ok := usersDoc["users"].([]any); ok {
		for _, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			id := strField(m, "id")
			a := account{
				ID:        id,
				Username:  strField(m, "username"),
				Email:     strField(m, "email"),
				BanReason: strField(m, "ban_reason"),
			}
			if b, _ := m["banned"].(bool); b {
				a.Banned = true
			}
			if p, ok := balances[normJoinID(id)]; ok {
				seen[normJoinID(id)] = true
				a.PlayerID, _ = p["user_id"].(string)
				a.HasPlayerData = true
				a.Credits = int64(jsonNumber(p["credits"]))
				a.Premium = int64(jsonNumber(p["premium"]))
				a.FreeXP = int64(jsonNumber(p["free_xp"]))
				a.Rank = int(jsonNumber(p["rank"]))
			}
			out = append(out, a)
		}
	}
	for id, p := range balances {
		if seen[id] {
			continue
		}
		name, _ := p["display_name"].(string)
		pid, _ := p["user_id"].(string)
		out = append(out, account{
			Username:      name,
			PlayerID:      pid,
			HasPlayerData: true,
			Credits:       int64(jsonNumber(p["credits"])),
			Premium:       int64(jsonNumber(p["premium"])),
			FreeXP:        int64(jsonNumber(p["free_xp"])),
			Rank:          int(jsonNumber(p["rank"])),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out, "count": len(out)})
}

func (s *server) apiInstances(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.gameMgrURL, "/instances", "", nil)
	writeJSON(w, codeOr(code), doc)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *server) apiInstance(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid instance id: expected UUID")
		return
	}
	code, doc := s.upstreamGet(s.cfg.gameMgrURL, "/instances/"+id, "", nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiServers(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.masterURL, "/servers", "", nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiChat(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "global"
	}
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/mmog/chat", "", map[string]string{
		"channel": channel,
		"limit":   "50",
	})
	writeJSON(w, codeOr(code), doc)
}

// apiBroadcast forwards an operator message to mmogbrain's /admin/broadcast:
// delivered live to connected channel members and stored in chat history.
func (s *server) apiBroadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content required")
		return
	}
	code, doc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/broadcast", s.cfg.adminKey, map[string]string{
		"channel": req.Channel, "content": req.Content,
	})
	s.log.WithField("channel", req.Channel).Info("dashboard broadcast")
	s.audit("broadcast", req.Channel)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiOnline(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/online", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiResults(w http.ResponseWriter, r *http.Request) {
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/results", s.cfg.adminKey, map[string]string{
		"limit": r.URL.Query().Get("limit"),
	})
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiBans(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.authURL, "/admin/bans", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiTiles(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.legacyURL, "/admin/tiles", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

var tileIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (s *server) apiUpsertTile(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	id, _ := payload["id"].(string)
	if !tileIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid tile id")
		return
	}
	code, doc := s.upstreamPostJSON(s.cfg.legacyURL, "/admin/tiles", s.cfg.adminKey, payload)
	s.log.WithField("tile", id).Info("dashboard tile saved")
	s.audit("tile-save", id)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiDeleteTile(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !tileIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid tile id")
		return
	}
	code, doc := s.upstreamDelete(s.cfg.legacyURL, "/admin/tiles/"+id, s.cfg.adminKey)
	s.log.WithField("tile", id).Info("dashboard tile deleted")
	s.audit("tile-delete", id)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiProvision(w http.ResponseWriter, r *http.Request) {	var payload map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	id, _ := payload["user_id"].(string)
	if !playerIDPattern.MatchString(strings.ToLower(id)) {
		writeError(w, http.StatusBadRequest, "user_id must be a 32-hex player id")
		return
	}
	code, doc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/provision", s.cfg.adminKey, payload)
	s.log.WithField("user_id", id).Warn("dashboard provisioned test account")
	s.audit("provision", id)
	writeJSON(w, codeOr(code), doc)
}

// apiReset forwards an account reset (currencies and/or research back to
// fresh-account values) to mmogbrain's /admin/reset.
func (s *server) apiReset(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	id, _ := payload["user_id"].(string)
	if !playerIDPattern.MatchString(strings.ToLower(id)) {
		writeError(w, http.StatusBadRequest, "user_id must be a 32-hex player id")
		return
	}
	code, doc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/reset", s.cfg.adminKey, payload)
	s.log.WithField("user_id", id).Warn("dashboard reset an account")
	s.audit("reset", id)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiPlayerDetail(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !playerIDPattern.MatchString(strings.ToLower(id)) {
		writeError(w, http.StatusBadRequest, "id must be a 32-hex player id")
		return
	}
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/player/"+id, s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiQueueKick(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid queue entry id: expected UUID")
		return
	}
	code, doc := s.upstreamDelete(s.cfg.mmogURL, "/admin/queue/kick/"+id, s.cfg.adminKey)
	s.log.WithField("queue_entry", id).Info("dashboard queue kick")
	s.audit("queue-kick", id)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiQueueClear(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/queue/clear", s.cfg.adminKey, nil)
	s.log.Info("dashboard queue clear")
	s.audit("queue-clear", "")
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiForceMatch(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/force-match", s.cfg.adminKey, nil)
	s.log.Info("dashboard force match")
	s.audit("force-match", "")
	writeJSON(w, codeOr(code), doc)
}

// apiBackups lists the timestamped backup archives scripts/backup.sh writes
// (default $PROJECT_DIR/backups). Read-only: it never runs a backup.
func (s *server) apiBackups(w http.ResponseWriter, _ *http.Request) {
	dir := ""
	for _, c := range []string{filepath.Join(s.cfg.runDir, "..", "backups"), "backups", filepath.Join("..", "backups")} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			dir = c
			break
		}
	}
	type archive struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		ModTime string `json:"mtime"`
	}
	out := []archive{}
	if dir != "" {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, archive{Name: e.Name(), Size: fi.Size(), ModTime: fi.ModTime().UTC().Format(time.RFC3339)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
		if len(out) > 20 {
			out = out[:20]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir, "backups": out, "count": len(out)})
}

// auditPath is the dashboard's own action log: every mutating operator action
// lands here as one JSON line (who did what, when). Read back via /api/audit.
func (s *server) auditPath() string {
	return filepath.Join(s.cfg.runDir, "web-dashboard-audit.log")
}

func (s *server) audit(action, detail string) {
	line, _ := json.Marshal(map[string]string{
		"time":   time.Now().UTC().Format(time.RFC3339),
		"action": action, "detail": detail,
	})
	f, err := os.OpenFile(s.auditPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.log.WithError(err).Warn("audit log write failed")
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// apiAudit tails the action log (newest last, like the other log endpoints).
func (s *server) apiAudit(w http.ResponseWriter, r *http.Request) {
	lines, _, err := tailFile(s.auditPath(), clampLines(r.URL.Query().Get("lines")))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": lines, "count": len(lines)})
}

// crashDir locates the gateway's crash-report directory: it defaults to
// "crash-reports" under the gateway's working directory (the repo root when
// started via start-services.sh), or CRASH_REPORT_DIR when overridden.
func (s *server) crashDir() string {
	if v := os.Getenv("CRASH_REPORT_DIR"); v != "" {
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			return v
		}
	}
	for _, c := range []string{"crash-reports", filepath.Join("..", "crash-reports")} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return ""
}

func validCrashName(name string) bool {
	return name != "" && !strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, ".")
}

// apiCrashes lists crash reports, or tails one text file inside. UE4 client
// crashes (.dmp) are binary and refused for viewing; the accompanying .log
// files tail like any other log. One directory level only.
func (s *server) apiCrashes(w http.ResponseWriter, r *http.Request) {
	dir := s.crashDir()
	if dir == "" {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []string{}, "note": "no crash reports yet"})
		return
	}
	sub, file := r.URL.Query().Get("dir"), r.URL.Query().Get("file")
	if file != "" {
		if !validCrashName(sub) && sub != "" || !validCrashName(file) {
			writeError(w, http.StatusBadRequest, "invalid file")
			return
		}
		path := filepath.Join(dir, file)
		if sub != "" {
			path = filepath.Join(dir, sub, file)
		}
		if lower := strings.ToLower(file); strings.HasSuffix(lower, ".dmp") || strings.HasSuffix(lower, ".mdmp") {
			writeError(w, http.StatusNotFound, "binary dumps are listed, not viewed")
			return
		}
		if st, err := os.Stat(path); err != nil || st.IsDir() || st.Size() > 1<<19 {
			writeError(w, http.StatusNotFound, "not a viewable report file")
			return
		}
		lines, truncated, err := tailFile(path, clampLines(r.URL.Query().Get("lines")))
		if err != nil {
			writeError(w, http.StatusNotFound, "cannot read report")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"file": file, "lines": lines, "truncated": truncated})
		return
	}
	if sub != "" {
		if !validCrashName(sub) {
			writeError(w, http.StatusBadRequest, "invalid directory")
			return
		}
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			writeError(w, http.StatusNotFound, "no such report")
			return
		}
		type entry struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		out := []entry{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, entry{Name: e.Name(), Size: fi.Size()})
		}
		writeJSON(w, http.StatusOK, map[string]any{"dir": sub, "files": out})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []string{}})
		return
	}
	type entry struct {
		Name  string `json:"name"`
		Dir   bool   `json:"dir"`
		Size  int64  `json:"size"`
		MTime string `json:"mtime"`
	}
	out := []entry{}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		var size int64
		if !e.IsDir() {
			size = fi.Size()
		}
		out = append(out, entry{Name: e.Name(), Dir: e.IsDir(), Size: size,
			MTime: fi.ModTime().UTC().Format(time.RFC3339)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	if len(out) > 100 {
		out = out[:100]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "count": len(out)})
}

func (s *server) apiMatches(w http.ResponseWriter, r *http.Request) {
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/matches", s.cfg.adminKey, map[string]string{
		"limit": r.URL.Query().Get("limit"),
	})
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiCatalog(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/catalog", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiMatchDetail(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if len(id) < 1 || len(id) > 64 || strings.ContainsAny(id, " \t\r\n/") {
		writeError(w, http.StatusBadRequest, "invalid match id")
		return
	}
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/match/"+id, s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiPlayerProgress(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !playerIDPattern.MatchString(strings.ToLower(id)) {
		writeError(w, http.StatusBadRequest, "id must be a 32-hex player id")
		return
	}
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/player/"+id+"/progress", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiHistory(w http.ResponseWriter, r *http.Request) {
	code, doc := s.upstreamGet(s.cfg.legacyURL, "/admin/matches", s.cfg.adminKey, map[string]string{
		"limit": r.URL.Query().Get("limit"),
	})
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiSessions(w http.ResponseWriter, _ *http.Request) {
	code, doc := s.upstreamGet(s.cfg.authURL, "/admin/sessions", s.cfg.adminKey, nil)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid session id: expected UUID")
		return
	}
	code, doc := s.upstreamDelete(s.cfg.authURL, "/admin/sessions/"+id, s.cfg.adminKey)
	s.log.WithField("session", id).Info("dashboard revoked a session")
	s.audit("revoke-session", id)
	writeJSON(w, codeOr(code), doc)
}

func codeOr(code int) int {
	if code == 0 {
		return http.StatusBadGateway
	}
	return code
}

// ---------------------------------------------------------------- actions (proxied, validated)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_\-.]{1,64}$`)

func (s *server) apiBan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !usernamePattern.MatchString(req.Username) {
		writeError(w, http.StatusBadRequest, "invalid username")
		return
	}
	code, doc := s.upstreamPostJSON(s.cfg.authURL, "/admin/ban", s.cfg.adminKey, map[string]string{
		"username": req.Username, "reason": req.Reason,
	})
	s.log.WithField("username", req.Username).Info("dashboard ban")
	s.audit("ban", req.Username+" / "+req.Reason)
	writeJSON(w, codeOr(code), doc)
}

func (s *server) apiUnban(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !usernamePattern.MatchString(req.Username) {
		writeError(w, http.StatusBadRequest, "invalid username")
		return
	}
	code, doc := s.upstreamPostJSON(s.cfg.authURL, "/admin/unban", s.cfg.adminKey, map[string]string{
		"username": req.Username,
	})
	s.log.WithField("username", req.Username).Info("dashboard unban")
	s.audit("unban", req.Username)
	writeJSON(w, codeOr(code), doc)
}

var playerIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

func (s *server) apiGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID  string `json:"user_id"`
		Credits *int64 `json:"credits"`
		Premium *int64 `json:"premium"`
		FreeXP  *int64 `json:"free_xp"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !playerIDPattern.MatchString(strings.ToLower(req.UserID)) {
		writeError(w, http.StatusBadRequest, "user_id must be a 32-hex player id")
		return
	}
	payload := map[string]any{"user_id": strings.ToLower(req.UserID)}
	for name, v := range map[string]*int64{"credits": req.Credits, "premium": req.Premium, "free_xp": req.FreeXP} {
		if v == nil {
			continue
		}
		if *v < 0 || *v > 1_000_000_000 {
			writeError(w, http.StatusBadRequest, name+" out of range")
			return
		}
		payload[name] = *v
	}
	code, doc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/grant", s.cfg.adminKey, payload)
	s.log.WithField("user_id", req.UserID).Info("dashboard grant")
	s.audit("grant", req.UserID)
	writeJSON(w, codeOr(code), doc)
}

// apiGrantAll adds the same amounts to EVERY account with player_state, so an
// operator can fund all testers at once instead of granting pid by pid. Each
// per-player grant goes through mmogbrain's /admin/grant (same validation,
// same audit log there); failures are collected per player, not fatal.
func (s *server) apiGrantAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credits int64 `json:"credits"`
		Premium int64 `json:"premium"`
		FreeXP  int64 `json:"free_xp"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	for name, v := range map[string]int64{"credits": req.Credits, "premium": req.Premium, "free_xp": req.FreeXP} {
		if v < 0 || v > 1_000_000_000 {
			writeError(w, http.StatusBadRequest, name+" out of range")
			return
		}
	}
	if req.Credits == 0 && req.Premium == 0 && req.FreeXP == 0 {
		writeError(w, http.StatusBadRequest, "nothing to grant")
		return
	}
	code, doc := s.upstreamGet(s.cfg.mmogURL, "/admin/players", s.cfg.adminKey, nil)
	if code != http.StatusOK {
		writeJSON(w, codeOr(code), doc)
		return
	}
	list, _ := doc["players"].([]any)
	type result struct {
		UserID string `json:"user_id"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(list))
	granted := 0
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["user_id"].(string)
		if id == "" {
			continue
		}
		payload := map[string]any{"user_id": id}
		if req.Credits > 0 {
			payload["credits"] = req.Credits
		}
		if req.Premium > 0 {
			payload["premium"] = req.Premium
		}
		if req.FreeXP > 0 {
			payload["free_xp"] = req.FreeXP
		}
		gcode, gdoc := s.upstreamPostJSON(s.cfg.mmogURL, "/admin/grant", s.cfg.adminKey, payload)
		if gcode == http.StatusOK {
			granted++
			results = append(results, result{UserID: id, OK: true})
		} else {
			msg, _ := gdoc["error"].(string)
			if msg == "" {
				msg = "grant failed"
			}
			results = append(results, result{UserID: id, Error: msg})
		}
	}
	s.log.WithFields(logrus.Fields{
		"granted": granted, "failed": len(results) - granted,
		"credits": req.Credits, "premium": req.Premium, "free_xp": req.FreeXP,
	}).Warn("dashboard grant-all")
	s.audit("grant-all", "granted")
	writeJSON(w, http.StatusOK, map[string]any{
		"granted": granted, "failed": len(results) - granted, "results": results,
	})
}

func (s *server) apiStopInstance(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid instance id: expected UUID")
		return
	}
	code, doc := s.upstreamDelete(s.cfg.gameMgrURL, "/instances/"+id, s.cfg.internalKey)
	s.log.WithField("instance_id", id).Info("dashboard stop-instance")
	s.audit("stop-instance", id)
	writeJSON(w, codeOr(code), doc)
}

// ---------------------------------------------------------------- config (non-secret)

var dnSwitches = []string{
	"PLAYERS_PER_MATCH",
	"DN_MATCH_AUTOSCALE",
	"DN_MATCH_MAX_PLAYERS",
	"DN_MATCH_MAX_WAIT",
	"DN_CONNECT_PUSH_DELAY",
	"DN_FORCE_GAME_MODE",
	"DN_CONTROL_PLANE",
	"DN_TECHTREE_LIMIT",
	"DN_NO_DEFER_PLAYER_FLEETS",
	"DN_ALLOW_MOCK_INSTANCES",
	"DN_ENGINE_LOG_CMDS",
	"DN_MAP_URL_OPTION",
}

// apiConfig reports operator-relevant configuration WITHOUT secret values:
// only whether a secret is set, never the value itself.
func (s *server) apiConfig(w http.ResponseWriter, _ *http.Request) {
	switches := make(map[string]string, len(dnSwitches))
	for _, name := range dnSwitches {
		switches[name] = os.Getenv(name)
	}
	gameBinary := os.Getenv("GAME_BINARY")
	certInfo := s.certInfo()
	if _, err := os.Stat(gameBinary); err == nil {
		certInfo["game_binary_exists"] = true
	} else {
		certInfo["game_binary_exists"] = false
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server_ip":       os.Getenv("SERVER_IP"),
		"public_host":     os.Getenv("PUBLIC_HOST"),
		"game_binary":     gameBinary,
		"dashboard_addr":  s.cfg.addr,
		"jwt_secret_set":  os.Getenv("JWT_SECRET") != "",
		"admin_key_set":   s.cfg.adminKey != "",
		"internal_key_set": s.cfg.internalKey != "",
		"switches":        switches,
		"cert":            certInfo,
	})
}

// certInfo parses the TLS certificate for expiry and SANs. Failures are
// reported as fields, never as HTTP errors: the dashboard must render even
// when certs are missing.
func (s *server) certInfo() map[string]any {
	info := map[string]any{"file": s.cfg.certFile}
	raw, err := os.ReadFile(filepath.Clean(s.cfg.certFile))
	if err != nil {
		info["error"] = "not found (run scripts/gen-certs.sh)"
		return info
	}
	var block *pem.Block
	for {
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		info["subject"] = cert.Subject.String()
		info["not_after"] = cert.NotAfter.UTC().Format(time.RFC3339)
		info["expired"] = time.Now().After(cert.NotAfter)
		info["dns_names"] = cert.DNSNames
		info["ip_addresses"] = cert.IPAddresses
		break
	}
	return info
}

// ---------------------------------------------------------------- metrics summary

// apiMetricsSummary scrapes every service's Prometheus exposition and extracts
// a small set of interesting gauges so the frontend can draw charts without
// parsing the full exposition format in the browser.
func (s *server) apiMetricsSummary(w http.ResponseWriter, _ *http.Request) {
	targets := []struct {
		name string
		url  string
	}{
		{"auth-server", s.cfg.authURL},
		{"legacy-api", s.cfg.legacyURL},
		{"mmogbrain", s.cfg.mmogURL},
		{"master-server", s.cfg.masterURL},
		{"game-manager", s.cfg.gameMgrURL},
	}
	prefixes := []string{"dn_", "go_goroutines", "go_memstats_alloc_bytes", "process_resident_memory_bytes"}
	out := make(map[string]any, len(targets))
	for _, t := range targets {
		out[t.name] = scrapeMetrics(s.http, t.url+"/metrics", prefixes)
	}
	writeJSON(w, http.StatusOK, out)
}

// scrapeMetrics fetches a Prometheus exposition and keeps only lines for
// series whose name starts with one of the given prefixes. Bounded and
// failure-tolerant: a down service yields {"error": ...}.
func scrapeMetrics(client *http.Client, url string, prefixes []string) map[string]any {
	resp, err := client.Get(url) //nolint:gosec // operator-configured loopback service URL.
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	gauges := make(map[string]float64)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
		} else if i := strings.LastIndex(line, " "); i >= 0 {
			name = line[:i]
		}
		keep := false
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				keep = true
				break
			}
		}
		if !keep {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			gauges[name] = v
		}
		if len(gauges) >= 64 {
			break
		}
	}
	// Deterministic key order helps the frontend render stable tables.
	keys := make([]string, 0, len(gauges))
	for k := range gauges {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(gauges)+1)
	for _, k := range keys {
		ordered[k] = gauges[k]
	}
	return ordered
}
