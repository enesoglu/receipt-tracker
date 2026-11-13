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
		files.ServeHTTP(w, r)
	}), nil
}
