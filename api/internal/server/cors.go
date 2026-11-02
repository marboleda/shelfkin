package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CORS settings. The API authenticates with bearer tokens in the
// Authorization header, not cookies, so Access-Control-Allow-Credentials is
// not needed; the allowed origin is always echoed exactly, never "*".
var (
	CORSAllowedMethods = []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}
	CORSAllowedHeaders = []string{"Authorization", "Content-Type"}
)

// CORSMaxAge is how long a browser may cache a preflight response.
const CORSMaxAge = 10 * time.Minute

// cors adds CORS headers for requests whose Origin is in allowedOrigins and
// answers their preflight requests with 204. It sets the headers before
// calling next, so every response, including 4xx and 5xx errors, carries
// them. Requests from other origins get no Access-Control-Allow-Origin
// header and are otherwise served as usual (a preflight from one falls
// through to the router's 405); the browser then blocks the page from
// reading the response.
func cors(allowedOrigins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	methods := strings.Join(CORSAllowedMethods, ", ")
	headers := strings.Join(CORSAllowedHeaders, ", ")
	maxAge := strconv.Itoa(int(CORSMaxAge.Seconds()))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// The response depends on Origin, so caches must key on it.
		h.Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
		}
		if origin == "" || !allowed[origin] {
			next.ServeHTTP(w, r)
			return
		}
		h.Set("Access-Control-Allow-Origin", origin)
		if preflight {
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Allow-Headers", headers)
			h.Set("Access-Control-Max-Age", maxAge)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
