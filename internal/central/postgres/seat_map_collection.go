package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/cineko-org/central/internal/central"
	catalogdomain "github.com/cineko-org/central/internal/domain/catalog"
	collectionpb "github.com/cineko-org/contracts/v3/gen/go/cineko/collection"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const seatMapCollectionNotifyChannel = "cineko_seat_map_collection"

var (
	errSeatMapCollectionStateNotFound     = errors.New("seat-map collection state not found")
	errSeatMapCollectionInvalidTrigger    = errors.New("invalid seat-map collection trigger")
	errSeatMapCollectionInvalidTransition = errors.New("invalid seat-map collection transition")
)

// PostgreSQL stores the generated collection oneofs as constrained text at the
// persistence boundary. These values are an adapter representation, not a
// second Central or wire contract.
const (
	seatMapStateQueued          = "queued"
	seatMapStateCollecting      = "collecting"
	seatMapStateWaitingShowtime = "waiting_for_showtime"
	seatMapStateRetryScheduled  = "retry_scheduled"
	seatMapStateBlocked         = "blocked"

	seatMapTriggerClientRequest   = "client_request"
	seatMapTriggerActiveMonitor   = "active_monitor"
	seatMapTriggerLayoutMissing   = "layout_missing"
	seatMapTriggerLayoutChanged   = "layout_changed"
	seatMapTriggerCatalogRefresh  = "catalog_refresh"
	seatMapTriggerOperatorRequest = "operator_request"
)

func seatMapCollectionTriggerKind(trigger *collectionpb.Trigger) (string, bool) {
	if trigger == nil {
		return "", false
	}
	switch {
	case trigger.GetClientRequest() != nil:
		return seatMapTriggerClientRequest, true
	case trigger.GetActiveMonitor() != nil:
		return seatMapTriggerActiveMonitor, true
	case trigger.GetLayoutMissing() != nil:
		return seatMapTriggerLayoutMissing, true
	case trigger.GetLayoutChanged() != nil:
		return seatMapTriggerLayoutChanged, true
	case trigger.GetCatalogRefresh() != nil:
		return seatMapTriggerCatalogRefresh, true
	case trigger.GetOperatorRequest() != nil:
		return seatMapTriggerOperatorRequest, true
	default:
		return "", false
	}
}

func seatMapCollectionTriggerFromSQL(value string) (*collectionpb.Trigger, bool) {
	trigger := &collectionpb.Trigger{}
	switch value {
	case seatMapTriggerClientRequest:
		trigger.SetClientRequest(&collectionpb.ClientRequest{})
	case seatMapTriggerActiveMonitor:
		trigger.SetActiveMonitor(&collectionpb.ActiveMonitor{})
	case seatMapTriggerLayoutMissing:
		trigger.SetLayoutMissing(&collectionpb.LayoutMissing{})
	case seatMapTriggerLayoutChanged:
		trigger.SetLayoutChanged(&collectionpb.LayoutChanged{})
	case seatMapTriggerCatalogRefresh:
		trigger.SetCatalogRefresh(&collectionpb.CatalogRefresh{})
	case seatMapTriggerOperatorRequest:
		trigger.SetOperatorRequest(&collectionpb.OperatorRequest{})
	default:
		return nil, false
	}
	return trigger, true
}

func seatMapCollectionCanReopenBlocked(trigger *collectionpb.Trigger) bool {
	if trigger == nil {
		return false
	}
	return trigger.GetLayoutChanged() != nil ||
		trigger.GetCatalogRefresh() != nil ||
		trigger.GetOperatorRequest() != nil
}

func seatMapCollectionStateKind(state *collectionpb.State) (string, bool) {
	if state == nil || state.GetIdle() != nil {
		return "", true
	}
	switch {
	case state.GetQueued() != nil:
		return seatMapStateQueued, true
	case state.GetCollecting() != nil:
		return seatMapStateCollecting, true
	case state.GetWaitingForShowtime() != nil:
		return seatMapStateWaitingShowtime, true
	case state.GetRetryScheduled() != nil:
		return seatMapStateRetryScheduled, true
	case state.GetBlocked() != nil:
		return seatMapStateBlocked, true
	default:
		return "", false
	}
}

