-- +goose Up

ALTER TABLE javdb_configuration ADD COLUMN encrypted_api_token TEXT NOT NULL DEFAULT '';

-- The previous UI accepted the App API origin as a website URL. Keep page
-- cookies on their original host by clearing them before restoring the website
-- address; metadata now calls the signed App API independently.
UPDATE javdb_configuration
SET base_url = 'https://javdb.com', encrypted_cookie = ''
WHERE base_url = 'https://jdforrepam.com';

-- +goose Down

ALTER TABLE javdb_configuration DROP COLUMN encrypted_api_token;
