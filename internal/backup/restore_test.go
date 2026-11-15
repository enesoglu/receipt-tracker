package backup_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
	"github.com/enesoglu/receipt-tracker/internal/backup"
)

type target struct {
	nonEmpty bool
	failure  bool
	seen     string
}

func (t *target) NonEmpty(_ context.Context, address string) (bool, error) {
	t.seen = address
	if t.failure {
		return false, errors.New("synthetic-password")
	}
	return t.nonEmpty, nil
}

func TestRestore_RefusesNonemptyAndSourceDatabases(t *testing.T) {
	//nolint:gosec // Synthetic connection used only to verify credential preservation.
	source := "postgres://user:synthetic-password@localhost:5432/application?sslmode=disable"
	inspector := &target{nonEmpty: true}
	restorer := backup.NewRestorer(nil, inspector, source)
	if err := restorer.Restore(t.Context(), "missing.dump", "application", true); !errors.Is(err, apperr.Invalid) {
		t.Fatalf("source overwrite: %v", err)
	}
	if inspector.seen != "" {
		t.Fatal("inspected the live database")
	}
	if err := restorer.Restore(t.Context(), "missing.dump", "scratch", false); !errors.Is(err, apperr.Conflict) {
		t.Fatalf("nonempty target: %v", err)
	}
	parsed, err := url.Parse(inspector.seen)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/scratch" || parsed.Host != "localhost:5432" || parsed.User.String() != "user:synthetic-password" {
		t.Fatal("changed credentials or server")
	}
	inspector.failure = true
	if err := restorer.Restore(t.Context(), "missing.dump", "scratch", true); err == nil || strings.Contains(err.Error(), "synthetic-password") {
		t.Fatalf("unsafe target error: %v", err)
	}
}

func TestTargetURL_RejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "../other", "other?password=x", "with space", "a;DROP DATABASE x", "postgres", "template0", "template1"} {
		_, err := backup.TargetURL("postgres://user:password@localhost/application", name)
		if !errors.Is(err, apperr.Invalid) {
			t.Fatalf("accepted target %q: %v", name, err)
		}
	}
}

func TestCLI_RejectsAmbiguousArguments(t *testing.T) {
	restorer := backup.NewRestorer(nil, &target{}, "postgres://localhost/application")
	for _, args := range [][]string{
		{},
		{"unknown"},
		{"run", "extra"},
		{"list", "extra"},
		{"restore"},
		{"restore", "x", "--target-db"},
		{"restore", "x", "--target-db", "scratch", "--target-db", "other"},
		{"restore", "x", "--target-db", "scratch", "--force", "--force"},
		{"restore", "x", "--target-db", "scratch", "--unknown"},
	} {
		if err := backup.CLI(t.Context(), args, nil, nil, restorer, io.Discard); !errors.Is(err, apperr.Invalid) {
			t.Fatalf("args %v: %v", args, err)
		}
	}
}
