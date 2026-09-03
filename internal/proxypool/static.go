package proxypool

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed webui
var webUI embed.FS

func webUIHandler() http.Handler {
	sub, err := fs.Sub(webUI, "webui")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".webmanifest") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if strings.HasSuffix(path, "sw.js") {
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Cache-Control", "no-store")
		}
		files.ServeHTTP(w, r)
	})
}
