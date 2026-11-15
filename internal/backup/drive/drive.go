// Package drive stores dumps beneath the shared app-created Drive root.
package drive

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/backup"
)

const folderType = "application/vnd.google-apps.folder"

// Connection supplies the current authorized API after each reconnect.
type Connection interface {
	Access(context.Context) (*gdrive.Service, string, error)
}

// Store isolates all dump operations within the backups child folder.
type Store struct {
	connection Connection
	mu         sync.Mutex
}

// New reuses the image-storage grant independently of the image backend.
func New(connection Connection) *Store { return &Store{connection: connection} }

func (s *Store) access(ctx context.Context) (*gdrive.Service, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	api, root, err := s.connection.Access(ctx)
	if err != nil {
		return nil, "", err
	}
	query := "trashed = false and mimeType = '" + folderType + "' and name = 'backups' and '" + escape(root) + "' in parents"
	files, err := api.Files.List().Q(query).PageSize(2).Fields("files(id)").Context(ctx).Do()
	if err != nil {
		return nil, "", safe(err)
	}
	if len(files.Files) > 1 {
		return nil, "", apperr.New(apperr.Conflict, "duplicate backups folder")
	}
	if len(files.Files) == 1 {
		return api, files.Files[0].Id, nil
	}
	folder, err := api.Files.Create(&gdrive.File{Name: "backups", MimeType: folderType, Parents: []string{root}}).Fields("id").Context(ctx).Do()
	if err != nil {
		return nil, "", safe(err)
	}
	if folder.Id == "" {
		return nil, "", safe(nil)
	}
	return api, folder.Id, nil
}

// Put uses bounded resumable upload chunks; failed streams never become complete dumps.
func (s *Store) Put(ctx context.Context, name string, source io.Reader) error {
	if err := backup.ValidateName(name); err != nil {
		return err
	}
	if source == nil {
		return apperr.New(apperr.Invalid, "missing backup stream")
	}
	api, folder, err := s.access(ctx)
	if err != nil {
		return err
	}
	existing, err := list(ctx, api, folder)
	if err != nil {
		return err
	}
	for _, file := range existing {
		if file.Name == name {
			return apperr.New(apperr.Conflict, "backup already exists")
		}
	}
	file, err := api.Files.Create(&gdrive.File{Name: name, Parents: []string{folder}}).
		Media(source, googleapi.ContentType("application/octet-stream"), googleapi.ChunkSize(1<<20)).Fields("id").Context(ctx).Do()
	if err != nil {
		return safe(err)
	}
	if file.Id == "" {
		return safe(nil)
	}
	return nil
}

func list(ctx context.Context, api *gdrive.Service, folder string) ([]*gdrive.File, error) {
	files := make([]*gdrive.File, 0)
	page := ""
	for {
		result, err := api.Files.List().Q("trashed = false and mimeType != '" + folderType + "' and '" + escape(folder) + "' in parents").
			Fields("nextPageToken,files(id,name,size)").PageSize(1000).PageToken(page).Context(ctx).Do()
		if err != nil {
			return nil, safe(err)
		}
		files = append(files, result.Files...)
		page = result.NextPageToken
		if page == "" {
			return files, nil
		}
	}
}

// List returns only dump objects inside the current backups folder.
func (s *Store) List(ctx context.Context) ([]backup.Object, error) {
	api, folder, err := s.access(ctx)
	if err != nil {
		return nil, err
	}
	files, err := list(ctx, api, folder)
	if err != nil {
		return nil, err
	}
	objects := make([]backup.Object, 0, len(files))
	for _, file := range files {
		if backup.ValidateName(file.Name) == nil {
			objects = append(objects, backup.Object{Name: file.Name, Size: file.Size})
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name < objects[j].Name })
	return objects, nil
}

func (s *Store) find(ctx context.Context, name string) (*gdrive.Service, string, error) {
	if err := backup.ValidateName(name); err != nil {
		return nil, "", err
	}
	api, folder, err := s.access(ctx)
	if err != nil {
		return nil, "", err
	}
	files, err := list(ctx, api, folder)
	if err != nil {
		return nil, "", err
	}
	id := ""
	for _, file := range files {
		if file.Name == name {
			if id != "" {
				return nil, "", apperr.New(apperr.Conflict, "duplicate backup name")
			}
			id = file.Id
		}
	}
	if id == "" {
		return nil, "", apperr.New(apperr.NotFound, "backup not found")
	}
	return api, id, nil
}

// Open resolves names only inside the backups folder before opening media.
func (s *Store) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	api, id, err := s.find(ctx, name)
	if err != nil {
		return nil, err
	}
	response, err := api.Files.Get(id).Context(ctx).Download()
	if err != nil {
		return nil, safe(err)
	}
	return response.Body, nil
}

// Delete resolves names only inside the backups folder before deleting.
func (s *Store) Delete(ctx context.Context, name string) error {
	api, id, err := s.find(ctx, name)
	if err != nil {
		return err
	}
	if err := api.Files.Delete(id).Context(ctx).Do(); err != nil {
		return safe(err)
	}
	return nil
}

func escape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", `\'`)
}

func safe(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var response *googleapi.Error
	if errors.As(err, &response) && response.Code == 404 {
		return apperr.New(apperr.NotFound, "backup not found")
	}
	return apperr.New(apperr.Unavailable, "Google Drive backup request failed")
}
