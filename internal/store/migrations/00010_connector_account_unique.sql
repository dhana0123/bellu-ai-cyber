-- +goose Up
-- Account-scope connections are created before the customer deploys the role,
-- so account_id is empty until it is derived from the role ARN at verify. The
-- original UNIQUE (org_id, provider, account_id) made every pending connection
-- in an org collide on ''. Enforce uniqueness only once an account is known.

ALTER TABLE connector_connections DROP CONSTRAINT connector_connections_provider_account_uniq;
CREATE UNIQUE INDEX connector_connections_provider_account_uniq
    ON connector_connections (org_id, provider, account_id)
    WHERE account_id <> '';

-- +goose Down
DROP INDEX connector_connections_provider_account_uniq;
ALTER TABLE connector_connections
    ADD CONSTRAINT connector_connections_provider_account_uniq UNIQUE (org_id, provider, account_id);
