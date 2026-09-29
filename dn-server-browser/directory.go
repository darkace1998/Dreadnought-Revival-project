// Portable master-master directory client (no Windows calls, so this file
// and its tests run on any OS). The Windows UI and game launch live in the
// *_windows.go files.
package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Cluster is one entry of the public directory (master-master GET
// /clusters). No secrets cross here: names, addresses, versions, counts and
// the cluster's CA certificate.
type Cluster struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WebURL        string `json:"web_url"`
	BattleIP      string `json:"battle_ip"`
	Version       string `json:"version"`
	MOTD          string `json:"motd"`
	CACert        string `json:"ca_cert"`
	CAFingerprint string `json:"ca_fingerprint"`
	Players       int    `json:"players"`
	Servers       int    `json:"servers"`
}

// DirectoryClient reads the public cluster list.
type DirectoryClient struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *DirectoryClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// List returns the currently listed clusters, newest heartbeat first (the
// server already orders them that way).
func (c *DirectoryClient) List() ([]Cluster, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("no directory configured")
	}
	resp, err := c.http().Get(base + "/clusters")
	if err != nil {
		return nil, fmt.Errorf("contact the directory: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("directory answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read directory response: %w", err)
	}
	var doc struct {
		Clusters []Cluster `json:"clusters"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse directory response: %w", err)
	}
	return doc.Clusters, nil
}

// normalizeFingerprint folds every common fingerprint spelling (colon
// separated, upper/lower case, whitespace) into plain lowercase hex, so the
// directory's value, a pasted value and a computed value always compare.
func normalizeFingerprint(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), ":", ""), " ", ""))
}

// RegisterCheck asks the directory whether this callsign or address is
// already taken on ANY cluster (the mirror knows every pushed account).
// Callers fail OPEN on transport errors: the cluster's own registration
// stays authoritative with its 409, this is only the early friendly answer.
func (c *DirectoryClient) RegisterCheck(username, email string) (bool, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return false, fmt.Errorf("no directory configured")
	}
	username, email = strings.TrimSpace(username), strings.TrimSpace(email)
	if username == "" && email == "" {
		return false, fmt.Errorf("username or email required")
	}
	target := base + "/register-check?username=" + url.QueryEscape(username) +
		"&email=" + url.QueryEscape(email)
	resp, err := c.http().Get(target)
	if err != nil {
		return false, fmt.Errorf("contact the directory: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("directory answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return false, fmt.Errorf("read directory response: %w", err)
	}
	var doc struct {
		Taken bool `json:"taken"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, fmt.Errorf("parse directory response: %w", err)
	}
	return doc.Taken, nil
}

// normalizeUserID folds both account id spellings (dashed auth UUID,
// undashed game pid) into the undashed lowercase form the sync mesh keys
// everything by.
func normalizeUserID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

// Presence asks the directory whether this account is right now in a match
// on any OTHER cluster. except excludes the cluster the player is joining
// (directory id, or cluster name for hand-added servers that have no id).
// A transport failure is an error — callers fail OPEN (let the player in)
// because a dead directory must not strand anyone; the guard is best-effort,
// not a lock.
func (c *DirectoryClient) Presence(userID, except string) (inMatch bool, cluster string, err error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return false, "", fmt.Errorf("no directory configured")
	}
	uid := normalizeUserID(userID)
	if uid == "" {
		return false, "", fmt.Errorf("no account id")
	}
	target := base + "/presence/" + uid
	if strings.TrimSpace(except) != "" {
		q := url.QueryEscape(strings.TrimSpace(except))
		target += "?except=" + q
	}
	resp, err := c.http().Get(target)
	if err != nil {
		return false, "", fmt.Errorf("contact the directory: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("directory answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return false, "", fmt.Errorf("read directory response: %w", err)
	}
	var doc struct {
		InMatch bool   `json:"in_match"`
		Cluster string `json:"cluster"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, "", fmt.Errorf("parse directory response: %w", err)
	}
	return doc.InMatch, doc.Cluster, nil
}
func FingerprintOfCACert(pemData []byte) (string, error) {
	for rest := pemData; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return "", fmt.Errorf("not a certificate: %w", err)
		}
		sum := sha256.Sum256(block.Bytes)
		return hex.EncodeToString(sum[:]), nil
	}
	return "", fmt.Errorf("no CERTIFICATE block found")
}

// FingerprintDisplay renders plain hex as colon-separated octets, the form
// operators publish and players compare.
func FingerprintDisplay(hexFP string) string {
	hexFP = normalizeFingerprint(hexFP)
	parts := make([]string, 0, len(hexFP)/2)
	for i := 0; i+1 < len(hexFP); i += 2 {
		parts = append(parts, strings.ToUpper(hexFP[i:i+2]))
	}
	return strings.Join(parts, ":")
}
