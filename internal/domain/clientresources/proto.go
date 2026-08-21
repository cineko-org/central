package clientresources

import (
	"fmt"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"
	commonpb "github.com/cineko-org/contracts/gen/go/cineko/common"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Kind returns the persistence identity for a typed Client resource.
func Kind(resource *clientpb.Resource) string {
	switch {
	case resource == nil:
		return ""
	case resource.GetSettings() != nil:
		return "settings"
	case resource.GetPreset() != nil:
		return "presets"
	case resource.GetMonitor() != nil:
		return "monitors"
	case resource.GetReservation() != nil:
		return "reservations"
	case resource.GetExternalOperation() != nil:
		return "external-operations"
	case resource.GetAppEvent() != nil:
		return "app-events"
	default:
		return ""
	}
}

// KindMessage returns the latest Proto discriminator for a persisted resource
// kind.
func KindMessage(kind string) (*clientpb.ResourceKind, error) {
	message := &clientpb.ResourceKind{}
	switch kind {
	case "settings":
		message.SetSettings(&clientpb.SettingsResource{})
	case "presets":
		message.SetPreset(&clientpb.PresetResource{})
	case "monitors":
		message.SetMonitor(&clientpb.MonitorResource{})
	case "reservations":
		message.SetReservation(&clientpb.ReservationResource{})
	case "external-operations":
		message.SetExternalOperation(&clientpb.ExternalOperationResource{})
	case "app-events":
		message.SetAppEvent(&clientpb.AppEventResource{})
	default:
		return nil, fmt.Errorf("unsupported Client resource kind %q", kind)
	}
	return message, nil
}

// Payload encodes only the typed resource body because resource identity has
// dedicated relational columns.
func Payload(resource *clientpb.Resource) ([]byte, error) {
	var message proto.Message
	switch Kind(resource) {
	case "settings":
		message = resource.GetSettings()
	case "presets":
		message = resource.GetPreset()
	case "monitors":
		message = resource.GetMonitor()
	case "reservations":
		message = resource.GetReservation()
	case "external-operations":
		message = resource.GetExternalOperation()
	case "app-events":
		message = resource.GetAppEvent()
	default:
		return nil, fmt.Errorf("unsupported Client resource")
	}
	return protojson.MarshalOptions{UseProtoNames: false}.Marshal(message)
}

// Decode reconstructs a typed Client resource from relational identity and a
// ProtoJSON payload.
func Decode(kind, id string, revision int64, createdAt, updatedAt time.Time, payload []byte) (*clientpb.Resource, error) {
	resource := &clientpb.Resource{}
	identity := &commonpb.ResourceIdentity{}
	identity.SetId(id)
	identity.SetRevision(revision)
	identity.SetCreatedAt(timestamppb.New(createdAt))
	identity.SetUpdatedAt(timestamppb.New(updatedAt))
	resource.SetIdentity(identity)
	options := protojson.UnmarshalOptions{DiscardUnknown: false}
	switch kind {
	case "settings":
		message := &clientpb.Settings{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		resource.SetSettings(message)
	case "presets":
		message := &clientpb.Preset{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		resource.SetPreset(message)
	case "monitors":
		message := &clientpb.Monitor{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		resource.SetMonitor(message)
	case "reservations":
		message := &clientpb.Reservation{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		resource.SetReservation(message)
	case "external-operations":
		message := &clientpb.ExternalOperation{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		resource.SetExternalOperation(message)
	case "app-events":
		message := &clientpb.AppEvent{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		resource.SetAppEvent(message)
	default:
		return nil, fmt.Errorf("unsupported Client resource kind %q", kind)
	}
	return resource, nil
}

// DecodeEventResource reconstructs the typed resource carried by a Client
// event from the event log's relational identity and ProtoJSON body.
func DecodeEventResource(kind, id string, revision int64, payload []byte) (*clientpb.EventResource, error) {
	resource, err := Decode(kind, id, revision, time.Time{}, time.Time{}, payload)
	if err != nil {
		return nil, err
	}
	eventResource := &clientpb.EventResource{}
	eventResource.SetId(id)
	eventResource.SetRevision(revision)
	switch kind {
	case "settings":
		eventResource.SetSettings(resource.GetSettings())
	case "presets":
		eventResource.SetPreset(resource.GetPreset())
	case "monitors":
		eventResource.SetMonitor(resource.GetMonitor())
	case "reservations":
		eventResource.SetReservation(resource.GetReservation())
	case "external-operations":
		eventResource.SetExternalOperation(resource.GetExternalOperation())
	case "app-events":
		eventResource.SetAppEvent(resource.GetAppEvent())
	default:
		return nil, fmt.Errorf("unsupported Client resource kind %q", kind)
	}
	return eventResource, nil
}
