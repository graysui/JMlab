-- +goose Up

-- The configurable JavDB endpoint is the signed App API. Website links remain
-- fixed to javdb.com and are generated internally by the metadata/provider code.
ALTER TABLE javdb_configuration RENAME COLUMN base_url TO api_base_url;
UPDATE javdb_configuration
SET api_base_url = 'https://jdforrepam.com'
WHERE api_base_url IS NULL OR api_base_url = '' OR api_base_url = 'https://javdb.com';

-- +goose Down

ALTER TABLE javdb_configuration RENAME COLUMN api_base_url TO base_url;
