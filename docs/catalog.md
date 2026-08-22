# Catalog

PostgreSQL is the authority for shared booking metadata:

`Provider -> Theater -> Auditorium -> Showtime <- Movie`

- Every row has a Central ID and an immutable provider `sourceKey`.
- Probe result commits update catalog metadata and append availability observations in one transaction.
- Authenticated Client discovery may publish the same canonical entities.
- Live availability is observation data, not catalog state.
- Client reads the catalog from Central, then opens the exact selected showtime and reads the current layout and
  availability from CGV before choosing seats. CGV account login is not required for this seat read.

Admin observation policies reference a Central theater ID selected from the catalog. One active theater policy owns
the shared schedule scan; user monitors only increase its cadence.

Client monitor resources store `movieId` as the canonical catalog Movie ID. The `movie` field is only a display
snapshot and is never used to match a showtime; execution matching fails closed when either Movie ID is missing or
different.

Stored seat layouts and anonymous Probe seat observations are separate data products. The immutable current layout
pointer is a reusable snapshot; `seat_map_collection_states` is the durable lifecycle for obtaining or validating
that snapshot and may be non-idle even while an older snapshot remains readable. A monitor may be created without a
cached layout, but the preset editor needs a layout to offer seat constraints and a seat-constrained execution command
is blocked until Central has exact layout-aware proof. The live CGV seat response read again on the user's Client is
authoritative.
