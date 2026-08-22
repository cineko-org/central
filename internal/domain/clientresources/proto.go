package clientresources

import (
	"fmt"

	clientpb "github.com/cineko-org/contracts/v3/gen/go/cineko/client"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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

// DecodeEventResource reconstructs the typed resource carried by a Client
// event from the event log's immutable ProtoJSON body.
func DecodeEventResource(kind, id string, revision int64, payload []byte) (*clientpb.EventResource, error) {
	eventResource := &clientpb.EventResource{}
	eventResource.SetId(id)
	eventResource.SetRevision(revision)
	options := protojson.UnmarshalOptions{DiscardUnknown: false}
	switch kind {
	case "settings":
		message := &clientpb.Settings{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		eventResource.SetSettings(message)
	case "presets":
		message := &clientpb.Preset{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		eventResource.SetPreset(message)
	case "monitors":
		message := &clientpb.Monitor{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		eventResource.SetMonitor(message)
	case "reservations":
		message := &clientpb.Reservation{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		eventResource.SetReservation(message)
	case "external-operations":
		message := &clientpb.ExternalOperation{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		eventResource.SetExternalOperation(message)
	case "app-events":
		message := &clientpb.AppEvent{}
		if err := options.Unmarshal(payload, message); err != nil {
			return nil, err
		}
		eventResource.SetAppEvent(message)
	default:
		return nil, fmt.Errorf("unsupported Client resource kind %q", kind)
	}
	return eventResource, nil
}
