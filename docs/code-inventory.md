# Source inventory

This inventory is based on the reviewed `origin/main` source snapshot before the
contract-document changes in this branch. The snapshot contains 2,826 tracked files:

| Surface | Files | Review boundary |
| --- | ---: | --- |
| First-party Go | 96 | command/configuration, domain, services, HTTP, PostgreSQL, reconciler, telemetry, and tests reviewed semantically |
| First-party TypeScript/TSX | 35 | admin application, pure views, API adapter, Storybook, and tests reviewed semantically |
| SQL migrations | 21 | replayed as one effective schema; every current table/column/check/FK/index and each deliberate drop inventoried |
| GitHub workflows | 3 | CI, image publication, and release triggers/permissions reviewed |
| Other YAML | 2 | dependency automation and lint configuration reviewed |
| Manifests and lock files | 6 | Go/npm module identity and locked dependency provenance reviewed |
| Domain documentation | 4 | checked against implemented contracts; the split contract documents in this branch replace implicit gaps |
| Other first-party configuration/assets | 14 | container/build/release configuration, HTML, embedded font, and generated web bundle reviewed |
| Vendored dependencies | 2,645 | provenance checked through `go.mod`, `go.sum`, and `vendor/modules.txt`; third-party source was not rewritten |

The first-party boundary is 181 tracked files. Generated embedded assets are treated as
build products: their HTML references resolve to the tracked hashed CSS/JavaScript and
their source bundle is owned by `frontend/`. Minified output, the font binary, npm lock,
Go checksums, and vendored code are reviewed for provenance and drift, not hand-edited.

## Contract indexes produced by the review

- `api-contract.md`: 59 literal router entries plus five resource templates expanded
  over five resource kinds, for 84 concrete service points. The method split is 35 GET,
  26 POST, 15 PUT, and 8 DELETE.
- `persistence-contract.md`: every external/background mutation, transaction boundary,
  durable idempotency boundary, and database effect.
- `state-contract.md`: every persisted state set, transition, retry/lease rule, terminal
  state, and retention owner.
- `schema-contract.md`: all 33 current tables and the effective columns, null/default
  behavior, checks, foreign keys, indexes, and retention ownership; historical dropped
  tables remain represented by migration fingerprints.

Focused tests derive the router inventory from source and verify all migration SHA-256
fingerprints. The PostgreSQL integration suite also replays the migrations and compares
all 33 resulting tables and columns with the schema contract. A new/changed route or
migration therefore requires an intentional contract update.

## Domain ownership boundary

Persisted Client resource validation is owned by
`internal/domain/resources`, not by the Central orchestration package. The
resource package depends on the Client domain types (`Preset` and `MonitorJob`)
and has no transport, PostgreSQL, or process-entrypoint dependency. Central
client/configuration use cases and the PostgreSQL execution selector call its
single `ValidatePayload` boundary; no compatibility wrapper remains in
`internal/central`.
