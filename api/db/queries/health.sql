-- name: Ping :one
-- Ping checks that the database answers a query. Used by GET /health.
SELECT 1::int4 AS ok;
