package central

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestClientConfigurationIncludesOnlyExportablePresetsAndMonitors(t *testing.T) {
	repository := &configurationRepositoryFake{
		snapshot: ConfigurationSnapshot{
			Revision: 3,
			Resources: []ConfigurationResource{
				{Kind: "presets", ID: "preset", Data: validConfigurationPreset()},
				{Kind: "monitors", ID: "monitor", Data: validConfigurationMonitor()},
			},
		},
		revision: 4,
	}
	service, err := NewClientService(repository, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.clock = func() time.Time { return clientTestTime }
	principal := ClientPrincipal{UserID: "user"}

	snapshot, err := service.SnapshotConfiguration(t.Context(), principal)
	if err != nil || snapshot.Revision != 3 || len(snapshot.Resources) != 2 {
		t.Fatalf("SnapshotConfiguration() = %+v, %v", snapshot, err)
	}
	for _, kind := range []string{"presets", "monitors"} {
		if !slices.Contains(repository.kinds, kind) {
			t.Fatalf("snapshot omitted %q: %v", kind, repository.kinds)
		}
	}
	if slices.Contains(repository.kinds, "settings") {
		t.Fatalf("device settings must not be exported: %v", repository.kinds)
	}

	revision, err := service.ReplaceConfiguration(
		t.Context(), principal, snapshot.Revision, snapshot.Resources, "replace_configuration",
	)
	if err != nil || revision != 4 {
		t.Fatalf("ReplaceConfiguration() = %d, %v", revision, err)
	}
	if repository.replacement.UserID != "user" || repository.replacement.CommandID != "replace_configuration" ||
		len(repository.replacement.Resources) != 2 || repository.replacement.PayloadSHA256 == "" {
		t.Fatalf("configuration replacement = %+v", repository.replacement)
	}
}

func validConfigurationPreset() json.RawMessage {
	return json.RawMessage(`{"id":"preset","userId":"user","name":"Preset","theaterId":"theater","auditoriumId":"auditorium","seatCount":1,"seatPreference":{}}`)
}

func validConfigurationMonitor() json.RawMessage {
	return json.RawMessage(`{"id":"monitor","userId":"user","presetId":"preset","movieId":"movie_1","movie":"Movie","targetDates":["2026-08-12"],"pollInterval":2000000000,"pollIntervalMax":3000000000,"status":"pending"}`)
}

type configurationRepositoryFake struct {
	ClientRepository
	snapshot    ConfigurationSnapshot
	kinds       []string
	replacement ConfigurationReplacement
	revision    int64
	err         error
}

func (repository *configurationRepositoryFake) SnapshotClientConfiguration(
	_ context.Context,
	_ string,
	kinds []string,
) (ConfigurationSnapshot, error) {
	repository.kinds = append([]string(nil), kinds...)
	return repository.snapshot, repository.err
}

func (repository *configurationRepositoryFake) ReplaceClientConfiguration(
	_ context.Context,
	replacement ConfigurationReplacement,
) (int64, error) {
	repository.replacement = replacement
	return repository.revision, repository.err
}