func seatMapCollectionStateFromSQL(value string) (*collectionpb.State, bool) {
	state := &collectionpb.State{}
	switch value {
	case "":
		return nil, true
	case seatMapStateQueued:
		state.SetQueued(&collectionpb.Queued{})
	case seatMapStateCollecting:
		state.SetCollecting(&collectionpb.Collecting{})
	case seatMapStateWaitingShowtime:
		state.SetWaitingForShowtime(&collectionpb.WaitingForShowtime{})
	case seatMapStateRetryScheduled:
		state.SetRetryScheduled(&collectionpb.RetryScheduled{})
	case seatMapStateBlocked:
		state.SetBlocked(&collectionpb.Blocked{})
	default:
		return nil, false
	}
	return state, true
}

func seatMapCollectionWaitingReasonCode(reason *collectionpb.WaitingReason) (string, bool) {
	if reason == nil {
		return "", false
	}
	switch {
	case reason.GetShowtimeNotDiscovered() != nil:
		return "showtime_not_discovered", true
	case reason.GetNoBookableShowtime() != nil:
		return "no_bookable_showtime", true
	case reason.GetTargetDateUnavailable() != nil:
		return "target_date_unavailable", true
	default:
		return "", false
	}
}

func seatMapCollectionFailureReasonCode(reason *collectionpb.FailureReason) (string, bool) {
	if reason == nil {
		return "", false
	}
	switch {
	case reason.GetIdentityMismatch() != nil:
		return "identity_mismatch", true
	case reason.GetProviderBlocked() != nil:
		return "provider_blocked", true
	case reason.GetProviderThrottled() != nil:
		return "provider_throttled", true
	case reason.GetCaptchaRequired() != nil:
		return "captcha_required", true
	case reason.GetAuthenticationRequired() != nil:
		return "authentication_required", true
	case reason.GetUiContractChanged() != nil:
		return "ui_contract_changed", true
	case reason.GetBrowserStartFailed() != nil:
		return "browser_start_failed", true
	case reason.GetProviderTransportFailed() != nil:
		return "provider_transport_failed", true
	case reason.GetProviderServerError() != nil:
		return "provider_server_error", true
	case reason.GetInvalidResult() != nil:
		return "invalid_result", true
	case reason.GetTimeout() != nil:
		return "timeout", true
	default:
		return "", false
	}
}

func seatMapCollectionFailureReasonFromSQL(code string) *collectionpb.FailureReason {
	reason := &collectionpb.FailureReason{}
	switch code {
	case "identity_mismatch":
		reason.SetIdentityMismatch(&collectionpb.IdentityMismatch{})
	case "provider_blocked":
		reason.SetProviderBlocked(&collectionpb.ProviderBlocked{})
	case "provider_throttled":
		reason.SetProviderThrottled(&collectionpb.ProviderThrottled{})
	case "captcha_required":
		reason.SetCaptchaRequired(&collectionpb.CaptchaRequired{})
	case "authentication_required":
		reason.SetAuthenticationRequired(&collectionpb.AuthenticationRequired{})
	case "ui_contract_changed":
		reason.SetUiContractChanged(&collectionpb.UIContractChanged{})
	case "browser_start_failed":
		reason.SetBrowserStartFailed(&collectionpb.BrowserStartFailed{})
	case "provider_transport_failed":
		reason.SetProviderTransportFailed(&collectionpb.ProviderTransportFailed{})
	case "provider_server_error":
		reason.SetProviderServerError(&collectionpb.ProviderServerError{})
	case "invalid_result":
		reason.SetInvalidResult(&collectionpb.InvalidResult{})
	case "timeout":
		reason.SetTimeout(&collectionpb.Timeout{})
	default:
		return nil
	}
	return reason
}

func seatMapCollectionWaitingReasonFromSQL(code string) *collectionpb.WaitingReason {
	reason := &collectionpb.WaitingReason{}
	switch code {
	case "showtime_not_discovered":
		reason.SetShowtimeNotDiscovered(&collectionpb.ShowtimeNotDiscovered{})
	case "no_bookable_showtime":
		reason.SetNoBookableShowtime(&collectionpb.NoBookableShowtime{})
	case "target_date_unavailable":
		reason.SetTargetDateUnavailable(&collectionpb.TargetDateUnavailable{})
	default:
		return nil
	}
	return reason
}

