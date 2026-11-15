package drive

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

func errorCode(err error) string {
	var classified *apperr.Error
	if errors.As(err, &classified) {
		return classified.Code
	}
	return ""
}

func putImage(t *testing.T, s *Service, key string) string {
	t.Helper()
	ref, err := s.Put(t.Context(), key, strings.NewReader("synthetic image"), 15, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func storedRoot(t *testing.T, s *Service) string {
	t.Helper()
	row, err := s.repo.GetDriveConnection(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return row.RootFolderRef
}

func TestRootFolderID_FollowsMovedAndRenamedFolderThroughRecovery(t *testing.T) {
	s, fake, admin, _ := newService(t)
	connect(t, s, admin)
	root := storedRoot(t, s)
	earlier := putImage(t, s, "user/receipt/earlier.png")

	// The owner moves the app's folder into a folder the app cannot see and renames it.
	fake.Move(root, "owner-vps-folder", "receipts-renamed")
	s.options.Config.Storage.DriveRootFolderID = root
	if err := s.Check(t.Context()); err != nil {
		t.Fatalf("check after move: %v", err)
	}
	later := putImage(t, s, "user/receipt/later.png")
	if got := fake.Parent(fake.Parent(fake.Parent(later))); got != root {
		t.Fatalf("new image is under %q, want the moved root %q", got, root)
	}

	// Recovery onto an empty database with a fresh grant.
	if err := s.repo.DeleteDriveConnection(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.store = nil
	connect(t, s, admin)
	if got := storedRoot(t, s); got != root {
		t.Fatalf("recovered root = %q, want %q", got, root)
	}
	_, access, err := s.Access(t.Context())
	if err != nil || access != root {
		t.Fatalf("backup root = %q, %v; want %q", access, err, root)
	}
	for _, ref := range []string{earlier, later} {
		r, err := s.Open(t.Context(), ref)
		if err != nil {
			t.Fatalf("open after recovery: %v", err)
		}
		data, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil || closeErr != nil || string(data) != "synthetic image" {
			t.Fatalf("image after recovery: %q %v %v", data, err, closeErr)
		}
	}
	if fake.FolderCount("receipt-tracker-test") != 0 {
		t.Fatal("recovery created a folder by name instead of using the configured id")
	}
	if fake.Parent(root) != "owner-vps-folder" {
		t.Fatal("the application moved the owner's folder")
	}
}

func TestRootFolderID_ReplacesStoredRootWithoutReconnect(t *testing.T) {
	s, fake, admin, _ := newService(t)
	connect(t, s, admin)
	other := fake.Folder("receipt-tracker-other", "owner-vps-folder")
	s.options.Config.Storage.DriveRootFolderID = other
	if err := s.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := storedRoot(t, s); got != other {
		t.Fatalf("stored root = %q, want %q", got, other)
	}
	ref := putImage(t, s, "user/receipt/image.png")
	if got := fake.Parent(fake.Parent(fake.Parent(ref))); got != other {
		t.Fatalf("image is under %q, want %q", got, other)
	}

	// Clearing the id keeps the stored root for the running connection.
	s.options.Config.Storage.DriveRootFolderID = ""
	if err := s.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, root, err := s.Access(t.Context()); err != nil || root != other {
		t.Fatalf("root after clearing the id = %q, %v", root, err)
	}
}

func TestRootFolderID_RefusesUnusableFolderAndCreatesNone(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"missing or created by another client", "storage_root_not_found"},
		{"not a folder", "storage_root_not_folder"},
		{"trashed", "storage_root_trashed"},
		{"read-only", "storage_root_not_writable"},
		{"hidden from a new grant", "storage_root_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, fake, admin, _ := newService(t)
			connect(t, s, admin)
			root := storedRoot(t, s)
			image := putImage(t, s, "user/receipt/image.png")
			id := root
			switch tt.name {
			case "missing or created by another client":
				id = "1SyntheticMissingFolder"
			case "not a folder":
				id = image
			case "trashed":
				fake.Trash(root)
			case "read-only":
				fake.ReadOnly(root)
			case "hidden from a new grant":
				fake.HideEarlier()
			}
			s.options.Config.Storage.DriveRootFolderID = id

			if tt.name != "hidden from a new grant" {
				// Running connection: writes stop with a stable reason, the grant stays valid.
				if err := s.Check(t.Context()); errorCode(err) != tt.want {
					t.Fatalf("check error = %v, want %s", err, tt.want)
				}
				if _, err := s.Put(t.Context(), "user/receipt/next.png", strings.NewReader("x"), 1, "image/png"); errorCode(err) != tt.want {
					t.Fatalf("put error = %v, want %s", err, tt.want)
				}
				status, err := s.Status(t.Context(), admin)
				if err != nil || status.Drive != "ok" {
					t.Fatalf("status = %+v, %v; a folder problem is not a lost grant", status, err)
				}
				if got := storedRoot(t, s); got != root {
					t.Fatalf("stored root changed to %q", got)
				}
			}

			// Recovery: the connection is not saved and no folder is created.
			if err := s.repo.DeleteDriveConnection(t.Context()); err != nil {
				t.Fatal(err)
			}
			s.store = nil
			err := s.Complete(t.Context(), admin, begin(t, s, admin), "synthetic-code", "")
			if errorCode(err) != tt.want {
				t.Fatalf("connect error = %v, want %s", err, tt.want)
			}
			if _, err := s.repo.GetDriveConnection(t.Context()); err == nil {
				t.Fatal("connection saved with an unusable root")
			}
			if fake.FolderCount("receipt-tracker-test") != 1 {
				t.Fatal("a replacement folder was created")
			}
		})
	}
}
