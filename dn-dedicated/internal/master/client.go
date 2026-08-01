// Package master talks to the Revival project's master-server.
//
// The wire contract is taken from master-server/handlers/handlers.go and matches
// what game-manager's spawner sends, so servers registered by this tool appear
// in the same server browser alongside ones the Revival stack launched.
package master

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HeartbeatInterval is how often a registered server must check in.
//
// master-server's cleanup goroutine marks a server offline once its
// last_heartbeat is more than 60 seconds old, and runs every 30 seconds
// (handlers.go StartCleanup). 20 seconds gives two chances to be heard before
// the 60-second deadline, so one dropped request cannot flip a healthy server
// to offline.
//
// This matters because game-manager's spawner registers instances and then
// never heartbeats at all: every match it starts is marked offline about a
// minute in, while still running. Registering without heartbeating is not a
// working registration.
const HeartbeatInterval = 20 * time.Second

// Client is a master-server API client.
type Client struct {
	BaseURL     string // e.g. "http://127.0.0.1:8084"
	InternalKey string // sent as X-Internal-Key
	HTTP        *http.Client
}

// New returns a Client with timeouts appropriate for a loopback service.
func New(baseURL, internalKey string) *Client {
	return &Client{
		BaseURL:     baseURL,
		InternalKey: internalKey,
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			},
		},
	}
}

// RegisterRequest is the POST /servers/register body.
type RegisterRequest struct {
	Name       string `json:"name"`
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	GameMode   string `json:"game_mode"`
	Map        string `json:"map"`
	MaxPlayers int    `json:"max_players"`
}

// Register announces a server and returns the id master-server assigned it.
func (c *Client) Register(ctx context.Context, req RegisterRequest) (string, error) {
	var out map[string]string
	if err := c.do(ctx, http.MethodPost, "/servers/register", req, &out); err != nil {
		return "", err
	}
	id := out["id"]
	if id == "" {
		return "", fmt.Errorf("master-server returned no server id")
	}
	return id, nil
}

// Heartbeat refreshes a server's last_heartbeat and reports its player count.
func (c *Client) Heartbeat(ctx context.Context, serverID string, currentPlayers int) error {
	body := map[string]int{"current_players": currentPlayers}
	return c.do(ctx, http.MethodPost, "/servers/"+serverID+"/heartbeat", body, nil)
}

// Deregister removes a server from the registry.
func (c *Client) Deregister(ctx context.Context, serverID string) error {
	return c.do(ctx, http.MethodDelete, "/servers/"+serverID, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out interface{}) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("marshal %s %s: %w", method, path, err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Internal-Key", c.InternalKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%s %s: 403 forbidden -- INTERNAL_API_KEY must match master-server's", method, path)
		}
		return fmt.Errorf("%s %s: master-server returned HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return nil
}
