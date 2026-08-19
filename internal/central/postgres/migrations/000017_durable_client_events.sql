CREATE TABLE client_event_cursors (
    user_id text PRIMARY KEY REFERENCES client_users(id) ON DELETE CASCADE,
    pruned_through bigint NOT NULL DEFAULT 0 CHECK (pruned_through >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION notify_cineko_client_event() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('cineko_client_events', NEW.user_id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER client_events_notify_after_insert
AFTER INSERT ON client_events
FOR EACH ROW EXECUTE FUNCTION notify_cineko_client_event();

CREATE OR REPLACE FUNCTION notify_cineko_release_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.generation IS DISTINCT FROM OLD.generation THEN
        PERFORM pg_notify('cineko_client_events', 'release');
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER desktop_release_generation_notify_after_update
AFTER UPDATE OF generation ON desktop_release_registry_state
FOR EACH ROW EXECUTE FUNCTION notify_cineko_release_generation();
