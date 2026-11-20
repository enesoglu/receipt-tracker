package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandler_VendorAssetsCacheImmutably(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCache  string
	}{
		{
			name:       "vendor asset is immutable",
			path:       "/vendor/jscanify-1.4.3.js",
			wantStatus: http.StatusOK,
			wantCache:  "public, max-age=31536000, immutable",
		},
		{
			name:       "hashed asset gets no cache header",
			path:       "/assets/app-C23QeHZS.js",
			wantStatus: http.StatusOK,
			wantCache:  "",
		},
		{
			name:       "missing vendor file is not cached",
			path:       "/vendor/jscanify-0.1.0.js",
			wantStatus: http.StatusNotFound,
			wantCache:  "",
		},
	}

	files := fstest.MapFS{
		"vendor/jscanify-1.4.3.js": &fstest.MapFile{Data: []byte("engine source")},
		"assets/app-C23QeHZS.js":   &fstest.MapFile{Data: []byte("app bundle")},
		"index.html":               &fstest.MapFile{Data: []byte("<!doctype html>")},
	}
	underTest := handler(files)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			underTest.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantCache)
			}
		})
	}
}
