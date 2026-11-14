package web

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// NewStaticHandler serves the frontend from dir.
// A path that doesn't match a file gets index.html, so client-side routes (/history, /admin, /join) load the app.
func NewStaticHandler(dir string) http.Handler {
	fsys := os.DirFS(dir)
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(fsys, name); err != nil {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		if name == "." {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
