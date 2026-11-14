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
//
// Every file is sent with "Cache-Control: no-cache": the browser keeps its copy but checks it on
// each load (a cheap 304 when unchanged). The modules aren't fingerprinted, so without this a
// browser may run a stale script next to a fresh stylesheet after a change.
func NewStaticHandler(dir string) http.Handler {
	fsys := os.DirFS(dir)
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(fsys, name); err != nil {
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}
