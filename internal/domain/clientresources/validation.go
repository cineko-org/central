package clientresources

import (
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	"github.com/cineko-org/central/internal/domain"
	catalogpb "github.com/cineko-org/contracts/v3/gen/go/cineko/catalog"
	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Validate applies resource-domain rules directly to the generated contract.
func Validate(userID, kind, id string, resource *clientpb.Resource) error {
	if resource == nil || Kind(resource) != kind {
		return errors.New("resource kind does not match its generated contract")
	}
	if err := validateGeneratedContract(resource); err != nil {
		return err
	}
	switch kind {
	case "settings":
		return validateSettingsResource(id, resource.GetSettings())
	case "presets":
		return validatePresetResource(userID, id, resource.GetPreset())
	case "monitors":
		return validateMonitorResource(userID, id, resource.GetMonitor())
	case "reservations":
		return validateReservationResource(userID, id, resource.GetReservation())
	case "external-operations":
		return validateExternalOperationResource(userID, id, resource.GetExternalOperation())
	case "app-events":
		return validateAppEventResource(userID, id, resource.GetAppEvent())
	}
	return nil
}

func validateGeneratedContract(resource *clientpb.Resource) error {
	if _, err := (proto.MarshalOptions{Deterministic: true}).Marshal(resource); err != nil {
		return fmt.Errorf("resource is not a valid generated protobuf message: %w", err)
	}
	if err := protovalidate.Validate(resource); err != nil {
		return fmt.Errorf("resource violates the generated protobuf contract: %w", err)
	}
	return nil
}

func validateSettingsResource(id string, settings *clientpb.Settings) error {
	if id != "settings" {
		return errors.New("settings identity must be settings")
	}
	return validateSettings(settings)
}

func validatePresetResource(userID, id string, preset *clientpb.Preset) error {
	if preset.GetId() != id || preset.GetUserId() != userID {
		return errors.New("preset identity does not match its owning resource")
	}
	if err := domain.ValidatePreset(preset); err != nil {
		return err
	}
	return validateOptionalTimestamps(
		newTimestampField("preset created_at", preset.GetCreatedAt()),
		newTimestampField("preset updated_at", preset.GetUpdatedAt()),
	)
}

func validateMonitorResource(userID, id string, monitor *clientpb.Monitor) error {
	if monitor.GetId() != id || monitor.GetUserId() != userID {
		return errors.New("monitor identity does not match its owning resource")
	}
	if err := domain.ValidateMonitor(monitor); err != nil {
		return err
	}
	return validateOptionalTimestamps(
		newTimestampField("monitor last_checked_at", monitor.GetLastCheckedAt()),
		newTimestampField("monitor created_at", monitor.GetCreatedAt()),
		newTimestampField("monitor updated_at", monitor.GetUpdatedAt()),
	)
}

func validateReservationResource(userID, id string, reservation *clientpb.Reservation) error {
	if reservation.GetId() != id || reservation.GetUserId() != userID {
		return errors.New("reservation identity does not match its owning resource")
	}
	if reservation.GetMonitorId() == "" || !reservation.HasState() || reservation.GetShowtime() == nil ||
		reservation.GetShowtime().GetId() == "" || reservation.GetShowtime().GetMovie() == nil ||
		reservation.GetShowtime().GetAuditorium() == nil {
		return errors.New("reservation monitor, state, and showtime are required")
	}
	for _, seatLabel := range reservation.GetSeatLabels() {
		if seatLabel == "" {
			return errors.New("reservation seat labels must not be empty")
		}
	}
	if err := validateReservationShowtime(reservation.GetShowtime()); err != nil {
		return err
	}
	return validateOptionalTimestamps(
		newTimestampField("reservation booked_at", reservation.GetBookedAt()),
		newTimestampField("reservation cancelled_at", reservation.GetCancelledAt()),
	)
}

func validateExternalOperationResource(userID, id string, operation *clientpb.ExternalOperation) error {
	if operation.GetId() != id || operation.GetUserId() != userID {
		return errors.New("external operation identity does not match its owning resource")
	}
	if operation.GetReservationId() == "" || !operation.HasKind() || !operation.HasState() {
		return errors.New("external operation reservation, kind, and state are required")
	}
	return validateOptionalTimestamps(
		newTimestampField("external operation created_at", operation.GetCreatedAt()),
		newTimestampField("external operation updated_at", operation.GetUpdatedAt()),
	)
}

func validateAppEventResource(userID, id string, event *clientpb.AppEvent) error {
	if event.GetId() != id || event.GetUserId() != userID {
		return errors.New("app event identity does not match its owning resource")
	}
	if event.GetKind() == "" || !event.HasTone() {
		return errors.New("app event kind and tone are required")
	}
	return validateOptionalTimestamps(
		newTimestampField("app event created_at", event.GetCreatedAt()),
		newTimestampField("app event read_at", event.GetReadAt()),
	)
}

func validateSettings(settings *clientpb.Settings) error {
	if settings == nil {
		return errors.New("settings are required")
	}
	if proxy := settings.GetNetwork().GetProxy(); proxy != nil {
		if !proxy.GetHasPassword() && proxy.GetPassword() != "" {
			return errors.New("proxy password requires has_password")
		}
		for _, url := range proxy.GetUrls() {
			if url == "" {
				return errors.New("proxy URLs must not be empty")
			}
		}
	}
	for _, webhook := range settings.GetWebhooks() {
		if webhook == nil {
			return errors.New("webhook targets must not be nil")
		}
		for _, eventKind := range webhook.GetEventKinds() {
			if eventKind == "" {
				return errors.New("webhook event kinds must not be empty")
			}
		}
	}
	return nil
}

type timestampField struct {
	name  string
	value *timestamppb.Timestamp
}

func newTimestampField(name string, value *timestamppb.Timestamp) timestampField {
	return timestampField{name: name, value: value}
}

func validateOptionalTimestamps(fields ...timestampField) error {
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		if err := field.value.CheckValid(); err != nil {
			return fmt.Errorf("%s is invalid: %w", field.name, err)
		}
	}
	return nil
}

func validateReservationShowtime(showtime *catalogpb.Showtime) error {
	if showtime.GetMovie().GetId() == "" || showtime.GetAuditorium().GetId() == "" {
		return errors.New("reservation showtime movie and auditorium identities are required")
	}
	startsAt, endsAt := showtime.GetStartsAt(), showtime.GetEndsAt()
	if startsAt == nil || endsAt == nil {
		return errors.New("reservation showtime start and end are required")
	}
	if err := validateOptionalTimestamps(
		newTimestampField("reservation showtime starts_at", startsAt),
		newTimestampField("reservation showtime ends_at", endsAt),
	); err != nil {
		return err
	}
	if !endsAt.AsTime().After(startsAt.AsTime()) {
		return errors.New("reservation showtime must end after it starts")
	}
	if showtime.GetAvailableSeats() < 0 || showtime.GetCapacity() < 0 ||
		showtime.GetAvailableSeats() > showtime.GetCapacity() || showtime.GetAuditorium().GetCapacity() < 0 {
		return errors.New("reservation showtime availability and capacity are invalid")
	}
	for _, screenType := range showtime.GetAuditorium().GetScreenTypes() {
		if screenType == "" {
			return errors.New("reservation auditorium screen types must not be empty")
		}
	}
	return nil
}
