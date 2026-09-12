package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var assets embed.FS

func Handler() http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return handler(dist)
}

func handler(dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/static" || strings.HasPrefix(r.URL.Path, "/static/") {
			requested := path.Clean(strings.TrimPrefix(r.URL.Path, "/static/"))
			info, err := fs.Stat(dist, requested)
			if requested == "." || !fs.ValidPath(requested) || err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
			request := r.Clone(r.Context())
			urlCopy := *r.URL
			urlCopy.Path = "/" + requested
			request.URL = &urlCopy
			files.ServeHTTP(w, request)
			return
		}

		if _, err := fs.Stat(dist, "index.html"); err == nil {
			r.URL.Path = "/"
			files.ServeHTTP(w, r)
			return
		}

		http.NotFound(w, r)
	})
}