func showtimeNotDiscoveredReason() *collectionpb.WaitingReason {
	reason := &collectionpb.WaitingReason{}
	reason.SetShowtimeNotDiscovered(&collectionpb.ShowtimeNotDiscovered{})
	return reason
}

func queuedSeatMapCollectionState(trigger *collectionpb.Trigger, queuedAt time.Time) *collectionpb.State {
	return (&collectionpb.State_builder{
		Queued: (&collectionpb.Queued_builder{
			QueuedAt: timestamppb.New(queuedAt),
			Trigger:  trigger,
		}).Build(),
	}).Build()
}

func waitingSeatMapCollectionState(reason *collectionpb.WaitingReason) *collectionpb.State {
	return (&collectionpb.State_builder{
		WaitingForShowtime: (&collectionpb.WaitingForShowtime_builder{Reason: reason}).Build(),
	}).Build()
}

var seatMapCollectionTransitions = map[string]map[string]struct{}{
	"": {
		seatMapStateQueued: {}, seatMapStateWaitingShowtime: {},
	},
	seatMapStateQueued: {
		seatMapStateCollecting: {}, seatMapStateWaitingShowtime: {},
		seatMapStateRetryScheduled: {}, seatMapStateBlocked: {},
	},
	seatMapStateCollecting: {
		seatMapStateRetryScheduled: {}, seatMapStateWaitingShowtime: {}, seatMapStateBlocked: {},
	},
	seatMapStateWaitingShowtime: {
		seatMapStateQueued: {}, seatMapStateBlocked: {},
	},
	seatMapStateRetryScheduled: {
		seatMapStateQueued: {}, seatMapStateCollecting: {},
		seatMapStateWaitingShowtime: {}, seatMapStateBlocked: {},
	},
	seatMapStateBlocked: {
		seatMapStateQueued: {}, seatMapStateWaitingShowtime: {},
	},
}

// validSeatMapCollectionTransition validates generated lifecycle messages
// before the adapter writes their constrained PostgreSQL representation.
func validSeatMapCollectionTransition(from, to *collectionpb.State, trigger *collectionpb.Trigger) bool {
	fromKind, fromOK := seatMapCollectionStateKind(from)
	toKind, toOK := seatMapCollectionStateKind(to)
	if !fromOK || !toOK {
		return false
	}
	if _, triggerOK := seatMapCollectionTriggerKind(trigger); !triggerOK {
		return false
	}
	if fromKind == toKind {
		return true
	}
	allowedTargets, knownState := seatMapCollectionTransitions[fromKind]
	if !knownState {
		return false
	}
	if _, allowed := allowedTargets[toKind]; !allowed {
		return false
	}
	return fromKind != seatMapStateBlocked || seatMapCollectionCanReopenBlocked(trigger)
}

