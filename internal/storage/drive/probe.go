package drive

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/config"
)

// Probe runs the session-free storage check used by the owner's recovery exercise.
// Output contains file names and references only; provider errors and tokens are never printed.
func (s *Service) Probe(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 3 || args[0] != "storage" || args[1] != "probe" || (args[2] != "--write" && args[2] != "--list") {
		return apperr.New(apperr.Invalid, "usage: server storage probe --write|--list")
	}
	if s.options.Config.Storage.Backend != config.StorageDrive && s.options.Config.Backup.Destination != config.BackupDrive {
		return unavailable("storage_not_required")
	}
	api, root, err := s.Access(ctx)
	if err != nil {
		return err
	}
	if args[2] == "--write" {
		data := "receipt-tracker synthetic storage probe\n"
		ref, err := newStore(api, root).Put(ctx, "probe-"+uuid.NewString()+".txt", strings.NewReader(data), int64(len(data)), "text/plain")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "written %s\n", ref); err != nil {
			return fmt.Errorf("write probe output: %w", err)
		}
		return nil
	}
	return listProbe(ctx, api, root, output)
}

func listProbe(ctx context.Context, api *gdrive.Service, root string, output io.Writer) error {
	token := ""
	files, unreadable := 0, false
	for {
		page, err := api.Files.List().Q("trashed = false and '" + escape(root) + "' in parents").Fields("nextPageToken,files(id,name,mimeType)").PageSize(1000).PageToken(token).Context(ctx).Do()
		if err != nil {
			return operationError(err)
		}
		for _, file := range page.Files {
			if file.MimeType == folderType {
				if _, err := fmt.Fprintf(output, "%q %s folder\n", file.Name, file.Id); err != nil {
					return fmt.Errorf("write probe output: %w", err)
				}
				continue
			}
			files++
			result := "readable"
			reader, err := newStore(api, root).Open(ctx, file.Id)
			if err == nil {
				_, err = io.Copy(io.Discard, reader)
				closeErr := reader.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err != nil {
				result, unreadable = "unreadable", true
			}
			if _, err := fmt.Fprintf(output, "%q %s %s\n", file.Name, file.Id, result); err != nil {
				return fmt.Errorf("write probe output: %w", err)
			}
		}
		token = page.NextPageToken
		if token == "" {
			break
		}
	}
	if files == 0 {
		if _, err := fmt.Fprintln(output, "no files visible in configured Drive root"); err != nil {
			return fmt.Errorf("write probe output: %w", err)
		}
		return unavailable("storage_probe_empty")
	}
	if unreadable {
		return unavailable("storage_probe_unreadable")
	}
	return nil
}
