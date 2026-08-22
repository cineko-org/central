-- The client resource body is the latest generated ProtoJSON contract.  This
-- migration performs a hard cutover: every legacy body is checked before it is
-- removed, and any malformed/unknown field aborts the migration transaction.

CREATE TABLE client_settings (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'settings'),
    id text NOT NULL,
    network_mode text CHECK (network_mode IN ('direct', 'proxy')),
    proxy_username text NOT NULL DEFAULT '',
    proxy_password text NOT NULL DEFAULT '',
    proxy_has_password boolean NOT NULL DEFAULT false,
    PRIMARY KEY (user_id, resource_kind, id),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, resource_kind, id)
        REFERENCES client_resources (user_id, kind, id) ON DELETE CASCADE,
    CHECK (proxy_has_password OR proxy_password = ''),
    CHECK (network_mode = 'proxy' OR (proxy_username = '' AND proxy_password = '' AND NOT proxy_has_password))
);

CREATE TABLE client_setting_proxy_urls (
    user_id text NOT NULL,
    settings_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    url text NOT NULL CHECK (url <> ''),
    PRIMARY KEY (user_id, settings_id, position),
    FOREIGN KEY (user_id, settings_id)
        REFERENCES client_settings (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_setting_webhooks (
    user_id text NOT NULL,
    settings_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    id text NOT NULL,
    name text NOT NULL DEFAULT '',
    url text NOT NULL DEFAULT '',
    secret text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT false,
    has_secret boolean NOT NULL DEFAULT false,
    PRIMARY KEY (user_id, settings_id, position),
    FOREIGN KEY (user_id, settings_id)
        REFERENCES client_settings (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_setting_webhook_event_kinds (
    user_id text NOT NULL,
    settings_id text NOT NULL,
    webhook_position integer NOT NULL CHECK (webhook_position >= 0),
    position integer NOT NULL CHECK (position >= 0),
    event_kind text NOT NULL CHECK (event_kind <> ''),
    PRIMARY KEY (user_id, settings_id, webhook_position, position),
    FOREIGN KEY (user_id, settings_id, webhook_position)
        REFERENCES client_setting_webhooks (user_id, settings_id, position) ON DELETE CASCADE
);

CREATE TABLE client_presets (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'presets'),
    id text NOT NULL,
    name text NOT NULL DEFAULT '',
    theater_id text NOT NULL REFERENCES theaters (id) ON DELETE RESTRICT,
    auditorium_id text NOT NULL REFERENCES auditoriums (id) ON DELETE RESTRICT,
    seat_count integer NOT NULL CHECK (seat_count >= 0),
    has_seat_preference boolean NOT NULL DEFAULT false,
    together boolean NOT NULL DEFAULT false,
    avoid_edges boolean NOT NULL DEFAULT false,
    preset_created_at timestamptz,
    preset_updated_at timestamptz,
    PRIMARY KEY (user_id, resource_kind, id),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, resource_kind, id)
        REFERENCES client_resources (user_id, kind, id) ON DELETE CASCADE
);

CREATE TABLE client_preset_explicit_seats (
    user_id text NOT NULL,
    preset_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    seat_label text NOT NULL CHECK (seat_label <> ''),
    PRIMARY KEY (user_id, preset_id, position),
    FOREIGN KEY (user_id, preset_id)
        REFERENCES client_presets (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_preset_preferred_rows (
    user_id text NOT NULL,
    preset_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    row_label text NOT NULL CHECK (row_label <> ''),
    PRIMARY KEY (user_id, preset_id, position),
    FOREIGN KEY (user_id, preset_id)
        REFERENCES client_presets (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_preset_preferred_zones (
    user_id text NOT NULL,
    preset_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    name text NOT NULL DEFAULT '',
    min_x double precision NOT NULL DEFAULT 0,
    max_x double precision NOT NULL DEFAULT 0,
    min_y double precision NOT NULL DEFAULT 0,
    max_y double precision NOT NULL DEFAULT 0,
    weight integer NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, preset_id, position),
    FOREIGN KEY (user_id, preset_id)
        REFERENCES client_presets (user_id, id) ON DELETE CASCADE,
    CHECK (min_x <= max_x AND min_y <= max_y)
);

CREATE TABLE client_preset_preferred_types (
    user_id text NOT NULL,
    preset_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    seat_type text NOT NULL CHECK (seat_type <> ''),
    PRIMARY KEY (user_id, preset_id, position),
    FOREIGN KEY (user_id, preset_id)
        REFERENCES client_presets (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_monitors (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'monitors'),
    id text NOT NULL,
    preset_id text NOT NULL,
    movie_id text NOT NULL REFERENCES movies (id) ON DELETE RESTRICT,
    movie_title text NOT NULL DEFAULT '',
    search_horizon_days integer NOT NULL DEFAULT 14 CHECK (search_horizon_days BETWEEN 1 AND 14),
    earliest_minute integer CHECK (earliest_minute BETWEEN 0 AND 1439),
    latest_minute integer CHECK (latest_minute BETWEEN 0 AND 1439),
    state text NOT NULL CHECK (state IN ('pending', 'running', 'triggered', 'booked', 'failed', 'stopped', 'payment-unknown')),
    state_reason text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    reservation_id text,
    monitor_created_at timestamptz,
    monitor_updated_at timestamptz,
    PRIMARY KEY (user_id, resource_kind, id),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, resource_kind, id)
        REFERENCES client_resources (user_id, kind, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, preset_id)
        REFERENCES client_presets (user_id, id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE client_monitor_target_dates (
    user_id text NOT NULL,
    monitor_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    target_date date NOT NULL,
    PRIMARY KEY (user_id, monitor_id, position),
    FOREIGN KEY (user_id, monitor_id)
        REFERENCES client_monitors (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_monitor_target_weekdays (
    user_id text NOT NULL,
    monitor_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    target_weekday smallint NOT NULL CHECK (target_weekday BETWEEN 0 AND 6),
    PRIMARY KEY (user_id, monitor_id, position),
    FOREIGN KEY (user_id, monitor_id)
        REFERENCES client_monitors (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_reservations (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'reservations'),
    id text NOT NULL,
    monitor_id text NOT NULL,
    booking_number text NOT NULL DEFAULT '',
    total_price text NOT NULL DEFAULT '',
    booked_at timestamptz,
    cancelled_at timestamptz,
    refund_amount text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (state IN ('prepared', 'booked', 'cancellation-committing', 'cancellation-unknown', 'cancelled')),
    PRIMARY KEY (user_id, resource_kind, id),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, resource_kind, id)
        REFERENCES client_resources (user_id, kind, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, monitor_id)
        REFERENCES client_monitors (user_id, id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE client_reservation_seats (
    user_id text NOT NULL,
    reservation_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    seat_label text NOT NULL CHECK (seat_label <> ''),
    PRIMARY KEY (user_id, reservation_id, position),
    FOREIGN KEY (user_id, reservation_id)
        REFERENCES client_reservations (user_id, id) ON DELETE CASCADE
);

CREATE TABLE client_reservation_showtimes (
    user_id text NOT NULL,
    reservation_id text NOT NULL,
    showtime_id text NOT NULL,
    provider_id text NOT NULL DEFAULT '',
    source_key text NOT NULL DEFAULT '',
    theater_id text NOT NULL DEFAULT '',
    movie_id text NOT NULL DEFAULT '',
    movie_provider_id text NOT NULL DEFAULT '',
    movie_source_key text NOT NULL DEFAULT '',
    movie_title text NOT NULL DEFAULT '',
    movie_poster_url text NOT NULL DEFAULT '',
    auditorium_id text NOT NULL DEFAULT '',
    auditorium_theater_id text NOT NULL DEFAULT '',
    auditorium_source_key text NOT NULL DEFAULT '',
    auditorium_name text NOT NULL DEFAULT '',
    auditorium_capacity integer NOT NULL DEFAULT 0 CHECK (auditorium_capacity >= 0),
    auditorium_layout_hash text NOT NULL DEFAULT '',
    schedule_date date NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    available_seats integer NOT NULL CHECK (available_seats >= 0),
    capacity integer NOT NULL CHECK (capacity >= 0),
    sold_out boolean NOT NULL,
    PRIMARY KEY (user_id, reservation_id),
    FOREIGN KEY (user_id, reservation_id)
        REFERENCES client_reservations (user_id, id) ON DELETE CASCADE,
    CHECK (ends_at > starts_at),
    CHECK (available_seats <= capacity)
);

CREATE TABLE client_reservation_showtime_screen_types (
    user_id text NOT NULL,
    reservation_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    screen_type text NOT NULL CHECK (screen_type <> ''),
    PRIMARY KEY (user_id, reservation_id, position),
    FOREIGN KEY (user_id, reservation_id)
        REFERENCES client_reservation_showtimes (user_id, reservation_id) ON DELETE CASCADE
);

CREATE TABLE client_external_operations (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'external-operations'),
    id text NOT NULL,
    monitor_id text,
    reservation_id text NOT NULL,
    kind text NOT NULL CHECK (kind = 'cancellation'),
    state text NOT NULL CHECK (state IN ('prepared', 'unknown', 'attention-required', 'confirmed', 'reconciled')),
    refund_amount text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    operation_created_at timestamptz,
    operation_updated_at timestamptz,
    PRIMARY KEY (user_id, resource_kind, id),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, resource_kind, id)
        REFERENCES client_resources (user_id, kind, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, reservation_id)
        REFERENCES client_reservations (user_id, id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE client_app_events (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'app-events'),
    id text NOT NULL,
    kind text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    event_created_at timestamptz,
    tone text NOT NULL CHECK (tone IN ('info', 'success', 'warning', 'error')),
    read_at timestamptz,
    PRIMARY KEY (user_id, resource_kind, id),
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, resource_kind, id)
        REFERENCES client_resources (user_id, kind, id) ON DELETE CASCADE
);

CREATE INDEX client_presets_catalog_target_idx
    ON client_presets (theater_id, auditorium_id, user_id);
CREATE INDEX client_monitors_execution_idx
    ON client_monitors (movie_id, state, user_id, id);
CREATE INDEX client_monitor_target_dates_idx
    ON client_monitor_target_dates (target_date, user_id, monitor_id);
CREATE INDEX client_monitor_target_weekdays_idx
    ON client_monitor_target_weekdays (target_weekday, user_id, monitor_id);
CREATE INDEX client_reservations_monitor_idx
    ON client_reservations (user_id, monitor_id, id);
CREATE INDEX client_external_operations_state_idx
    ON client_external_operations (user_id, state, id);
CREATE INDEX client_app_events_unread_idx
    ON client_app_events (user_id, read_at, id);

ALTER TABLE observation_assignments
    ADD COLUMN auditorium_id text NOT NULL DEFAULT '';

ALTER TABLE client_execution_commands
    ADD COLUMN observed_at timestamptz;

CREATE INDEX client_execution_commands_observed_idx
    ON client_execution_commands (user_id, observed_at DESC);

CREATE OR REPLACE FUNCTION cineko_require_object(value jsonb, context text)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF value IS NULL OR jsonb_typeof(value) <> 'object' THEN
        RAISE EXCEPTION '% must be a JSON object', context;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_backfill_presets()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    resource_row record;
    body jsonb;
    preference jsonb;
    user_id_value text;
    payload_id text;
    name_value text;
    theater_id_value text;
    auditorium_id_value text;
    seat_count_value bigint;
    has_preference boolean;
    together_value boolean;
    avoid_edges_value boolean;
    created_at_value timestamptz;
    updated_at_value timestamptz;
    item record;
    zone jsonb;
BEGIN
    FOR resource_row IN
        SELECT user_id, id, payload
        FROM client_resources
        WHERE kind = 'presets'
    LOOP
        body := resource_row.payload;
        PERFORM cineko_require_keys(body, 'client preset ' || resource_row.id,
            ARRAY['id', 'userId', 'name', 'theaterId', 'auditoriumId', 'seatCount',
                  'seatPreference', 'createdAt', 'updatedAt']);
        payload_id := cineko_json_text(body->'id', 'client preset.id', true);
        user_id_value := cineko_json_text(body->'userId', 'client preset.userId', true);
        IF payload_id <> resource_row.id OR user_id_value <> resource_row.user_id THEN
            RAISE EXCEPTION 'client preset % identity does not match client_resources', resource_row.id;
        END IF;
        name_value := COALESCE(cineko_json_text(body->'name', 'client preset.name', false), '');
        theater_id_value := cineko_json_text(body->'theaterId', 'client preset.theaterId', true);
        auditorium_id_value := cineko_json_text(body->'auditoriumId', 'client preset.auditoriumId', true);
        seat_count_value := COALESCE(cineko_json_integer(body->'seatCount', 'client preset.seatCount', false), 0);
        IF seat_count_value < 0 OR seat_count_value > 2147483647 THEN
            RAISE EXCEPTION 'client preset % seatCount is outside integer range', resource_row.id;
        END IF;
        has_preference := body ? 'seatPreference' AND jsonb_typeof(body->'seatPreference') <> 'null';
        together_value := false;
        avoid_edges_value := false;
        IF has_preference THEN
            preference := body->'seatPreference';
            PERFORM cineko_require_keys(preference, 'client preset seatPreference',
                ARRAY['explicitSeats', 'preferredRows', 'preferredZones', 'preferredTypes', 'together', 'avoidEdges']);
            together_value := COALESCE(cineko_json_boolean(preference->'together',
                'client preset seatPreference.together', false), false);
            avoid_edges_value := COALESCE(cineko_json_boolean(preference->'avoidEdges',
                'client preset seatPreference.avoidEdges', false), false);
        END IF;
        created_at_value := cineko_json_timestamp(body->'createdAt', 'client preset.createdAt', false);
        updated_at_value := cineko_json_timestamp(body->'updatedAt', 'client preset.updatedAt', false);
        INSERT INTO client_presets (
            user_id, resource_kind, id, name, theater_id, auditorium_id, seat_count,
            has_seat_preference, together, avoid_edges, preset_created_at, preset_updated_at
        ) VALUES (
            resource_row.user_id, 'presets', resource_row.id, name_value, theater_id_value,
            auditorium_id_value, seat_count_value::integer, has_preference, together_value,
            avoid_edges_value, created_at_value, updated_at_value
        );
        IF NOT has_preference THEN
            CONTINUE;
        END IF;
        IF preference ? 'explicitSeats' THEN
            IF jsonb_typeof(preference->'explicitSeats') <> 'array' THEN
                RAISE EXCEPTION 'client preset % explicitSeats must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(preference->'explicitSeats') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                INSERT INTO client_preset_explicit_seats (user_id, preset_id, position, seat_label)
                VALUES (resource_row.user_id, resource_row.id, item.item_position,
                    cineko_json_text(item.value, 'client preset explicit seat', true));
            END LOOP;
        END IF;
        IF preference ? 'preferredRows' THEN
            IF jsonb_typeof(preference->'preferredRows') <> 'array' THEN
                RAISE EXCEPTION 'client preset % preferredRows must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(preference->'preferredRows') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                INSERT INTO client_preset_preferred_rows (user_id, preset_id, position, row_label)
                VALUES (resource_row.user_id, resource_row.id, item.item_position,
                    cineko_json_text(item.value, 'client preset preferred row', true));
            END LOOP;
        END IF;
        IF preference ? 'preferredTypes' THEN
            IF jsonb_typeof(preference->'preferredTypes') <> 'array' THEN
                RAISE EXCEPTION 'client preset % preferredTypes must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(preference->'preferredTypes') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                INSERT INTO client_preset_preferred_types (user_id, preset_id, position, seat_type)
                VALUES (resource_row.user_id, resource_row.id, item.item_position,
                    cineko_json_text(item.value, 'client preset preferred type', true));
            END LOOP;
        END IF;
        IF preference ? 'preferredZones' THEN
            IF jsonb_typeof(preference->'preferredZones') <> 'array' THEN
                RAISE EXCEPTION 'client preset % preferredZones must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(preference->'preferredZones') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                zone := item.value;
                PERFORM cineko_require_keys(zone, 'client preset preferred zone',
                    ARRAY['name', 'minX', 'maxX', 'minY', 'maxY', 'weight']);
                INSERT INTO client_preset_preferred_zones (
                    user_id, preset_id, position, name, min_x, max_x, min_y, max_y, weight
                ) VALUES (
                    resource_row.user_id, resource_row.id, item.item_position,
                    COALESCE(cineko_json_text(zone->'name', 'client preset zone.name', false), ''),
                    COALESCE(cineko_json_number(zone->'minX', 'client preset zone.minX', false), 0),
                    COALESCE(cineko_json_number(zone->'maxX', 'client preset zone.maxX', false), 0),
                    COALESCE(cineko_json_number(zone->'minY', 'client preset zone.minY', false), 0),
                    COALESCE(cineko_json_number(zone->'maxY', 'client preset zone.maxY', false), 0),
                    COALESCE(cineko_json_integer(zone->'weight', 'client preset zone.weight', false), 0)::integer
                );
            END LOOP;
        END IF;
    END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_require_keys(value jsonb, context text, allowed text[])
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    unknown_key text;
BEGIN
    PERFORM cineko_require_object(value, context);
    SELECT key INTO unknown_key
    FROM jsonb_object_keys(value) AS key
    WHERE NOT (key = ANY (allowed))
    LIMIT 1;
    IF unknown_key IS NOT NULL THEN
        RAISE EXCEPTION '% contains unknown field %', context, unknown_key;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_text(value jsonb, context text, required boolean)
RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    result text;
BEGIN
    IF value IS NULL OR jsonb_typeof(value) = 'null' THEN
        IF required THEN
            RAISE EXCEPTION '% is required', context;
        END IF;
        RETURN NULL;
    END IF;
    IF jsonb_typeof(value) <> 'string' THEN
        RAISE EXCEPTION '% must be a JSON string', context;
    END IF;
    result := value #>> '{}';
    IF required AND result = '' THEN
        RAISE EXCEPTION '% must not be empty', context;
    END IF;
    RETURN result;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_integer(value jsonb, context text, required boolean)
RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
    numeric_value numeric;
BEGIN
    IF value IS NULL OR jsonb_typeof(value) = 'null' THEN
        IF required THEN
            RAISE EXCEPTION '% is required', context;
        END IF;
        RETURN NULL;
    END IF;
    IF jsonb_typeof(value) <> 'number' THEN
        RAISE EXCEPTION '% must be a JSON number', context;
    END IF;
    BEGIN
        numeric_value := (value #>> '{}')::numeric;
    EXCEPTION WHEN others THEN
        RAISE EXCEPTION '% is not a valid integer', context;
    END;
    IF numeric_value <> trunc(numeric_value)
        OR numeric_value < -9223372036854775808::numeric
        OR numeric_value > 9223372036854775807::numeric THEN
        RAISE EXCEPTION '% is not a valid integer', context;
    END IF;
    RETURN numeric_value::bigint;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_number(value jsonb, context text, required boolean)
RETURNS double precision
LANGUAGE plpgsql
AS $$
DECLARE
    numeric_value numeric;
BEGIN
    IF value IS NULL OR jsonb_typeof(value) = 'null' THEN
        IF required THEN
            RAISE EXCEPTION '% is required', context;
        END IF;
        RETURN NULL;
    END IF;
    IF jsonb_typeof(value) <> 'number' THEN
        RAISE EXCEPTION '% must be a JSON number', context;
    END IF;
    BEGIN
        numeric_value := (value #>> '{}')::numeric;
    EXCEPTION WHEN others THEN
        RAISE EXCEPTION '% is not a valid number', context;
    END;
    RETURN numeric_value::double precision;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_boolean(value jsonb, context text, required boolean)
RETURNS boolean
LANGUAGE plpgsql
AS $$
BEGIN
    IF value IS NULL OR jsonb_typeof(value) = 'null' THEN
        IF required THEN
            RAISE EXCEPTION '% is required', context;
        END IF;
        RETURN NULL;
    END IF;
    IF jsonb_typeof(value) <> 'boolean' THEN
        RAISE EXCEPTION '% must be a JSON boolean', context;
    END IF;
    RETURN (value #>> '{}')::boolean;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_timestamp(value jsonb, context text, required boolean)
RETURNS timestamptz
LANGUAGE plpgsql
AS $$
DECLARE
    text_value text;
    result timestamptz;
BEGIN
    text_value := cineko_json_text(value, context, required);
    IF text_value IS NULL THEN
        RETURN NULL;
    END IF;
    BEGIN
        result := text_value::timestamptz;
    EXCEPTION WHEN others THEN
        RAISE EXCEPTION '% is not a valid RFC3339 timestamp', context;
    END;
    RETURN result;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_duration_nanos(value jsonb, context text, required boolean)
RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
    text_value text;
    sign numeric := 1;
    seconds numeric;
    fraction text;
    result numeric;
BEGIN
    text_value := cineko_json_text(value, context, required);
    IF text_value IS NULL THEN
        RETURN NULL;
    END IF;
    IF text_value !~ '^-?[0-9]+(\.[0-9]{1,9})?s$' THEN
        RAISE EXCEPTION '% must use canonical protobuf duration syntax', context;
    END IF;
    IF left(text_value, 1) = '-' THEN
        sign := -1;
        text_value := substr(text_value, 2);
    END IF;
    text_value := left(text_value, length(text_value) - 1);
    IF position('.' IN text_value) > 0 THEN
        seconds := split_part(text_value, '.', 1)::numeric;
        fraction := split_part(text_value, '.', 2);
        result := seconds * 1000000000
            + (fraction || repeat('0', 9 - length(fraction)))::numeric;
    ELSE
        result := text_value::numeric * 1000000000;
    END IF;
    result := result * sign;
    IF result < -9223372036854775808::numeric
        OR result > 9223372036854775807::numeric THEN
        RAISE EXCEPTION '% is outside bigint nanoseconds range', context;
    END IF;
    RETURN result::bigint;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_local_date(value jsonb, context text)
RETURNS date
LANGUAGE plpgsql
AS $$
DECLARE
    year_value bigint;
    month_value bigint;
    day_value bigint;
BEGIN
    PERFORM cineko_require_keys(value, context, ARRAY['year', 'month', 'day']);
    year_value := cineko_json_integer(value->'year', context || '.year', true);
    month_value := cineko_json_integer(value->'month', context || '.month', true);
    day_value := cineko_json_integer(value->'day', context || '.day', true);
    BEGIN
        RETURN make_date(year_value::integer, month_value::integer, day_value::integer);
    EXCEPTION WHEN others THEN
        RAISE EXCEPTION '% is not a valid calendar date', context;
    END;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_local_minutes(value jsonb, context text)
RETURNS integer
LANGUAGE plpgsql
AS $$
DECLARE
    hour_value bigint := 0;
    minute_value bigint := 0;
BEGIN
    PERFORM cineko_require_keys(value, context, ARRAY['hour', 'minute']);
    IF value ? 'hour' THEN
        hour_value := cineko_json_integer(value->'hour', context || '.hour', true);
    END IF;
    IF value ? 'minute' THEN
        minute_value := cineko_json_integer(value->'minute', context || '.minute', true);
    END IF;
    IF hour_value NOT BETWEEN 0 AND 23 OR minute_value NOT BETWEEN 0 AND 59 THEN
        RAISE EXCEPTION '% is not a valid local time', context;
    END IF;
    RETURN (hour_value * 60 + minute_value)::integer;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_oneof(value jsonb, context text, choices text[])
RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    choice text;
    member_count integer;
BEGIN
    PERFORM cineko_require_keys(value, context, choices);
    SELECT count(*), min(key) INTO member_count, choice
    FROM jsonb_object_keys(value) AS key;
    IF member_count <> 1 OR NOT (choice = ANY (choices)) THEN
        RAISE EXCEPTION '% must contain exactly one oneof member', context;
    END IF;
    PERFORM cineko_require_object(value->choice, context || '.' || choice);
    RETURN choice;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_json_oneof_fields(value jsonb, context text, choices text[])
RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    choice text;
    member_count integer;
BEGIN
    PERFORM cineko_require_object(value, context);
    SELECT count(*), min(candidate)
    INTO member_count, choice
    FROM unnest(choices) AS candidate
    WHERE value ? candidate;
    IF member_count <> 1 THEN
        RAISE EXCEPTION '% must contain exactly one oneof member', context;
    END IF;
    PERFORM cineko_require_object(value->choice, context || '.' || choice);
    RETURN choice;
END;
$$;

CREATE OR REPLACE FUNCTION cineko_validate_theater(value jsonb, context text)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM cineko_require_keys(value, context,
        ARRAY['id', 'providerId', 'sourceKey', 'region', 'name']);
    PERFORM cineko_json_text(value->'id', context || '.id', true);
    PERFORM cineko_json_text(value->'providerId', context || '.providerId', false);
    PERFORM cineko_json_text(value->'sourceKey', context || '.sourceKey', false);
    PERFORM cineko_json_text(value->'region', context || '.region', false);
    PERFORM cineko_json_text(value->'name', context || '.name', false);
END;
$$;

CREATE OR REPLACE FUNCTION cineko_validate_movie(value jsonb, context text)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM cineko_require_keys(value, context,
        ARRAY['id', 'providerId', 'sourceKey', 'title', 'posterUrl']);
    PERFORM cineko_json_text(value->'id', context || '.id', true);
    PERFORM cineko_json_text(value->'providerId', context || '.providerId', false);
    PERFORM cineko_json_text(value->'sourceKey', context || '.sourceKey', false);
    PERFORM cineko_json_text(value->'title', context || '.title', false);
    PERFORM cineko_json_text(value->'posterUrl', context || '.posterUrl', false);
END;
$$;

CREATE OR REPLACE FUNCTION cineko_validate_auditorium(value jsonb, context text)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    screen_type jsonb;
    capacity_value bigint;
BEGIN
    PERFORM cineko_require_keys(value, context,
        ARRAY['id', 'theaterId', 'sourceKey', 'name', 'screenTypes', 'capacity', 'currentLayoutHash']);
    PERFORM cineko_json_text(value->'id', context || '.id', true);
    PERFORM cineko_json_text(value->'theaterId', context || '.theaterId', false);
    PERFORM cineko_json_text(value->'sourceKey', context || '.sourceKey', false);
    PERFORM cineko_json_text(value->'name', context || '.name', false);
    IF value ? 'screenTypes' THEN
        IF jsonb_typeof(value->'screenTypes') <> 'array' THEN
            RAISE EXCEPTION '% .screenTypes must be an array', context;
        END IF;
        FOR screen_type IN SELECT jsonb_array_elements(value->'screenTypes') LOOP
            PERFORM cineko_json_text(screen_type, context || '.screenTypes[]', true);
        END LOOP;
    END IF;
    capacity_value := cineko_json_integer(value->'capacity', context || '.capacity', false);
    IF capacity_value IS NOT NULL AND capacity_value < 0 THEN
        RAISE EXCEPTION '% .capacity must be non-negative', context;
    END IF;
    PERFORM cineko_json_text(value->'currentLayoutHash', context || '.currentLayoutHash', false);
END;
$$;

CREATE OR REPLACE FUNCTION cineko_validate_showtime(value jsonb, context text)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    starts_at_value timestamptz;
    ends_at_value timestamptz;
    available_value bigint := 0;
    capacity_value bigint := 0;
BEGIN
    PERFORM cineko_require_keys(value, context,
        ARRAY['id', 'providerId', 'sourceKey', 'theaterId', 'movie', 'auditorium',
              'scheduleDate', 'startsAt', 'endsAt', 'availableSeats', 'capacity', 'soldOut']);
    PERFORM cineko_json_text(value->'id', context || '.id', true);
    PERFORM cineko_json_text(value->'providerId', context || '.providerId', false);
    PERFORM cineko_json_text(value->'sourceKey', context || '.sourceKey', false);
    PERFORM cineko_json_text(value->'theaterId', context || '.theaterId', false);
    PERFORM cineko_validate_movie(value->'movie', context || '.movie');
    PERFORM cineko_validate_auditorium(value->'auditorium', context || '.auditorium');
    PERFORM cineko_json_local_date(value->'scheduleDate', context || '.scheduleDate');
    starts_at_value := cineko_json_timestamp(value->'startsAt', context || '.startsAt', true);
    ends_at_value := cineko_json_timestamp(value->'endsAt', context || '.endsAt', true);
    available_value := COALESCE(cineko_json_integer(value->'availableSeats', context || '.availableSeats', false), 0);
    capacity_value := COALESCE(cineko_json_integer(value->'capacity', context || '.capacity', false), 0);
    IF available_value < 0 OR capacity_value < 0 OR available_value > capacity_value THEN
        RAISE EXCEPTION '% has invalid availability/capacity', context;
    END IF;
    IF ends_at_value <= starts_at_value THEN
        RAISE EXCEPTION '% has a non-positive duration', context;
    END IF;
    PERFORM cineko_json_boolean(value->'soldOut', context || '.soldOut', false);
END;
$$;

CREATE OR REPLACE FUNCTION cineko_validate_assignment_task(value jsonb, task_kind_value text)
RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    task_key text;
    task_value jsonb;
    date_value jsonb;
    auditorium_value jsonb;
    auditorium_id_value text;
BEGIN
    PERFORM cineko_require_keys(value, 'observation assignment task_data',
        ARRAY['egress', 'schedule', 'catalog', 'seatMap']);
    IF NOT value ? 'egress' THEN
        RAISE EXCEPTION 'observation assignment task_data.egress is required';
    END IF;
    PERFORM cineko_require_keys(value->'egress', 'observation assignment task_data.egress',
        ARRAY['direct', 'managedScan']);
    PERFORM cineko_json_oneof(value->'egress', 'observation assignment task_data.egress',
        ARRAY['direct', 'managedScan']);
    task_key := CASE task_kind_value
        WHEN 'cgv.schedule.capture' THEN 'schedule'
        WHEN 'cgv.catalog.capture' THEN 'catalog'
        WHEN 'cgv.seat-map.capture' THEN 'seatMap'
        ELSE NULL
    END;
    IF task_key IS NULL OR NOT value ? task_key THEN
        RAISE EXCEPTION 'unsupported or mismatched observation task kind %', task_kind_value;
    END IF;
    task_value := value->task_key;
    IF task_key = 'seatMap' THEN
        PERFORM cineko_require_keys(task_value, 'observation assignment task_data.seatMap',
            ARRAY['theater', 'auditorium', 'showtime', 'locale', 'timeZone', 'targetDates']);
    ELSE
        PERFORM cineko_require_keys(task_value, 'observation assignment task_data.' || task_key,
            ARRAY['theater', 'targetDates', 'locale', 'timeZone']);
    END IF;
    PERFORM cineko_validate_theater(task_value->'theater',
        'observation assignment task_data.' || task_key || '.theater');
    IF task_value ? 'targetDates' THEN
        IF jsonb_typeof(task_value->'targetDates') <> 'array' THEN
            RAISE EXCEPTION 'observation assignment % .targetDates must be an array', task_key;
        END IF;
        FOR date_value IN SELECT jsonb_array_elements(task_value->'targetDates') LOOP
            PERFORM cineko_json_local_date(date_value,
                'observation assignment task_data.' || task_key || '.targetDates[]');
        END LOOP;
    END IF;
    PERFORM cineko_json_text(task_value->'locale',
        'observation assignment task_data.' || task_key || '.locale', false);
    PERFORM cineko_json_text(task_value->'timeZone',
        'observation assignment task_data.' || task_key || '.timeZone', false);
    IF task_key = 'seatMap' THEN
        auditorium_value := task_value->'auditorium';
        PERFORM cineko_validate_auditorium(auditorium_value,
            'observation assignment task_data.seatMap.auditorium');
        auditorium_id_value := cineko_json_text(auditorium_value->'id',
            'observation assignment task_data.seatMap.auditorium.id', true);
        IF task_value ? 'showtime' THEN
            PERFORM cineko_validate_showtime(task_value->'showtime',
                'observation assignment task_data.seatMap.showtime');
        END IF;
        RETURN auditorium_id_value;
    END IF;
    RETURN '';
END;
$$;

-- Historical client_events are decoded with the latest generated ProtoJSON
-- contract at runtime. Validate their resource bodies during the same hard
-- cutover that normalizes client_resources. Deleted events are metadata-only
-- tombstones: the runtime reconstructs DeletedResource from the event row, so
-- their payload is canonicalized to {} without changing append-only metadata.
CREATE OR REPLACE FUNCTION cineko_validate_client_event_resource(
    value jsonb,
    kind_value text,
    event_user_id text,
    resource_id_value text,
    resource_revision_value bigint
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    body_value jsonb;
    item_value jsonb;
    mode_name text;
    state_name text;
    payload_id text;
    payload_user_id text;
BEGIN
    IF resource_revision_value <= 0 THEN
        RAISE EXCEPTION 'client event resource revision must be positive: %', resource_revision_value;
    END IF;

    CASE kind_value
        WHEN 'settings' THEN
            PERFORM cineko_require_keys(value, 'client settings event', ARRAY['network', 'webhooks']);
            IF value ? 'network' AND jsonb_typeof(value->'network') <> 'null' THEN
                body_value := value->'network';
                PERFORM cineko_require_keys(body_value, 'client settings event network', ARRAY['direct', 'proxy']);
                mode_name := cineko_json_oneof(body_value, 'client settings event network', ARRAY['direct', 'proxy']);
                IF mode_name = 'proxy' THEN
                    PERFORM cineko_require_keys(body_value->'proxy', 'client settings event proxy', ARRAY['urls', 'username', 'password', 'hasPassword']);
                    PERFORM cineko_json_text(body_value->'proxy'->'username', 'client settings event proxy.username', false);
                    PERFORM cineko_json_text(body_value->'proxy'->'password', 'client settings event proxy.password', false);
                    PERFORM cineko_json_boolean(body_value->'proxy'->'hasPassword', 'client settings event proxy.hasPassword', false);
                    IF body_value->'proxy' ? 'urls' AND jsonb_typeof(body_value->'proxy'->'urls') <> 'null' THEN
                        IF jsonb_typeof(body_value->'proxy'->'urls') <> 'array' THEN
                            RAISE EXCEPTION 'client settings event proxy.urls must be an array';
                        END IF;
                        FOR item_value IN SELECT jsonb_array_elements(body_value->'proxy'->'urls') LOOP
                            PERFORM cineko_json_text(item_value, 'client settings event proxy.urls[]', true);
                        END LOOP;
                    END IF;
                END IF;
            END IF;
            IF value ? 'webhooks' AND jsonb_typeof(value->'webhooks') <> 'null' THEN
                IF jsonb_typeof(value->'webhooks') <> 'array' THEN
                    RAISE EXCEPTION 'client settings event webhooks must be an array';
                END IF;
                FOR item_value IN SELECT jsonb_array_elements(value->'webhooks') LOOP
                    PERFORM cineko_require_keys(item_value, 'client settings event webhook', ARRAY['id', 'name', 'url', 'secret', 'eventKinds', 'enabled', 'hasSecret']);
                    PERFORM cineko_json_text(item_value->'id', 'client settings event webhook.id', true);
                    PERFORM cineko_json_text(item_value->'name', 'client settings event webhook.name', false);
                    PERFORM cineko_json_text(item_value->'url', 'client settings event webhook.url', false);
                    PERFORM cineko_json_text(item_value->'secret', 'client settings event webhook.secret', false);
                    PERFORM cineko_json_boolean(item_value->'enabled', 'client settings event webhook.enabled', false);
                    PERFORM cineko_json_boolean(item_value->'hasSecret', 'client settings event webhook.hasSecret', false);
                    IF item_value ? 'eventKinds' AND jsonb_typeof(item_value->'eventKinds') <> 'null' THEN
                        IF jsonb_typeof(item_value->'eventKinds') <> 'array' THEN
                            RAISE EXCEPTION 'client settings event webhook.eventKinds must be an array';
                        END IF;
                        FOR body_value IN SELECT jsonb_array_elements(item_value->'eventKinds') LOOP
                            PERFORM cineko_json_text(body_value, 'client settings event webhook.eventKinds[]', true);
                        END LOOP;
                    END IF;
                END LOOP;
            END IF;

        WHEN 'presets' THEN
            PERFORM cineko_require_keys(value, 'client preset event', ARRAY['id', 'userId', 'name', 'theaterId', 'auditoriumId', 'seatCount', 'seatPreference', 'createdAt', 'updatedAt']);
            payload_id := cineko_json_text(value->'id', 'client preset event.id', true);
            payload_user_id := cineko_json_text(value->'userId', 'client preset event.userId', true);
            IF payload_id <> resource_id_value OR payload_user_id <> event_user_id THEN
                RAISE EXCEPTION 'client preset event identity does not match event metadata';
            END IF;
            PERFORM cineko_json_text(value->'name', 'client preset event.name', true);
            PERFORM cineko_json_text(value->'theaterId', 'client preset event.theaterId', true);
            PERFORM cineko_json_text(value->'auditoriumId', 'client preset event.auditoriumId', true);
            PERFORM cineko_json_integer(value->'seatCount', 'client preset event.seatCount', false);
            IF value ? 'seatPreference' AND jsonb_typeof(value->'seatPreference') <> 'null' THEN
                body_value := value->'seatPreference';
                PERFORM cineko_require_keys(body_value, 'client preset event.seatPreference', ARRAY['explicitSeats', 'preferredRows', 'preferredZones', 'preferredTypes', 'together', 'avoidEdges']);
                PERFORM cineko_json_boolean(body_value->'together', 'client preset event.seatPreference.together', false);
                PERFORM cineko_json_boolean(body_value->'avoidEdges', 'client preset event.seatPreference.avoidEdges', false);
                IF body_value ? 'preferredRows' AND jsonb_typeof(body_value->'preferredRows') <> 'null' THEN
                    IF jsonb_typeof(body_value->'preferredRows') <> 'array' THEN RAISE EXCEPTION 'client preset event preferredRows must be an array'; END IF;
                    FOR item_value IN SELECT jsonb_array_elements(body_value->'preferredRows') LOOP PERFORM cineko_json_text(item_value, 'client preset event preferredRows[]', true); END LOOP;
                END IF;
                IF body_value ? 'explicitSeats' AND jsonb_typeof(body_value->'explicitSeats') <> 'null' THEN
                    IF jsonb_typeof(body_value->'explicitSeats') <> 'array' THEN RAISE EXCEPTION 'client preset event explicitSeats must be an array'; END IF;
                    FOR item_value IN SELECT jsonb_array_elements(body_value->'explicitSeats') LOOP PERFORM cineko_json_text(item_value, 'client preset event explicitSeats[]', true); END LOOP;
                END IF;
                IF body_value ? 'preferredTypes' AND jsonb_typeof(body_value->'preferredTypes') <> 'null' THEN
                    IF jsonb_typeof(body_value->'preferredTypes') <> 'array' THEN RAISE EXCEPTION 'client preset event preferredTypes must be an array'; END IF;
                    FOR item_value IN SELECT jsonb_array_elements(body_value->'preferredTypes') LOOP PERFORM cineko_json_text(item_value, 'client preset event preferredTypes[]', true); END LOOP;
                END IF;
                IF body_value ? 'preferredZones' AND jsonb_typeof(body_value->'preferredZones') <> 'null' THEN
                    IF jsonb_typeof(body_value->'preferredZones') <> 'array' THEN RAISE EXCEPTION 'client preset event preferredZones must be an array'; END IF;
                    FOR item_value IN SELECT jsonb_array_elements(body_value->'preferredZones') LOOP
                        PERFORM cineko_require_keys(item_value, 'client preset event preferred zone', ARRAY['name', 'minX', 'maxX', 'minY', 'maxY', 'weight']);
                        PERFORM cineko_json_text(item_value->'name', 'client preset event preferred zone.name', true);
                        PERFORM cineko_json_number(item_value->'minX', 'client preset event preferred zone.minX', false);
                        PERFORM cineko_json_number(item_value->'maxX', 'client preset event preferred zone.maxX', false);
                        PERFORM cineko_json_number(item_value->'minY', 'client preset event preferred zone.minY', false);
                        PERFORM cineko_json_number(item_value->'maxY', 'client preset event preferred zone.maxY', false);
                        PERFORM cineko_json_integer(item_value->'weight', 'client preset event preferred zone.weight', false);
                    END LOOP;
                END IF;
            END IF;
            PERFORM cineko_json_timestamp(value->'createdAt', 'client preset event.createdAt', false);
            PERFORM cineko_json_timestamp(value->'updatedAt', 'client preset event.updatedAt', false);

        WHEN 'monitors' THEN
            PERFORM cineko_require_keys(value, 'client monitor event', ARRAY['id', 'userId', 'presetId', 'movieId', 'movieTitle', 'targetDates', 'targetWeekdays', 'searchHorizonDays', 'earliestTime', 'latestTime', 'state', 'lastCheckedAt', 'reservationId', 'createdAt', 'updatedAt']);
            payload_id := cineko_json_text(value->'id', 'client monitor event.id', true);
            payload_user_id := cineko_json_text(value->'userId', 'client monitor event.userId', true);
            IF payload_id <> resource_id_value OR payload_user_id <> event_user_id THEN
                RAISE EXCEPTION 'client monitor event identity does not match event metadata';
            END IF;
            PERFORM cineko_json_text(value->'presetId', 'client monitor event.presetId', true);
            PERFORM cineko_json_text(value->'movieId', 'client monitor event.movieId', true);
            PERFORM cineko_json_text(value->'movieTitle', 'client monitor event.movieTitle', false);
            PERFORM cineko_json_integer(value->'searchHorizonDays', 'client monitor event.searchHorizonDays', false);
            PERFORM cineko_json_timestamp(value->'lastCheckedAt', 'client monitor event.lastCheckedAt', false);
            IF value ? 'targetDates' AND jsonb_typeof(value->'targetDates') <> 'null' THEN
                IF jsonb_typeof(value->'targetDates') <> 'array' THEN RAISE EXCEPTION 'client monitor event targetDates must be an array'; END IF;
                FOR item_value IN SELECT jsonb_array_elements(value->'targetDates') LOOP PERFORM cineko_json_local_date(item_value, 'client monitor event.targetDates[]'); END LOOP;
            END IF;
            IF value ? 'targetWeekdays' AND jsonb_typeof(value->'targetWeekdays') <> 'null' THEN
                IF jsonb_typeof(value->'targetWeekdays') <> 'array' THEN RAISE EXCEPTION 'client monitor event targetWeekdays must be an array'; END IF;
                FOR item_value IN SELECT jsonb_array_elements(value->'targetWeekdays') LOOP PERFORM cineko_json_integer(item_value, 'client monitor event.targetWeekdays[]', true); END LOOP;
            END IF;
            IF value ? 'earliestTime' AND jsonb_typeof(value->'earliestTime') <> 'null' THEN PERFORM cineko_json_local_minutes(value->'earliestTime', 'client monitor event.earliestTime'); END IF;
            IF value ? 'latestTime' AND jsonb_typeof(value->'latestTime') <> 'null' THEN PERFORM cineko_json_local_minutes(value->'latestTime', 'client monitor event.latestTime'); END IF;
            PERFORM cineko_json_text(value->'reservationId', 'client monitor event.reservationId', false);
            PERFORM cineko_json_timestamp(value->'createdAt', 'client monitor event.createdAt', false);
            PERFORM cineko_json_timestamp(value->'updatedAt', 'client monitor event.updatedAt', false);
            state_name := cineko_json_oneof(value->'state', 'client monitor event.state', ARRAY['pending', 'running', 'triggered', 'booked', 'failed', 'stopped', 'paymentUnknown']);
            IF state_name = 'failed' THEN
                PERFORM cineko_require_keys(value->'state'->'failed', 'client monitor event.state.failed', ARRAY['reason']);
                PERFORM cineko_json_text(value->'state'->'failed'->'reason', 'client monitor event.state.failed.reason', false);
            END IF;

        WHEN 'reservations' THEN
            PERFORM cineko_require_keys(value, 'client reservation event', ARRAY['id', 'userId', 'monitorId', 'bookingNumber', 'seatLabels', 'totalPrice', 'bookedAt', 'cancelledAt', 'refundAmount', 'prepared', 'booked', 'cancellationCommitting', 'cancellationUnknown', 'cancelled', 'showtime']);
            payload_id := cineko_json_text(value->'id', 'client reservation event.id', true);
            payload_user_id := cineko_json_text(value->'userId', 'client reservation event.userId', true);
            IF payload_id <> resource_id_value OR payload_user_id <> event_user_id THEN RAISE EXCEPTION 'client reservation event identity does not match event metadata'; END IF;
            PERFORM cineko_json_text(value->'monitorId', 'client reservation event.monitorId', true);
            PERFORM cineko_json_text(value->'bookingNumber', 'client reservation event.bookingNumber', false);
            PERFORM cineko_json_text(value->'totalPrice', 'client reservation event.totalPrice', false);
            PERFORM cineko_json_text(value->'refundAmount', 'client reservation event.refundAmount', false);
            PERFORM cineko_json_timestamp(value->'bookedAt', 'client reservation event.bookedAt', false);
            PERFORM cineko_json_timestamp(value->'cancelledAt', 'client reservation event.cancelledAt', false);
            PERFORM cineko_validate_showtime(value->'showtime', 'client reservation event.showtime');
            IF value ? 'seatLabels' AND jsonb_typeof(value->'seatLabels') <> 'null' THEN
                IF jsonb_typeof(value->'seatLabels') <> 'array' THEN RAISE EXCEPTION 'client reservation event seatLabels must be an array'; END IF;
                FOR item_value IN SELECT jsonb_array_elements(value->'seatLabels') LOOP PERFORM cineko_json_text(item_value, 'client reservation event.seatLabels[]', true); END LOOP;
            END IF;
            state_name := cineko_json_oneof_fields(value, 'client reservation event.state', ARRAY['prepared', 'booked', 'cancellationCommitting', 'cancellationUnknown', 'cancelled']);
            PERFORM cineko_require_keys(value->state_name, 'client reservation event.state.' || state_name, ARRAY[]::text[]);

        WHEN 'external-operations' THEN
            PERFORM cineko_require_keys(value, 'client external operation event', ARRAY['id', 'userId', 'monitorId', 'reservationId', 'refundAmount', 'lastError', 'createdAt', 'updatedAt', 'cancellation', 'prepared', 'unknown', 'attentionRequired', 'confirmed', 'reconciled']);
            payload_id := cineko_json_text(value->'id', 'client external operation event.id', true);
            payload_user_id := cineko_json_text(value->'userId', 'client external operation event.userId', true);
            IF payload_id <> resource_id_value OR payload_user_id <> event_user_id THEN RAISE EXCEPTION 'client external operation event identity does not match event metadata'; END IF;
            PERFORM cineko_json_text(value->'reservationId', 'client external operation event.reservationId', true);
            PERFORM cineko_json_text(value->'monitorId', 'client external operation event.monitorId', false);
            PERFORM cineko_json_text(value->'refundAmount', 'client external operation event.refundAmount', false);
            PERFORM cineko_json_text(value->'lastError', 'client external operation event.lastError', false);
            state_name := cineko_json_oneof_fields(value, 'client external operation event.state', ARRAY['prepared', 'unknown', 'attentionRequired', 'confirmed', 'reconciled']);
            PERFORM cineko_require_keys(value->state_name, 'client external operation event.' || state_name, ARRAY[]::text[]);
            PERFORM cineko_require_keys(value->'cancellation', 'client external operation event.cancellation', ARRAY[]::text[]);
            PERFORM cineko_json_timestamp(value->'createdAt', 'client external operation event.createdAt', false);
            PERFORM cineko_json_timestamp(value->'updatedAt', 'client external operation event.updatedAt', false);

        WHEN 'app-events' THEN
            PERFORM cineko_require_keys(value, 'client app event', ARRAY['id', 'userId', 'kind', 'message', 'createdAt', 'readAt', 'info', 'success', 'warning', 'error']);
            payload_id := cineko_json_text(value->'id', 'client app event.id', true);
            payload_user_id := cineko_json_text(value->'userId', 'client app event.userId', true);
            IF payload_id <> resource_id_value OR payload_user_id <> event_user_id THEN RAISE EXCEPTION 'client app event identity does not match event metadata'; END IF;
            PERFORM cineko_json_text(value->'kind', 'client app event.kind', true);
            PERFORM cineko_json_text(value->'message', 'client app event.message', true);
            state_name := cineko_json_oneof_fields(value, 'client app event.tone', ARRAY['info', 'success', 'warning', 'error']);
            PERFORM cineko_require_keys(value->state_name, 'client app event.' || state_name, ARRAY[]::text[]);
            PERFORM cineko_json_timestamp(value->'createdAt', 'client app event.createdAt', false);

        ELSE
            RAISE EXCEPTION 'unsupported client event resource kind: %', kind_value;
    END CASE;
END;
$$;

SELECT cineko_backfill_presets();

CREATE OR REPLACE FUNCTION cineko_backfill_monitors()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    resource_row record;
    body jsonb;
    state_value jsonb;
    state text;
    state_reason text := '';
    payload_id text;
    user_id_value text;
    preset_id_value text;
    movie_id_value text;
    movie_title_value text;
    reservation_id_value text;
    search_horizon_days_value bigint;
    earliest_minute_value integer;
    latest_minute_value integer;
    last_checked_at_value timestamptz;
    created_at_value timestamptz;
    updated_at_value timestamptz;
    item record;
    date_value date;
    weekday_value bigint;
BEGIN
    FOR resource_row IN
        SELECT user_id, id, payload
        FROM client_resources
        WHERE kind = 'monitors'
    LOOP
        body := resource_row.payload;
        PERFORM cineko_require_keys(body, 'client monitor ' || resource_row.id,
            ARRAY['id', 'userId', 'presetId', 'mode', 'movieId', 'movieTitle', 'targetDates',
                  'targetWeekdays', 'searchHorizonDays', 'earliestTime', 'latestTime',
                  'pollInterval', 'maximumPollInterval', 'state', 'lastCheckedAt',
                  'reservationId', 'createdAt', 'updatedAt']);
        payload_id := cineko_json_text(body->'id', 'client monitor.id', true);
        user_id_value := cineko_json_text(body->'userId', 'client monitor.userId', true);
        IF payload_id <> resource_row.id OR user_id_value <> resource_row.user_id THEN
            RAISE EXCEPTION 'client monitor % identity does not match client_resources', resource_row.id;
        END IF;
        preset_id_value := cineko_json_text(body->'presetId', 'client monitor.presetId', true);
        movie_id_value := cineko_json_text(body->'movieId', 'client monitor.movieId', true);
        movie_title_value := COALESCE(cineko_json_text(body->'movieTitle', 'client monitor.movieTitle', false), '');
        search_horizon_days_value := LEAST(14, GREATEST(1,
            COALESCE(cineko_json_integer(body->'searchHorizonDays',
                'client monitor.searchHorizonDays', false), 14)));
        IF body ? 'earliestTime' AND jsonb_typeof(body->'earliestTime') <> 'null' THEN
            earliest_minute_value := cineko_json_local_minutes(body->'earliestTime', 'client monitor.earliestTime');
        ELSE
            earliest_minute_value := NULL;
        END IF;
        IF body ? 'latestTime' AND jsonb_typeof(body->'latestTime') <> 'null' THEN
            latest_minute_value := cineko_json_local_minutes(body->'latestTime', 'client monitor.latestTime');
        ELSE
            latest_minute_value := NULL;
        END IF;
        state_value := body->'state';
        state := cineko_json_oneof(state_value, 'client monitor.state',
            ARRAY['pending', 'running', 'triggered', 'booked', 'failed', 'stopped', 'paymentUnknown']);
        IF state = 'failed' THEN
            PERFORM cineko_require_keys(state_value->state, 'client monitor.state.failed', ARRAY['reason']);
            state_reason := COALESCE(cineko_json_text(state_value->state->'reason',
                'client monitor.state.failed.reason', false), '');
        ELSE
            PERFORM cineko_require_keys(state_value->state, 'client monitor.state.' || state, ARRAY[]::text[]);
            state_reason := '';
        END IF;
        IF state = 'paymentUnknown' THEN
            state := 'payment-unknown';
        END IF;
        last_checked_at_value := cineko_json_timestamp(body->'lastCheckedAt', 'client monitor.lastCheckedAt', false);
        reservation_id_value := COALESCE(cineko_json_text(body->'reservationId',
            'client monitor.reservationId', false), '');
        created_at_value := cineko_json_timestamp(body->'createdAt', 'client monitor.createdAt', false);
        updated_at_value := cineko_json_timestamp(body->'updatedAt', 'client monitor.updatedAt', false);
        INSERT INTO client_monitors (
            user_id, resource_kind, id, preset_id, movie_id, movie_title,
            search_horizon_days, earliest_minute, latest_minute,
            state, state_reason,
            last_checked_at, reservation_id, monitor_created_at, monitor_updated_at
        ) VALUES (
            resource_row.user_id, 'monitors', resource_row.id, preset_id_value,
            movie_id_value, movie_title_value, search_horizon_days_value::integer,
            earliest_minute_value, latest_minute_value, state, state_reason, last_checked_at_value,
            NULLIF(reservation_id_value, ''), created_at_value, updated_at_value
        );
        IF body ? 'targetDates' THEN
            IF jsonb_typeof(body->'targetDates') <> 'array' THEN
                RAISE EXCEPTION 'client monitor % targetDates must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(body->'targetDates') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                date_value := cineko_json_local_date(item.value, 'client monitor target date');
                INSERT INTO client_monitor_target_dates (user_id, monitor_id, position, target_date)
                VALUES (resource_row.user_id, resource_row.id, item.item_position, date_value);
            END LOOP;
        END IF;
        IF body ? 'targetWeekdays' THEN
            IF jsonb_typeof(body->'targetWeekdays') <> 'array' THEN
                RAISE EXCEPTION 'client monitor % targetWeekdays must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(body->'targetWeekdays') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                weekday_value := cineko_json_integer(item.value, 'client monitor target weekday', true);
                IF weekday_value NOT BETWEEN 0 AND 6 THEN
                    RAISE EXCEPTION 'client monitor % target weekday is outside 0..6', resource_row.id;
                END IF;
                INSERT INTO client_monitor_target_weekdays (user_id, monitor_id, position, target_weekday)
                VALUES (resource_row.user_id, resource_row.id, item.item_position, weekday_value::smallint);
            END LOOP;
        END IF;
    END LOOP;
END;
$$;

SELECT cineko_backfill_monitors();

CREATE OR REPLACE FUNCTION cineko_backfill_reservations()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    resource_row record;
    body jsonb;
    state_value jsonb;
    state text;
    payload_id text;
    user_id_value text;
    monitor_id_value text;
    booking_number_value text;
    total_price_value text;
    refund_amount_value text;
    booked_at_value timestamptz;
    cancelled_at_value timestamptz;
    item record;
    showtime jsonb;
    movie jsonb;
    auditorium jsonb;
    showtime_id_value text;
    provider_id_value text;
    source_key_value text;
    theater_id_value text;
    movie_id_value text;
    movie_provider_id_value text;
    movie_source_key_value text;
    movie_title_value text;
    movie_poster_url_value text;
    auditorium_id_value text;
    auditorium_theater_id_value text;
    auditorium_source_key_value text;
    auditorium_name_value text;
    auditorium_capacity_value bigint;
    auditorium_layout_hash_value text;
    schedule_date_value date;
    starts_at_value timestamptz;
    ends_at_value timestamptz;
    available_seats_value bigint;
    capacity_value bigint;
    sold_out_value boolean;
    screen_type_item jsonb;
BEGIN
    FOR resource_row IN
        SELECT user_id, id, payload
        FROM client_resources
        WHERE kind = 'reservations'
    LOOP
        body := resource_row.payload;
        PERFORM cineko_require_keys(body, 'client reservation ' || resource_row.id,
            ARRAY['id', 'userId', 'monitorId', 'bookingNumber', 'seatLabels', 'totalPrice',
                  'bookedAt', 'cancelledAt', 'refundAmount', 'prepared', 'booked',
                  'cancellationCommitting', 'cancellationUnknown', 'cancelled', 'showtime']);
        payload_id := cineko_json_text(body->'id', 'client reservation.id', true);
        user_id_value := cineko_json_text(body->'userId', 'client reservation.userId', true);
        IF payload_id <> resource_row.id OR user_id_value <> resource_row.user_id THEN
            RAISE EXCEPTION 'client reservation % identity does not match client_resources', resource_row.id;
        END IF;
        monitor_id_value := cineko_json_text(body->'monitorId', 'client reservation.monitorId', true);
        booking_number_value := COALESCE(cineko_json_text(body->'bookingNumber',
            'client reservation.bookingNumber', false), '');
        total_price_value := COALESCE(cineko_json_text(body->'totalPrice',
            'client reservation.totalPrice', false), '');
        refund_amount_value := COALESCE(cineko_json_text(body->'refundAmount',
            'client reservation.refundAmount', false), '');
        booked_at_value := cineko_json_timestamp(body->'bookedAt', 'client reservation.bookedAt', false);
        cancelled_at_value := cineko_json_timestamp(body->'cancelledAt', 'client reservation.cancelledAt', false);
        state := cineko_json_oneof_fields(body, 'client reservation.state',
            ARRAY['prepared', 'booked', 'cancellationCommitting', 'cancellationUnknown', 'cancelled']);
        PERFORM cineko_require_keys(body->state, 'client reservation.state.' || state, ARRAY[]::text[]);
        IF state = 'cancellationCommitting' THEN
            state := 'cancellation-committing';
        ELSIF state = 'cancellationUnknown' THEN
            state := 'cancellation-unknown';
        END IF;
        INSERT INTO client_reservations (
            user_id, resource_kind, id, monitor_id, booking_number, total_price,
            booked_at, cancelled_at, refund_amount, state
        ) VALUES (
            resource_row.user_id, 'reservations', resource_row.id, monitor_id_value,
            booking_number_value, total_price_value, booked_at_value, cancelled_at_value,
            refund_amount_value, state
        );
        IF body ? 'seatLabels' THEN
            IF jsonb_typeof(body->'seatLabels') <> 'array' THEN
                RAISE EXCEPTION 'client reservation % seatLabels must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(body->'seatLabels') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                INSERT INTO client_reservation_seats (user_id, reservation_id, position, seat_label)
                VALUES (resource_row.user_id, resource_row.id, item.item_position,
                    cineko_json_text(item.value, 'client reservation seat label', true));
            END LOOP;
        END IF;
        IF NOT body ? 'showtime' OR jsonb_typeof(body->'showtime') = 'null' THEN
            RAISE EXCEPTION 'client reservation % showtime is required for relational snapshot', resource_row.id;
        END IF;
        showtime := body->'showtime';
        PERFORM cineko_validate_showtime(showtime, 'client reservation showtime');
        movie := showtime->'movie';
        auditorium := showtime->'auditorium';
        showtime_id_value := cineko_json_text(showtime->'id', 'client reservation showtime.id', true);
        provider_id_value := COALESCE(cineko_json_text(showtime->'providerId',
            'client reservation showtime.providerId', false), '');
        source_key_value := COALESCE(cineko_json_text(showtime->'sourceKey',
            'client reservation showtime.sourceKey', false), '');
        theater_id_value := COALESCE(cineko_json_text(showtime->'theaterId',
            'client reservation showtime.theaterId', false), '');
        movie_id_value := cineko_json_text(movie->'id', 'client reservation showtime.movie.id', true);
        movie_provider_id_value := COALESCE(cineko_json_text(movie->'providerId',
            'client reservation showtime.movie.providerId', false), '');
        movie_source_key_value := COALESCE(cineko_json_text(movie->'sourceKey',
            'client reservation showtime.movie.sourceKey', false), '');
        movie_title_value := COALESCE(cineko_json_text(movie->'title',
            'client reservation showtime.movie.title', false), '');
        movie_poster_url_value := COALESCE(cineko_json_text(movie->'posterUrl',
            'client reservation showtime.movie.posterUrl', false), '');
        auditorium_id_value := cineko_json_text(auditorium->'id',
            'client reservation showtime.auditorium.id', true);
        auditorium_theater_id_value := COALESCE(cineko_json_text(auditorium->'theaterId',
            'client reservation showtime.auditorium.theaterId', false), '');
        auditorium_source_key_value := COALESCE(cineko_json_text(auditorium->'sourceKey',
            'client reservation showtime.auditorium.sourceKey', false), '');
        auditorium_name_value := COALESCE(cineko_json_text(auditorium->'name',
            'client reservation showtime.auditorium.name', false), '');
        auditorium_capacity_value := COALESCE(cineko_json_integer(auditorium->'capacity',
            'client reservation showtime.auditorium.capacity', false), 0);
        auditorium_layout_hash_value := COALESCE(cineko_json_text(auditorium->'currentLayoutHash',
            'client reservation showtime.auditorium.currentLayoutHash', false), '');
        schedule_date_value := cineko_json_local_date(showtime->'scheduleDate',
            'client reservation showtime.scheduleDate');
        starts_at_value := cineko_json_timestamp(showtime->'startsAt',
            'client reservation showtime.startsAt', true);
        ends_at_value := cineko_json_timestamp(showtime->'endsAt',
            'client reservation showtime.endsAt', true);
        available_seats_value := COALESCE(cineko_json_integer(showtime->'availableSeats',
            'client reservation showtime.availableSeats', false), 0);
        capacity_value := COALESCE(cineko_json_integer(showtime->'capacity',
            'client reservation showtime.capacity', false), 0);
        sold_out_value := COALESCE(cineko_json_boolean(showtime->'soldOut',
            'client reservation showtime.soldOut', false), false);
        INSERT INTO client_reservation_showtimes (
            user_id, reservation_id, showtime_id, provider_id, source_key, theater_id,
            movie_id, movie_provider_id, movie_source_key, movie_title, movie_poster_url,
            auditorium_id, auditorium_theater_id, auditorium_source_key, auditorium_name,
            auditorium_capacity, auditorium_layout_hash, schedule_date, starts_at, ends_at,
            available_seats, capacity, sold_out
        ) VALUES (
            resource_row.user_id, resource_row.id, showtime_id_value, provider_id_value,
            source_key_value, theater_id_value, movie_id_value, movie_provider_id_value,
            movie_source_key_value, movie_title_value, movie_poster_url_value,
            auditorium_id_value, auditorium_theater_id_value, auditorium_source_key_value,
            auditorium_name_value, auditorium_capacity_value::integer, auditorium_layout_hash_value,
            schedule_date_value, starts_at_value, ends_at_value, available_seats_value::integer,
            capacity_value::integer, sold_out_value
        );
        IF auditorium ? 'screenTypes' THEN
            IF jsonb_typeof(auditorium->'screenTypes') <> 'array' THEN
                RAISE EXCEPTION 'client reservation % screenTypes must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(auditorium->'screenTypes') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                screen_type_item := item.value;
                INSERT INTO client_reservation_showtime_screen_types (
                    user_id, reservation_id, position, screen_type
                ) VALUES (
                    resource_row.user_id, resource_row.id, item.item_position,
                    cineko_json_text(screen_type_item, 'client reservation screen type', true)
                );
            END LOOP;
        END IF;
    END LOOP;
END;
$$;

SELECT cineko_backfill_reservations();

CREATE OR REPLACE FUNCTION cineko_backfill_external_operations()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    resource_row record;
    body jsonb;
    payload_id text;
    user_id_value text;
    monitor_id_value text;
    reservation_id_value text;
    refund_amount_value text;
    last_error_value text;
    created_at_value timestamptz;
    updated_at_value timestamptz;
    operation_kind text;
    state text;
BEGIN
    FOR resource_row IN
        SELECT user_id, id, payload
        FROM client_resources
        WHERE kind = 'external-operations'
    LOOP
        body := resource_row.payload;
        PERFORM cineko_require_keys(body, 'client external operation ' || resource_row.id,
            ARRAY['id', 'userId', 'monitorId', 'reservationId', 'refundAmount', 'lastError',
                  'createdAt', 'updatedAt', 'cancellation', 'prepared', 'unknown',
                  'attentionRequired', 'confirmed', 'reconciled']);
        payload_id := cineko_json_text(body->'id', 'client external operation.id', true);
        user_id_value := cineko_json_text(body->'userId', 'client external operation.userId', true);
        IF payload_id <> resource_row.id OR user_id_value <> resource_row.user_id THEN
            RAISE EXCEPTION 'client external operation % identity does not match client_resources', resource_row.id;
        END IF;
        monitor_id_value := COALESCE(cineko_json_text(body->'monitorId',
            'client external operation.monitorId', false), '');
        reservation_id_value := cineko_json_text(body->'reservationId',
            'client external operation.reservationId', true);
        refund_amount_value := COALESCE(cineko_json_text(body->'refundAmount',
            'client external operation.refundAmount', false), '');
        last_error_value := COALESCE(cineko_json_text(body->'lastError',
            'client external operation.lastError', false), '');
        created_at_value := cineko_json_timestamp(body->'createdAt',
            'client external operation.createdAt', false);
        updated_at_value := cineko_json_timestamp(body->'updatedAt',
            'client external operation.updatedAt', false);
        operation_kind := cineko_json_oneof_fields(body, 'client external operation.kind', ARRAY['cancellation']);
        PERFORM cineko_require_keys(body->operation_kind,
            'client external operation.' || operation_kind, ARRAY[]::text[]);
        state := cineko_json_oneof_fields(body, 'client external operation.state',
            ARRAY['prepared', 'unknown', 'attentionRequired', 'confirmed', 'reconciled']);
        PERFORM cineko_require_keys(body->state,
            'client external operation.' || state, ARRAY[]::text[]);
        IF state = 'attentionRequired' THEN
            state := 'attention-required';
        END IF;
        INSERT INTO client_external_operations (
            user_id, resource_kind, id, monitor_id, reservation_id, refund_amount,
            last_error, operation_created_at, operation_updated_at, kind, state
        ) VALUES (
            resource_row.user_id, 'external-operations', resource_row.id,
            NULLIF(monitor_id_value, ''), reservation_id_value, refund_amount_value,
            last_error_value, created_at_value, updated_at_value, operation_kind, state
        );
    END LOOP;
END;
$$;

SELECT cineko_backfill_external_operations();

CREATE OR REPLACE FUNCTION cineko_backfill_app_events()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    resource_row record;
    body jsonb;
    payload_id text;
    user_id_value text;
    kind_value text;
    message_value text;
    created_at_value timestamptz;
    read_at_value timestamptz;
    tone text;
BEGIN
    FOR resource_row IN
        SELECT user_id, id, payload
        FROM client_resources
        WHERE kind = 'app-events'
    LOOP
        body := resource_row.payload;
        PERFORM cineko_require_keys(body, 'client app event ' || resource_row.id,
            ARRAY['id', 'userId', 'kind', 'message', 'createdAt', 'readAt',
                  'info', 'success', 'warning', 'error']);
        payload_id := cineko_json_text(body->'id', 'client app event.id', true);
        user_id_value := cineko_json_text(body->'userId', 'client app event.userId', true);
        IF payload_id <> resource_row.id OR user_id_value <> resource_row.user_id THEN
            RAISE EXCEPTION 'client app event % identity does not match client_resources', resource_row.id;
        END IF;
        kind_value := COALESCE(cineko_json_text(body->'kind', 'client app event.kind', false), '');
        message_value := COALESCE(cineko_json_text(body->'message', 'client app event.message', false), '');
        created_at_value := cineko_json_timestamp(body->'createdAt', 'client app event.createdAt', false);
        read_at_value := cineko_json_timestamp(body->'readAt', 'client app event.readAt', false);
        tone := cineko_json_oneof_fields(body, 'client app event.tone', ARRAY['info', 'success', 'warning', 'error']);
        PERFORM cineko_require_keys(body->tone, 'client app event.' || tone, ARRAY[]::text[]);
        INSERT INTO client_app_events (
            user_id, resource_kind, id, kind, message, event_created_at, read_at, tone
        ) VALUES (
            resource_row.user_id, 'app-events', resource_row.id, kind_value, message_value,
            created_at_value, read_at_value, tone
        );
    END LOOP;
END;
$$;

SELECT cineko_backfill_app_events();

UPDATE observation_assignments
SET auditorium_id = cineko_validate_assignment_task(task_data, task_kind);

ALTER TABLE observation_assignments
    ADD CONSTRAINT observation_assignments_seat_map_auditorium_check CHECK (
        task_kind <> 'cgv.seat-map.capture' OR auditorium_id <> ''
    );

CREATE INDEX observation_assignments_auditorium_idx
    ON observation_assignments (auditorium_id, status, deadline)
    WHERE auditorium_id <> '';

DO $$
DECLARE
    command_row record;
    observed_at_value timestamptz;
BEGIN
    FOR command_row IN SELECT id, payload FROM client_execution_commands LOOP
        PERFORM cineko_require_keys(command_row.payload,
            'client execution command ' || command_row.id || '.payload',
            ARRAY['showtime', 'observedAt']);
        PERFORM cineko_validate_showtime(command_row.payload->'showtime',
            'client execution command ' || command_row.id || '.payload.showtime');
        observed_at_value := cineko_json_timestamp(command_row.payload->'observedAt',
            'client execution command ' || command_row.id || '.payload.observedAt', true);
        UPDATE client_execution_commands
        SET observed_at = observed_at_value
        WHERE id = command_row.id;
    END LOOP;
END;
$$;

ALTER TABLE client_execution_commands
    ALTER COLUMN observed_at SET NOT NULL;

COMMENT ON COLUMN client_execution_commands.payload IS
    'Canonical execution Payload ProtoJSON is retained because the embedded showtime availability snapshot is historical and cannot be reconstructed from current catalog joins.';

CREATE OR REPLACE FUNCTION cineko_backfill_settings()
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    resource_row record;
    body jsonb;
    network jsonb;
    proxy jsonb;
    mode text;
    username text;
    password text;
    proxy_has_password_value boolean;
    item record;
    webhook jsonb;
    webhook_id text;
    event_value jsonb;
    event_item record;
    url_item jsonb;
BEGIN
    FOR resource_row IN
        SELECT user_id, id, payload
        FROM client_resources
        WHERE kind = 'settings'
    LOOP
        body := resource_row.payload;
        PERFORM cineko_require_keys(body, 'client settings ' || resource_row.id,
            ARRAY['network', 'webhooks']);
        mode := NULL;
        network := NULL;
        proxy := NULL;
        IF body ? 'network' AND jsonb_typeof(body->'network') <> 'null' THEN
            network := body->'network';
            PERFORM cineko_require_keys(network, 'client settings ' || resource_row.id || '.network',
                ARRAY['direct', 'proxy']);
            mode := cineko_json_oneof(network,
                'client settings ' || resource_row.id || '.network', ARRAY['direct', 'proxy']);
        END IF;
        username := '';
        password := '';
        proxy_has_password_value := false;
        IF mode = 'proxy' THEN
            proxy := network->'proxy';
            PERFORM cineko_require_keys(proxy,
                'client settings ' || resource_row.id || '.network.proxy',
                ARRAY['urls', 'username', 'password', 'hasPassword']);
            username := COALESCE(cineko_json_text(proxy->'username',
                'client settings ' || resource_row.id || '.network.proxy.username', false), '');
            password := COALESCE(cineko_json_text(proxy->'password',
                'client settings ' || resource_row.id || '.network.proxy.password', false), '');
            proxy_has_password_value := COALESCE(cineko_json_boolean(proxy->'hasPassword',
                'client settings ' || resource_row.id || '.network.proxy.hasPassword', false), false);
            IF proxy_has_password_value = false AND password <> '' THEN
                RAISE EXCEPTION 'client settings % contains a password without hasPassword', resource_row.id;
            END IF;
            IF proxy ? 'urls' THEN
                IF jsonb_typeof(proxy->'urls') <> 'array' THEN
                    RAISE EXCEPTION 'client settings % proxy.urls must be an array', resource_row.id;
                END IF;
                FOR url_item IN SELECT jsonb_array_elements(proxy->'urls') LOOP
                    PERFORM cineko_json_text(url_item,
                        'client settings ' || resource_row.id || '.network.proxy.urls[]', true);
                END LOOP;
            END IF;
        END IF;
        INSERT INTO client_settings (
            user_id, resource_kind, id, network_mode, proxy_username, proxy_password, proxy_has_password
        ) VALUES (
            resource_row.user_id, 'settings', resource_row.id, mode, username, password,
            proxy_has_password_value
        );
        IF mode = 'proxy' AND proxy ? 'urls' THEN
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(proxy->'urls') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                INSERT INTO client_setting_proxy_urls (user_id, settings_id, position, url)
                VALUES (resource_row.user_id, resource_row.id, item.item_position,
                    cineko_json_text(item.value, 'client settings proxy URL', true));
            END LOOP;
        END IF;
        IF body ? 'webhooks' THEN
            IF jsonb_typeof(body->'webhooks') <> 'array' THEN
                RAISE EXCEPTION 'client settings % webhooks must be an array', resource_row.id;
            END IF;
            FOR item IN
                SELECT value, (ordinality - 1)::integer AS item_position
                FROM jsonb_array_elements(body->'webhooks') WITH ORDINALITY AS entry(value, ordinality)
            LOOP
                webhook := item.value;
                PERFORM cineko_require_keys(webhook,
                    'client settings ' || resource_row.id || '.webhooks[]',
                    ARRAY['id', 'name', 'url', 'secret', 'eventKinds', 'enabled', 'hasSecret']);
                webhook_id := COALESCE(cineko_json_text(webhook->'id',
                    'client settings webhook.id', false), '');
                IF webhook ? 'eventKinds' THEN
                    IF jsonb_typeof(webhook->'eventKinds') <> 'array' THEN
                        RAISE EXCEPTION 'client settings % webhook eventKinds must be an array', resource_row.id;
                    END IF;
                    FOR event_value IN SELECT jsonb_array_elements(webhook->'eventKinds') LOOP
                        PERFORM cineko_json_text(event_value,
                            'client settings webhook eventKinds[]', true);
                    END LOOP;
                END IF;
                INSERT INTO client_setting_webhooks (
                    user_id, settings_id, position, id, name, url, secret, enabled, has_secret
                ) VALUES (
                    resource_row.user_id, resource_row.id, item.item_position, webhook_id,
                    COALESCE(cineko_json_text(webhook->'name', 'client settings webhook.name', false), ''),
                    COALESCE(cineko_json_text(webhook->'url', 'client settings webhook.url', false), ''),
                    COALESCE(cineko_json_text(webhook->'secret', 'client settings webhook.secret', false), ''),
                    COALESCE(cineko_json_boolean(webhook->'enabled', 'client settings webhook.enabled', false), false),
                    COALESCE(cineko_json_boolean(webhook->'hasSecret', 'client settings webhook.hasSecret', false), false)
                );
                IF webhook ? 'eventKinds' THEN
                    FOR event_item IN
                        SELECT value, (ordinality - 1)::integer AS event_position
                        FROM jsonb_array_elements(webhook->'eventKinds') WITH ORDINALITY AS entry(value, ordinality)
                    LOOP
                        INSERT INTO client_setting_webhook_event_kinds (
                            user_id, settings_id, webhook_position, position, event_kind
                        ) VALUES (
                            resource_row.user_id, resource_row.id, item.item_position, event_item.event_position,
                            cineko_json_text(event_item.value, 'client settings webhook event kind', true)
                        );
                    END LOOP;
                END IF;
            END LOOP;
        END IF;
    END LOOP;
END;
$$;

SELECT cineko_backfill_settings();

DO $$
DECLARE
    event_row record;
    canonical_payload jsonb;
BEGIN
    FOR event_row IN
        SELECT sequence, user_id, event_type, resource_kind, resource_id, resource_revision, payload
        FROM client_events
        ORDER BY sequence
    LOOP
        IF event_row.event_type = 'execution.ready' THEN
            IF event_row.resource_kind <> 'executions' OR event_row.resource_id = '' OR event_row.resource_revision <> 1 THEN
                RAISE EXCEPTION 'execution.ready metadata is incompatible with the latest contract at sequence %', event_row.sequence;
            END IF;
            PERFORM cineko_require_keys(event_row.payload, 'client execution.ready event', ARRAY['commandId', 'monitorId', 'reason']);
            PERFORM cineko_json_text(event_row.payload->'commandId', 'client execution.ready event.commandId', true);
            PERFORM cineko_json_text(event_row.payload->'monitorId', 'client execution.ready event.monitorId', true);
            PERFORM cineko_json_text(event_row.payload->'reason', 'client execution.ready event.reason', true);
        ELSIF event_row.event_type = event_row.resource_kind || '.deleted' THEN
            IF event_row.resource_kind NOT IN ('settings', 'presets', 'monitors', 'reservations', 'external-operations', 'app-events') THEN
                RAISE EXCEPTION 'unsupported deleted client event resource kind % at sequence %', event_row.resource_kind, event_row.sequence;
            END IF;
            IF event_row.resource_id = '' OR event_row.resource_revision <= 0 THEN
                RAISE EXCEPTION 'deleted client event metadata is incomplete at sequence %', event_row.sequence;
            END IF;
            -- DeletedResource is rebuilt from event metadata by the runtime;
            -- discard any legacy body while preserving the event itself.
            UPDATE client_events
            SET payload = '{}'::jsonb
            WHERE sequence = event_row.sequence;
        ELSIF event_row.event_type = event_row.resource_kind || '.updated' THEN
            canonical_payload := event_row.payload;
            IF event_row.resource_kind = 'monitors' THEN
                canonical_payload := canonical_payload - 'mode' - 'pollInterval' - 'maximumPollInterval';
                canonical_payload := jsonb_set(
                    canonical_payload,
                    '{searchHorizonDays}',
                    to_jsonb(LEAST(14, GREATEST(1, COALESCE(
                        cineko_json_integer(canonical_payload->'searchHorizonDays',
                            'client monitor event.searchHorizonDays', false),
                        14
                    )))),
                    true
                );
                UPDATE client_events
                SET payload = canonical_payload
                WHERE sequence = event_row.sequence;
            END IF;
            PERFORM cineko_validate_client_event_resource(
                canonical_payload,
                event_row.resource_kind,
                event_row.user_id,
                event_row.resource_id,
                event_row.resource_revision
            );
        ELSE
            RAISE EXCEPTION 'unsupported client event type % at sequence %', event_row.event_type, event_row.sequence;
        END IF;
    END LOOP;
END;
$$;

DROP FUNCTION cineko_backfill_settings();
DROP FUNCTION cineko_backfill_presets();
DROP FUNCTION cineko_backfill_monitors();
DROP FUNCTION cineko_backfill_reservations();
DROP FUNCTION cineko_backfill_external_operations();
DROP FUNCTION cineko_backfill_app_events();
DROP FUNCTION cineko_validate_assignment_task(jsonb, text);
DROP FUNCTION cineko_validate_showtime(jsonb, text);
DROP FUNCTION cineko_validate_auditorium(jsonb, text);
DROP FUNCTION cineko_validate_movie(jsonb, text);
DROP FUNCTION cineko_validate_theater(jsonb, text);
DROP FUNCTION cineko_json_oneof_fields(jsonb, text, text[]);
DROP FUNCTION cineko_json_oneof(jsonb, text, text[]);
DROP FUNCTION cineko_json_local_minutes(jsonb, text);
DROP FUNCTION cineko_json_local_date(jsonb, text);
DROP FUNCTION cineko_json_duration_nanos(jsonb, text, boolean);
DROP FUNCTION cineko_json_timestamp(jsonb, text, boolean);
DROP FUNCTION cineko_json_boolean(jsonb, text, boolean);
DROP FUNCTION cineko_json_number(jsonb, text, boolean);
DROP FUNCTION cineko_json_integer(jsonb, text, boolean);
DROP FUNCTION cineko_json_text(jsonb, text, boolean);
DROP FUNCTION cineko_require_keys(jsonb, text, text[]);
DROP FUNCTION cineko_require_object(jsonb, text);
DROP FUNCTION cineko_validate_client_event_resource(jsonb, text, text, text, bigint);

ALTER TABLE client_resources DROP COLUMN payload;
