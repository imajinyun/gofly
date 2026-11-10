package entity

import "time"

const UserTable = "users"

const UserCacheKeyPrefix = "cache:users"

var UserColumns = []string{"user_id", "subject", "login_name", "password_hash", "account_type", "user_state", "display_name", "avatar_url", "created_at", "updated_at"}

type User struct {
	UserID       int64     `db:"user_id" json:"userId"`
	Subject      string    `db:"subject" json:"subject"`
	LoginName    string    `db:"login_name" json:"loginName"`
	PasswordHash string    `db:"password_hash" json:"passwordHash"`
	AccountType  string    `db:"account_type" json:"accountType"`
	UserState    string    `db:"user_state" json:"userState"`
	DisplayName  string    `db:"display_name" json:"displayName"`
	AvatarURL    string    `db:"avatar_url" json:"avatarUrl"`
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt    time.Time `db:"updated_at" json:"updatedAt"`
}

var _ Tabler = (*User)(nil)

func (User) TableName() string { return UserTable }
