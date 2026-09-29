// Portable TOFU trust store (no Windows calls). Remembers which CA
// fingerprint was accepted for which cluster, so the fingerprint dialog
// appears exactly once per cluster: on first join, or when the cluster's
// certificate changes (which is then shown as a change, not silently
// accepted).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TrustStore persists accepted CA fingerprints as {key: fingerprint}, both
// plain lowercase hex.
type TrustStore struct {
	path string
	data map[string]string
}

// clusterTrustKey identifies one trust decision. Listed clusters are keyed by
// their stable directory id; manually added servers (no id) by URL.
func clusterTrustKey(clusterID, webURL string) string {
	if id := strings.TrimSpace(clusterID); id != "" {
		return "id:" + id
	}
	return "manual:" + strings.ToLower(strings.TrimSpace(webURL))
}

// OpenTrustStore loads the store file, tolerating a missing or corrupt file
// (a corrupt store forgets decisions; it never blocks joining).
func OpenTrustStore(path string) (*TrustStore, error) {
	s := &TrustStore{path: path, data: map[string]string{}}
	//nolint:gosec // Path is the browser's own trust file under its private directory.
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read trust store: %w", err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		s.data = map[string]string{}
		return s, nil
	}
	if s.data == nil {
		s.data = map[string]string{}
	}
	return s, nil
}

// Trusted reports whether fingerprint was accepted for key before. An empty
// stored value never matches.
func (s *TrustStore) Trusted(key, fingerprint string) bool {
	want := normalizeFingerprint(fingerprint)
	if want == "" {
		return false
	}
	got, ok := s.data[key]
	return ok && normalizeFingerprint(got) == want && want != ""
}

// Remember stores an accepted fingerprint (creating the directory).
func (s *TrustStore) Remember(key, fingerprint string) error {
	want := normalizeFingerprint(fingerprint)
	if want == "" {
		return fmt.Errorf("refusing to remember an empty fingerprint")
	}
	s.data[key] = want
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	//nolint:gosec // 0600 under the user's own private directory.
	return os.WriteFile(s.path, raw, 0o600)
}
