package local_test

import (
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/backup"
	"github.com/enesoglu/receipt-tracker/internal/backup/backuptest"
	"github.com/enesoglu/receipt-tracker/internal/backup/local"
)

func TestLocal_DestinationContract(t *testing.T) {
	backuptest.Run(t, func(t *testing.T) backup.Destination {
		store, err := local.New(t.Context(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
		return store
	})
}
