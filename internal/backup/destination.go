// Package backup streams database dumps to replaceable destinations.
package backup

import (
	"context"
	"io"
	"time"

	"github.com/enesoglu/receipt-tracker/internal/apperr"
)

// Destination stores instance-owned database dumps. List returns names in ascending order.
type Destination interface {
	Put(context.Context, string, io.Reader) error
	List(context.Context) ([]Object, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// Object describes a complete stored dump.
type Object struct {
	Name string
	Size int64
}

// NameLayout uses sortable UTC timestamps with nanoseconds to avoid collisions.
const NameLayout = "20060102T150405.000000000Z.dump"

// ValidateName accepts only canonical dump names, never paths.
func ValidateName(name string) error {
	stamp, err := time.Parse(NameLayout, name)
	if err != nil || stamp.Format(NameLayout) != name {
		return apperr.New(apperr.Invalid, "invalid backup name")
	}
	return nil
}
