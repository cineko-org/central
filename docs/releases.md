# Release registry

## Ownership

- Central application releases are independent container deployments and never change component generations.
- PostgreSQL owns immutable Launcher, Client, Chromium, Playwright, and Probe release records.
- `desktop_release_registry_state.generation` changes once only when a publish changes the aggregate active Launcher or fully resolved Runtime manifest for any supported stable desktop target.
- Probe releases are deployment inventory and never change the desktop generation.

## Wire contract

- CI registers strict typed `ReleaseSet<T>` JSON with `POST /v1/release-registry/{component}` after artifact or image verification.
- Authentication is `Authorization: Bearer $CINEKO_RELEASE_PUBLISH_TOKEN`. Request and response bodies use only the current generated release ProtoJSON contract.
- Desktop sets must contain exactly `darwin/arm64`, `linux/amd64`, and `windows/amd64`. Central validation is the supported-target source of truth; a partial set never activates.
- Probe sets contain one verified multi-architecture image digest and do not affect desktop clients.
- A new immutable set returns `201`; an identical retry returns `200`; changed data for the same component, channel, and version returns `409 conflict`.
- Every Central response carries `X-Cineko-Release-Generation`. The existing event stream repeats it in keepalive comments.
- Launcher and runtime current endpoints select from one PostgreSQL snapshot using the same resolver that owns the active-manifest fingerprint.
- Launch tickets persist the generation. Stale issue or exchange attempts return `409 stale_release`.

There is no update-only polling endpoint or update-only event stream.

Optional `CINEKO_*_RELEASES_JSON` values seed only component kinds that have no PostgreSQL inventory. CI publication owns every later release.
