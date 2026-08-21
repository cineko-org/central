package clientresources

import (
	"errors"
	"fmt"
	"time"

	"github.com/cineko-org/central/internal/domain"
)

// ValidatePayload decodes the persisted ProtoJSON and applies resource domain rules.
func ValidatePayload(userID, kind, id string, payload []byte) error {
	resource, err := Decode(kind, id, 0, time.Time{}, time.Time{}, payload)
	if err != nil {
		return fmt.Errorf("decode typed client resource: %w", err)
	}
	switch kind {
	case "settings":
		if id != "settings" {
			return errors.New("settings identity must be settings")
		}
	case "presets":
		preset := resource.GetPreset()
		if preset.GetId() != id || preset.GetUserId() != userID {
			return errors.New("preset identity does not match its owning resource")
		}
		return domain.ValidatePreset(preset, nil)
	case "monitors":
		monitor := resource.GetMonitor()
		if monitor.GetId() != id || monitor.GetUserId() != userID {
			return errors.New("monitor identity does not match its owning resource")
		}
		return domain.ValidateMonitor(monitor)
	}
	return nil
}
