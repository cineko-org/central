# Persistence and mutation contract

PostgreSQL is the authority for Central state. The tables below describe externally
reachable mutations and scheduled background mutations. Each operation is atomic
unless explicitly marked as a single-row statement (which is atomic by PostgreSQL).

## Authentication and user ownership

| Operation | Database effect and transaction | Idempotency |
| --- | --- | --- |
| Admin login/logout | Insert or revoke `admin_sessions`; expired/revoked sessions are rejected | login creates a new session; logout is safe to repeat after authentication |
| Client credential/PIN exchange | Validate `client_credentials` or `client_pins`, update `client_pin_attempts`, insert `client_sessions` | a successful exchange creates fresh session tokens |
| Client refresh/logout | Lock and revoke the old `client_sessions` row; refresh inserts its replacement in the same transaction | old refresh token cannot be consumed twice; logout revocation is monotonic |
| Admin create/rotate/delete user | Create `client_users` plus `client_pins`; rotate the PIN digest; delete the user | create conflicts on duplicate identity; delete cascades sessions, credentials, PIN, launch tickets, executions, cursor, and CASCADE-owned rows and explicitly removes remaining RESTRICT-owned user rows in one transaction |
| Device upsert | Upsert `client_devices` scoped to the authenticated user | same identity updates mutable runtime fields |
| Startup admin bootstrap | Lock `admin_credentials`; if the table is empty, insert the configured password hashes; if any admin exists, leave all rows unchanged | restart-safe and first-start only |
| Startup client credential seed | Upsert configured `client_users` and `client_credentials` together; store only token hashes and clear credential revocation | deterministic by user ID; changed configured token intentionally rotates that user's bootstrap credential |

## Client resources, events, and execution

| Operation | Database effect and transaction | Idempotency |
| --- | --- | --- |
| Resource create/update/delete | Lock `client_commands` idempotency key, validate revision, mutate/soft-delete `client_resources`, append `client_events`, then record the command result | persisted `(user_id, command_id)` replay returns the same result; reuse for a different mutation conflicts |
| Settings/configuration update | Uses the same resource transaction for their singleton resource IDs | same as resource mutation |
| Launch ticket issue/exchange | Insert one-time `client_launch_tickets`; exchange locks and consumes it and inserts a bound `client_sessions` row | launcher nonce is unique per user; a consumed/expired ticket cannot be reused |
| Probe bootstrap issue/register | Ticket signing is stateless; registration records `consumed_probe_bootstrap_tickets` and upserts `probe_runtimes` | ticket ID is single-use; registration request key is required but the durable ticket is the replay boundary |
| Execution enqueue | Insert a unique `client_execution_commands` row or rearm an availability-wait failure after its 30-second cooldown, and append one `execution.ready.v1` `client_events` row in the same Probe-result transaction | queued/completed/conflicting or early availability replay is a no-op and emits no duplicate event |
| Execution claim | Expire queued/leased commands whose showtime has started, then lock the next future `client_execution_commands`; active monitor/auditorium matches are selected first, followed by newest discovery with deterministic ties | one active lease per command; stale showtimes become terminal `failed` with `showtime_started` and are never claimed |
| Execution heartbeat/result/retry | Validate user, installation, lease token, and expiry; extend lease or set completion/failure fields. Missing preferred seats and not-yet-selectable showtimes wait for a later observation instead of consuming immediate retries. Other automatic retries and explicit owner retries append `execution.ready.v1` in the same transaction | valid queued explicit retry is idempotent and emits no duplicate event; completed/leased commands conflict |
| Event retention | Delete an ordered bounded page from `client_events`, then advance `client_event_cursors.pruned_through` in the leader transaction | monotonic cursor makes a repeated cycle safe |

## Catalog and observation

