# HTTP contract inventory

This document is the service-point inventory for the current API. It describes public
wire behavior only; deployment addresses, secret material, and network topology are
intentionally outside this contract.

The router has 56 literal service points and five resource-family declarations. The
five declarations expand over five resource kinds, producing 81 concrete method/path
service points in total.

## Shared rules

- Every request, response, event, and persisted service payload uses the current
  generated Cineko protobuf message directly. Handwritten DTOs, aliases that rename
  generated messages, schema/protocol version fields, and compatibility envelopes are
  prohibited; ProtoJSON is only the HTTP and JSONB encoding of that message.
- `client` and `probe` routes require a Bearer credential. `admin` routes require the
  strict admin session cookie.
- `publisher` routes require the release-publisher Bearer credential. Authentication
  exchanges have distinct credentials: the client
  credential and six-digit PIN remain reusable until revoked/rotated; a refresh token
  is single-use because successful exchange rotates it; launch and Probe bootstrap
  tickets are one-time and expire shortly after issue.
- Every response has `X-Request-Id`, `Cache-Control: no-store`, and
  `X-Cineko-Release-Generation`. Catalog reads and writes also return
  `X-Cineko-Catalog-Generation`.
- JSON errors use `{"error":{"code","message","retryable","requestId"}}`.
  Stable shared codes are `idempotency_key_required`, `invalid_request`,
  `unauthorized`, `rate_limited`, `not_found`, `lease_expired`,
  `idempotency_conflict`, `revision_conflict`, `conflict`, `stale_release`,
  `corrupt_resource`, and `internal_error`. A disabled optional plane returns its
  plane-specific `*_unavailable` code with `503`.
- Validation failures expose a stable public message rather than the wrapped internal
  error. Unclassified failures expose only `internal_error` plus the request ID; the
  detailed error is written to structured server logs.
- JSON decoding rejects unknown fields, multiple values, and bodies larger than the
  server limit. An `Idempotency-Key` requirement means the header must be non-empty;
  only the operations explicitly described as replay-safe persist its result.
- Admin browser mutations marked `same-origin` reject an `Origin` that differs from
  the request origin. Creation uses `If-None-Match: *`; revisioned update and delete
  use `If-Match: "<positive revision>"`.

## Public and authentication plane

| Endpoint | Auth | Mutation and precondition | Result |
| --- | --- | --- | --- |
| `GET /health` | public | readiness read | `200`, otherwise API error |
| `GET /livez` | public | process liveness read | `200` |
| `GET /readyz` | public | readiness read | `200`, otherwise API error |
| `GET /health/reconciler` | public | reconciler snapshot read | `200` healthy, `503` unavailable or unhealthy |
| `GET /` | public | embedded admin web asset read | HTML/static asset or `404` |
| `POST /v1/auth/exchange` | client credential | exchanges a reusable credential until it is revoked | `200` session pair |
| `POST /v1/auth/pin` | PIN | exchanges a reusable six-digit PIN until rotation/deletion; rate limited by source and device | `200` session pair |
| `POST /v1/auth/refresh` | refresh token | consumes and rotates the refresh token atomically | `200` replacement session pair |
| `POST /v1/auth/logout` | client | revokes the current client session | `204` |

## Admin plane

All rows require the admin session. `same-origin` is additionally required only where
shown; this distinction is deliberate documentation of the implemented boundary.

| Endpoint | Mutation and precondition | Result |
| --- | --- | --- |
| `POST /v1/admin/login` | same-origin; verifies the configured admin credential and creates a session | `200` principal |
| `POST /v1/admin/logout` | same-origin; revokes the session and clears its cookie | `204` |
| `GET /v1/admin/session` | session read | `200` principal |
| `GET /v1/admin/status` | service status read | `200` |
| `GET /v1/admin/configuration` | effective public-safe configuration read | `200` |
| `GET /v1/admin/probes` | probe inventory read | `200` list |
| `DELETE /v1/admin/probes/{probeId}` | same-origin; only an offline, unreferenced probe can be removed | `204`, `404`, or `409` |
| `GET /v1/admin/data` | aggregate data read | `200` |
| `GET /v1/admin/catalog` | shared catalog read | `200` catalog |
| `GET /v1/admin/catalog-refresh` | refresh request state read | `200` |
| `POST /v1/admin/catalog-refresh` | same-origin; requests a catalog refresh, no revision header | `202` state |
| `GET /v1/admin/observation-policies` | policy inventory read | `200` list |
| `POST /v1/admin/observation-policies` | same-origin, `If-None-Match: *`; one active policy per task/theater | `201`, `409` on duplicate |
| `PUT /v1/admin/observation-policies/{policyId}` | same-origin, `If-Match`; updates the selected theater policy | `200`, `409` on stale revision |
| `DELETE /v1/admin/observation-policies/{policyId}` | same-origin, `If-Match`; soft-deletes the policy | `204`, `409` on stale revision |
| `GET /v1/admin/observation-intelligence` | derived schedule intelligence read | `200` |
| `GET /v1/admin/releases` | release inventory read | `200` |
| `GET /v1/admin/releases/probe/current` | current probe release read | `200` |
| `GET /v1/admin/users` | client-user/PIN inventory read | `200` list |
| `POST /v1/admin/users` | same-origin; creates a user and initial PIN | `201` |
| `POST /v1/admin/users/{userId}/pin` | same-origin; atomically replaces the PIN | `200` new PIN |
| `DELETE /v1/admin/users/{userId}` | same-origin; deletes the user and cascading user-owned data | `204` |

