CREATE TABLE seat_map_seats (
    version_id text NOT NULL REFERENCES seat_map_versions(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 1),
    seat_id text NOT NULL,
    label text NOT NULL CHECK (label <> ''),
    row_label text NOT NULL CHECK (row_label <> ''),
    seat_number integer NOT NULL CHECK (seat_number >= 1),
    x double precision NOT NULL CHECK (x BETWEEN 0 AND 1),
    y double precision NOT NULL CHECK (y BETWEEN 0 AND 1),
    seat_type text NOT NULL CHECK (seat_type <> ''),
    zone_name text NOT NULL DEFAULT '',
    zone_kind text NOT NULL DEFAULT '',
    sale_form_code text NOT NULL DEFAULT '',
    sale_form_name text NOT NULL DEFAULT '',
    left_aisle boolean NOT NULL DEFAULT false,
    right_aisle boolean NOT NULL DEFAULT false,
    source_label text NOT NULL DEFAULT '',
    source_seat_kind_code text NOT NULL DEFAULT '',
    source_seat_kind_name text NOT NULL DEFAULT '',
    PRIMARY KEY (version_id, position),
    UNIQUE (version_id, seat_id),
    UNIQUE (version_id, label)
);

CREATE INDEX seat_map_seats_row_idx
    ON seat_map_seats (version_id, row_label, seat_number);

CREATE TABLE seat_map_seat_features (
    version_id text NOT NULL,
    seat_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 1),
    feature text NOT NULL CHECK (feature <> ''),
    PRIMARY KEY (version_id, seat_id, position),
    UNIQUE (version_id, seat_id, feature),
    FOREIGN KEY (version_id, seat_id)
        REFERENCES seat_map_seats(version_id, seat_id) ON DELETE CASCADE
);

CREATE TABLE seat_map_seat_source_classes (
    version_id text NOT NULL,
    seat_id text NOT NULL,
    position integer NOT NULL CHECK (position >= 1),
    source_class text NOT NULL CHECK (source_class <> ''),
    PRIMARY KEY (version_id, seat_id, position),
    UNIQUE (version_id, seat_id, source_class),
    FOREIGN KEY (version_id, seat_id)
        REFERENCES seat_map_seats(version_id, seat_id) ON DELETE CASCADE
);

CREATE TABLE seat_map_zones (
    version_id text NOT NULL REFERENCES seat_map_versions(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 1),
    code text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    kind_code text NOT NULL DEFAULT '',
    kind_name text NOT NULL DEFAULT '',
    min_x double precision NOT NULL CHECK (min_x BETWEEN 0 AND 1),
    max_x double precision NOT NULL CHECK (max_x BETWEEN 0 AND 1),
    min_y double precision NOT NULL CHECK (min_y BETWEEN 0 AND 1),
    max_y double precision NOT NULL CHECK (max_y BETWEEN 0 AND 1),
    capacity integer NOT NULL CHECK (capacity >= 0),
    PRIMARY KEY (version_id, position),
    CHECK (min_x <= max_x AND min_y <= max_y)
);

CREATE TABLE seat_map_blocks (
    version_id text NOT NULL REFERENCES seat_map_versions(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 1),
    code text NOT NULL DEFAULT '',
    name text NOT NULL DEFAULT '',
    kind_code text NOT NULL DEFAULT '',
    kind_name text NOT NULL DEFAULT '',
    min_x double precision NOT NULL CHECK (min_x BETWEEN 0 AND 1),
    max_x double precision NOT NULL CHECK (max_x BETWEEN 0 AND 1),
    min_y double precision NOT NULL CHECK (min_y BETWEEN 0 AND 1),
    max_y double precision NOT NULL CHECK (max_y BETWEEN 0 AND 1),
    PRIMARY KEY (version_id, position),
    CHECK (min_x <= max_x AND min_y <= max_y)
);

