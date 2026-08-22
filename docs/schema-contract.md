# PostgreSQL schema contract

Notation: `!` means `NOT NULL`; `?` means nullable; `=value` records a database
default. Named checks, foreign-key actions, and indexes are listed separately. This is
the effective schema after every migration, not a history of intermediate tables.
The current schema contains 59 tables. Client resource bodies are normalized into the
typed tables below; `client_resources` retains only common identity, revision, timestamps,
and soft-delete state.

## Runtime and observation

| Table | Columns | Checks and foreign keys | Indexes | Retention owner |
| --- | --- | --- | --- | --- |
| `cineko_schema_migrations` | `version bigint! PK`; `checksum text! = ''`; `applied_at timestamptz! = now()` | — | PK | migration runner, permanent |
| `probe_runtimes` | `id text! PK`; `installation_id text! UNIQUE`; `kind text!`; `network_id text!`; `network_hint text! = ''`; `capabilities text[]!`; `max_concurrency integer!`; `runtime_version text!`; `browser_revision text!`; `platform text!`; `architecture text!`; `token_hash bytea!`; `token_expires_at timestamptz!`; `status text!`; `draining boolean! = false`; `available_slots integer! = 0`; `health text! = 'healthy'`; `reason_code text! = ''`; `last_heartbeat_at timestamptz?`; `created_at timestamptz!`; `updated_at timestamptz!`; `owner_user_id text! = ''`; `device_id text! = ''`; `available_capabilities text[]! = '{}'` | kind container/client; concurrency 1..32; status online/offline; slots nonnegative; health healthy/degraded | unique token hash; health, network, and conditional owner indexes | reconciler, offline default 30 days; references can retain |
| `observation_policies` | `id text! PK`; `enabled boolean! = true`; `revision bigint! = 1`; `task_kind text!`; `theater_id text!`; `theater_region text!`; `theater_name text!`; `target_date_mode text!`; `target_dates date[]! = '{}'`; `horizon_days integer?`; `locale text!`; `time_zone text!`; `egress_policy_id text!`; `priority smallint! = 50`; `min_interval_seconds integer!`; `max_interval_seconds integer!`; `execution_window_seconds integer!`; `next_run_at timestamptz?`; `last_finished_at timestamptz?`; `last_outcome text?`; `last_error_code text! = ''`; `created_at timestamptz!`; `updated_at timestamptz!`; `deleted_at timestamptz?`; `display_name text! = ''`; `demand_min_interval_seconds integer! = 120`; `demand_max_interval_seconds integer! = 300`; `burst_min_interval_seconds integer! = 30`; `burst_max_interval_seconds integer! = 90`; `burst_duration_seconds integer! = 3600`; `burst_until timestamptz?`; `theater_provider_id text! = 'cgv'`; `theater_source_key text! = ''` | revision positive; mode explicit/rolling with matching date/horizon shape; priority 0..100; baseline >=30 and increasing; demand >=1 and increasing; burst >=15 and increasing; burst duration 300..21600; outcome completed/partial/failed/missed | unique active task/theater; conditional due index | soft delete, otherwise permanent |
| `observation_assignments` | `id text! PK`; `task_kind text!`; `theater_id text!`; `theater_region text!`; `theater_name text!`; `target_dates date[]!`; `locale text!`; `time_zone text!`; `egress_policy_id text!`; `status text!`; `not_before timestamptz!`; `deadline timestamptz!`; `probe_id text?`; `lease_token_hash bytea?`; `lease_expires_at timestamptz?`; `run_id text?`; `result_hash text?`; `result_payload jsonb?`; `started_at timestamptz?`; `finished_at timestamptz?`; `created_at timestamptz!`; `updated_at timestamptz!`; `policy_id text?`; `priority smallint! = 50`; `terminal_reason text! = ''`; `completed_by_probe_id text?`; `theater_provider_id text! = 'cgv'`; `theater_source_key text! = ''`; `task_data jsonb?`; `lane text! = 'baseline'`; `hot_target_fingerprint text! = ''`; `auditorium_id text! = ''`; `showtime_id text?` | deadline after not-before; status queued/leased/retry_pending/completed/partial/failed/missed; lane baseline/hot; leased iff probe/token/expiry present; seat-map task requires a nonempty auditorium; seat-availability task requires an exact showtime; probe FK SET NULL; policy FK RESTRICT; showtime FK RESTRICT; priority 0..100 | unique leased task/theater; unique active policy; unique active exact-showtime availability; claim and probe indexes; auditorium/status/deadline | none |
| `assignment_attempts` | `assignment_id text!`; `probe_id text!`; `attempt integer!`; `started_at timestamptz!`; `finished_at timestamptz?`; `status text!`; `error_code text! = ''`; `lease_token_hash bytea?`; `network_id text?`; `run_id text?`; `result_hash text?`; `result_payload jsonb?`; PK assignment/attempt | assignment FK RESTRICT; attempt positive; status leased/completed/partial/failed/expired; run/result hash nullity matches | unique assignment/probe; probe/time | none |
| `assignment_eligible_probes` | `assignment_id text!`; `probe_id text!`; `network_id text!`; `eligible_at timestamptz!`; PK assignment/probe | assignment FK RESTRICT | probe/assignment | none |
| `observation_payloads` | `content_hash text! PK`; `payload jsonb!`; `created_at timestamptz!` | hash is 64 lowercase hex | PK | none |
| `schedule_captures` | `assignment_id text!`; `run_id text!`; `target_date date!`; `observed_at timestamptz!`; `complete boolean!`; `error_code text! = ''`; `content_hash text!`; `created_at timestamptz!`; PK assignment/run/date | assignment and payload FKs RESTRICT | target date/observed; assignment/date/complete/observed | none |
| `showtime_observations` | `assignment_id text!`; `run_id text!`; `target_date date!`; `source_key text!`; `theater_id text!`; `auditorium_name text!`; `screen_types text[]!`; `movie_id text?`; `movie_title text!`; `poster_url text! = ''`; `starts_at timestamptz!`; `ends_at timestamptz!`; `available_seats integer!`; `capacity integer!`; `sold_out boolean!`; `observed_at timestamptz!`; `auditorium_id text! = ''`; PK assignment/run/date/source | capture composite FK RESTRICT; movie FK RESTRICT for new observations; nullable only for unmatched legacy rows; end after start; seats 0..capacity | theater analysis/start, occurrence, and movie history | none |
| `consumed_probe_bootstrap_tickets` | `ticket_id text! PK`; `expires_at timestamptz!`; `consumed_at timestamptz!` | — | expiry | registration path |