## Release and launcher plane

| Endpoint | Auth | Mutation and precondition | Result |
| --- | --- | --- | --- |
| `GET /v1/releases/runtime/current` | client | requires channel/platform/arch resolution | `200` immutable runtime manifest |
| `GET /v1/releases/launcher/current` | client | requires channel/platform/arch resolution | `200` immutable launcher manifest |
| `POST /v1/release-registry/{component}` | publisher | immutable typed set; identical retry is accepted, divergent same version conflicts | `201` new, `200` replay, `409` conflict |
| `POST /v1/launch-tickets` | client | `Idempotency-Key` must equal request nonce | `201` one-time ticket |
| `POST /v1/client-sessions` | one-time launch ticket | consumes the ticket and binds the runtime | `200` client session |

## Client plane

| Endpoint | Mutation and precondition | Result |
| --- | --- | --- |
| `POST /v1/probe-bootstrap-tickets` | validates the owned device and issues a short-lived, one-time signed ticket | `201` |
| `POST /v1/executions:claim` | leases the next eligible user execution to an installation | `200` command or `204` |
| `PUT /v1/executions/{executionId}/heartbeat` | valid execution lease; extends it | `200` |
| `PUT /v1/executions/{executionId}/result` | valid execution lease; records terminal or retryable outcome | `204` |
| `POST /v1/executions/{executionId}/retry` | user-owned terminal failure only; reset is idempotent while queued | `204`, `404`, or `409` |
| `PUT /v1/devices/{installationId}` | path is authoritative for installation ID; upsert | `200` device |
| `GET /v1/client/bootstrap` | reads user resources, release generation, and device state | `200` |
| `GET /v1/events/stream` | durable cursor from query/`Last-Event-ID`; session is revalidated | SSE stream or reset control event |
| `GET /v1/settings` | reads the singleton settings resource | `200` |
| `PUT /v1/settings` | `Idempotency-Key`, revision precondition | `200` |
| `GET /v1/catalog` | reads active shared catalog | `200` |
| `POST /v1/catalog/snapshots` | non-empty `Idempotency-Key`; `X-Cineko-Installation-Id` must identify this user's online Client Probe with catalog capability; additive canonical upsert | `200` generation |
| `GET /v1/catalog/auditoriums/{auditoriumId}/seat-map` | reads current stored layout | `200` or `404` |
| `POST /v1/catalog/auditoriums/{auditoriumId}/seat-map:resolve` | returns the stored current layout immediately; when absent, returns `waiting` after Central arranges the work behind this boundary | `200` resolution |

The resource family is `presets`, `monitors`, `reservations`,
`external-operations`, and `app-events`. For each `{resource}`:

| Endpoint family | Mutation and precondition | Result |
| --- | --- | --- |
| `GET /v1/{resource}` | list active user-owned resources | `200` list |
| `POST /v1/{resource}` | `Idempotency-Key`, `If-None-Match: *`; replay-safe command record | `201` |
| `GET /v1/{resource}/{resourceId}` | read one active user-owned resource | `200` or `404` |
| `PUT /v1/{resource}/{resourceId}` | `Idempotency-Key`, `If-Match`; replay-safe command record | `200` |
| `DELETE /v1/{resource}/{resourceId}` | `Idempotency-Key`, `If-Match`; replay-safe soft delete | `200` tombstone |

## Probe plane

| Endpoint | Mutation and precondition | Result |
| --- | --- | --- |
| `POST /v1/probes/register` | `Idempotency-Key`, valid enrollment/bootstrap credential | `200` probe token and runtime |
| `PUT /v1/probes/{probeId}/heartbeat` | probe Bearer identity must match path | `200` renewed runtime state |
| `POST /v1/probes/{probeId}/disconnect` | probe Bearer identity must match path | `204` offline |
| `POST /v1/probes/{probeId}/assignments:claim` | probe Bearer identity; atomically leases eligible work | `200` lease or `204` |
| `PUT /v1/assignments/{assignmentId}/heartbeat` | probe Bearer plus `X-Cineko-Lease-Token` | `200` extended lease |
| `PUT /v1/assignments/{assignmentId}/result` | probe Bearer, lease token, `Idempotency-Key == runId`; a full catalog result must contain at least one theater | `200` durable receipt; same result replay-safe |
