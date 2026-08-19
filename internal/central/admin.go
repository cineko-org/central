package central

import (
	"context"
	"time"
)

type AdminCredential struct {
	UserID       string
	DisplayName  string
	PasswordHash string
}

type AdminSession struct {
	TokenHash   [32]byte
	UserID      string
	DisplayName string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type AdminSessionRepository interface {
	BootstrapAdminCredentials(context.Context, []AdminCredential) error
	FindAdminCredential(context.Context, string) (AdminCredential, error)
	CreateAdminSession(context.Context, AdminSession) error
	AuthenticateAdminSession(context.Context, [32]byte, time.Time) (AdminSession, error)
	RevokeAdminSession(context.Context, [32]byte, time.Time) error
}
