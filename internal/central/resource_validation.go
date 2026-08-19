package central

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cineko-org/central/internal/domain"
)

type ClientResourceReferences struct{}

func ValidateClientResourcePayload(
	userID string,
	kind string,
	id string,
	payload json.RawMessage,
) (ClientResourceReferences, error) {
	switch kind {
	case "settings":
		return validateSettingsResource(id, payload)
	case "presets":
		return validatePresetResource(userID, id, payload)
	case "monitors":
		return validateMonitorResource(userID, id, payload)
	default:
		return validateUntypedResource(payload)
	}
}

func validateSettingsResource(id string, payload json.RawMessage) (ClientResourceReferences, error) {
	if id != "settings" {
		return ClientResourceReferences{}, errors.New("settings identity must be settings")
	}
	var settings map[string]json.RawMessage
	if err := decodeStrictPayload(payload, &settings); err != nil {
		return ClientResourceReferences{}, err
	}
	if settings == nil {
		return ClientResourceReferences{}, errors.New("settings payload must be a JSON object")
	}
	return ClientResourceReferences{}, nil
}

func validatePresetResource(
	userID string,
	id string,
	payload json.RawMessage,
) (ClientResourceReferences, error) {
	var preset domain.Preset
	if err := decodeStrictPayload(payload, &preset); err != nil {
		return ClientResourceReferences{}, err
	}
	if preset.ID != id || !preset.Owns(userID) {
		return ClientResourceReferences{}, errors.New("preset identity does not match its owning resource")
	}
	return ClientResourceReferences{}, preset.Validate(nil)
}

func validateMonitorResource(
	userID string,
	id string,
	payload json.RawMessage,
) (ClientResourceReferences, error) {
	var monitor domain.MonitorJob
	if err := decodeStrictPayload(payload, &monitor); err != nil {
		return ClientResourceReferences{}, err
	}
	if monitor.ID != id || monitor.UserID != userID {
		return ClientResourceReferences{}, errors.New("monitor identity does not match its owning resource")
	}
	return ClientResourceReferences{}, monitor.Validate()
}

func validateUntypedResource(payload json.RawMessage) (ClientResourceReferences, error) {
	if !json.Valid(payload) {
		return ClientResourceReferences{}, errors.New("invalid JSON payload")
	}
	return ClientResourceReferences{}, nil
}

func decodeStrictPayload(payload json.RawMessage, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode typed client resource: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("client resource must contain one JSON value")
	}
	return nil
}