// queueSeatMapCollectionStateTx records one durable request and wakes the
// reconciler after the state mutation commits. A missing exact showtime keeps
// the row waiting instead of issuing a blind browser task.
func queueSeatMapCollectionStateTx(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	trigger *collectionpb.Trigger,
	priority int16,
	requestedAt time.Time,
	exactShowtimeID string,
) error {
	triggerKind, validTrigger := seatMapCollectionTriggerKind(trigger)
	if !validTrigger {
		return errSeatMapCollectionInvalidTrigger
	}
	exactShowtimeID, err := resolveSeatMapCollectionShowtimeTx(
		ctx, tx, auditoriumID, exactShowtimeID, requestedAt,
	)
	if err != nil {
		return err
	}

	force := seatMapCollectionCanReopenBlocked(trigger)
	requestedState := requestedSeatMapCollectionState(trigger, requestedAt, exactShowtimeID)
	requestedStateKind, _ := seatMapCollectionStateKind(requestedState)
	currentState, currentStateValue, err := loadOptionalSeatMapCollectionStateForUpdate(ctx, tx, auditoriumID)
	if err != nil {
		return err
	}
	transitionState := requestedState
	if currentStateValue == seatMapStateCollecting || (currentStateValue == seatMapStateBlocked && !force) {
		transitionState = currentState
	}
	if !validSeatMapCollectionTransition(currentState, transitionState, trigger) {
		return errSeatMapCollectionInvalidTransition
	}
	transitionStateKind, _ := seatMapCollectionStateKind(transitionState)
	reason := ""
	if transitionStateKind == seatMapStateWaitingShowtime {
		var reasonOK bool
		reason, reasonOK = seatMapCollectionWaitingReasonCode(showtimeNotDiscoveredReason())
		if !reasonOK {
			return errSeatMapCollectionInvalidTransition
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO seat_map_collection_states (
			auditorium_id, state, trigger_kind, priority, showtime_id, reason_code,
			requested_at, next_attempt_at, updated_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, NULL, $7)
		ON CONFLICT (auditorium_id) DO UPDATE SET
			state = CASE
				WHEN seat_map_collection_states.state = 'collecting' THEN seat_map_collection_states.state
				WHEN seat_map_collection_states.state = 'blocked' AND NOT $8 THEN seat_map_collection_states.state
				WHEN $8 THEN EXCLUDED.state
				ELSE EXCLUDED.state
			END,
			trigger_kind = CASE
				WHEN seat_map_collection_states.state = 'blocked' AND NOT $8 THEN seat_map_collection_states.trigger_kind
				WHEN $8 OR EXCLUDED.priority > seat_map_collection_states.priority THEN EXCLUDED.trigger_kind
				ELSE seat_map_collection_states.trigger_kind
			END,
			priority = GREATEST(seat_map_collection_states.priority, EXCLUDED.priority),
			showtime_id = CASE
				WHEN seat_map_collection_states.state = 'collecting' THEN seat_map_collection_states.showtime_id
				WHEN seat_map_collection_states.state = 'blocked' AND NOT $8 THEN seat_map_collection_states.showtime_id
				ELSE EXCLUDED.showtime_id
			END,
			assignment_id = CASE
				WHEN seat_map_collection_states.state = 'collecting' THEN seat_map_collection_states.assignment_id
				ELSE NULL
			END,
			reason_code = CASE
				WHEN seat_map_collection_states.state = 'collecting' THEN seat_map_collection_states.reason_code
				WHEN seat_map_collection_states.state = 'blocked' AND NOT $8 THEN seat_map_collection_states.reason_code
				ELSE EXCLUDED.reason_code
			END,
			requested_at = LEAST(seat_map_collection_states.requested_at, EXCLUDED.requested_at),
			next_attempt_at = CASE
				WHEN seat_map_collection_states.state = 'collecting' THEN seat_map_collection_states.next_attempt_at
				ELSE EXCLUDED.next_attempt_at
			END,
			consecutive_failures = CASE
				WHEN $8 AND seat_map_collection_states.state <> 'collecting' THEN 0
				ELSE seat_map_collection_states.consecutive_failures
			END,
			updated_at = EXCLUDED.updated_at
	`, auditoriumID, requestedStateKind, triggerKind, priority, exactShowtimeID, reason, requestedAt, force); err != nil {
		return err
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

func resolveSeatMapCollectionShowtimeTx(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	exactShowtimeID string,
	requestedAt time.Time,
) (string, error) {
	if exactShowtimeID != "" {
		var verifiedShowtimeID string
		err := tx.QueryRow(ctx, `
			SELECT id FROM showtimes
			WHERE id = $1 AND auditorium_id = $2 AND active AND starts_at > $3
		`, exactShowtimeID, auditoriumID, requestedAt).Scan(&verifiedShowtimeID)
		if err == nil {
			return verifiedShowtimeID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	var showtimeID string
	err := tx.QueryRow(ctx, `
		SELECT id FROM showtimes
		WHERE auditorium_id = $1 AND active AND starts_at > $2
		ORDER BY starts_at, id
		LIMIT 1
	`, auditoriumID, requestedAt).Scan(&showtimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return showtimeID, err
}

func requestedSeatMapCollectionState(
	trigger *collectionpb.Trigger,
	requestedAt time.Time,
	showtimeID string,
) *collectionpb.State {
	if showtimeID != "" {
		return queuedSeatMapCollectionState(trigger, requestedAt)
	}
	return waitingSeatMapCollectionState(showtimeNotDiscoveredReason())
}

func loadOptionalSeatMapCollectionStateForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
) (*collectionpb.State, string, error) {
	var stateValue string
	err := tx.QueryRow(ctx, `
		SELECT state FROM seat_map_collection_states
		WHERE auditorium_id = $1
		FOR UPDATE
	`, auditoriumID).Scan(&stateValue)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, "", err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		stateValue = ""
	}
	state, valid := seatMapCollectionStateFromSQL(stateValue)
	if !valid {
		return nil, "", errSeatMapCollectionInvalidTransition
	}
	return state, stateValue, nil
}

func clearSeatMapCollectionStateTx(ctx context.Context, tx pgx.Tx, auditoriumID string) error {
	result, err := tx.Exec(ctx, `DELETE FROM seat_map_collection_states WHERE auditorium_id = $1`, auditoriumID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

func markSeatMapCollectionCollectingTx(ctx context.Context, tx pgx.Tx, auditoriumID, assignmentID string, now time.Time) error {
	if assignmentID == "" {
		return errSeatMapCollectionStateNotFound
	}
	var currentStateValue, triggerValue string
	err := tx.QueryRow(ctx, `
		SELECT state, trigger_kind
		FROM seat_map_collection_states
		WHERE auditorium_id = $1
		FOR UPDATE
	`, auditoriumID).Scan(&currentStateValue, &triggerValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSeatMapCollectionStateNotFound
	}
	if err != nil {
		return err
	}
	currentState, validState := seatMapCollectionStateFromSQL(currentStateValue)
	trigger, validTrigger := seatMapCollectionTriggerFromSQL(triggerValue)
	if !validState || !validTrigger {
		return errSeatMapCollectionInvalidTransition
	}
	assignmentValue := assignmentID
	collecting := (&collectionpb.State_builder{
		Collecting: (&collectionpb.Collecting_builder{
			AssignmentId: &assignmentValue,
			StartedAt:    timestamppb.New(now),
		}).Build(),
	}).Build()
	if !validSeatMapCollectionTransition(currentState, collecting, trigger) {
		return errSeatMapCollectionInvalidTransition
	}
	result, err := tx.Exec(ctx, `
		UPDATE seat_map_collection_states
		SET state = 'collecting', assignment_id = $2, last_attempt_at = $3,
			next_attempt_at = NULL, updated_at = $3
		WHERE auditorium_id = $1 AND state IN ('queued', 'retry_scheduled')
	`, auditoriumID, assignmentID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errSeatMapCollectionStateNotFound
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

func loadSeatMapCollectionStateForUpdate(ctx context.Context, tx pgx.Tx, auditoriumID string) (*collectionpb.State, *collectionpb.Trigger, error) {
	var stateValue, triggerValue string
	if err := tx.QueryRow(ctx, `
		SELECT state, trigger_kind
		FROM seat_map_collection_states
		WHERE auditorium_id = $1
		FOR UPDATE
	`, auditoriumID).Scan(&stateValue, &triggerValue); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errSeatMapCollectionStateNotFound
		}
		return nil, nil, err
	}
	state, validState := seatMapCollectionStateFromSQL(stateValue)
	trigger, validTrigger := seatMapCollectionTriggerFromSQL(triggerValue)
	if !validState || !validTrigger {
		return nil, nil, errSeatMapCollectionInvalidTransition
	}
	return state, trigger, nil
}

func seatMapCollectionRetryableFailureReason(reason *collectionpb.FailureReason) bool {
	code, valid := seatMapCollectionFailureReasonCode(reason)
	if !valid {
		return false
	}
	switch code {
	case "provider_blocked", "provider_throttled", "browser_start_failed",
		"provider_transport_failed", "provider_server_error", "timeout":
		return true
	default:
		return false
	}
}

// scheduleSeatMapCollectionRetryTx persists a generated FailureReason and
// leaves retry timing entirely under Central control.
func scheduleSeatMapCollectionRetryTx(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	reason *collectionpb.FailureReason,
	nextAttemptAt, now time.Time,
) error {
	reasonCode, validReason := seatMapCollectionFailureReasonCode(reason)
	if !validReason || !seatMapCollectionRetryableFailureReason(reason) || !nextAttemptAt.After(now) {
		return errSeatMapCollectionInvalidTransition
	}
	currentState, trigger, err := loadSeatMapCollectionStateForUpdate(ctx, tx, auditoriumID)
	if err != nil {
		return err
	}
	target := (&collectionpb.State_builder{
		RetryScheduled: (&collectionpb.RetryScheduled_builder{
			Reason:        reason,
			NextAttemptAt: timestamppb.New(nextAttemptAt),
		}).Build(),
	}).Build()
	if !validSeatMapCollectionTransition(currentState, target, trigger) {
		return errSeatMapCollectionInvalidTransition
	}
	result, err := tx.Exec(ctx, `
		UPDATE seat_map_collection_states
		SET state = 'retry_scheduled', reason_code = $2, next_attempt_at = $3,
			assignment_id = NULL, last_attempt_at = $4,
			consecutive_failures = consecutive_failures + 1, updated_at = $4
		WHERE auditorium_id = $1
			AND state IN ('queued', 'collecting', 'retry_scheduled')
	`, auditoriumID, reasonCode, nextAttemptAt, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errSeatMapCollectionStateNotFound
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

// recordSeatMapCollectionFailureTx applies the Central retry budget to one
// completed seat-map attempt. Failures one through five are scheduled with
// the canonical backoff; failure six is terminal blocked.
func recordSeatMapCollectionFailureTx(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	reason *collectionpb.FailureReason,
	now time.Time,
) error {
	if !seatMapCollectionRetryableFailureReason(reason) {
		return blockSeatMapCollectionTx(ctx, tx, auditoriumID, reason, now)
	}
	var failures int
	if err := tx.QueryRow(ctx, `
		SELECT consecutive_failures FROM seat_map_collection_states
		WHERE auditorium_id = $1 FOR UPDATE
	`, auditoriumID).Scan(&failures); err != nil {
		return err
	}
	nextFailure := failures + 1
	if catalogdomain.SeatMapCollectionBlockedAfter(nextFailure) {
		return blockSeatMapCollectionTx(ctx, tx, auditoriumID, reason, now)
	}
	return scheduleSeatMapCollectionRetryTx(
		ctx, tx, auditoriumID, reason,
		now.Add(catalogdomain.SeatMapRetryDelay(nextFailure)), now,
	)
}

// blockSeatMapCollectionTx records a terminal generated FailureReason after
// Central exhausts its retry budget. A transient failure may still be the
// reason for blocked when the budget is exhausted.
func blockSeatMapCollectionTx(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	reason *collectionpb.FailureReason,
	now time.Time,
) error {
	reasonCode, validReason := seatMapCollectionFailureReasonCode(reason)
	if !validReason {
		return errSeatMapCollectionInvalidTransition
	}
	currentState, trigger, err := loadSeatMapCollectionStateForUpdate(ctx, tx, auditoriumID)
	if err != nil {
		return err
	}
	target := (&collectionpb.State_builder{
		Blocked: (&collectionpb.Blocked_builder{Reason: reason}).Build(),
	}).Build()
	if !validSeatMapCollectionTransition(currentState, target, trigger) {
		return errSeatMapCollectionInvalidTransition
	}
	result, err := tx.Exec(ctx, `
		UPDATE seat_map_collection_states
		SET state = 'blocked', reason_code = $2, next_attempt_at = NULL,
			assignment_id = NULL, last_attempt_at = $3,
			consecutive_failures = consecutive_failures + 1, updated_at = $3
		WHERE auditorium_id = $1
			AND state IN ('queued', 'collecting', 'retry_scheduled')
	`, auditoriumID, reasonCode, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errSeatMapCollectionStateNotFound
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

// waitSeatMapCollectionForShowtimeTx records a generated WaitingReason and
// removes the exact showtime hint until catalog discovery supplies one.
func waitSeatMapCollectionForShowtimeTx(
	ctx context.Context,
	tx pgx.Tx,
	auditoriumID string,
	reason *collectionpb.WaitingReason,
	now time.Time,
) error {
	reasonCode, validReason := seatMapCollectionWaitingReasonCode(reason)
	if !validReason {
		return errSeatMapCollectionInvalidTransition
	}
	currentState, trigger, err := loadSeatMapCollectionStateForUpdate(ctx, tx, auditoriumID)
	if err != nil {
		return err
	}
	target := waitingSeatMapCollectionState(reason)
	if !validSeatMapCollectionTransition(currentState, target, trigger) {
		return errSeatMapCollectionInvalidTransition
	}
	result, err := tx.Exec(ctx, `
		UPDATE seat_map_collection_states
		SET state = 'waiting_for_showtime', showtime_id = NULL, reason_code = $2,
			next_attempt_at = NULL, assignment_id = NULL, last_attempt_at = $3,
			updated_at = $3
		WHERE auditorium_id = $1
			AND state IN ('queued', 'collecting', 'retry_scheduled', 'waiting_for_showtime')
	`, auditoriumID, reasonCode, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errSeatMapCollectionStateNotFound
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

func requeueSeatMapCollectionForAssignmentTx(ctx context.Context, tx pgx.Tx, assignmentID string, now time.Time) error {
	var auditoriumID string
	showtimeNotDiscovered, reasonOK := seatMapCollectionWaitingReasonCode(showtimeNotDiscoveredReason())
	if !reasonOK {
		return errSeatMapCollectionInvalidTransition
	}
	err := tx.QueryRow(ctx, `
		WITH next_showtime AS (
			SELECT state.auditorium_id, showtime.id AS showtime_id
			FROM seat_map_collection_states AS state
			LEFT JOIN LATERAL (
				SELECT candidate.id
				FROM showtimes AS candidate
				WHERE candidate.auditorium_id = state.auditorium_id
					AND candidate.active
					AND candidate.starts_at > $2
				ORDER BY candidate.starts_at, candidate.id
				LIMIT 1
			) AS showtime ON true
			WHERE state.assignment_id = $1
		)
		UPDATE seat_map_collection_states AS state
		SET state = CASE
				WHEN next_showtime.showtime_id IS NULL THEN 'waiting_for_showtime'
				ELSE 'queued'
			END,
			showtime_id = next_showtime.showtime_id,
			reason_code = CASE
				WHEN next_showtime.showtime_id IS NULL THEN $3
				ELSE ''
			END,
			assignment_id = NULL,
			next_attempt_at = NULL,
			updated_at = $2
		FROM next_showtime
		WHERE state.auditorium_id = next_showtime.auditorium_id
		RETURNING state.auditorium_id
	`, assignmentID, now, showtimeNotDiscovered).Scan(&auditoriumID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return notifySeatMapCollection(ctx, tx, auditoriumID)
}

// refreshSeatMapCollectionShowtimeTargetsTx replaces expired or inactive
// showtime hints before scheduling. If no later showtime exists, collection
// returns to a durable waiting state until catalog discovery wakes it.
func refreshSeatMapCollectionShowtimeTargetsTx(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, err := tx.Query(ctx, `
		WITH retargeted AS (
			SELECT state.auditorium_id, state.state, state.reason_code,
				state.next_attempt_at, replacement.id AS showtime_id
			FROM seat_map_collection_states AS state
			LEFT JOIN LATERAL (
				SELECT showtime.id
				FROM showtimes AS showtime
				WHERE showtime.auditorium_id = state.auditorium_id
					AND showtime.active
					AND showtime.starts_at > $1
				ORDER BY showtime.starts_at, showtime.id
				LIMIT 1
			) AS replacement ON true
			WHERE state.state IN ('queued', 'retry_scheduled')
				AND NOT EXISTS (
					SELECT 1 FROM showtimes AS current_showtime
					WHERE current_showtime.id = state.showtime_id
						AND current_showtime.active
						AND current_showtime.starts_at > $1
				)
		), updated AS (
			UPDATE seat_map_collection_states AS state
			SET state = CASE
					WHEN retargeted.showtime_id IS NULL THEN 'waiting_for_showtime'
					ELSE retargeted.state
				END,
				showtime_id = retargeted.showtime_id,
				reason_code = CASE
					WHEN retargeted.showtime_id IS NULL THEN 'showtime_not_discovered'
					WHEN retargeted.state = 'queued' THEN ''
					ELSE retargeted.reason_code
				END,
				next_attempt_at = CASE
					WHEN retargeted.showtime_id IS NULL THEN NULL
					ELSE retargeted.next_attempt_at
				END,
				assignment_id = NULL,
				updated_at = $1
			FROM retargeted
			WHERE state.auditorium_id = retargeted.auditorium_id
			RETURNING state.auditorium_id
		)
		SELECT auditorium_id FROM updated
	`, now)
	if err != nil {
		return err
	}
	defer rows.Close()
	var auditoriumIDs []string
	for rows.Next() {
		var auditoriumID string
		if err := rows.Scan(&auditoriumID); err != nil {
			return err
		}
		auditoriumIDs = append(auditoriumIDs, auditoriumID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, auditoriumID := range auditoriumIDs {
		if err := notifySeatMapCollection(ctx, tx, auditoriumID); err != nil {
			return err
		}
	}
	return nil
}

func notifySeatMapCollection(ctx context.Context, tx pgx.Tx, payload string) error {
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, seatMapCollectionNotifyChannel, payload)
	return err
}

// SeatMapCollectionState returns the generated lifecycle state at the wire
// boundary. The SQL columns remain an internal persistence adapter.
func (store *Store) SeatMapCollectionState(ctx context.Context, auditoriumID string) (*collectionpb.State, error) {
	var stateValue, triggerValue, reasonCode string
	var requestedAt, lastAttemptAt, nextAttemptAt time.Time
	var assignmentID string
	err := store.pool.QueryRow(ctx, `
		SELECT state, trigger_kind, COALESCE(assignment_id, ''), COALESCE(reason_code, ''),
			requested_at, COALESCE(last_attempt_at, updated_at), COALESCE(next_attempt_at, updated_at)
		FROM seat_map_collection_states
		WHERE auditorium_id = $1
	`, auditoriumID).Scan(&stateValue, &triggerValue, &assignmentID, &reasonCode,
		&requestedAt, &lastAttemptAt, &nextAttemptAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, central.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	trigger, ok := seatMapCollectionTriggerFromSQL(triggerValue)
	if !ok {
		return nil, errSeatMapCollectionInvalidTrigger
	}
	state := &collectionpb.State{}
	switch stateValue {
	case seatMapStateQueued:
		state.SetQueued((&collectionpb.Queued_builder{
			QueuedAt: timestamppb.New(requestedAt), Trigger: trigger,
		}).Build())
	case seatMapStateCollecting:
		state.SetCollecting((&collectionpb.Collecting_builder{
			AssignmentId: stringPointer(assignmentID), StartedAt: timestamppb.New(lastAttemptAt),
		}).Build())
	case seatMapStateWaitingShowtime:
		reason := seatMapCollectionWaitingReasonFromSQL(reasonCode)
		if reason == nil {
			return nil, errSeatMapCollectionInvalidTransition
		}
		state.SetWaitingForShowtime((&collectionpb.WaitingForShowtime_builder{Reason: reason}).Build())
	case seatMapStateRetryScheduled:
		reason := seatMapCollectionFailureReasonFromSQL(reasonCode)
		if reason == nil || nextAttemptAt.IsZero() {
			return nil, errSeatMapCollectionInvalidTransition
		}
		state.SetRetryScheduled((&collectionpb.RetryScheduled_builder{
			Reason: reason, NextAttemptAt: timestamppb.New(nextAttemptAt),
		}).Build())
	case seatMapStateBlocked:
		reason := seatMapCollectionFailureReasonFromSQL(reasonCode)
		if reason == nil {
			return nil, errSeatMapCollectionInvalidTransition
		}
		state.SetBlocked((&collectionpb.Blocked_builder{Reason: reason}).Build())
	default:
		return nil, errSeatMapCollectionInvalidTransition
	}
	return state, nil
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// WatchSeatMapCollection installs LISTEN before the first observation so a
// committed transition cannot be lost between the initial read and the wait.
func (store *Store) WatchSeatMapCollection(
	ctx context.Context,
	auditoriumID string,
	observe func() error,
) error {
	if observe == nil {
		return errors.New("seat-map collection observer is required")
	}
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "LISTEN "+seatMapCollectionNotifyChannel); err != nil {
		return err
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = connection.Exec(cleanupContext, "UNLISTEN "+seatMapCollectionNotifyChannel)
	}()
	if err := observe(); err != nil {
		return err
	}
	for {
		notification, err := connection.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if notification.Payload == auditoriumID {
			if err := observe(); err != nil {
				return err
			}
		}
	}
}
