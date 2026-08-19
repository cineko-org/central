ALTER TABLE client_launch_tickets
    ADD COLUMN browser_artifact_sha256 text NOT NULL DEFAULT '',
    ADD COLUMN playwright_version text NOT NULL DEFAULT '',
    ADD COLUMN playwright_artifact_sha256 text NOT NULL DEFAULT '';

ALTER TABLE client_launch_tickets
    ALTER COLUMN browser_artifact_sha256 DROP DEFAULT,
    ALTER COLUMN playwright_version DROP DEFAULT,
    ALTER COLUMN playwright_artifact_sha256 DROP DEFAULT;
