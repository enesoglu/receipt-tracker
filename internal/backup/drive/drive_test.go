package drive_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/backup"
	"github.com/enesoglu/receipt-tracker/internal/backup/backuptest"
	"github.com/enesoglu/receipt-tracker/internal/backup/drive"
)

type connection struct {
	api  *gdrive.Service
	root string
}

func (c connection) Access(context.Context) (*gdrive.Service, string, error) {
	return c.api, c.root, nil
}

func newDestination(t *testing.T) (*drive.Store, *gdrive.Service, *backuptest.Fake) {
	t.Helper()
	fake := backuptest.NewFake(t)
	api, err := gdrive.NewService(t.Context(), option.WithHTTPClient(fake.Server.Client()), option.WithEndpoint(fake.Server.URL+"/drive/v3/"))
	if err != nil {
		t.Fatal(err)
	}
	return drive.New(connection{api: api, root: "synthetic-root"}), api, fake
}

func TestDrive_DestinationContract(t *testing.T) {
	backuptest.Run(t, func(t *testing.T) backup.Destination { store, _, _ := newDestination(t); return store })
}

func TestDrive_DoesNotAccessOtherFolders(t *testing.T) {
	store, api, fake := newDestination(t)
	name := "20261115T040000.000000000Z.dump"
	outside, err := api.Files.Create(&gdrive.File{Name: name, Parents: []string{"outside-root"}}).Context(t.Context()).Do()
	if err != nil {
		t.Fatal(err)
	}
	objects, err := store.List(t.Context())
	if err != nil || len(objects) != 0 {
		t.Fatalf("leaked listing: %+v %v", objects, err)
	}
	if err := store.Delete(t.Context(), name); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("outside deletion: %v", err)
	}
	if _, err := api.Files.Get(outside.Id).Context(t.Context()).Do(); err != nil {
		t.Fatalf("outside file deleted: %v", err)
	}
	if err := store.Put(t.Context(), name, strings.NewReader("dump")); err != nil {
		t.Fatal(err)
	}
	// A fresh adapter with the same app root must discover the original folder.
	fresh := drive.New(connection{api: api, root: "synthetic-root"})
	objects, err = fresh.List(t.Context())
	if err != nil || len(objects) != 1 || fake.FolderCount("backups") != 1 {
		t.Fatalf("recovery duplicated folder: %+v %v", objects, err)
	}
}

func TestDrive_FreshAdapterCanReadEarlierDump(t *testing.T) {
	store, _, fake := newDestination(t)
	name := "20261115T050000.000000000Z.dump"
	if err := store.Put(t.Context(), name, strings.NewReader("earlier dump")); err != nil {
		t.Fatal(err)
	}
	freshAPI, err := gdrive.NewService(t.Context(), option.WithHTTPClient(fake.Server.Client()), option.WithEndpoint(fake.Server.URL+"/drive/v3/"))
	if err != nil {
		t.Fatal(err)
	}
	freshAPI.UserAgent = "new-connection"
	fresh := drive.New(connection{api: freshAPI, root: "synthetic-root"})
	objects, err := fresh.List(t.Context())
	if err != nil || len(objects) != 1 {
		t.Fatalf("fresh listing: %+v %v", objects, err)
	}
	stream, err := fresh.Open(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	closeErr := stream.Close()
	if err != nil || closeErr != nil || string(data) != "earlier dump" {
		t.Fatalf("fresh read: %q %v %v", data, err, closeErr)
	}
	if fake.FolderCount("backups") != 1 {
		t.Fatal("duplicate recovery folder")
	}
}
