// Package drivetest provides a synthetic Google OAuth and Drive protocol server.
package drivetest

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	gdrive "google.golang.org/api/drive/v3"
)

type entry struct {
	file       gdrive.File
	path       string
	size       int64
	ready      bool
	generation int
}

// Fake persists synthetic streams on disk to keep upload allocation measurements meaningful.
type Fake struct {
	Server         *httptest.Server
	mu             sync.Mutex
	files          map[string]*entry
	directory      string
	buffer         [32 << 10]byte
	generation     int
	hideEarlier    bool
	tokenError     string
	tokenStatus    int
	missingRefresh bool
	unreadable     bool
	revocations    int
}

// New starts an isolated server. Unexpected endpoints, including revoke, fail the test.
func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{files: make(map[string]*entry), directory: t.TempDir()}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.Contains(r.URL.Path, "revoke") {
			f.revocations++
			t.Error("application called revoke")
		}
		if !f.serve(w, r) {
			t.Errorf("unexpected Google request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Server.Close)
	return f
}

// SetTokenError changes the OAuth refresh/exchange response without exposing provider secrets.
func (f *Fake) SetTokenError(code string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenError, f.tokenStatus = code, status
}

// HideEarlier simulates loss of visibility after a fresh grant.
func (f *Fake) HideEarlier() { f.mu.Lock(); defer f.mu.Unlock(); f.hideEarlier = true }

// OmitRefresh simulates a provider that does not issue offline credentials.
func (f *Fake) OmitRefresh() { f.mu.Lock(); defer f.mu.Unlock(); f.missingRefresh = true }

// Unreadable simulates a listed object that cannot be downloaded.
func (f *Fake) Unreadable() { f.mu.Lock(); defer f.mu.Unlock(); f.unreadable = true }

// FolderCount counts all folders, even those hidden from a later grant.
func (f *Fake) FolderCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, value := range f.files {
		if value.file.Name == name && value.file.MimeType == "application/vnd.google-apps.folder" {
			count++
		}
	}
	return count
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/token":
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(400)
			return true
		}
		if f.tokenError != "" {
			w.WriteHeader(f.tokenStatus)
			writeJSON(w, map[string]string{"error": f.tokenError, "error_description": "synthetic-provider-secret"})
			return true
		}
		if r.Form.Get("grant_type") == "authorization_code" {
			f.generation++
		}
		refresh := "synthetic-refresh-token"
		if f.missingRefresh {
			refresh = ""
		}
		writeJSON(w, map[string]interface{}{"access_token": "synthetic-access-token", "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files":
		result := []*gdrive.File{}
		query := r.URL.Query().Get("q")
		for _, value := range f.files {
			if !value.ready || (f.hideEarlier && value.generation < f.generation) {
				continue
			}
			if strings.Contains(query, "mimeType = '") && value.file.MimeType != "application/vnd.google-apps.folder" {
				continue
			}
			if strings.Contains(query, "name = '") && !strings.Contains(query, "name = '"+value.file.Name+"'") {
				continue
			}
			if len(value.file.Parents) > 0 && !strings.Contains(query, "'"+value.file.Parents[0]+"' in parents") {
				continue
			}
			result = append(result, &value.file)
		}
		writeJSON(w, &gdrive.FileList{Files: result})
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/drive/v3/files":
		var metadata gdrive.File
		if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
			w.WriteHeader(400)
			return true
		}
		value := f.create(metadata)
		value.ready = true
		writeJSON(w, &value.file)
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/upload/drive/v3/files":
		if r.URL.Query().Get("uploadType") == "resumable" {
			var metadata gdrive.File
			if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
				w.WriteHeader(400)
				return true
			}
			value := f.create(metadata)
			w.Header().Set("Location", f.Server.URL+"/upload-session/"+value.file.Id)
			return true
		}
		_, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			w.WriteHeader(400)
			return true
		}
		parts := multipart.NewReader(r.Body, parameters["boundary"])
		metadataPart, err := parts.NextPart()
		if err != nil {
			w.WriteHeader(400)
			return true
		}
		var metadata gdrive.File
		if err := json.NewDecoder(metadataPart).Decode(&metadata); err != nil {
			w.WriteHeader(400)
			return true
		}
		media, err := parts.NextPart()
		if err != nil {
			w.WriteHeader(400)
			return true
		}
		value := f.create(metadata)
		if !f.append(w, media, value) {
			return true
		}
		value.ready = true
		writeJSON(w, &value.file)
		return true
	case (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.HasPrefix(r.URL.Path, "/upload-session/"):
		value := f.files[strings.TrimPrefix(r.URL.Path, "/upload-session/")]
		if value == nil {
			w.WriteHeader(404)
			return true
		}
		if !f.append(w, r.Body, value) {
			return true
		}
		_, total, _ := strings.Cut(r.Header.Get("Content-Range"), "/")
		size, err := strconv.ParseInt(total, 10, 64)
		if err == nil && value.size == size {
			value.ready = true
			writeJSON(w, &value.file)
			return true
		}
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", value.size-1))
		w.Header().Set("X-Http-Status-Code-Override", "308")
		w.WriteHeader(200)
		return true
	case strings.HasPrefix(r.URL.Path, "/drive/v3/files/"):
		ref := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
		value := f.files[ref]
		if value == nil || !value.ready || (f.hideEarlier && value.generation < f.generation) || f.unreadable {
			w.WriteHeader(404)
			writeJSON(w, map[string]interface{}{"error": map[string]interface{}{"code": 404}})
			return true
		}
		if r.Method == http.MethodDelete {
			delete(f.files, ref)
			w.WriteHeader(204)
			return true
		}
		if r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media" {
			file, err := os.Open(value.path)
			if err != nil {
				w.WriteHeader(500)
				return true
			}
			defer func() { _ = file.Close() }()
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.CopyBuffer(w, struct{ io.Reader }{file}, f.buffer[:])
			return true
		}
	}
	return false
}

func (f *Fake) create(metadata gdrive.File) *entry {
	metadata.Id = uuid.NewString()
	value := &entry{file: metadata, path: filepath.Join(f.directory, metadata.Id), generation: f.generation}
	f.files[metadata.Id] = value
	return value
}

func (f *Fake) append(w http.ResponseWriter, r io.Reader, value *entry) bool {
	file, err := os.OpenFile(value.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.WriteHeader(500)
		return false
	}
	count, copyErr := io.CopyBuffer(struct{ io.Writer }{file}, r, f.buffer[:])
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		w.WriteHeader(500)
		return false
	}
	value.size += count
	return true
}

func writeJSON(w http.ResponseWriter, value interface{}) { _ = json.NewEncoder(w).Encode(value) }
