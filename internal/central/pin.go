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

	adminpb "github.com/cineko-org/contracts/v3/gen/go/cineko/admin"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	ClientPINLength       = 6
	ClientPINFailureLimit = 10
	ClientPINBlockTime    = 10 * time.Minute
	pinGenerationAttempts = 8
)

type ClientPINRepository interface {
	ListClientPINUsers(context.Context) ([]*adminpb.ClientPinUser, error)
	CreateClientPINUser(context.Context, *clientpb.User, [32]byte) error
	RotateClientPIN(context.Context, string, [32]byte, time.Time) (*clientpb.User, error)
	DeleteClientPINUser(context.Context, string, time.Time) error
	ExchangeClientPIN(
		context.Context, [32]byte, []PINAttemptScope, time.Time,
	) (*clientpb.User, error)
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

func (service *PINService) ListUsers(ctx context.Context) ([]*adminpb.ClientPinUser, error) {
	return service.repository.ListClientPINUsers(ctx)
}

func (service *PINService) CreateUser(ctx context.Context, displayName string) (*adminpb.ClientPinIssue, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 100 {
		return nil, fmt.Errorf("%w: displayName is required and must not exceed 100 characters", ErrInvalid)
	}
	for range pinGenerationAttempts {
		userID, err := service.userID()
		if err != nil {
			return nil, err
		}
		pin, err := service.pin()
		if err != nil {
			return nil, err
		}
		now := service.clock().UTC()
		user := &clientpb.User{}
		user.SetId(userID)
		user.SetDisplayName(displayName)
		user.SetCreatedAt(timestamppb.New(now))
		user.SetUpdatedAt(timestamppb.New(now))
		err = service.repository.CreateClientPINUser(ctx, user, service.digest("pin", pin))
		if err == nil {
			issue := &adminpb.ClientPinIssue{}
			issue.SetUser(user)
			issue.SetPin(pin)
			return issue, nil
		}
		if !errors.Is(err, ErrConflict) {
			return nil, err
		}
	}
	return nil, errors.New("generate unique Client PIN")
}

func (service *PINService) Rotate(ctx context.Context, userID string) (*adminpb.ClientPinIssue, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: userId is required", ErrInvalid)
	}
	for range pinGenerationAttempts {
		pin, err := service.pin()
		if err != nil {
			return nil, err
		}
		user, err := service.repository.RotateClientPIN(
			ctx, userID, service.digest("pin", pin), service.clock().UTC(),
		)
		if err == nil {
			issue := &adminpb.ClientPinIssue{}
			issue.SetUser(user)
			issue.SetPin(pin)
			return issue, nil
		}
		if !errors.Is(err, ErrConflict) {
			return nil, err
		}
	}
	return nil, errors.New("generate unique Client PIN")
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
	request *clientpb.PinExchangeRequest,
	source string,
) (*clientpb.AuthenticationResponse, error) {
	pin := strings.TrimSpace(request.GetPin())
	installationID := strings.TrimSpace(request.GetInstallationId())
	deviceID := strings.TrimSpace(request.GetDeviceId())
	if !validPIN(pin) || !validPINIdentity(installationID) ||
		!validPINIdentity(deviceID) || strings.TrimSpace(source) == "" {
		return nil, ErrUnauthorized
	}
	user, err := service.repository.ExchangeClientPIN(
		ctx,
		service.digest("pin", pin),
		[]PINAttemptScope{
			{
				Hash: service.digest("source", source), FailureLimit: ClientPINFailureLimit,
				BlockTime: ClientPINBlockTime,
			},
			{
				Hash:         service.digest("device", installationID+"\x00"+deviceID),
				FailureLimit: ClientPINFailureLimit, BlockTime: ClientPINBlockTime, ResetOnSuccess: true,
			},
		},
		service.clock().UTC(),
	)
	if err != nil {
		return nil, err
	}
	response, session, err := service.clients.issueSession(user, service.clock().UTC())
	if err != nil {
		return nil, err
	}
	if err := service.clients.repository.CreateClientSession(ctx, session); err != nil {
		return nil, err
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
