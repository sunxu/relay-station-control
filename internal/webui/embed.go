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

	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requested == "." || requested == "" {
			requested = "index.html"
		}
		if _, err := fs.Stat(dist, requested); err == nil {
			files.ServeHTTP(w, r)
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
