-- +goose Up

-- JavDB metadata now comes from the signed App API. The App API does not use
-- the website session cookie, so permanently remove the old stored cookie.
ALTER TABLE javdb_configuration DROP COLUMN encrypted_cookie;

-- Full-video playback requires a JavDB website login. Do not advertise it as
-- an enabled built-in source when the application no longer stores cookies.
UPDATE online_providers SET enabled = 0 WHERE id = 'provider-javdb-video';

-- +goose Down

ALTER TABLE javdb_configuration ADD COLUMN encrypted_cookie TEXT NOT NULL DEFAULT '';
UPDATE online_providers SET enabled = 1 WHERE id = 'provider-javdb-video';
