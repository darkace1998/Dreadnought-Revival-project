package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// instanceIDPattern matches the canonical UUID format game-manager assigns to
// instances (uuid.New().String(), e.g. "3fa85f64-5717-4562-b3fc-2c963f66afa6").
// Instance IDs are validated against this before being used to build a
// request URL, so path-traversal or query/fragment injection via a crafted
// "id" argument (e.g. "../admin", "foo?x=1") is rejected up front.
var instanceIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	commandStatus = "status"
	fieldError    = "error"
)

const usage = `Dreadnought Private Server — Admin CLI

Usage: admin-cli <command> [args...]

Environment variables:
  AUTH_URL    Auth server base URL   (default: http://127.0.0.1:8081)
  MASTER_URL  Master server base URL (default: http://127.0.0.1:8084)
  MMOG_URL    Mmogbrain base URL     (default: http://127.0.0.1:8083)
  GM_URL      Game manager base URL  (default: http://127.0.0.1:8085)
  ADMIN_KEY   Shared admin secret    (default: changeme-admin-key)

Commands:
  status                    Show all services health
  servers                   List active game servers
  instances                 List running game instances
  stop-instance <id>        Stop a running game instance
  ban <username> <reason>   Ban a player
  unban <username>          Unban a player
  queue                     Show current matchmaking queue
  chat [channel]            Show recent chat (default channel: global)
  help                      Show this help
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(0)
	}

	authURL := getenv("AUTH_URL", "http://127.0.0.1:8081")
	masterURL := getenv("MASTER_URL", "http://127.0.0.1:8084")
	mmogURL := getenv("MMOG_URL", "http://127.0.0.1:8083")
	gmURL := getenv("GM_URL", "http://127.0.0.1:8085")
	adminKey := getenv("ADMIN_KEY", "changeme-admin-key")

	c := &client{
		http:      &http.Client{Timeout: 10 * time.Second},
		authURL:   authURL,
		masterURL: masterURL,
		mmogURL:   mmogURL,
		gmURL:     gmURL,
		adminKey:  adminKey,
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case commandStatus:
		c.status()
	case "servers":
		c.servers()
	case "instances":
		c.instances()
	case "stop-instance":
		if len(args) < 1 {
			die("usage: stop-instance <id>")
		}
		c.stopInstance(args[0])
	case "ban":
		if len(args) < 2 {
			die("usage: ban <username> <reason>")
		}
		c.ban(args[0], strings.Join(args[1:], " "))
	case "unban":
		if len(args) < 1 {
			die("usage: unban <username>")
		}
		c.unban(args[0])
	case "queue":
		c.queue()
	case "chat":
		channel := "global"
		if len(args) > 0 {
			channel = args[0]
		}
		c.chat(channel)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		fmt.Print(usage)
		os.Exit(1)
	}
}

type client struct {
	http      *http.Client
	authURL   string
	masterURL string
	mmogURL   string
	gmURL     string
	adminKey  string
}

func (c *client) joinPath(base string, elem ...string) string {
	result, err := url.JoinPath(base, elem...)
	if err != nil {
		return base
	}
	return result
}

func (c *client) get(url string) map[string]any {
	//nolint:gosec // Admin CLI intentionally targets operator-provided service endpoints.
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return map[string]interface{}{fieldError: err.Error()}
	}
	req.Header.Set("X-Admin-Key", c.adminKey)
	//nolint:gosec // Admin CLI intentionally targets operator-provided service endpoints.
	resp, err := c.http.Do(req)
	if err != nil {
		return map[string]interface{}{fieldError: err.Error()}
	}
	defer closeBody(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]interface{}{fieldError: "read response: " + err.Error()}
	}
	result := decodeJSONMap(body)
	if result == nil {
		result = map[string]interface{}{"raw": string(body), commandStatus: fmt.Sprintf("%d", resp.StatusCode)}
	}
	return result
}

func (c *client) post(url string, payload interface{}) map[string]interface{} {
	body, _ := json.Marshal(payload)
	//nolint:gosec // Admin CLI intentionally targets operator-provided service endpoints.
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return map[string]interface{}{fieldError: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Key", c.adminKey)
	//nolint:gosec // Admin CLI intentionally targets operator-provided service endpoints.
	resp, err := c.http.Do(req)
	if err != nil {
		return map[string]interface{}{fieldError: err.Error()}
	}
	defer closeBody(resp.Body)
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]interface{}{fieldError: "read response: " + err.Error()}
	}
	result := decodeJSONMap(rb)
	return result
}

func (c *client) del(url string) map[string]interface{} {
	//nolint:gosec // Admin CLI intentionally targets operator-provided service endpoints.
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return map[string]interface{}{fieldError: err.Error()}
	}
	req.Header.Set("X-Admin-Key", c.adminKey)
	//nolint:gosec // Admin CLI intentionally targets operator-provided service endpoints.
	resp, err := c.http.Do(req)
	if err != nil {
		return map[string]interface{}{fieldError: err.Error()}
	}
	defer closeBody(resp.Body)
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]interface{}{fieldError: "read response: " + err.Error()}
	}
	result := decodeJSONMap(rb)
	return result
}

func (c *client) status() {
	services := []struct{ name, url string }{
		{"auth-server", c.joinPath(c.authURL, "/health")},
		{"legacy-api", c.joinPath(strings.Replace(c.authURL, "8081", "8082", 1), "/health")},
		{"mmogbrain", c.joinPath(c.mmogURL, "/health")},
		{"master-server", c.joinPath(c.masterURL, "/health")},
		{"game-manager", c.joinPath(c.gmURL, "/health")},
	}
	fmt.Printf("%-20s  %-8s  %s\n", "SERVICE", "STATUS", "DETAILS")
	fmt.Println(strings.Repeat("-", 60))
	for _, svc := range services {
		r := c.get(svc.url)
		status := "DOWN"
		details := ""
		if s, ok := r[commandStatus].(string); ok {
			status = strings.ToUpper(s)
		}
		if err, ok := r[fieldError].(string); ok {
			status = "DOWN"
			details = err
		}
		// Collect interesting fields
		for k, v := range r {
			if k == commandStatus || k == "service" {
				continue
			}
			details += fmt.Sprintf("%s=%v ", k, v)
		}
		fmt.Printf("%-20s  %-8s  %s\n", svc.name, status, strings.TrimSpace(details))
	}
}

func (c *client) servers() {
	r := c.get(c.joinPath(c.masterURL, "/servers"))
	servers, _ := r["servers"].([]interface{})
	fmt.Printf("Active game servers: %v\n\n", r["count"])
	if len(servers) == 0 {
		fmt.Println("  (none)")
		return
	}
	fmt.Printf("%-36s  %-20s  %-16s  %-6s  %-20s  %s\n",
		"ID", "NAME", "IP:PORT", "MODE", "MAP", "PLAYERS")
	fmt.Println(strings.Repeat("-", 120))
	for _, s := range servers {
		srv, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		addr := fmt.Sprintf("%s:%v", srv["ip"], srv["port"])
		fmt.Printf("%-36s  %-20s  %-16s  %-6s  %-20s  %v/%v\n",
			srv["id"], srv["name"], addr, srv["game_mode"], srv["map"],
			srv["current_players"], srv["max_players"])
	}
}

func (c *client) instances() {
	r := c.get(c.joinPath(c.gmURL, "/instances"))
	instances, _ := r["instances"].([]interface{})
	fmt.Printf("Running game instances: %v  (ports used: %v)\n\n", r["count"], r["ports_used"])
	if len(instances) == 0 {
		fmt.Println("  (none)")
		return
	}
	fmt.Printf("%-36s  %-6s  %-15s  %-20s  %s\n", "INSTANCE ID", "PORT", "MODE", "MAP", "STARTED")
	fmt.Println(strings.Repeat("-", 110))
	for _, inst := range instances {
		i, ok := inst.(map[string]interface{})
		if !ok {
			continue
		}
		fmt.Printf("%-36s  %-6v  %-15s  %-20s  %s\n",
			i["id"], i["port"], i["game_mode"], i["map"], i["started_at"])
	}
}

func (c *client) stopInstance(id string) {
	if !instanceIDPattern.MatchString(id) {
		die(fmt.Sprintf("invalid instance id %q: expected a UUID (e.g. 3fa85f64-5717-4562-b3fc-2c963f66afa6)", id))
	}
	// url.PathEscape is redundant once the UUID format above is enforced, but
	// is kept as defense-in-depth against future changes to the ID format.
	r := c.del(c.joinPath(c.gmURL, "/instances", url.PathEscape(id)))
	printJSON(r)
}

func (c *client) ban(username, reason string) {
	r := c.post(c.joinPath(c.authURL, "/admin/ban"), map[string]string{
		"username": username,
		"reason":   reason,
	})
	printJSON(r)
}

func (c *client) unban(username string) {
	r := c.post(c.joinPath(c.authURL, "/admin/unban"), map[string]string{
		"username": username,
	})
	printJSON(r)
}

func (c *client) queue() {
	r := c.get(c.joinPath(c.mmogURL, "/admin/queue"))
	printJSON(r)
}

func (c *client) chat(channel string) {
	u, _ := url.Parse(c.mmogURL)
	u = u.JoinPath("/mmog/chat")
	q := url.Values{}
	q.Set("channel", channel)
	q.Set("limit", "20")
	u.RawQuery = q.Encode()
	r := c.get(u.String())
	msgs, _ := r["messages"].([]interface{})
	if len(msgs) == 0 {
		fmt.Printf("No messages in channel '%s'\n", channel)
		return
	}
	fmt.Printf("Recent messages in '%s':\n\n", channel)
	for _, m := range msgs {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		fmt.Printf("[%s] <%s> %s\n", msg["sent_at"], msg["sender_id"], msg["content"])
	}
}

func printJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func closeBody(body io.Closer) {
	if body == nil {
		return
	}
	_ = body.Close()
}

func decodeJSONMap(body []byte) map[string]interface{} {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}

	return result
}
