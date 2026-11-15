package backup

import (
	"strings"
	"testing"
)

func TestDatabaseEnv_PreservesCredentialsOutsideArguments(t *testing.T) {
	t.Setenv("PGHOST", "unrelated-server")
	t.Setenv("PGPASSWORD", "unrelated-password")
	address := "postgres://synthetic:encoded%40password@localhost:15432/scratch?sslmode=verify-full&application_name=backup" //nolint:gosec // Synthetic encoding fixture.
	environment, err := databaseEnv(address)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for _, value := range environment {
		key, content, _ := strings.Cut(value, "=")
		values[key] = content
	}
	for key, want := range map[string]string{"PGHOST": "localhost", "PGPORT": "15432", "PGUSER": "synthetic", "PGPASSWORD": "encoded@password", "PGDATABASE": "scratch", "PGSSLMODE": "verify-full", "PGAPPNAME": "backup"} {
		if values[key] != want {
			t.Errorf("%s was not preserved", key)
		}
	}
	if _, err := databaseEnv("postgres://synthetic@localhost/database?host=other-server"); err == nil {
		t.Fatal("accepted hidden server override")
	}
}
