package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const fieldStatus = "status"

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// route maps a Host header to an upstream URL.
type route struct {
	host     string
	upstream *url.URL
}

// rateLimiter is a simple per-IP in-memory rate limiter.
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-rl.window)
		for ip, times := range rl.requests {
			filtered := times[:0]
			for _, t := range times {
				if t.After(cutoff) {
					filtered = append(filtered, t)
				}
			}
			if len(filtered) == 0 {
				delete(rl.requests, ip)
			} else {
				rl.requests[ip] = filtered
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.window)
	times := rl.requests[ip]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) == 0 && len(times) > 0 {
		delete(rl.requests, ip)
	}
	filtered = append(filtered, now)
	rl.requests[ip] = filtered
	return len(filtered) <= rl.limit
}

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	httpAddr := getenv("HTTP_ADDR", ":80")
	httpsAddr := getenv("HTTPS_ADDR", ":443")
	certFile := getenv("TLS_CERT", "certs/server.crt")
	keyFile := getenv("TLS_KEY", "certs/server.key")

	// Route table — maps Host headers to internal services
	routes := []route{
		{host: "profile-api.prod.greybox.sixfoot.live", upstream: mustParseURL("http://127.0.0.1:8081")},
		{host: "legacyapi.prod.greybox.sixfoot.live", upstream: mustParseURL("http://127.0.0.1:8082")},
		{host: "mmog.greybox.sixfoot.live", upstream: mustParseURL("http://127.0.0.1:8083")},
		{host: "masterserver.local", upstream: mustParseURL("http://127.0.0.1:8084")},
		{host: "gamemanager.local", upstream: mustParseURL("http://127.0.0.1:8085")},
	}

	rl := newRateLimiter(100, time.Minute)

	// Build route map for O(1) lookup
	routeMap := make(map[string]*httputil.ReverseProxy)
	redirectHosts := make(map[string]string)
	for _, r := range routes {
		proxy := httputil.NewSingleHostReverseProxy(r.upstream)
		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			log.WithError(err).WithField("host", req.Host).Warn("upstream error")
			http.Error(w, `{"error":"upstream unavailable"}`, http.StatusBadGateway)
		}
		upstream := r.upstream
		proxy.Director = func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
		}
		routeMap[r.host] = proxy
		redirectHosts[r.host] = r.host
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Rate limit auth endpoint
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if !rl.allow(ip) {
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
		}

		// Internal gateway-only paths
		if r.URL.Path == "/health" {
			writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok", "service": "gateway"})
			return
		}
		if r.URL.Path == "/metrics" {
			promhttp.Handler().ServeHTTP(w, r)
			return
		}

		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		proxy, ok := routeMap[host]
		if !ok {
			http.Error(w, `{"error":"unknown host"}`, http.StatusNotFound)
			log.WithField("host", host).Warn("no route for host")
			return
		}

		rw := &responseWriter{ResponseWriter: w, status: 200}
		proxy.ServeHTTP(rw, r)
		log.WithFields(logrus.Fields{
			"host":    host,
			"method":  r.Method,
			"path":    r.URL.Path,
			"status":  rw.status,
			"latency": time.Since(start).Milliseconds(),
		}).Info("proxy")
	})

	// HTTP redirect to HTTPS
	httpSrv := &http.Server{
		Addr:              httpAddr,
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, ok := redirectHost(r.Host, redirectHosts)
			if !ok {
				http.Error(w, `{"error":"unknown host"}`, http.StatusBadRequest)
				return
			}
			target := "https://" + host + r.URL.RequestURI()
			//nolint:gosec // Redirect host is selected from the fixed route table to preserve HTTP->HTTPS upgrade behavior.
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}),
	}

	// HTTPS server — explicitly include RSA key exchange suites (removed from Go defaults
	// in 1.22+) because the Dreadnought launcher only offers legacy RSA cipher suites.
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Log TLS handshake errors so we can diagnose client cert/cipher issues
		VerifyConnection: func(cs tls.ConnectionState) error {
			log.WithFields(logrus.Fields{
				"server_name": cs.ServerName,
				"version":     cs.Version,
				"cipher":      cs.CipherSuite,
			}).Debug("tls handshake ok")
			return nil
		},
		CipherSuites: []uint16{
			// Modern ECDHE suites (preferred, for forward secrecy)
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			// Legacy RSA key exchange — required by old game launcher (TLS 1.2 only)
			//nolint:gosec // Legacy launcher compatibility requires RSA key-exchange fallback suites.
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			//nolint:gosec // Legacy launcher compatibility requires RSA key-exchange fallback suites.
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			//nolint:gosec // Legacy launcher compatibility requires RSA+CBC fallback suites.
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			//nolint:gosec // Legacy launcher compatibility requires RSA+CBC fallback suites.
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			//nolint:gosec // Legacy launcher compatibility requires RSA+CBC fallback suites.
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
	}
	httpsSrv := &http.Server{
		Addr:              httpsAddr,
		Handler:           handler,
		TLSConfig:         tlsCfg,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Log TLS handshake errors (untrusted cert, cipher mismatch, etc.)
		ErrorLog: stdlog.New(log.WriterLevel(logrus.WarnLevel), "[tls] ", 0),
	}

	go func() {
		log.WithField("addr", httpAddr).Info("gateway HTTP starting")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Warn("http server")
		}
	}()

	go func() {
		log.WithField("addr", httpsAddr).Info("gateway HTTPS starting")
		if err := httpsSrv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Warn("https server (cert may not exist yet; run scripts/gen-certs.sh)")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down gateway")
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown http server")
	}
	if err := httpsSrv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown https server")
	}
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("invalid upstream URL %q: %v", raw, err))
	}
	return u
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func redirectHost(rawHost string, redirectHosts map[string]string) (string, bool) {
	host := rawHost
	if h, _, err := net.SplitHostPort(rawHost); err == nil {
		host = h
	}
	redirectHost, ok := redirectHosts[host]
	if !ok {
		return "", false
	}

	return redirectHost, true
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