## Users, resources, and executions

| Table | Columns | Checks and foreign keys | Indexes | Retention owner |
| --- | --- | --- | --- | --- |
| `client_users` | `id text! PK`; `display_name text!`; `created_at timestamptz!`; `updated_at timestamptz!` | — | PK | admin delete |
| `client_credentials` | `user_id text! PK`; `token_hash bytea! UNIQUE`; `revoked_at timestamptz?`; `created_at timestamptz!`; `updated_at timestamptz!` | user FK CASCADE | token unique | user delete |
| `client_sessions` | `id text! PK`; `user_id text!`; `token_hash bytea! UNIQUE`; `expires_at timestamptz!`; `revoked_at timestamptz?`; `created_at timestamptz!`; `refresh_token_hash bytea! UNIQUE`; `refresh_expires_at timestamptz!` | user FK CASCADE | conditional active user/expiry | expiry enforced; no purge owner |
| `client_devices` | `installation_id text! PK`; `user_id text!`; `device_id text!`; `platform text!`; `architecture text!`; `app_version text!`; `last_seen_at timestamptz!`; `created_at timestamptz!`; `updated_at timestamptz!` | user FK RESTRICT; unique user/device | owner/last seen | user delete transaction |
| `client_resources` | `user_id text!`; `kind text!`; `id text!`; `revision bigint!`; `created_at timestamptz!`; `updated_at timestamptz!`; `deleted_at timestamptz?`; composite PK user/kind/id | user FK RESTRICT; revision positive; kind settings/presets/monitors/reservations/external-operations/app-events; typed parents use a composite FK to this identity | conditional active list | common identity/revision/soft-delete owner; typed rows cascade; user delete transaction |
| `client_commands` | `user_id text!`; `command_id text!`; `operation text!`; `resource_kind text!`; `resource_id text!`; `result_revision bigint!`; `created_at timestamptz!`; PK user/command | user FK RESTRICT; operation put/delete | PK | user delete transaction |
| `client_events` | identity `sequence bigint! PK`; `id text! UNIQUE`; `user_id text!`; `event_type text!`; `resource_kind text!`; `resource_id text!`; `resource_revision bigint!`; `payload jsonb!`; `occurred_at timestamptz!` | user FK RESTRICT | user/sequence; occurred retention; after-insert notify trigger | reconciler, 180 days |
| `client_event_cursors` | `user_id text! PK`; `pruned_through bigint! = 0`; `updated_at timestamptz! = now()` | user FK CASCADE; cursor nonnegative | PK | user delete; monotonic |
| `client_launch_tickets` | `id text! PK`; `user_id text!`; `installation_id text!`; `device_id text!`; `client_version text!`; `artifact_sha256 text!`; `browser_revision text!`; `launcher_nonce text!`; `client_nonce text?`; `token_hash bytea! UNIQUE`; `expires_at timestamptz!`; `consumed_at timestamptz?`; `created_at timestamptz!`; `release_generation bigint!`; `browser_artifact_sha256 text!`; `playwright_version text!`; `playwright_artifact_sha256 text!` | user and installation FKs CASCADE; generation positive; unique user/launcher nonce | expiry | expiry enforced; no purge owner |
| `client_execution_commands` | `id text! PK`; `user_id text!`; `monitor_id text!`; `showtime_id text!`; `starts_at timestamptz!`; `payload jsonb!`; `status text!`; `leased_installation_id text?`; `last_installation_id text?`; `lease_token_hash bytea?`; `lease_expires_at timestamptz?`; `attempt_count integer! = 0`; `reason_code text! = ''`; `completed_at timestamptz?`; `created_at timestamptz!`; `updated_at timestamptz!`; `observed_at timestamptz!` | user FK CASCADE; leased installation FK SET NULL; status queued/leased/completed/failed; attempts 0..3; observed_at records the latest observation used for execution selection; leased iff installation/token/expiry present; unique user/monitor/showtime/start | conditional claim; observed_at DESC | terminal retained until user delete; payload remains the execution audit envelope |
| `client_pins` | `user_id text! PK`; `pin_digest bytea! UNIQUE`; `revoked_at timestamptz?`; `created_at timestamptz!`; `updated_at timestamptz!` | user FK CASCADE | digest unique | user delete/rotation |
| `client_pin_attempts` | `scope_hash bytea! PK`; `failure_count integer!`; `blocked_until timestamptz?`; `updated_at timestamptz!` | failures nonnegative | updated retention | authentication path; no age purge |
| `admin_sessions` | `token_hash bytea! PK`; `user_id text!`; `display_name text!`; `expires_at timestamptz!`; `revoked_at timestamptz?`; `created_at timestamptz!` | — | expiry | expiry/revocation enforced; no purge owner |
| `admin_credentials` | `user_id text! PK`; `display_name text!`; `password_hash text!`; `created_at timestamptz!`; `updated_at timestamptz!` | password hash length 64..512 | PK | bootstrap/update, permanent |

