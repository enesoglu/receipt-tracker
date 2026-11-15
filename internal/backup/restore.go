package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// Target checks a destination database using configured credentials, without logging them.
type Target interface {
	NonEmpty(context.Context, string) (bool, error)
}

// Restorer streams dumps into a separate database on the configured server.
type Restorer struct {
	destination Destination
	target      Target
	databaseURL string
	binary      string
}

// NewRestorer binds source, target inspection and private database credentials.
func NewRestorer(destination Destination, target Target, databaseURL string) *Restorer {
	return &Restorer{destination: destination, target: target, databaseURL: databaseURL, binary: "pg_restore"}
}

// TargetURL replaces only the database name and refuses restoring over the source.
func TargetURL(source, name string) (string, error) {
	valid, err := regexp.MatchString(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`, name)
	if err != nil || !valid || name == "postgres" || name == "template0" || name == "template1" {
		return "", apperr.New(apperr.Invalid, "invalid target database name")
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", apperr.New(apperr.Invalid, "invalid database configuration")
	}
	if strings.TrimPrefix(parsed.Path, "/") == name {
		return "", apperr.New(apperr.Invalid, "restore target must differ from the application database")
	}
	if parsed.Query().Has("dbname") {
		return "", apperr.New(apperr.Invalid, "database name must be configured in the URL path")
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}

// Restore accepts an object name or a local dump path and refuses nonempty targets by default.
// The target must already exist; createdb can create it using the same environment credentials.
func (r *Restorer) Restore(ctx context.Context, object, name string, force bool) (result error) {
	targetURL, err := TargetURL(r.databaseURL, name)
	if err != nil {
		return err
	}
	nonEmpty, err := r.target.NonEmpty(ctx, targetURL)
	if err != nil {
		return errors.New("inspect restore target: database unavailable")
	}
	if nonEmpty && !force {
		return apperr.New(apperr.Conflict, "restore target is not empty; use --force to replace its objects")
	}
	var stream io.ReadCloser
	// A canonical object name always means the destination object; explicit paths mean local files.
	if ValidateName(object) == nil {
		if r.destination == nil {
			return apperr.New(apperr.Unavailable, "backup destination is disabled; specify a local file path")
		}
		stream, err = r.destination.Open(ctx, object)
	} else {
		stream, err = os.Open(object) //nolint:gosec // Explicit administrator CLI path, never HTTP input.
	}
	if err != nil {
		return errors.New("open restore dump: file unavailable")
	}
	defer func() { result = errors.Join(result, stream.Close()) }()
	args := []string{"--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges"}
	// An empty --dbname makes libpq use PGDATABASE, keeping credentials off the command line.
	args = append(args, "--dbname=")
	if force {
		args = append(args, "--clean", "--if-exists")
	}
	command := exec.CommandContext(ctx, r.binary, args...) //nolint:gosec // Fixed executable; arguments contain only restore flags, never credentials.
	environment, err := databaseEnv(targetURL)
	if err != nil {
		return err
	}
	command.Env = environment
	command.Stdin = stream
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return errors.New("pg_restore failed; target transaction rolled back")
	}
	return nil
}

// CLI executes backup commands and emits only names, sizes and stable errors.
func CLI(ctx context.Context, args []string, service *Service, destination Destination, restorer *Restorer, output io.Writer) error {
	if len(args) == 0 {
		return apperr.New(apperr.Invalid, "usage: backup run | list | restore <object> --target-db <name> [--force]")
	}
	switch args[0] {
	case "run":
		if len(args) != 1 || service == nil {
			return apperr.New(apperr.Invalid, "backup run requires an enabled destination")
		}
		return service.Run(ctx)
	case "list":
		if len(args) != 1 || destination == nil {
			return apperr.New(apperr.Invalid, "backup list requires an enabled destination")
		}
		objects, err := destination.List(ctx)
		if err != nil {
			return err
		}
		for _, object := range objects {
			if _, err := fmt.Fprintf(output, "%s\t%d\n", object.Name, object.Size); err != nil {
				return err
			}
		}
		return nil
	case "restore":
		if len(args) < 4 || restorer == nil {
			return apperr.New(apperr.Invalid, "restore requires an object and --target-db <name>")
		}
		object, target, force := args[1], "", false
		for index := 2; index < len(args); index++ {
			switch args[index] {
			case "--target-db":
				if target != "" || index+1 >= len(args) {
					return apperr.New(apperr.Invalid, "invalid restore target")
				}
				index++
				target = args[index]
			case "--force":
				if force {
					return apperr.New(apperr.Invalid, "duplicate --force")
				}
				force = true
			default:
				return apperr.New(apperr.Invalid, "unknown restore argument")
			}
		}
		return restorer.Restore(ctx, object, target, force)
	default:
		return apperr.New(apperr.Invalid, "unknown backup command")
	}
}
