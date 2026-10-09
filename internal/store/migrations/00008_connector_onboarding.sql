-- +goose Up
-- Richer connector onboarding (scope, installation type, deployment method,
-- organization management account) and a contract change: role_arn is now
-- optional at create time because the customer deploys the IAM role before
-- supplying its ARN at verification. Still NO SECRETS: no credential columns.

ALTER TABLE connector_connections
    ADD COLUMN scope                 TEXT NOT NULL DEFAULT 'account',        -- account|organization
    ADD COLUMN installation_type     TEXT NOT NULL DEFAULT 'standard',       -- standard (outpost reserved)
    ADD COLUMN deployment_method     TEXT NOT NULL DEFAULT 'cloudformation', -- cloudformation|terraform|manual
    ADD COLUMN management_account_id TEXT NOT NULL DEFAULT '';               -- org management account (org scope)

-- role_arn becomes optional: the role is deployed after create, and its ARN is
-- supplied at verify time.
ALTER TABLE connector_connections ALTER COLUMN role_arn DROP NOT NULL;
ALTER TABLE connector_connections ALTER COLUMN role_arn SET DEFAULT '';

-- +goose Down
ALTER TABLE connector_connections ALTER COLUMN role_arn DROP DEFAULT;
ALTER TABLE connector_connections ALTER COLUMN role_arn SET NOT NULL;
ALTER TABLE connector_connections
    DROP COLUMN management_account_id,
    DROP COLUMN deployment_method,
    DROP COLUMN installation_type,
    DROP COLUMN scope;