## Normalized client resources

Every typed parent has `user_id`, a constant-checked `resource_kind`, and `id`, plus a
composite foreign key to `client_resources(user_id, kind, id)`. Repeated protobuf fields
are child rows with a zero-based `position`; parent and child rows are replaced atomically
with the common revision on a resource mutation.

| Table | Columns | Checks and foreign keys | Indexes | Retention owner |
| --- | --- | --- | --- | --- |
| `client_settings` | `user_id text!`; `resource_kind text! = 'settings'`; `id text!`; `network_mode text?`; `proxy_username text! = ''`; `proxy_password text! = ''`; `proxy_has_password boolean! = false`; composite PK user/resource-kind/id | resource identity FK CASCADE; network mode direct/proxy or nullable for an empty Settings message; proxy fields are valid only in proxy mode; password presence is explicit | unique user/id | common resource identity; children cascade; user delete |
| `client_setting_proxy_urls` | `user_id text!`; `settings_id text!`; `position integer!`; `url text!`; composite PK user/settings/position | settings FK CASCADE; position nonnegative; URL nonempty | PK | settings child; user delete |
| `client_setting_webhooks` | `user_id text!`; `settings_id text!`; `position integer!`; `id text!`; `name text!`; `url text!`; `secret text!`; `enabled boolean!`; `has_secret boolean!`; composite PK user/settings/position | settings FK CASCADE; position nonnegative; explicit secret presence | PK | settings child; user delete |
| `client_setting_webhook_event_kinds` | `user_id text!`; `settings_id text!`; `webhook_position integer!`; `position integer!`; `event_kind text!`; composite PK user/settings/webhook-position/position | webhook FK CASCADE; both positions nonnegative; event kind nonempty | PK | settings child; user delete |
| `client_presets` | `user_id text!`; `resource_kind text! = 'presets'`; `id text!`; `name text!`; `theater_id text!`; `auditorium_id text!`; `seat_count integer!`; `has_seat_preference boolean! = false`; `together boolean! = false`; `avoid_edges boolean! = false`; `preset_created_at timestamptz?`; `preset_updated_at timestamptz?`; composite PK user/resource-kind/id | resource identity FK CASCADE; theater/auditorium FKs RESTRICT; seat count nonnegative; presence distinguishes absent SeatPreference | catalog target index | common resource identity; children cascade; user delete |
| `client_preset_explicit_seats` | `user_id text!`; `preset_id text!`; `position integer!`; `seat_label text!`; composite PK user/preset/position | preset FK CASCADE; position nonnegative; label nonempty | PK | preset child; user delete |
| `client_preset_preferred_rows` | `user_id text!`; `preset_id text!`; `position integer!`; `row_label text!`; composite PK user/preset/position | preset FK CASCADE; position nonnegative; label nonempty | PK | preset child; user delete |
| `client_preset_preferred_zones` | `user_id text!`; `preset_id text!`; `position integer!`; `name text!`; `min_x double precision!`; `max_x double precision!`; `min_y double precision!`; `max_y double precision!`; `weight integer!`; composite PK user/preset/position | preset FK CASCADE; position nonnegative; bounds normalized | PK | preset child; user delete |
| `client_preset_preferred_types` | `user_id text!`; `preset_id text!`; `position integer!`; `seat_type text!`; composite PK user/preset/position | preset FK CASCADE; position nonnegative; type nonempty | PK | preset child; user delete |
| `client_monitors` | `user_id text!`; `resource_kind text! = 'monitors'`; `id text!`; `preset_id text!`; `movie_id text!`; `movie_title text!`; `search_horizon_days integer!`; `earliest_minute integer?`; `latest_minute integer?`; `state text!`; `state_reason text!`; `last_checked_at timestamptz?`; `reservation_id text?`; `monitor_created_at timestamptz?`; `monitor_updated_at timestamptz?`; composite PK user/resource-kind/id | resource identity FK CASCADE; preset/movie FKs RESTRICT and preset reference deferred; horizon 1..14; local times are nullable minutes; state includes payment-unknown | movie/state execution index | common resource identity; children cascade; user delete |
| `client_monitor_target_dates` | `user_id text!`; `monitor_id text!`; `position integer!`; `target_date date!`; composite PK user/monitor/position | monitor FK CASCADE; position nonnegative | date/user/monitor | monitor child; user delete |
| `client_monitor_target_weekdays` | `user_id text!`; `monitor_id text!`; `position integer!`; `target_weekday smallint!`; composite PK user/monitor/position | monitor FK CASCADE; position 0..6 | weekday/user/monitor | monitor child; user delete |
| `client_reservations` | `user_id text!`; `resource_kind text! = 'reservations'`; `id text!`; `monitor_id text!`; `booking_number text!`; `total_price text!`; `booked_at timestamptz?`; `cancelled_at timestamptz?`; `refund_amount text!`; `state text!`; composite PK user/resource-kind/id | resource identity FK CASCADE; monitor FK RESTRICT and deferred; states use prepared/booked/cancellation-committing/cancellation-unknown/cancelled | monitor index | common resource identity; children cascade; user delete |
| `client_reservation_seats` | `user_id text!`; `reservation_id text!`; `position integer!`; `seat_label text!`; composite PK user/reservation/position | reservation FK CASCADE; position nonnegative; label nonempty | PK | reservation child; user delete |
| `client_reservation_showtimes` | `user_id text!`; `reservation_id text!`; `showtime_id text!`; `provider_id text!`; `source_key text!`; `theater_id text!`; `movie_id text!`; `movie_provider_id text!`; `movie_source_key text!`; `movie_title text!`; `movie_poster_url text!`; `auditorium_id text!`; `auditorium_theater_id text!`; `auditorium_source_key text!`; `auditorium_name text!`; `auditorium_capacity integer!`; `auditorium_layout_hash text!`; `schedule_date date!`; `starts_at timestamptz!`; `ends_at timestamptz!`; `available_seats integer!`; `capacity integer!`; `sold_out boolean!`; PK user/reservation | reservation FK CASCADE; complete historical showtime/movie/auditorium snapshot; provider schedule date remains distinct from civil start date; end after start; seats do not exceed capacity | PK | reservation child; user delete |
| `client_reservation_showtime_screen_types` | `user_id text!`; `reservation_id text!`; `position integer!`; `screen_type text!`; composite PK user/reservation/position | showtime FK CASCADE; position nonnegative; screen type nonempty | PK | reservation child; user delete |
| `client_external_operations` | `user_id text!`; `resource_kind text! = 'external-operations'`; `id text!`; `monitor_id text?`; `reservation_id text!`; `kind text! = 'cancellation'`; `state text!`; `refund_amount text!`; `last_error text!`; `operation_created_at timestamptz?`; `operation_updated_at timestamptz?`; composite PK user/resource-kind/id | resource identity FK CASCADE; reservation FK RESTRICT and deferred; state includes attention-required; only `kind` identifies the operation | state index | common resource identity; user delete |
| `client_app_events` | `user_id text!`; `resource_kind text! = 'app-events'`; `id text!`; `kind text!`; `message text!`; `event_created_at timestamptz?`; `read_at timestamptz?`; `tone text!`; composite PK user/resource-kind/id | resource identity FK CASCADE; tone info/success/warning/error | unread index | common resource identity; user delete |

