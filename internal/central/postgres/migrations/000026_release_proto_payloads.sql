UPDATE release_components
SET payload = CASE
    WHEN payload ? 'schemaVersion' AND payload ? 'payload' THEN payload -> 'payload'
    ELSE payload
END;

UPDATE release_components
SET payload = ((payload - 'arch') - 'protocol') || jsonb_build_object('architecture', payload -> 'arch')
WHERE payload ? 'arch';

UPDATE release_components
SET payload = payload - 'protocol'
WHERE payload ? 'protocol';

ALTER TABLE release_components
    DROP COLUMN schema_version;

ALTER TABLE desktop_release_registry_state
    DROP COLUMN resolver_version;
