# Source inventory

This inventory is based on the reviewed source snapshot for this branch. The snapshot
contains 3,237 tracked files after this change:

| Surface | Files | Review boundary |
| --- | ---: | --- |
| First-party Go | 137 | command/configuration, domain, services, HTTP, PostgreSQL, reconciler, telemetry, and tests reviewed semantically |
| First-party TypeScript/TSX | 36 | admin application, pure views, API adapter, Storybook, and tests reviewed semantically |
| SQL migrations | 32 | replayed as one effective schema; every current table/column/check/FK/index and each deliberate drop inventoried |
| GitHub workflows | 3 | CI, image publication, and release triggers/permissions reviewed |
| Other YAML | 2 | dependency automation and lint configuration reviewed |
| Manifests and lock files | 6 | Go/npm module identity and locked dependency provenance reviewed |
| Domain documentation | 9 | checked against implemented contracts; the split contract documents in this branch replace implicit gaps |
| Other first-party configuration/assets | 17 | container/build/release configuration, HTML, embedded font, and generated web bundle reviewed |
| Vendored dependencies | 2,995 | provenance checked through `go.mod`, `go.sum`, and `vendor/modules.txt`; third-party source was not rewritten |

The first-party boundary is 242 tracked files. Generated embedded assets are treated as
build products: their HTML references resolve to the tracked hashed CSS/JavaScript and
their source bundle is owned by `frontend/`. Minified output, the font binary, npm lock,
Go checksums, and vendored code are reviewed for provenance and drift, not hand-edited.

## Contract indexes produced by the review

- `api-contract.md`: 55 literal router entries plus five resource templates expanded
  over five resource kinds, for 80 concrete service points. The method split is 34 GET,
  25 POST, 13 PUT, and 8 DELETE.
- `persistence-contract.md`: every external/background mutation, transaction boundary,
  durable idempotency boundary, and database effect.
- `state-contract.md`: every persisted state set, transition, retry/lease rule, terminal
  state, and retention owner.
- `schema-contract.md`: all 60 current tables and the effective columns, null/default
  behavior, checks, foreign keys, indexes, and retention ownership. The six Client
  resource kinds use typed parents and ordered child tables; historical dropped tables
  remain represented by migration fingerprints.

Focused tests derive the router inventory from source and verify all migration SHA-256
fingerprints, including migrations `000028_normalize_client_resources.sql` through
`000030_seat_availability.sql` and `000032_seat_map_collection_state.sql`. The PostgreSQL integration suite also replays the migrations and compares all 60 resulting
tables and columns with the schema contract, including the removal of
`client_resources.payload`, the normalized Client resource tables,
`observation_assignments.auditorium_id` and `showtime_id`,
`client_execution_commands.observed_at`, and the normalized exact-showtime availability
tables. A new/changed route or migration therefore
requires an intentional contract update.

## Domain ownership boundary

Persisted Client resource validation is owned by
`internal/domain/clientresources`, not by the Central orchestration package. The
package accepts the latest generated `client.Resource` directly, applies generated
Proto validation plus resource-domain invariants, and has no transport, PostgreSQL,
or process-entrypoint dependency. Central use cases call its single `Validate`
boundary; PostgreSQL stores normalized typed columns and reconstructs the generated
resource without a handwritten DTO or a JSON round trip.
