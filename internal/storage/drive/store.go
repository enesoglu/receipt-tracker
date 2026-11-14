// Package drive stores images using the owner's app-scoped Google Drive grant.
package drive

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/storage"
)

const folderType = "application/vnd.google-apps.folder"

// Store streams complete images to a private Drive tree. Callers enforce ownership.
type Store struct {
	api     *gdrive.Service
	root    string
	folders *sync.Mutex
}

// newStore creates an adapter rooted at an already authorized app-created folder.
func newStore(api *gdrive.Service, root string) *Store {
	return &Store{api: api, root: root, folders: &sync.Mutex{}}
}

// Put stores exactly size bytes. A failed stream never publishes a complete object.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	if err := storage.ValidateKey(key); err != nil {
		return "", err
	}
	if size < 0 || r == nil {
		return "", apperr.New(apperr.Invalid, "invalid image stream")
	}
	parts := strings.Split(key, "/")
	parent := s.root
	// Drive has no unique folder constraint. Serialize discovery and creation in this adapter.
	s.folders.Lock()
	for _, part := range parts[:len(parts)-1] {
		var err error
		parent, err = findFolder(ctx, s.api, parent, part)
		if err != nil {
			s.folders.Unlock()
			return "", err
		}
	}
	s.folders.Unlock()
	source := &exactReader{ctx: ctx, reader: r, remaining: size}
	file, err := s.api.Files.Create(&gdrive.File{Name: parts[len(parts)-1], Parents: []string{parent}}).
		Media(source, googleapi.ContentType(contentType), googleapi.ChunkSize(1<<20)).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", operationError(err)
	}
	if file.Id == "" {
		return "", unavailable("storage_request_failed")
	}
	return file.Id, nil
}

// Open returns an owned response stream or NotFound for an invisible file.
func (s *Store) Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	response, err := s.api.Files.Get(ref).Context(ctx).Download()
	if err != nil {
		return nil, operationError(err)
	}
	return response.Body, nil
}

// Delete removes a file. A missing or invisible file is NotFound.
func (s *Store) Delete(ctx context.Context, ref string) error {
	if err := s.api.Files.Delete(ref).Context(ctx).Do(); err != nil {
		return operationError(err)
	}
	return nil
}

func findFolder(ctx context.Context, api *gdrive.Service, parent, name string) (string, error) {
	query := "trashed = false and mimeType = '" + folderType + "' and name = '" + escape(name) + "' and '" + escape(parent) + "' in parents"
	files, err := api.Files.List().Q(query).Fields("files(id)").PageSize(2).Context(ctx).Do()
	if err != nil {
		return "", operationError(err)
	}
	// An ambiguous recovery tree must not silently pick one copy and lose data.
	if len(files.Files) > 1 {
		return "", unavailable("storage_duplicate_folder")
	}
	if len(files.Files) == 1 {
		return files.Files[0].Id, nil
	}
	folder, err := api.Files.Create(&gdrive.File{Name: name, MimeType: folderType, Parents: []string{parent}}).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", operationError(err)
	}
	if folder.Id == "" {
		return "", unavailable("storage_request_failed")
	}
	return folder.Id, nil
}

func escape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", `\'`)
}

func unavailable(code string) error {
	return apperr.New(apperr.Unavailable, "Google Drive is unavailable").WithCode(code)
}

func operationError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var classified *apperr.Error
	if errors.As(err, &classified) {
		return classified
	}
	var response *googleapi.Error
	if errors.As(err, &response) && response.Code == 404 {
		return apperr.New(apperr.NotFound, "image not found")
	}
	// Google errors may contain tokens, request URLs and file metadata.
	return unavailable("storage_request_failed")
}

type exactReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining int64
}

func (r *exactReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining == 0 {

		var extra [1]byte
		n, err := r.reader.Read(extra[:])
		if n > 0 {
			return 0, apperr.New(apperr.Invalid, "image stream exceeds declared size")
		}
		return 0, err
	}
	n, err := r.reader.Read(p[:min(int64(len(p)), r.remaining)])
	r.remaining -= int64(n)
	if errors.Is(err, io.EOF) && r.remaining > 0 {
		return n, io.ErrUnexpectedEOF
	}
	// Let the next read check for excess bytes, even if this read ends at EOF.
	if errors.Is(err, io.EOF) && r.remaining == 0 {
		err = nil
	}
	return n, err
}
