package central

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cineko-org/central/internal/domain/clientresources"
)

func TestConfigurationSnapshotBoundaryCoverage(t *testing.T) {
	t.Parallel()
	principal := ClientPrincipal{UserID: "user"}

	t.Run("repository unavailable", func(t *testing.T) {
		service, err := NewClientService(&clientRepositoryFake{}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.SnapshotConfiguration(t.Context(), principal); err == nil {
			t.Fatal("SnapshotConfiguration() accepted a repository without configuration support")
		}
	})

	t.Run("repository error", func(t *testing.T) {
		service := newConfigurationBoundaryService(t, &configurationRepositoryFake{err: errInjectedClient})
		if _, err := service.SnapshotConfiguration(t.Context(), principal); !errors.Is(err, errInjectedClient) {
			t.Fatalf("SnapshotConfiguration() error = %v", err)
		}
	})

	for _, test := range []struct {
		name     string
		resource ConfigurationResource
	}{
		{
			name:     "unexpected resource kind",
			resource: ConfigurationResource{Kind: "sessions", ID: "session", Data: json.RawMessage(`{}`)},
		},
		{
			name:     "corrupt portable resource",
			resource: ConfigurationResource{Kind: "presets", ID: "preset", Data: json.RawMessage(`null`)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &configurationRepositoryFake{
				snapshot: ConfigurationSnapshot{Resources: []ConfigurationResource{test.resource}},
			}
			service := newConfigurationBoundaryService(t, repository)
			if _, err := service.SnapshotConfiguration(t.Context(), principal); !errors.Is(err, ErrCorruptResource) {
				t.Fatalf("SnapshotConfiguration() error = %v", err)
			}
		})
	}
}

func TestConfigurationReplaceBoundaryCoverage(t *testing.T) {
	t.Parallel()
	principal := ClientPrincipal{UserID: "user"}

	invalid := []struct {
		name             string
		expectedRevision int64
		commandID        string
		resources        []ConfigurationResource
	}{
		{name: "negative revision", expectedRevision: -1, commandID: "replace"},
		{name: "empty command", expectedRevision: 1, commandID: "  "},
		{
			name: "unsupported kind", expectedRevision: 1, commandID: "replace",
			resources: []ConfigurationResource{{Kind: "sessions", ID: "session", Data: json.RawMessage(`{}`)}},
		},
		{
			name: "empty id", expectedRevision: 1, commandID: "replace",
			resources: []ConfigurationResource{{Kind: "presets", ID: " ", Data: validConfigurationPreset()}},
		},
		{
			name: "invalid json", expectedRevision: 1, commandID: "replace",
			resources: []ConfigurationResource{{Kind: "presets", ID: "preset", Data: json.RawMessage(`{`)}},
		},
		{
			name: "duplicate resource", expectedRevision: 1, commandID: "replace",
			resources: []ConfigurationResource{
				{Kind: " presets ", ID: " preset ", Data: validConfigurationPreset()},
				{Kind: "presets", ID: "preset", Data: validConfigurationPreset()},
			},
		},
		{
			name: "invalid typed payload", expectedRevision: 1, commandID: "replace",
			resources: []ConfigurationResource{{Kind: "presets", ID: "preset", Data: json.RawMessage(`null`)}},
		},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			service := newConfigurationBoundaryService(t, &configurationRepositoryFake{})
			if _, err := service.ReplaceConfiguration(
				t.Context(), principal, test.expectedRevision, test.resources, test.commandID,
			); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ReplaceConfiguration() error = %v", err)
			}
		})
	}

	t.Run("repository unavailable", func(t *testing.T) {
		service, err := NewClientService(&clientRepositoryFake{}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ReplaceConfiguration(t.Context(), principal, 1, nil, "replace"); err == nil {
			t.Fatal("ReplaceConfiguration() accepted a repository without configuration support")
		}
	})

	t.Run("repository error", func(t *testing.T) {
		service := newConfigurationBoundaryService(t, &configurationRepositoryFake{err: errInjectedClient})
		if _, err := service.ReplaceConfiguration(
			t.Context(), principal, 1, nil, "replace",
		); !errors.Is(err, errInjectedClient) {
			t.Fatalf("ReplaceConfiguration() error = %v", err)
		}
	})

	t.Run("normalizes and hashes replacement", func(t *testing.T) {
		repository := &configurationRepositoryFake{revision: 9}
		service := newConfigurationBoundaryService(t, repository)
		resources := []ConfigurationResource{{Kind: " presets ", ID: " preset ", Data: validConfigurationPreset()}}
		revision, err := service.ReplaceConfiguration(t.Context(), principal, 8, resources, " replace ")
		if err != nil || revision != 9 {
			t.Fatalf("ReplaceConfiguration() = %d, %v", revision, err)
		}
		payload, err := json.Marshal(resources)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if repository.replacement.CommandID != "replace" ||
			repository.replacement.PayloadSHA256 != hex.EncodeToString(digest[:]) ||
			!repository.replacement.Now.Equal(clientTestTime) {
			t.Fatalf("configuration replacement = %+v", repository.replacement)
		}
	})
}

func TestUntypedClientResourceValidationBoundaryCoverage(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"theaters", "booking-catalogs", "auditoriums", "reservations", "unknown-kind"} {
		t.Run(kind+" normal", func(t *testing.T) {
			if err := clientresources.ValidatePayload("user", kind, "resource", json.RawMessage(`{"ok":true}`)); err != nil {
				t.Fatalf("ValidatePayload(%q) error = %v", kind, err)
			}
		})
		t.Run(kind+" invalid json", func(t *testing.T) {
			if err := clientresources.ValidatePayload("user", kind, "resource", json.RawMessage(`{`)); err == nil {
				t.Fatalf("ValidatePayload(%q) accepted invalid JSON", kind)
			}
		})
	}
}

func newConfigurationBoundaryService(t *testing.T, repository ClientRepository) *ClientService {
	t.Helper()
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	return service
}
