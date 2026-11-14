package drive

import (
	"bytes"
	"strings"
	"testing"
)

func TestProbe_RoundTripAndReauthorizationVisibility(t *testing.T) {
	for _, name := range []string{"reused grant", "hidden earlier files", "unreadable file"} {
		t.Run(name, func(t *testing.T) {
			s, fake, admin, _ := newService(t)
			connect(t, s, admin)
			var written, listed bytes.Buffer
			if err := s.Probe(t.Context(), []string{"storage", "probe", "--write"}, &written); err != nil {
				t.Fatal(err)
			}
			ref := strings.TrimSpace(strings.TrimPrefix(written.String(), "written "))
			if ref == "" {
				t.Fatal("missing marker ref")
			}
			if name == "hidden earlier files" {
				fake.HideEarlier()
			}
			if err := s.repo.DeleteDriveConnection(t.Context()); err != nil {
				t.Fatal(err)
			}
			connect(t, s, admin)
			if name == "unreadable file" {
				fake.Unreadable()
			}
			err := s.Probe(t.Context(), []string{"storage", "probe", "--list"}, &listed)
			switch name {
			case "reused grant":
				if err != nil || !strings.Contains(listed.String(), ref+" readable") || fake.FolderCount("receipt-tracker-test") != 1 {
					t.Fatalf("old marker: %q %v", listed.String(), err)
				}
			case "hidden earlier files":
				if err == nil || !strings.Contains(listed.String(), "no files visible") {
					t.Fatalf("empty listing reported success: %q %v", listed.String(), err)
				}
			case "unreadable file":
				if err == nil || !strings.Contains(listed.String(), ref+" unreadable") {
					t.Fatalf("unreadable object reported success: %q %v", listed.String(), err)
				}
			}
			for _, secret := range []string{"synthetic-refresh-token", "synthetic-access-token", "synthetic-provider-secret"} {
				if strings.Contains(written.String()+listed.String(), secret) {
					t.Fatal("probe exposed a token")
				}
			}
		})
	}
}

func TestProbe_ValidatesArgumentsAndRequiresConnection(t *testing.T) {
	s, _, _, _ := newService(t)
	for _, args := range [][]string{nil, {"storage", "probe"}, {"storage", "probe", "--unknown"}, {"storage", "probe", "--write", "--list"}, {"other", "probe", "--list"}, {"storage", "probe", "--list"}} {
		var output bytes.Buffer
		if err := s.Probe(t.Context(), args, &output); err == nil {
			t.Fatalf("accepted args %v without a connection", args)
		}
	}
}
