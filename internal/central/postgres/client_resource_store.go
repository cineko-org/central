package postgres

import (
	"context"
	"fmt"
	"time"

	clientpb "github.com/cineko-org/contracts/gen/go/cineko/client"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type clientResourceQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// loadClientResourceBody rebuilds the latest generated resource from its
// normalized relational record.
func loadClientResourceBody(
	ctx context.Context,
	queryer clientResourceQueryer,
	resource *storedClientResource,
) error {
	var (
		body *clientpb.Resource
		err  error
	)
	switch resource.kind {
	case "settings":
		body, err = loadClientSettings(ctx, queryer, resource.userID, resource.id)
	case "presets":
		body, err = loadClientPreset(ctx, queryer, resource.userID, resource.id)
	case "monitors":
		body, err = loadClientMonitor(ctx, queryer, resource.userID, resource.id)
	case "reservations":
		body, err = loadClientReservation(ctx, queryer, resource.userID, resource.id)
	case "external-operations":
		body, err = loadClientExternalOperation(ctx, queryer, resource.userID, resource.id)
	case "app-events":
		body, err = loadClientAppEvent(ctx, queryer, resource.userID, resource.id)
	default:
		err = fmt.Errorf("unsupported normalized Client resource kind %q", resource.kind)
	}
	if err != nil {
		return err
	}
	resource.body = body
	return nil
}

// writeClientResourceBody projects the latest generated resource into its
// normalized relational record without introducing a handwritten DTO.
func writeClientResourceBody(ctx context.Context, tx pgx.Tx, resource storedClientResource) error {
	if resource.body == nil {
		return fmt.Errorf("normalized %s resource body is required", resource.kind)
	}
	switch resource.kind {
	case "settings":
		return writeClientSettings(ctx, tx, resource)
	case "presets":
		return writeClientPreset(ctx, tx, resource)
	case "monitors":
		return writeClientMonitor(ctx, tx, resource)
	case "reservations":
		return writeClientReservation(ctx, tx, resource)
	case "external-operations":
		return writeClientExternalOperation(ctx, tx, resource)
	case "app-events":
		return writeClientAppEvent(ctx, tx, resource)
	default:
		return fmt.Errorf("unsupported normalized Client resource kind %q", resource.kind)
	}
}

func protoTimestamp(value *timestamppb.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	result := value.AsTime()
	return &result
}

func nullableProtoTimestamp(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}
