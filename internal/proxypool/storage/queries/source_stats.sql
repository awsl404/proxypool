-- name: UpsertSourceStat :exec
INSERT INTO source_stats(
  url,
  fetch_ok,
  fetch_fail,
  imported,
  duplicates,
  checked,
  last_error,
  last_run,
  last_imported
)
VALUES(
  sqlc.arg(url),
  sqlc.arg(fetch_ok),
  sqlc.arg(fetch_fail),
  sqlc.arg(imported),
  sqlc.arg(duplicates),
  sqlc.arg(checked),
  sqlc.arg(last_error),
  sqlc.arg(last_run),
  sqlc.arg(last_imported)
)
ON CONFLICT(url) DO UPDATE SET
  fetch_ok = fetch_ok + excluded.fetch_ok,
  fetch_fail = fetch_fail + excluded.fetch_fail,
  imported = imported + excluded.imported,
  duplicates = duplicates + excluded.duplicates,
  checked = checked + excluded.checked,
  last_error = excluded.last_error,
  last_run = excluded.last_run,
  last_imported = excluded.last_imported;

-- name: ListSourceStats :many
SELECT
  url,
  fetch_ok,
  fetch_fail,
  imported,
  duplicates,
  checked,
  last_error,
  last_run,
  last_imported
FROM source_stats
WHERE
  (sqlc.arg(query) = '' OR url LIKE '%' || sqlc.arg(query) || '%' OR last_error LIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(failed_only) = 0 OR fetch_fail > 0)
ORDER BY last_run DESC, imported DESC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: CountSourceStats :one
SELECT COUNT(*)
FROM source_stats
WHERE
  (sqlc.arg(query) = '' OR url LIKE '%' || sqlc.arg(query) || '%' OR last_error LIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(failed_only) = 0 OR fetch_fail > 0);

-- name: DeleteSourceStat :execrows
DELETE FROM source_stats
WHERE url = sqlc.arg(url);

-- name: ClearSourceStats :execrows
DELETE FROM source_stats;
