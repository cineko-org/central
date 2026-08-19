package central

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	contracts "github.com/cineko-org/contracts/v3"
)

const (
	ClientPINLength       = 6
	ClientPINFailureLimit = 10
	ClientPINBlockTime    = 10 * time.Minute
	pinGenerationAttempts = 8
)

type ClientPINUser struct {
	User        ClientUser `json:"user"`
	PINActive   bool       `json:"pinActive"`
	DeviceCount int        `json:"deviceCount"`
}

type ClientPINIssue struct {
	User ClientUser `json:"user"`
	PIN  string     `json:"pin"`
}

type ClientPINExchangeRequest = contracts.ClientPINExchangeRequest

type ClientPINRepository interface {
	ListClientPINUsers(context.Context) ([]ClientPINUser, error)
	CreateClientPINUser(context.Context, ClientUser, [32]byte) error
	RotateClientPIN(context.Context, string, [32]byte, time.Time) (ClientUser, error)
	DeleteClientPINUser(context.Context, string, time.Time) error
	ExchangeClientPIN(
		context.Context, [32]byte, []PINAttemptScope, time.Time,
	) (ClientUser, error)
}

// PINAttemptScope defines one independently enforced brute-force boundary.
// Source scopes intentionally survive a successful login so rotating a device,
// or authenticating with another valid PIN, cannot reset the source-wide budget.
type PINAttemptScope struct {
	Hash           [32]byte
	FailureLimit   int
	BlockTime      time.Duration
	ResetOnSuccess bool
}

type PINService struct {
	repository ClientPINRepository
	clients    *ClientService
	pepper     []byte
	clock      func() time.Time
	random     func([]byte) (int, error)
}

func NewPINService(
	repository ClientPINRepository,
	clients *ClientService,
	pepper string,
) (*PINService, error) {
	if repository == nil || clients == nil || len(strings.TrimSpace(pepper)) < 32 {
		return nil, errors.New("PIN repository, Client service, and a pepper of at least 32 characters are required")
	}
	return &PINService{
		repository: repository, clients: clients, pepper: []byte(pepper),
		clock: time.Now, random: rand.Read,
	}, nil
}

func (service *PINService) ListUsers(ctx context.Context) ([]ClientPINUser, error) {
	return service.repository.ListClientPINUsers(ctx)
}

func (service *PINService) CreateUser(ctx context.Context, displayName string) (ClientPINIssue, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 100 {
		return ClientPINIssue{}, fmt.Errorf("%w: displayName is required and must not exceed 100 characters", ErrInvalid)
	}
	for range pinGenerationAttempts {
		userID, err := service.userID()
		if err != nil {
			return ClientPINIssue{}, err
		}
		pin, err := service.pin()
		if err != nil {
			return ClientPINIssue{}, err
		}
		now := service.clock().UTC()
		user := ClientUser{ID: userID, DisplayName: displayName, CreatedAt: now, UpdatedAt: now}
		err = service.repository.CreateClientPINUser(ctx, user, service.digest("pin", pin))
		if err == nil {
			return ClientPINIssue{User: user, PIN: pin}, nil
		}
		if !errors.Is(err, ErrConflict) {
			return ClientPINIssue{}, err
		}
	}
	return ClientPINIssue{}, errors.New("generate unique Client PIN")
}

func (service *PINService) Rotate(ctx context.Context, userID string) (ClientPINIssue, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ClientPINIssue{}, fmt.Errorf("%w: userId is required", ErrInvalid)
	}
	for range pinGenerationAttempts {
		pin, err := service.pin()
		if err != nil {
			return ClientPINIssue{}, err
		}
		user, err := service.repository.RotateClientPIN(
			ctx, userID, service.digest("pin", pin), service.clock().UTC(),
		)
		if err == nil {
			return ClientPINIssue{User: user, PIN: pin}, nil
		}
		if !errors.Is(err, ErrConflict) {
			return ClientPINIssue{}, err
		}
	}
	return ClientPINIssue{}, errors.New("generate unique Client PIN")
}

func (service *PINService) DeleteUser(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("%w: userId is required", ErrInvalid)
	}
	return service.repository.DeleteClientPINUser(ctx, userID, service.clock().UTC())
}

func (service *PINService) Exchange(
	ctx context.Context,
	request ClientPINExchangeRequest,
	source string,
) (AuthExchangeResponse, error) {
	request.PIN = strings.TrimSpace(request.PIN)
	request.InstallationID = strings.TrimSpace(request.InstallationID)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	if !validPIN(request.PIN) || !validPINIdentity(request.InstallationID) ||
		!validPINIdentity(request.DeviceID) || strings.TrimSpace(source) == "" {
		return AuthExchangeResponse{}, ErrUnauthorized
	}
	user, err := service.repository.ExchangeClientPIN(
		ctx,
		service.digest("pin", request.PIN),
		[]PINAttemptScope{
			{
				Hash: service.digest("source", source), FailureLimit: ClientPINFailureLimit,
				BlockTime: ClientPINBlockTime,
			},
			{
				Hash:         service.digest("device", request.InstallationID+"\x00"+request.DeviceID),
				FailureLimit: ClientPINFailureLimit, BlockTime: ClientPINBlockTime, ResetOnSuccess: true,
			},
		},
		service.clock().UTC(),
	)
	if err != nil {
		return AuthExchangeResponse{}, err
	}
	response, session, err := service.clients.issueSession(user, service.clock().UTC())
	if err != nil {
		return AuthExchangeResponse{}, err
	}
	if err := service.clients.repository.CreateClientSession(ctx, session); err != nil {
		return AuthExchangeResponse{}, err
	}
	return response, nil
}

func (service *PINService) pin() (string, error) {
	const sampleLimit = (uint64(1) << 32) / 1_000_000 * 1_000_000
	buffer := make([]byte, 4)
	for {
		count, err := service.random(buffer)
		if err != nil || count != len(buffer) {
			if err == nil {
				err = errors.New("generate complete PIN sample")
			}
			return "", err
		}
		value := uint64(binary.BigEndian.Uint32(buffer))
		if value < sampleLimit {
			return fmt.Sprintf("%06d", value%1_000_000), nil
		}
	}
}

func (service *PINService) userID() (string, error) {
	buffer := make([]byte, 16)
	count, err := service.random(buffer)
	if err != nil || count != len(buffer) {
		if err == nil {
			err = errors.New("generate complete Client user ID")
		}
		return "", err
	}
	return "user_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (service *PINService) digest(kind string, value string) [32]byte {
	mac := hmac.New(sha256.New, service.pepper)
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

func validPIN(value string) bool {
	if len(value) != ClientPINLength {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validPINIdentity(value string) bool {
	return len(value) >= 16 && len(value) <= 200
}
