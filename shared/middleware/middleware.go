package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

type contextKey string

const UserIDKey contextKey = "user_id"
const UsernameKey contextKey = "username"

func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(UserIDKey).(string); ok {
		return v
	}
	return ""
}

func UsernameFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(UsernameKey).(string); ok {
		return v
	}
	return ""
}

// Claims represents the JWT payload used across Dreadnought services.
type Claims struct {
	UserID   string `json:"sub"`
	Username string `json:"username"`
	Realm    string `json:"realm"`
	jwt.RegisteredClaims
}

// JWTMiddleware validates Bearer tokens using the supplied HMAC secret (HS256).
// On success it stores the user_id and username in the request context for
// downstream handlers.
func JWTMiddleware(secret []byte, log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(auth, "Bearer ")
			claims := &Claims{}
			token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return secret, nil
			})
			if err != nil || !token.Valid {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			for _, a := range claims.Audience {
				if a == "dreadnought" {
					goto audOk
				}
			}
			for _, a := range claims.Audience {
				if a == "launcher" {
					goto audOk
				}
			}
			http.Error(w, `{"error":"invalid audience"}`, http.StatusUnauthorized)
			return
	audOk:
		ctx := context.WithValue(r.Context(), UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, UsernameKey, claims.Username)
		r = r.WithContext(ctx)
		r.Header.Set("X-User-ID", claims.UserID)
		r.Header.Set("X-Username", claims.Username)
		next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter is a simple in-memory token-bucket rate limiter per IP.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
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

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		now := time.Now()
		cutoff := now.Add(-rl.window)

		rl.mu.Lock()
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
		exceeded := len(filtered) > rl.limit
		rl.mu.Unlock()

		if exceeded {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Logger logs each request as a JSON line.
func Logger(log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			log.WithFields(logrus.Fields{
				"method":  r.Method,
				"path":    r.URL.Path,
				"status":  rw.status,
				"latency": time.Since(start).Milliseconds(),
				"ip":      r.RemoteAddr,
			}).Info("request")
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.status == http.StatusOK {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}
