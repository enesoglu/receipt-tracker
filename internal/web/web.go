// Package web serves the embedded single-page application.
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// assets is populated by the compiler and is never modified at runtime.
//
//go:embed dist
var assets embed.FS

// New serves built assets and falls back to the app for browser routes.
func New(ctx context.Context) (http.Handler, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("load web assets: %w", err)
	}
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("open web assets: %w", err)
	}
	return handler(root), nil
}

// handler serves the files of root. Browser routes that match no file fall
// back to the app shell; files under vendor/ carry their version in the
// name, so they are sent with an immutable cache header.
func handler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			if path.Ext(name) != "" || !strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.NotFound(w, r)
				return
			}
			clone := r.Clone(r.Context())
			clone.URL.Path = "/"
			files.ServeHTTP(w, clone)
			return
		}
		if strings.HasPrefix(name, "vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