## Catalog and releases

| Table | Columns | Checks and foreign keys | Indexes | Retention owner |
| --- | --- | --- | --- | --- |
| `catalog_state` | `id smallint! PK`; `generation bigint!`; `updated_at timestamptz!`; `refresh_requested_at timestamptz?` | singleton ID 1; generation positive | PK | permanent singleton |
| `providers` | `id text! PK`; `name text!`; `content_hash text!`; `first_seen_at timestamptz!`; `last_seen_at timestamptz!`; `updated_at timestamptz!` | hash 64 lowercase hex | PK | none |
| `theaters` | `id text! PK`; `provider_id text!`; `source_key text!`; `region text!`; `name text!`; `active boolean! = true`; `content_hash text!`; `first_seen_at timestamptz!`; `last_seen_at timestamptz!`; `updated_at timestamptz!` | provider FK RESTRICT; hash format; unique provider/source | browse | none |
| `movies` | `id text! PK`; `provider_id text!`; `source_key text!`; `title text!`; `poster_url text! = ''`; `display_order integer! = 2147483647`; `active boolean! = true`; `content_hash text!`; `first_seen_at timestamptz!`; `last_seen_at timestamptz!`; `updated_at timestamptz!` | provider FK RESTRICT; display order nonnegative; hash format; unique provider/source; historical master rows are never deleted by retention | Client browse requires a current future showtime; operator history uses the permanent row | none |
| `auditoriums` | `id text! PK`; `theater_id text!`; `source_key text!`; `name text!`; `screen_types text[]! = '{}'`; `capacity integer! = 0`; `active boolean! = true`; `content_hash text!`; `first_seen_at timestamptz!`; `last_seen_at timestamptz!`; `updated_at timestamptz!`; `current_seat_map_version_id text?`; `seat_map_requested_at timestamptz?` | theater and current seat-map FKs RESTRICT; capacity nonnegative; unique theater/source | browse; conditional missing-seat-map | none |
| `showtimes` | `id text! PK`; `provider_id text!`; `source_key text!`; `theater_id text!`; `movie_id text!`; `auditorium_id text!`; `schedule_date date!`; `starts_at timestamptz!`; `ends_at timestamptz!`; `active boolean! = true`; `content_hash text!`; `first_seen_at timestamptz!`; `last_seen_at timestamptz!`; `updated_at timestamptz!` | all entity FKs RESTRICT; provider schedule date is not inferred from civil start time; end after start; hash format; unique provider/source/start | browse + theater/schedule date | none |
| `seat_map_versions` | `id text! PK`; `auditorium_id text!`; `layout_hash text!`; `capacity integer!`; `observed_at timestamptz!`; `first_seen_at timestamptz!`; `last_seen_at timestamptz!` | auditorium FK RESTRICT; hash format; capacity positive; unique auditorium/layout hash | uniqueness | none |
| `seat_map_seats` | `version_id text!`; `position integer!`; `seat_id text!`; `label text!`; `row_label text!`; `seat_number integer!`; `x double precision!`; `y double precision!`; `seat_type text!`; `zone_name text! = ''`; `zone_kind text! = ''`; `sale_form_code text! = ''`; `sale_form_name text! = ''`; `left_aisle boolean! = false`; `right_aisle boolean! = false`; `source_label text! = ''`; `source_seat_kind_code text! = ''`; `source_seat_kind_name text! = ''`; composite PK version/position | version FK CASCADE; position and seat number positive; coordinates normalized; unique version/seat and version/label | row and seat number | version retention |
| `seat_map_seat_features` | `version_id text!`; `seat_id text!`; `position integer!`; `feature text!`; composite PK version/seat/position | composite seat FK CASCADE; positive position; unique version/seat/feature | PK | version retention |
| `seat_map_seat_source_classes` | `version_id text!`; `seat_id text!`; `position integer!`; `source_class text!`; composite PK version/seat/position | composite seat FK CASCADE; positive position; unique version/seat/source class | PK | version retention |
| `seat_map_zones` | `version_id text!`; `position integer!`; `code text! = ''`; `name text! = ''`; `kind_code text! = ''`; `kind_name text! = ''`; `min_x double precision!`; `max_x double precision!`; `min_y double precision!`; `max_y double precision!`; `capacity integer!`; composite PK version/position | version FK CASCADE; normalized ordered bounds; nonnegative capacity | PK | version retention |
| `seat_map_blocks` | `version_id text!`; `position integer!`; `code text! = ''`; `name text! = ''`; `kind_code text! = ''`; `kind_name text! = ''`; `min_x double precision!`; `max_x double precision!`; `min_y double precision!`; `max_y double precision!`; composite PK version/position | version FK CASCADE; normalized ordered bounds | PK | version retention |
| `seat_availability_snapshots` | `id text! PK`; `showtime_id text!`; `auditorium_id text!`; `layout_hash text!`; `content_hash text!`; `observed_at timestamptz!`; `created_at timestamptz!` | showtime and auditorium FKs RESTRICT; hashes are 64 lowercase hex; unique showtime/observed time | showtime history | immutable distinct adjacent live-seat state |
| `seat_availability_snapshot_seats` | `snapshot_id text!`; `position integer!`; `seat_id text!`; composite PK snapshot/position | snapshot FK CASCADE; position positive; seat ID nonempty; unique snapshot/seat | seat/snapshot lookup | snapshot child |
| `monitor_showtime_availability` | `user_id text!`; `monitor_id text!`; `showtime_id text!`; `snapshot_id text?`; `matched boolean!`; `observed_at timestamptz!`; `updated_at timestamptz!`; composite PK user/monitor/showtime | monitor FK CASCADE; showtime and snapshot FKs RESTRICT | showtime/match/observed time | latest per-monitor exact or coarse match state |
| `release_components` | `kind text!`; `channel text!`; `platform text!`; `architecture text!`; `version text!`; `payload jsonb!`; `published_at timestamptz!`; `created_at timestamptz! = now()`; composite PK kind/channel/platform/architecture/version | kind client/browser/playwright/launcher/probe | lookup by target/published | none, immutable |
| `desktop_release_registry_state` | `singleton boolean! PK = true`; `generation bigint!`; `active_manifest_sha256 text!`; `updated_at timestamptz!` | singleton true; generation nonnegative; hash format; generation update notify trigger | PK | permanent singleton |