| Operation | Database effect and transaction | Idempotency |
| --- | --- | --- |
| Catalog refresh request | Set `catalog_state.refresh_requested_at` if absent | coalesces repeated requests |
| Catalog snapshot | Require an online capable Client Probe for direct Client writes. In a serializable transaction, lock `catalog_state`, then additively upsert provider/theaters/movies/auditoriums/showtimes by canonical identity and content hash; omission never deletes/deactivates; a newly discovered auditorium without a current layout is queued for seat-map collection | content-identical replay does not increment generation; HTTP request key is presence-only; partial writes never clear a pending full refresh |
| Seat-map version | A completed Central assignment is validated against its auditorium, then a serializable transaction inserts/updates `seat_map_versions`, sets `auditoriums.current_seat_map_version_id`, clears its request marker, and increments catalog generation on change | `(auditorium_id, layout_hash)` is unique; Client has no direct seat-map write endpoint |
| Seat-map backfill request | Set `auditoriums.seat_map_requested_at` only when a current request is absent | coalesces repeated requests |
| Observation policy create | Resolve active catalog theater, then insert or revive the deterministic theater policy | one active `(task_kind, theater_id)`; duplicate live policy conflicts |
| Observation policy update/delete | Revision-guarded transaction; disabling or soft-deleting clears `next_run_at`, fails the current lease attempt, and terminally cancels queued/leased/retry work | stale revision conflicts; delete is not replayable after the revision changes |
| Probe register/heartbeat/disconnect | Upsert or update `probe_runtimes`; token hashes only are stored | registration identity is installation-bound; heartbeat/disconnect are state updates |
| Assignment claim | Transactionally select eligible queued/retry work, lease `observation_assignments`, and append `assignment_attempts` | a probe may attempt an assignment once; only one lease is active |
| Assignment heartbeat | Validate lease token/expiry and extend assignment and current attempt | repeated valid heartbeat only extends expiry |
| Assignment result | Lock assignment and attempt, validate run/result identity, persist payload/captures/observations/catalog effects, finalize attempt and assignment, and enqueue matching execution commands in one transaction. A full catalog result with no theater is rejected and cannot clear the refresh request | `run_id` plus result hash makes an identical retry return the prior receipt; divergent replay conflicts |
| Reconciler cycle | One advisory-lock transaction expires leases, processes results, schedules user-demand policies before catalog/seat-map maintenance, prunes retained rows, and advances policy state | only the elected leader mutates a cycle; saved policy intervals/priorities are used; unique active-policy/assignment indexes prevent duplicate active work |

## Release and startup ownership

| Operation | Database effect and transaction | Idempotency |
| --- | --- | --- |
| Startup schema application | Hold one advisory lock; initialize `cineko_schema_migrations`; verify stored checksums and apply each missing migration in its own transaction | recorded version plus checksum prevents repeat or drift |
| Release registry bootstrap/publish | Insert one immutable component/version/platform set; recompute active desktop manifest and update `desktop_release_registry_state` generation/fingerprint in the same transaction | identical set replay is accepted; divergent immutable identity conflicts |
| Release notification | A generation change triggers PostgreSQL notification; connected event streams compare their generation and wake | notification is advisory; durable generation remains authoritative |

## Transaction boundaries that must not drift

- A resource mutation, its durable event, and its idempotency command are one transaction.
- A committed Probe result, all normalized observations/catalog changes, its attempt,
  assignment state, policy scheduling state, matching Client commands, and execution-ready
  events are one transaction.
- A release set and the aggregate desktop generation update are one transaction.
- A catalog content mutation and catalog generation update are one serializable transaction.
- User deletion must not leave user-owned monitors, reservations, sessions, commands,
  events, executions, devices, PINs, or launch tickets.

## Known idempotency distinction

`Idempotency-Key` on client resource mutations, launch tickets, and Probe results is a
durable replay contract. On catalog snapshot, seat-map version, and Probe registration
it is currently only a required request marker; semantic identity/content constraints,
not a stored command record, provide replay behavior.
