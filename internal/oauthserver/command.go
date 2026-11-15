package oauthserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/enesoglu/receipt-tracker/internal/db/sqlc"
)

// CLI lists or revokes connections for an explicitly selected user. Running this
// local command requires the server's database credentials, like backup.
func (s *Service) CLI(ctx context.Context, args []string, out io.Writer) error {
	if len(args) < 3 || args[1] != "--user" {
		return errors.New("usage: oauth list --user UUID | oauth revoke --user UUID --client UUID")
	}
	userID, err := uuid.Parse(args[2])
	if err != nil || userID == uuid.Nil {
		return errors.New("invalid user id")
	}
	switch {
	case args[0] == "list" && len(args) == 3:
		rows, err := sqlc.New(s.db).ListOAuthConnections(ctx, sqlc.ListOAuthConnectionsParams{UserID: userID, ExpiresAt: s.now().UTC()})
		if err != nil {
			return fmt.Errorf("list oauth connections: %w", err)
		}
		for _, row := range rows {
			if _, err := fmt.Fprintf(out, "%s %q %q\n", row.ID, row.Name, strings.Join(row.RedirectUris, ", ")); err != nil {
				return err
			}
		}
		return nil
	case args[0] == "revoke" && len(args) == 5 && args[3] == "--client":
		clientID, err := uuid.Parse(args[4])
		if err != nil || clientID == uuid.Nil {
			return errors.New("invalid client id")
		}
		return s.transaction(ctx, func(q *sqlc.Queries) error {
			if _, err := q.LockOAuthUser(ctx, userID); err != nil {
				return err
			}
			now := s.now().UTC()
			count, err := q.RevokeOAuthClientTokens(ctx, sqlc.RevokeOAuthClientTokensParams{UserID: userID, ClientID: clientID, RevokedAt: &now})
			if err != nil {
				return err
			}
			if err := q.RevokeOAuthClientCodes(ctx, sqlc.RevokeOAuthClientCodesParams{UserID: userID, ClientID: clientID, UsedAt: &now}); err != nil {
				return err
			}
			if count == 0 {
				return failure("invalid_grant")
			}
			return nil
		})
	default:
		return errors.New("unknown OAuth command")
	}
}
