package clientresources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cineko-org/central/internal/domain"
)

// ValidatePayload verifies the identity, ownership, shape, and domain rules
// for one portable resource received from a client.
func ValidatePayload(userID, kind, id string, payload json.RawMessage) error {
	switch kind {
	case "settings":
		return validateSettings(id, payload)
	case "presets":
		return validatePreset(userID, id, payload)
	case "monitors":
		return validateMonitor(userID, id, payload)
	default:
		return validateUntyped(payload)
	}
}

func validateSettings(id string, payload json.RawMessage) error {
	if id != "settings" {
		return errors.New("settings identity must be settings")
	}
	var settings map[string]json.RawMessage
	if err := decodeStrict(payload, &settings); err != nil {
		return err
	}
	if settings == nil {
		return errors.New("settings payload must be a JSON object")
	}
	return nil
}

func validatePreset(userID, id string, payload json.RawMessage) error {
	var preset domain.Preset
	if err := decodeStrict(payload, &preset); err != nil {
		return err
	}
	if preset.ID != id || !preset.Owns(userID) {
		return errors.New("preset identity does not match its owning resource")
	}
	return preset.Validate(nil)
}

func validateMonitor(userID, id string, payload json.RawMessage) error {
	var monitor domain.MonitorJob
	if err := decodeStrict(payload, &monitor); err != nil {
		return err
	}
	if monitor.ID != id || monitor.UserID != userID {
		return errors.New("monitor identity does not match its owning resource")
	}
	return monitor.Validate()
}

func validateUntyped(payload json.RawMessage) error {
	if !json.Valid(payload) {
		return errors.New("invalid JSON payload")
	}
	return nil
}

func decodeStrict(payload json.RawMessage, output any) error {
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
