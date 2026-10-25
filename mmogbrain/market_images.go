package main

import (
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Market pictures: the hero icons the client ships as cooked textures,
// decoded to PNG by scripts/gen-hero-market-data.py. The original store served
// its pictures from a CDN by URL, and the client still only takes URLs
// (full_image_url and friends), so the gateway serves them itself.

const marketImagePath = "/market-images/"

// marketImageName admits only the generator's file names, so a request can
// never leave the directory.
var marketImageName = regexp.MustCompile(`^[A-Za-z0-9_-]+\.png$`)

func handleMarketImage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, marketImagePath)
	if r.Method != http.MethodGet && r.Method != http.MethodHead || !marketImageName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(dreadconfig.MarketImagesDir(), name))
}

// gatewayAbsoluteImageURLs makes the catalog's picture paths absolute on the
// scheme and port the client fetched the catalog from, under the server's
// public NAME (PUBLIC_HOST) when it has one, else the host the client used.
//
// CHANGED 2026-10-21: the host was always the request's, i.e. the outside IP
// the launcher passes in -GatewayAddress. After that IP changed, the Market's
// pictures stopped loading while login and the catalogs on the same address
// kept working: a picture request was still "Processing" when the game closed
// a minute later (operator log, 21:52 run), and none were outstanding on the
// old IP (19 Oct run). GUESS: the picture downloader (DreadAsyncTaskDownloadImage
// via the image loader) checks the certificate's name where the web-services
// requests do not; the certificates name the old IP but carry PUBLIC_HOST as a
// DNS name, which also follows every IP change.
func gatewayAbsoluteImageURLs(payload map[string]any, r *http.Request) map[string]any {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	base := scheme + "://" + marketImageHost(r)
	for _, list := range payload {
		entities, ok := list.([]any)
		if !ok {
			continue
		}
		for _, e := range entities {
			entity, ok := e.(map[string]any)
			if !ok {
				continue
			}
			for key, v := range entity {
				if url, ok := v.(string); ok && strings.HasPrefix(url, marketImagePath) {
					entity[key] = base + url
				}
			}
		}
	}
	return payload
}

// marketImageHost is host[:port] for picture URLs: PUBLIC_HOST with the
// request's port, or the request's host when PUBLIC_HOST is unset or the
// client came in over loopback (local tools and tests).
func marketImageHost(r *http.Request) string {
	name := strings.TrimSpace(getenv("PUBLIC_HOST", ""))
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, ""
	}
	if ip := net.ParseIP(host); name == "" || ip != nil && ip.IsLoopback() || host == "localhost" {
		return r.Host
	}
	if port == "" {
		return name
	}
	return net.JoinHostPort(name, port)
}
