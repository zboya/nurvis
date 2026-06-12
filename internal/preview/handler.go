package preview

import (
	"net/http"
	"strings"
)

// NewHandler returns an http.Handler that serves static files for registered
// preview tokens at /v1/preview/{token}/...
func NewHandler(reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expected path: /v1/preview/{token}/{file...}
		const prefix = "/v1/preview/"
		path := r.URL.Path
		if !strings.HasPrefix(path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(path, prefix)
		// rest = "token/" or "token/subdir/file.html"
		slashIdx := strings.IndexByte(rest, '/')
		if slashIdx < 0 {
			// redirect /v1/preview/token → /v1/preview/token/
			http.Redirect(w, r, path+"/", http.StatusMovedPermanently)
			return
		}
		token := rest[:slashIdx]
		filePath := rest[slashIdx+1:] // may be ""

		dir := reg.Resolve(token)
		if dir == "" {
			http.Error(w, "preview not found or expired", http.StatusNotFound)
			return
		}

		// Serve from the registered directory.
		fs := http.Dir(dir)
		// Strip the /v1/preview/{token}/ prefix so the file server sees the relative path.
		_ = filePath
		handler := http.StripPrefix(prefix+token+"/", http.FileServer(fs))
		handler.ServeHTTP(w, r)
	})
}
