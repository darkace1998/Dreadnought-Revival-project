package main

import (
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
// host and scheme the client fetched the catalog from -- the one origin it is
// known to reach and to trust.
func gatewayAbsoluteImageURLs(payload map[string]any, r *http.Request) map[string]any {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	base := scheme + "://" + r.Host
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