INSERT INTO seat_map_seats (
    version_id, position, seat_id, label, row_label, seat_number, x, y, seat_type,
    zone_name, zone_kind, sale_form_code, sale_form_name, left_aisle, right_aisle,
    source_label, source_seat_kind_code, source_seat_kind_name
)
SELECT version.id, entry.position::integer,
    entry.value->>'id', entry.value->>'label', entry.value->>'row',
    (entry.value->>'number')::integer,
    COALESCE((entry.value->>'x')::double precision, 0),
    COALESCE((entry.value->>'y')::double precision, 0),
    entry.value->>'type',
    COALESCE(entry.value->>'zoneName', ''), COALESCE(entry.value->>'zoneKind', ''),
    COALESCE(entry.value->>'saleFormCode', ''), COALESCE(entry.value->>'saleFormName', ''),
    COALESCE((entry.value->>'leftAisle')::boolean, false),
    COALESCE((entry.value->>'rightAisle')::boolean, false),
    COALESCE(entry.value->>'sourceLabel', ''),
    COALESCE(entry.value->>'sourceSeatKindCode', ''),
    COALESCE(entry.value->>'sourceSeatKindName', '')
FROM seat_map_versions AS version
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(version.layout->'seats', '[]'::jsonb))
    WITH ORDINALITY AS entry(value, position);

INSERT INTO seat_map_seat_features (version_id, seat_id, position, feature)
SELECT version.id, seat.value->>'id', feature.position::integer, feature.value
FROM seat_map_versions AS version
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(version.layout->'seats', '[]'::jsonb)) AS seat(value)
CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(seat.value->'features', '[]'::jsonb))
    WITH ORDINALITY AS feature(value, position);

INSERT INTO seat_map_seat_source_classes (version_id, seat_id, position, source_class)
SELECT version.id, seat.value->>'id', source_class.position::integer, source_class.value
FROM seat_map_versions AS version
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(version.layout->'seats', '[]'::jsonb)) AS seat(value)
CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(seat.value->'sourceClasses', '[]'::jsonb))
    WITH ORDINALITY AS source_class(value, position);

INSERT INTO seat_map_zones (
    version_id, position, code, name, kind_code, kind_name,
    min_x, max_x, min_y, max_y, capacity
)
SELECT version.id, entry.position::integer,
    COALESCE(entry.value->>'code', ''), COALESCE(entry.value->>'name', ''),
    COALESCE(entry.value->>'kindCode', ''), COALESCE(entry.value->>'kindName', ''),
    COALESCE((entry.value->>'minX')::double precision, 0),
    COALESCE((entry.value->>'maxX')::double precision, 0),
    COALESCE((entry.value->>'minY')::double precision, 0),
    COALESCE((entry.value->>'maxY')::double precision, 0),
    COALESCE((entry.value->>'capacity')::integer, 0)
FROM seat_map_versions AS version
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(version.layout->'zones', '[]'::jsonb))
    WITH ORDINALITY AS entry(value, position);

INSERT INTO seat_map_blocks (
    version_id, position, code, name, kind_code, kind_name,
    min_x, max_x, min_y, max_y
)
SELECT version.id, entry.position::integer,
    COALESCE(entry.value->>'code', ''), COALESCE(entry.value->>'name', ''),
    COALESCE(entry.value->>'kindCode', ''), COALESCE(entry.value->>'kindName', ''),
    COALESCE((entry.value->>'minX')::double precision, 0),
    COALESCE((entry.value->>'maxX')::double precision, 0),
    COALESCE((entry.value->>'minY')::double precision, 0),
    COALESCE((entry.value->>'maxY')::double precision, 0)
FROM seat_map_versions AS version
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(version.layout->'blocks', '[]'::jsonb))
    WITH ORDINALITY AS entry(value, position);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM seat_map_versions AS version
        LEFT JOIN seat_map_seats AS seat ON seat.version_id = version.id
        GROUP BY version.id, version.capacity
        HAVING count(seat.seat_id) <> version.capacity
    ) THEN
        RAISE EXCEPTION 'seat-map normalization did not preserve capacity';
    END IF;
END $$;

ALTER TABLE seat_map_versions DROP COLUMN layout;
