package server

import (
	"net/http"

	"github.com/MojoHand70/fadodb/internal/server/web"
)

// serveAsset answers a file the pages load (package web), and false for
// any other address. It holds no secret and is the same for every
// database, so it is answered whatever the host, before any page.
func serveAsset(w http.ResponseWriter, r *http.Request) bool {
	f := web.Served(r.URL.Path)
	if f == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", f.Kind)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return true
	}
	_, _ = w.Write(f.Body)
	return true
}
