package central

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	domainresources "github.com/cineko-org/central/internal/domain/resources"
)

// ConfigurationResource is one exportable Client resource in an atomic backup
// snapshot. Device settings, runtime history, and authentication state are
// intentionally absent.
type ConfigurationResource struct {
	Kind string          `json:"kind"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

type ConfigurationSnapshot struct {
	Revision  int64                   `json:"revision"`
	Resources []ConfigurationResource `json:"resources"`
}

type ConfigurationReplacement struct {
	UserID           string
	ExpectedRevision int64
	Resources        []ConfigurationResource
	CommandID        string
	PayloadSHA256    string
	Now              time.Time
}

type ConfigurationRepository interface {
	SnapshotClientConfiguration(context.Context, string, []string) (ConfigurationSnapshot, error)
	ReplaceClientConfiguration(context.Context, ConfigurationReplacement) (int64, error)
}

var portableConfigurationKinds = []string{
	"presets", "monitors",
}

// PortableConfigurationKinds returns the complete .cnk import/export set.
// Callers receive a copy so repository filters cannot mutate the service policy.
func PortableConfigurationKinds() []string {
	return append([]string(nil), portableConfigurationKinds...)
}

func (service *ClientService) SnapshotConfiguration(
	ctx context.Context,
	principal ClientPrincipal,
) (ConfigurationSnapshot, error) {
	repository, ok := service.repository.(ConfigurationRepository)
	if !ok {
		return ConfigurationSnapshot{}, fmt.Errorf("configuration repository is unavailable")
	}
	snapshot, err := repository.SnapshotClientConfiguration(ctx, principal.UserID, PortableConfigurationKinds())
	if err != nil {
		return ConfigurationSnapshot{}, err
	}
	for _, resource := range snapshot.Resources {
		if !slices.Contains(portableConfigurationKinds, resource.Kind) {
			return ConfigurationSnapshot{}, fmt.Errorf("%w: unexpected configuration resource", ErrCorruptResource)
		}
		if err := domainresources.ValidatePayload(principal.UserID, resource.Kind, resource.ID, resource.Data); err != nil {
			return ConfigurationSnapshot{}, fmt.Errorf("%w: %s/%s: %w", ErrCorruptResource, resource.Kind, resource.ID, err)
		}
	}
	return snapshot, nil
}

func (service *ClientService) ReplaceConfiguration(
	ctx context.Context,
	principal ClientPrincipal,
	expectedRevision int64,
	resources []ConfigurationResource,
	commandID string,
) (int64, error) {
	commandID = strings.TrimSpace(commandID)
	if expectedRevision < 0 || commandID == "" {
		return 0, fmt.Errorf("%w: invalid configuration precondition", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(resources))
	for index := range resources {
		resource := &resources[index]
		resource.Kind = strings.TrimSpace(resource.Kind)
		resource.ID = strings.TrimSpace(resource.ID)
		key := resource.Kind + "\x00" + resource.ID
		if !slices.Contains(portableConfigurationKinds, resource.Kind) || resource.ID == "" || !json.Valid(resource.Data) {
			return 0, fmt.Errorf("%w: invalid configuration resource", ErrInvalid)
		}
		if _, duplicate := seen[key]; duplicate {
			return 0, fmt.Errorf("%w: duplicate configuration resource", ErrInvalid)
		}
		seen[key] = struct{}{}
		if err := domainresources.ValidatePayload(principal.UserID, resource.Kind, resource.ID, resource.Data); err != nil {
			return 0, fmt.Errorf("%w: invalid %s payload: %w", ErrInvalid, resource.Kind, err)
		}
	}
	// Every RawMessage was validated above; this fixed struct slice has no
	// remaining marshal failure mode.
	payload, _ := json.Marshal(resources)
	digest := sha256.Sum256(payload)
	repository, ok := service.repository.(ConfigurationRepository)
	if !ok {
		return 0, fmt.Errorf("configuration repository is unavailable")
	}
	return repository.ReplaceClientConfiguration(ctx, ConfigurationReplacement{
		UserID: principal.UserID, ExpectedRevision: expectedRevision, Resources: resources,
		CommandID: commandID, PayloadSHA256: hex.EncodeToString(digest[:]), Now: service.clock().UTC(),
	})
}
