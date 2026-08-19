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

Stored seat layouts and anonymous Probe seat observations are optional analysis or preview data. Their absence never
blocks a preset, monitor, execution command, or booking attempt. The live CGV seat response read again on the user's
Client is authoritative.
