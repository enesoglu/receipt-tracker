package backup_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/enesoglu/receipt-tracker/internal/backup"
)

func TestRetention_GapsAndMidnight(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, count := range []int{0, 1, 4, 7, 45} {
		objects := make([]backup.Object, 0, count)
		for index := range count {
			objects = append(objects, backup.Object{Name: now.AddDate(0, 0, -index*2).Format(backup.NameLayout)})
		}
		expired := backup.Expired(objects, now)
		if count <= 7 && len(expired) != 0 {
			t.Fatalf("deleted short history: %v", expired)
		}
		kept := count - len(expired)
		if kept > 11 {
			t.Fatalf("retained too many: %d", kept)
		}
		// A backward jump cannot delete a future dump and does not reinterpret UTC days.
		future := backup.Object{Name: now.Add(time.Nanosecond).Format(backup.NameLayout)}
		withFuture := append(append([]backup.Object{}, objects...), future)
		if diff := cmp.Diff(expired, backup.Expired(withFuture, now)); diff != "" {
			t.Fatal(diff)
		}
	}
	objects := []backup.Object{{Name: now.Add(-time.Second).Format(backup.NameLayout)}, {Name: now.Format(backup.NameLayout)}, {Name: "foreign-file"}}
	if expired := backup.Expired(objects, now); len(expired) != 0 {
		t.Fatal(expired)
	}
}

func TestNextRun_UsesUTCAndTomorrowAtBoundary(t *testing.T) {
	for _, test := range []struct{ now, want string }{
		{"2026-11-15T01:59:59Z", "2026-11-15T02:00:00Z"},
		{"2026-11-15T02:00:00Z", "2026-11-16T02:00:00Z"},
		{"2026-11-15T05:01:00+03:00", "2026-11-16T02:00:00Z"},
	} {
		now, err := time.Parse(time.RFC3339, test.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := backup.NextRun(now, 2, 0).Format(time.RFC3339); got != test.want {
			t.Fatalf("next %s: %s", test.now, got)
		}
	}
}

func TestRetention_KeepsSevenDailyAndFourWeeklyRepresentatives(t *testing.T) {
	now := time.Date(2026, 11, 15, 23, 0, 0, 0, time.UTC)
	objects := make([]backup.Object, 0, 46)
	for index := range 45 {
		objects = append(objects, backup.Object{Name: now.AddDate(0, 0, -index).Format(backup.NameLayout)})
	}
	earlierToday := now.Add(-time.Hour).Format(backup.NameLayout)
	objects = append(objects, backup.Object{Name: earlierToday})
	removed := make(map[string]bool)
	for _, name := range backup.Expired(objects, now) {
		removed[name] = true
	}
	kept := make([]string, 0)
	for _, object := range objects {
		if !removed[object.Name] {
			kept = append(kept, object.Name[:8])
		}
	}
	want := []string{"20261115", "20261114", "20261113", "20261112", "20261111", "20261110", "20261109", "20261108", "20261101", "20261025"}
	if diff := cmp.Diff(want, kept); diff != "" {
		t.Fatal(diff)
	}
	if !removed[earlierToday] {
		t.Fatal("kept an older dump from the same UTC day")
	}
}
