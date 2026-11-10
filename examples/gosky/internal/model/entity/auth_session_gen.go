package entity

import "time"

const AuthSessionTable = "auth_sessions"

const AuthSessionCacheKeyPrefix = "cache:auth_sessions"

var AuthSessionColumns = []string{"session_id", "user_id", "tenant_id", "realm", "refresh_token_hash", "refresh_generation", "issued_at", "expires_at", "last_seen_at", "revoked_at", "created_at", "updated_at"}

type AuthSession struct {
	SessionID         string     `db:"session_id" json:"sessionId"`
	UserID            int64      `db:"user_id" json:"userId"`
	TenantID          int64      `db:"tenant_id" json:"tenantId"`
	Realm             string     `db:"realm" json:"realm"`
	RefreshTokenHash  string     `db:"refresh_token_hash" json:"refreshTokenHash"`
	RefreshGeneration int64      `db:"refresh_generation" json:"refreshGeneration"`
	IssuedAt          time.Time  `db:"issued_at" json:"issuedAt"`
	ExpiresAt         time.Time  `db:"expires_at" json:"expiresAt"`
	LastSeenAt        time.Time  `db:"last_seen_at" json:"lastSeenAt"`
	RevokedAt         *time.Time `db:"revoked_at" json:"revokedAt"`
	CreatedAt         time.Time  `db:"created_at" json:"createdAt"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updatedAt"`
}

var _ Tabler = (*AuthSession)(nil)

func (AuthSession) TableName() string { return AuthSessionTable }
