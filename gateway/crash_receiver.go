package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const maxCrashReportUploadBytes int64 = 128 << 20
const maxCrashReportDirBytes int64 = 10 << 30 // 10GB aggregate quota for reportDir

func newCrashReceiverHandler(log *logrus.Logger, reportDir string, rl *rateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		pathLower := strings.ToLower(r.URL.Path)
		reportID := crashReportID(r)

		// This listener is entirely separate from the main HTTPS handler's
		// rate limiting (which only covers /auth/), and accepts up to
		// maxCrashReportUploadBytes (128MB) per request with no ceiling on
		// how many requests one caller can send — an anonymous client could
		// otherwise fill disk by repeatedly uploading near-max-size reports.
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !rl.allow(ip) {
			writeCrashReceiverResult(w, http.StatusTooManyRequests, reportID)
			logCrashReceiverRequest(log, r, reportID, 0, time.Since(start), fmt.Errorf("rate limit exceeded"))
			return
		}

		if strings.Contains(pathLower, "ping") {
			writeCrashReceiverResult(w, http.StatusOK, reportID)
			logCrashReceiverRequest(log, r, reportID, 0, time.Since(start), nil)
			return
		}

		if used, err := dirSize(reportDir); err == nil && used >= maxCrashReportDirBytes {
			writeCrashReceiverResult(w, http.StatusInsufficientStorage, reportID)
			logCrashReceiverRequest(log, r, reportID, 0, time.Since(start), fmt.Errorf("crash report storage quota exceeded (%d bytes used)", used))
			return
		}

		bytesWritten, err := storeCrashReceiverRequest(w, r, reportDir, reportID)
		if err != nil {
			writeCrashReceiverResult(w, http.StatusInternalServerError, reportID)
			logCrashReceiverRequest(log, r, reportID, bytesWritten, time.Since(start), err)
			return
		}

		writeCrashReceiverResult(w, http.StatusOK, reportID)
		logCrashReceiverRequest(log, r, reportID, bytesWritten, time.Since(start), nil)
	})
}

func storeCrashReceiverRequest(w http.ResponseWriter, r *http.Request, reportDir string, reportID string) (int64, error) {
	reportPath := filepath.Join(reportDir, sanitizeCrashPathPart(reportID, "unknown-report"))
	if err := os.MkdirAll(reportPath, 0o755); err != nil {
		return 0, err
	}
	if err := writeCrashRequestMetadata(reportPath, r); err != nil {
		return 0, err
	}

	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return 0, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCrashReportUploadBytes)

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return storeMultipartCrashRequest(r, reportPath)
	}

	fileName := firstQueryValue(r, "Filename", "FileName", "filename", "file")
	if fileName == "" {
		fileName = crashRequestFallbackFilename(r)
	}
	return storeCrashRequestBody(r.Body, reportPath, fileName)
}

func storeMultipartCrashRequest(r *http.Request, reportPath string) (int64, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return 0, err
	}

	var total int64
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}

		fileName := part.FileName()
		if fileName == "" {
			fileName = part.FormName() + ".txt"
		}
		written, err := storeCrashRequestBody(part, reportPath, fileName)
		total += written
		if err != nil {
			return total, err
		}
	}
}

func storeCrashRequestBody(body io.Reader, reportPath string, fileName string) (int64, error) {
	filePath := uniqueCrashFilePath(reportPath, sanitizeCrashPathPart(fileName, "upload.bin"))
	out, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = out.Close()
	}()
	return io.Copy(out, body)
}

func writeCrashRequestMetadata(reportPath string, r *http.Request) error {
	metadata := map[string]any{
		"time":        time.Now().UTC().Format(time.RFC3339Nano),
		"method":      r.Method,
		"path":        r.URL.Path,
		"raw_query":   r.URL.RawQuery,
		"remote_addr": r.RemoteAddr,
		"host":        r.Host,
		"headers":     r.Header,
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(reportPath, "requests.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	_, err = file.Write(append(data, '\n'))
	return err
}

func writeCrashReceiverResult(w http.ResponseWriter, status int, reportID string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?><CrashReporterResult><Success>true</Success><bSuccess>true</bSuccess><ReportID>%s</ReportID></CrashReporterResult>", reportID)
}

func logCrashReceiverRequest(log *logrus.Logger, r *http.Request, reportID string, bytesWritten int64, latency time.Duration, err error) {
	entry := log.WithFields(logrus.Fields{
		"host":           r.Host,
		"method":         r.Method,
		"path":           r.URL.Path,
		"report_id":      reportID,
		"remote":         r.RemoteAddr,
		"stored_bytes":   bytesWritten,
		"latency_ms":     latency.Milliseconds(),
		"content_type":   r.Header.Get("Content-Type"),
		"content_length": r.ContentLength,
	})
	if err != nil {
		entry.WithError(err).Warn("crash report receiver failed")
		return
	}
	entry.Info("crash report receiver request")
}

func crashReportID(r *http.Request) string {
	if value := firstQueryValue(r,
		"DirectoryName", "CrashGUID", "CrashGuid", "CrashGUID", "CrashID", "ReportID", "SessionID", "UserID"); value != "" {
		return sanitizeCrashPathPart(value, "unknown-report")
	}
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		remote = host
	}
	return sanitizeCrashPathPart(time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+remote+"-"+randomCrashSuffix(), "unknown-report")
}

func firstQueryValue(r *http.Request, keys ...string) string {
	query := r.URL.Query()
	for _, key := range keys {
		if value := strings.TrimSpace(query.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func crashRequestFallbackFilename(r *http.Request) string {
	base := sanitizeCrashPathPart(filepath.Base(r.URL.Path), "upload")
	if base == "." || base == "/" || base == "" {
		base = "upload"
	}
	return base + ".bin"
}

func sanitizeCrashPathPart(value string, fallback string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = filepath.Base(value)
	if value == "." || value == "/" || value == "" {
		value = fallback
	}

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.', r == '@':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	cleaned := strings.Trim(b.String(), ". _")
	if cleaned == "" {
		cleaned = fallback
	}
	if len(cleaned) > 120 {
		cleaned = cleaned[:120]
	}
	return cleaned
}

func uniqueCrashFilePath(dir string, fileName string) string {
	path := filepath.Join(dir, fileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func randomCrashSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}
