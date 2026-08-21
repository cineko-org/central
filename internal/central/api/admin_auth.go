package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/cineko-org/central/internal/central"
	adminpb "github.com/cineko-org/contracts/gen/go/cineko/admin"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	adminSessionCookie = "cineko_admin_session"
	defaultAdminTTL    = 12 * time.Hour
)

type AdminCredential struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

type AdminAuth struct {
	bootstrap      []central.AdminCredential
	repository     central.AdminSessionRepository
	ttl            time.Duration
	clock          func() time.Time
	random         func([]byte) (int, error)
	passwords      *passwordHasher
	verifyPassword func(string, string) (bool, error)
	dummyHash      string
	loginLimiter   *adminLoginLimiter
}

func NewAdminAuth(
	credentials []AdminCredential,
	passwordPepper string,
	repository central.AdminSessionRepository,
	ttl time.Duration,
) (*AdminAuth, error) {
	if repository == nil {
		return nil, errors.New("admin credential repository is required")
	}
	if ttl == 0 {
		ttl = defaultAdminTTL
	}
	if ttl <= 0 {
		return nil, errors.New("admin session TTL must be positive")
	}
	passwords, err := newPasswordHasher(passwordPepper)
	if err != nil {
		return nil, err
	}
	dummyHash, err := passwords.Hash("invalid-admin-password")
	if err != nil {
		return nil, err
	}
	auth := &AdminAuth{
		bootstrap:  make([]central.AdminCredential, 0, len(credentials)),
		repository: repository, ttl: ttl, clock: time.Now, random: rand.Read, passwords: passwords,
		verifyPassword: passwords.Verify, dummyHash: dummyHash,
		loginLimiter: newAdminLoginLimiter(defaultAdminLoginConcurrency),
	}
	seen := make(map[string]struct{}, len(credentials))
	for _, value := range credentials {
		userID := strings.TrimSpace(value.UserID)
		displayName := strings.TrimSpace(value.DisplayName)
		password := value.Password
		if userID == "" || displayName == "" || len(password) < 8 {
			return nil, errors.New("admin credentials require userId, displayName, and a password of at least 8 characters")
		}
		if _, exists := seen[userID]; exists {
			return nil, errors.New("admin credential user IDs must be unique")
		}
		seen[userID] = struct{}{}
		passwordHash, err := passwords.Hash(password)
		if err != nil {
			return nil, err
		}
		auth.bootstrap = append(auth.bootstrap, central.AdminCredential{
			UserID: userID, DisplayName: displayName, PasswordHash: passwordHash,
		})
	}
	return auth, nil
}

func (auth *AdminAuth) Bootstrap(ctx context.Context) error {
	return auth.repository.BootstrapAdminCredentials(ctx, auth.bootstrap)
}

func (auth *AdminAuth) Login(
	ctx context.Context,
	source string,
	userID string,
	password string,
) (string, *adminpb.Principal, error) {
	userID = strings.TrimSpace(userID)
	now := auth.clock().UTC()
	release, err := auth.loginLimiter.acquire(source, userID, now)
	if err != nil {
		return "", nil, err
	}
	defer release()
	credential, err := auth.repository.FindAdminCredential(ctx, userID)
	if err != nil {
		if errors.Is(err, central.ErrUnauthorized) {
			_, _ = auth.verifyPassword(password, auth.dummyHash)
			if auth.loginLimiter.recordFailure(source, userID, false, now) {
				return "", nil, central.ErrRateLimited
			}
			return "", nil, central.ErrUnauthorized
		}
		return "", nil, err
	}
	verified, err := auth.verifyPassword(password, credential.PasswordHash)
	if err != nil || !verified {
		if auth.loginLimiter.recordFailure(source, userID, true, now) {
			return "", nil, central.ErrRateLimited
		}
		return "", nil, central.ErrUnauthorized
	}
	if credential.UserID == "" {
		return "", nil, central.ErrUnauthorized
	}
	expiresAt := now.Add(auth.ttl)
	principal := &adminpb.Principal{}
	principal.SetUserId(credential.UserID)
	principal.SetDisplayName(credential.DisplayName)
	principal.SetExpiresAt(timestamppb.New(expiresAt))
	tokenBytes := make([]byte, 32)
	if count, err := auth.random(tokenBytes); err != nil || count != len(tokenBytes) {
		if err == nil {
			err = errors.New("generate complete admin session token")
		}
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if err := auth.repository.CreateAdminSession(ctx, central.AdminSession{
		TokenHash: sha256.Sum256([]byte(token)), UserID: principal.GetUserId(),
		DisplayName: principal.GetDisplayName(), ExpiresAt: expiresAt, CreatedAt: now,
	}); err != nil {
		return "", nil, err
	}
	return token, principal, nil
}

func (auth *AdminAuth) Verify(ctx context.Context, token string) (*adminpb.Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, central.ErrUnauthorized
	}
	session, err := auth.repository.AuthenticateAdminSession(
		ctx, sha256.Sum256([]byte(token)), auth.clock().UTC(),
	)
	if err != nil {
		if errors.Is(err, central.ErrUnauthorized) {
			return nil, central.ErrUnauthorized
		}
		return nil, err
	}
	principal := &adminpb.Principal{}
	principal.SetUserId(session.UserID)
	principal.SetDisplayName(session.DisplayName)
	principal.SetExpiresAt(timestamppb.New(session.ExpiresAt))
	return principal, nil
}

func (auth *AdminAuth) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return auth.repository.RevokeAdminSession(ctx, sha256.Sum256([]byte(token)), auth.clock().UTC())
}
