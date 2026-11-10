package app

import (
	"context"
	"errors"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
)

// OwnPasswordChange is the self-service credential operation distinct from
// an administrator reset: the verified principal proves the old password and
// other active sessions are revoked after the change.
type OwnPasswordChange struct {
	identity   OwnPasswordStore
	hasher     func(string) (string, error)
	complexity bool
}

// OwnPasswordStore locks the membership, reads the stored hash, writes the
// new hash, and revokes other sessions in one transaction.
type OwnPasswordStore interface {
	ChangeOwnPassword(ctx context.Context, subject string, tenantID uint64, oldHash, newHash string, now time.Time) error
}

func NewOwnPasswordChange(store OwnPasswordStore, complexityRequired bool) *OwnPasswordChange {
	return &OwnPasswordChange{
		identity:   store,
		hasher:     authn.HashPassword,
		complexity: complexityRequired,
	}
}

// Change validates the request, verifies the current password, and stores
// the new hash. oldPassword/newPassword are already SM4-decrypted plaintext.
func (s *OwnPasswordChange) Change(ctx context.Context, principal authn.RequestPrincipal, oldPassword, newPassword string) *coreerrors.Error {
	// Any authenticated admin-realm principal may change its own credential;
	// the capability boundary is the session itself rather than a managed
	// capability point.
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return err
	}
	oldPassword = strings.TrimSpace(oldPassword)
	newPassword = strings.TrimSpace(newPassword)
	if oldPassword == "" || newPassword == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "old and new password are required")
	}
	if newPassword == oldPassword {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "new password must differ from the old one")
	}
	if verifyErr := authn.ValidatePasswordComplexity(newPassword, s.complexity); verifyErr != nil {
		if errors.Is(verifyErr, authn.ErrPasswordTooWeak) {
			return coreerrors.New(coreerrors.CodeInvalidArgument, verifyErr.Error())
		}
		return coreerrors.New(coreerrors.CodeInvalidArgument, "password is invalid")
	}

	// Hash both passwords: the repository transaction compares the stored
	// hash with oldHash under a row lock, then writes newHash.
	oldHash, oldHashErr := s.hasher(oldPassword)
	if oldHashErr != nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "old password is invalid")
	}
	newHash, hashGenErr := s.hasher(newPassword)
	if hashGenErr != nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "new password is invalid")
	}
	if changeErr := s.identity.ChangeOwnPassword(ctx, principal.Subject, tenantID, oldHash, newHash, time.Now().UTC()); changeErr != nil {
		switch {
		case errors.Is(changeErr, ErrOldPasswordMismatch):
			return coreerrors.New(coreerrors.CodeFailedPrecondition, "old password does not match")
		case errors.Is(changeErr, ErrRBACUserNotFound):
			return coreerrors.New(coreerrors.CodeNotFound, "account is unavailable")
		default:
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "change password", changeErr)
		}
	}
	return nil
}
