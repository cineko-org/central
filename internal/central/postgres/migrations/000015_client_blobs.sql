CREATE TABLE client_blobs (
    user_id text NOT NULL REFERENCES client_users(id) ON DELETE CASCADE,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    media_type text NOT NULL CHECK (media_type = 'image/png'),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 33554432),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    content bytea NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, sha256),
    CHECK (octet_length(content) = size_bytes)
);

CREATE INDEX client_blobs_orphan_gc_idx ON client_blobs (created_at, user_id, sha256);

CREATE TABLE client_blob_references (
    user_id text NOT NULL,
    resource_kind text NOT NULL CHECK (resource_kind = 'seat-maps'),
    resource_id text NOT NULL,
    blob_sha256 text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, resource_kind, resource_id),
    FOREIGN KEY (user_id, resource_kind, resource_id)
        REFERENCES client_resources(user_id, kind, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, blob_sha256)
        REFERENCES client_blobs(user_id, sha256) ON DELETE RESTRICT
);

CREATE INDEX client_blob_references_blob_idx
    ON client_blob_references (user_id, blob_sha256);