## Migration fingerprints

The runtime migration table protects already-applied SQL. The focused contract test
also requires every migration filename and SHA-256 below to match, so a schema change
cannot merge without an intentional update to this inventory.

```text
000001_probe_runtime.sql 99da08650bd94a9b6642f56b962c21dfdc428895da71170393e170aff5256acf
000002_reconciler.sql 66118ceab72fabc17118b71b3fc20abddb72746da50a044cf48bedf09e260c3e
000003_client_probe_bootstrap.sql 2ac370b34f59d4e686d5b7bb41979933ea7b7da3eb8e33108adf984c586ee6a3
000004_client_plane.sql 2e18e13e5a998476cc4913279b6df029a29d0322b7eb2c7f277edce33f371e92
000005_client_session_refresh.sql f61dd960d480891f5ec722360c197386ca989d245123de4fbf84a72148200caa
000006_launch_tickets.sql 82fa42e2bbeae9875d4a47e17e3e33dc28d048b9dbc60b8e294cc556d6403ff6
000007_client_executions.sql 96be00b2ab3af714a6a2fe96e46c91b2e3a291d7745b4e8e6b60ace8c517ffc1
000008_collection_policy_subscriptions.sql 04c5ac71ee346cd648ea36a069798dbc1e585044e45312ef35d6ad9630c9ccd7
000009_schedule_intelligence.sql 29f565b5570b9fd27e631996a2046542725fbb65786d2b5df75be93f867fdda3
000010_admin_sessions.sql b6527ade6181b0552209b2945ecda63e7fcabe923b77bd56b5b4f0e759e650a2
000011_client_pins.sql a6c685a4e8030ccf5728b22572ab1c381b064bb35b62d7b503813b840fe36299
000012_admin_credentials.sql b4c529d5295e563236cd71cb9cbe8e9dad41b1580891b6f91449a2383d990db6
000013_release_registry.sql ccb8da3199ad4c184af17c3e5b7e4361feff6e79a92275a14be1993e148615b5
000014_retryable_assignment_results.sql 14bd31068809f10bee4aaa7de72d02e38608be55bc558c68e55fda60ff88f398
000015_client_blobs.sql 32956d62a95f0e5c60a68c4c4bf87536649906127d5d3a51e1d83cf1dffaa3df
000016_launch_ticket_runtime_binding.sql 886fe82534e6843525d19573415b0ba7aaaa272f9adeeba0ecd74eac7e504a8d
000017_durable_client_events.sql 1a305c12e0650bb6b7ec97d982995ecb00d21b679aafb94804ea2448b31ff84b
000018_operator_observations.sql fb82e632ab87f01ccdbbdbc483c2163d730cd3c097a7520d2a4cfcfdf20695cc
000019_catalog_hard_cutover.sql 64731f240daa5bc20f24ea3d0e04b810efa48de37383f8b08ddc232787f67714
000020_catalog_refresh.sql ebba6bd43dbed18f6f3f301baf2da084e41d6c741f4cbd878424a7ebee5feb5c
000021_seat_map_backfill.sql 2d99f002c92f63ab80b3e8f81c69e590127ab83275b21c7314a18e5a3fe1122a
000022_movie_display_order.sql e30629dc790c36f282e889eb2873b7ee1052011e397b9cc6f3ee22c6d5a7c127
000023_observation_movie_identity.sql 67122e9f9a48276838f6152ba3df196af119cf34105a0b6d3235714abb2cc2eb
000024_drop_protocol_versions.sql c637c130635ac0f56230068c406b747f3a51eea2f082f4e6ff76b5ac03ad277c
000025_assignment_task_proto.sql baafb5a48e80c7c872b9c08a4386b7d4c095347931caa832e1c2a1d7635adf8b
000026_release_proto_payloads.sql 5be6660eea7bc8bce5583c9c9156c8ae22a3f323a23bb583a3c5d208360bbc99
000027_normalize_seat_maps.sql de8d5e5b77fb21cdaa55c070a6557a05dbbc1e567dcebb126a5c45a2cc4dad0f
000028_normalize_client_resources.sql 33920f16e7ad35ddc81fc14de69fa3811fe83d05cb64890cef018db7f5fa9899
000029_showtime_schedule_dates.sql adbcbeea805c098b593cf121f627fccbd44b84fab0ebbe9d30160faecc2c6256
000030_seat_availability.sql a79b291109cc2044126d2a75213f93da1dd370b3e3093c90bd74eb83c3397f86
000031_demand_observation_cadence.sql 6c7c761c269f83a21df2799d6ae47a0fcbd30916ea3e4e6b05cc3f7f89507917
```

Migration `000008` creates `observation_policy_subscriptions`, `000015` creates the
legacy blob tables, and later hard-cutover migrations deliberately drop them. They are
therefore fingerprinted history but not current tables.
Migration `000028` is the hard cutover for the six Client resource kinds: it strictly
backfills the latest generated-ProtoJSON into the typed parents and ordered children,
adds `observation_assignments.auditorium_id` and
`client_execution_commands.observed_at`, then drops only the obsolete
`client_resources.payload`. Historical Client resource events are validated against the
same latest message shapes, deleted events become metadata-only tombstones, and
`client_events.payload` plus execution command payloads remain durable event/audit
envelopes. This is a forward-only migration and requires a successful read-only data
preflight plus a recoverable database snapshot before it is applied.
Migration `000029` makes the provider schedule date explicit on current and historical
showtime snapshots, so extended-clock provider identity is never inferred from the civil
start instant. Migration `000030` adds exact-showtime assignment identity and normalized
adjacent live-seat snapshots. The per-monitor state table is the false-to-true edge fence:
unchanged positive snapshots cannot create duplicate execution commands. Migration
`000031` permits the Central-owned 2–5 second booking-demand cadence while preserving
strictly increasing positive interval bounds.
