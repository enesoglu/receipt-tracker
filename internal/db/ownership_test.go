package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/enesoglu/receipt-tracker/internal/db/dbtest"
)

// TestQueries_UserOwnedTablesRequireBoundOwnership is a source guard, paired
// with real two-user transport tests. Discovering tables from the migrated
// schema makes a new domain subject to the guard without a hand-kept list.
// It checks parameter binding, not full SQL semantics; isolation tests remain
// necessary for joins, predicates and transaction behavior.
func TestQueries_UserOwnedTablesRequireBoundOwnership(t *testing.T) {
	pool := dbtest.New(t)
	rows, err := pool.Query(t.Context(), `SELECT table_name FROM information_schema.columns WHERE table_schema='public' AND column_name='user_id'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	owned := map[string]bool{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		owned[table] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("queries/*.sql")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find query sources: %v", err)
	}
	annotation := regexp.MustCompile(`(?m)^-- name: (\w+) :\w+`)
	count := 0
	for _, path := range paths {
		//nolint:gosec // The fixed glob enumerates only repository query sources.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		matches := annotation.FindAllStringSubmatchIndex(text, -1)
		for i, match := range matches {
			end := len(text)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			name := text[match[2]:match[3]]
			query := text[match[1]:end]
			t.Run(name, func(t *testing.T) {
				if !hasBoundOwnership(query, owned) {
					t.Errorf("%s: user-owned query needs a bound user_id predicate (or user_id on insert)", path)
				}
			})
			count++
		}
	}
	if count == 0 || len(owned) == 0 {
		t.Fatal("ownership guard inspected no queries or tables")
	}
}

func hasBoundOwnership(query string, owned map[string]bool) bool {
	comments := regexp.MustCompile(`(?m)--[^\n]*|(?s:/\*.*?\*/)`)
	query = strings.ToLower(comments.ReplaceAllString(query, " "))
	tables := regexp.MustCompile(`\b(from|join|update|into)\s+([a-z_][a-z_0-9]*)`).FindAllStringSubmatch(query, -1)
	touchesOwned := false
	for _, table := range tables {
		touchesOwned = touchesOwned || owned[table[2]]
	}
	if !touchesOwned {
		return true
	}
	parameter := `(?:@user_id\b|sqlc\.(?:arg|narg)\(user_id\)|\$[1-9][0-9]*)`
	if strings.HasPrefix(strings.TrimSpace(query), "insert") {
		// Insert ownership is a value rather than a WHERE clause. Any owned
		// rows read by INSERT ... SELECT must also be filtered below.
		insert := regexp.MustCompile(`(?s)^\s*insert\s+into\s+\w+\s*\([^)]*\buser_id\b[^)]*\).*` + parameter)
		if !insert.MatchString(query) {
			return false
		}
		readsOwned := false
		for _, table := range tables {
			readsOwned = readsOwned || ((table[1] == "from" || table[1] == "join") && owned[table[2]])
		}
		if !readsOwned {
			return true
		}
	}
	filter := regexp.MustCompile(`(?s)\bwhere\b.*\buser_id\s*=\s*` + parameter)
	return filter.MatchString(query)
}

func TestOwnershipGuard_RejectsMissingOrUnboundFilters(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        bool
	}{
		{"bound read", "SELECT * FROM receipts WHERE user_id = @user_id", true},
		{"positional read", "SELECT * FROM receipts WHERE user_id = $1", true},
		{"bound insert", "INSERT INTO receipts (user_id,title) VALUES (@user_id,@title)", true},
		{"instance data", "SELECT * FROM jobs", true},
		{"no predicate", "SELECT * FROM receipts", false},
		{"id only", "SELECT * FROM receipts WHERE id = @id", false},
		{"projected owner", "SELECT user_id FROM receipts WHERE id = @id", false},
		{"comment is not protection", "SELECT * FROM receipts -- WHERE user_id = @user_id", false},
		{"unbound owner", "SELECT * FROM receipts WHERE user_id = user_id", false},
		{"missing insert owner", "INSERT INTO receipts (title) VALUES (@title)", false},
		{"unscoped insert select", "INSERT INTO receipts (user_id,title) SELECT @user_id,title FROM receipts", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasBoundOwnership(tc.query, map[string]bool{"receipts": true}); got != tc.want {
				t.Fatalf("ownership guard = %v, want %v", got, tc.want)
			}
		})
	}
}
