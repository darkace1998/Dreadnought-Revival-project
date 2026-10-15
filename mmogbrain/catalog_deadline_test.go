package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The catalog response extends its own write deadline past the server's
// WriteTimeout (catalogWriteTimeout): a slow download used to be cut at 15 s.
func TestCatalogWriteDeadlineOutlivesTheServerTimeout(t *testing.T) {
	t.Setenv("DN_CATALOG_WRITE_TIMEOUT", "5")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(catalogWriteTimeout())); err != nil {
			t.Errorf("deadline: %v", err)
		}
		time.Sleep(400 * time.Millisecond) // longer than the server's WriteTimeout
		_, _ = w.Write([]byte("catalog"))
	}))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.StartTLS()
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("response cut: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if b, _ := io.ReadAll(resp.Body); string(b) != "catalog" {
		t.Fatalf("body %q", b)
	}
}
