package authn

import (
	"context"
	"errors"
	"time"
)

// Repository results and audit vocabulary belong to authentication use cases.
// These are internal query projections, not generated table entities or HTTP DTOs.
var (
	ErrIdentityNotFound  = errors.New("identity record not found")
	ErrIdentityAmbiguous = errors.New("identity record is ambiguous")
)

const (
	AuthOperationLogin         = "login"
	AuthOperationRefresh       = "refresh"
	AuthOperationLogout        = "logout"
	AuthOperationAuthenticate  = "authenticate"
	AuthOperationSessionList   = "session_list"
	AuthOperationSessionRevoke = "session_revoke"

	AuthOutcomeAllowed = "allowed"
	AuthOutcomeDenied  = "denied"
	AuthOutcomeError   = "error"
)

// LoginIdentity is the minimal credential-verification record. It never
// crosses the HTTP response boundary and keeps the password hash internal.
type LoginIdentity struct {
	UserID       uint64
	TenantID     uint64
	Subject      string
	LoginName    string
	TenantCode   string
	PasswordHash string
	AccountType  string
	MemberRealm  string
}

// SessionRecord is the persisted revocation and refresh-rotation boundary.
// The refresh token field is a SHA-256 digest, never the opaque token itself.
type SessionRecord struct {
	SessionID         string
	UserID            uint64
	TenantID          uint64
	Realm             string
	RefreshTokenHash  string
	RefreshGeneration uint64
	IssuedAt          time.Time
	ExpiresAt         time.Time
	LastSeenAt        time.Time
	RevokedAt         *time.Time
}

// RefreshIdentity joins an active refresh session to its canonical subject.
// It is used only while consuming a hashed opaque refresh token; it is never
// serialized into an HTTP response or audit log.
type RefreshIdentity struct {
	Session SessionRecord
	Subject string
}

// AuthAuditRecord contains low-cardinality authentication audit metadata. It
// intentionally has no password, bearer token, refresh token, or hash field.
type AuthAuditRecord struct {
	UserID     *uint64
	TenantID   *uint64
	Realm      string
	LoginName  string
	Subject    string
	Operation  string
	Outcome    string
	ReasonCode string
	RequestID  string
	TraceID    string
}

// IdentityRepository defines the identity persistence operations
// used by the authentication service. Its implementation accepts only
// parameterized values and stores refresh-token hashes rather than tokens.
type IdentityRepository interface {
	FindLoginIdentity(context.Context, string, string, string) (LoginIdentity, error)
	FindUniqueLoginIdentity(context.Context, string, string) (LoginIdentity, error)
	FindSmartWebIdentity(context.Context, string, string, uint64, string, time.Time) (LoginIdentity, error)
	CreateSession(context.Context, SessionRecord) error
	FindActiveSession(context.Context, string, string, uint64, string, time.Time) (SessionRecord, error)
	FindRefreshIdentity(context.Context, string, time.Time) (RefreshIdentity, error)
	RotateRefreshToken(context.Context, string, string, time.Time) (bool, error)
	RevokeSession(context.Context, string, time.Time) error
	ListActiveSessions(context.Context, uint64, uint64, string, time.Time, uint) ([]SessionRecord, error)
	RevokeOwnedSessionWithAudit(context.Context, string, uint64, uint64, string, time.Time, AuthAuditRecord) (bool, error)
	RecordAudit(context.Context, AuthAuditRecord) error
}
