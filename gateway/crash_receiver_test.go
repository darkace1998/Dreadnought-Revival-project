package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestCrashReceiverStoresUploadAndReturnsSuccess(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()
	log.SetOutput(io.Discard)
	handler := newCrashReceiverHandler(log, dir, newRateLimiter(100, time.Minute))

	req := httptest.NewRequest(http.MethodPost, "/CrashReporter/UploadReportFile?CrashGUID=test-crash&Filename=Diagnostics.txt", strings.NewReader("crash details"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "CrashReporterResult") {
		t.Fatalf("response %q missing CrashReporterResult", rec.Body.String())
	}

	stored, err := os.ReadFile(filepath.Join(dir, "test-crash", "Diagnostics.txt"))
	if err != nil {
		t.Fatalf("read stored upload: %v", err)
	}
	if string(stored) != "crash details" {
		t.Fatalf("stored upload = %q", string(stored))
	}
	if _, err := os.Stat(filepath.Join(dir, "test-crash", "requests.ndjson")); err != nil {
		t.Fatalf("metadata not written: %v", err)
	}
}

func TestCrashReceiverPingDoesNotRequireBody(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	handler := newCrashReceiverHandler(log, t.TempDir(), newRateLimiter(100, time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/CrashReporter/Ping", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "Success") {
		t.Fatalf("response %q missing success marker", rec.Body.String())
	}
}
