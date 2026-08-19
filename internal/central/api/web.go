package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/*
var centralWebAssets embed.FS

func centralWebHandler() http.Handler {
	assets, err := fs.Sub(centralWebAssets, "assets")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		if request.URL.Path == "/" {
			request.URL.Path = "/central.html"
			files.ServeHTTP(writer, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/assets/") {
			request.URL.Path = strings.TrimPrefix(request.URL.Path, "/assets")
			files.ServeHTTP(writer, request)
			return
		}
		http.NotFound(writer, request)
	})
}
