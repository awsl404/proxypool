-- name: InsertProxy :execrows
INSERT OR IGNORE INTO proxies(
  scheme,
  host,
  port,
  username,
  password,
  tags,
  source,
  anonymity,
  reachability,
  target_capability,
  enabled,
  status,
  created_at,
  updated_at
)
VALUES(
  sqlc.arg(scheme),
  sqlc.arg(host),
  sqlc.arg(port),
  sqlc.arg(username),
  sqlc.arg(password),
  sqlc.arg(tags),
  sqlc.arg(source),
  sqlc.arg(anonymity),
  'unknown',
  sqlc.arg(target_capability),
  1,
  'unchecked',
  sqlc.arg(created_at),
  sqlc.arg(updated_at)
);

-- name: GetProxyID :one
SELECT id
FROM proxies
WHERE scheme = sqlc.arg(scheme)
  AND host = sqlc.arg(host)
  AND port = sqlc.arg(port)
  AND username = sqlc.arg(username)
  AND password = sqlc.arg(password);

-- name: GetProxyTagsByIdentity :one
SELECT tags
FROM proxies
WHERE scheme = sqlc.arg(scheme)
  AND host = sqlc.arg(host)
  AND port = sqlc.arg(port)
  AND username = sqlc.arg(username)
  AND password = sqlc.arg(password);

-- name: UpdateProxyTagsByIdentity :exec
UPDATE proxies
SET tags = sqlc.arg(tags),
    updated_at = sqlc.arg(updated_at)
WHERE scheme = sqlc.arg(scheme)
  AND host = sqlc.arg(host)
  AND port = sqlc.arg(port)
  AND username = sqlc.arg(username)
  AND password = sqlc.arg(password);

-- name: ListEndpointProxyTags :many
SELECT scheme, tags
FROM proxies
WHERE host = sqlc.arg(host)
  AND port = sqlc.arg(port)
  AND username = sqlc.arg(username)
  AND password = sqlc.arg(password);

-- name: UpdateProxyTagsByEndpoint :exec
UPDATE proxies
SET tags = sqlc.arg(tags),
    updated_at = sqlc.arg(updated_at)
WHERE host = sqlc.arg(host)
  AND port = sqlc.arg(port)
  AND username = sqlc.arg(username)
  AND password = sqlc.arg(password);

-- name: ListProxiesFiltered :many
SELECT
  id,
  scheme,
  host,
  port,
  username,
  password,
  tags,
  source,
  anonymity,
  reachability,
  target_capability,
  enabled,
  status,
  fail_count,
  success_count,
  latency_ms,
  last_checked,
  last_used,
  created_at,
  updated_at
FROM proxies
WHERE
  (sqlc.arg(query) = '' OR host LIKE '%' || sqlc.arg(query) || '%' OR username LIKE '%' || sqlc.arg(query) || '%' OR source LIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(scheme_filter) = '' OR scheme = sqlc.arg(scheme_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (sqlc.arg(anonymity_filter) = '' OR anonymity = sqlc.arg(anonymity_filter))
  AND (
    sqlc.arg(reachability_filter) = ''
    OR reachability = sqlc.arg(reachability_filter)
    OR (sqlc.arg(reachability_filter) = 'domestic' AND reachability = 'both')
    OR (sqlc.arg(reachability_filter) = 'foreign' AND reachability = 'both')
  )
  AND (
    sqlc.arg(target_capability_filter) = ''
    OR target_capability = sqlc.arg(target_capability_filter)
    OR (sqlc.arg(target_capability_filter) = 'http' AND target_capability = 'both')
    OR (sqlc.arg(target_capability_filter) = 'https' AND target_capability = 'both')
  )
ORDER BY enabled DESC,
  CASE status WHEN 'available' THEN 0 ELSE 1 END,
  latency_ms ASC,
  id DESC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: CountProxiesFiltered :one
SELECT COUNT(*)
FROM proxies
WHERE
  (sqlc.arg(query) = '' OR host LIKE '%' || sqlc.arg(query) || '%' OR username LIKE '%' || sqlc.arg(query) || '%' OR source LIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(scheme_filter) = '' OR scheme = sqlc.arg(scheme_filter))
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (sqlc.arg(anonymity_filter) = '' OR anonymity = sqlc.arg(anonymity_filter))
  AND (
    sqlc.arg(reachability_filter) = ''
    OR reachability = sqlc.arg(reachability_filter)
    OR (sqlc.arg(reachability_filter) = 'domestic' AND reachability = 'both')
    OR (sqlc.arg(reachability_filter) = 'foreign' AND reachability = 'both')
  )
  AND (
    sqlc.arg(target_capability_filter) = ''
    OR target_capability = sqlc.arg(target_capability_filter)
    OR (sqlc.arg(target_capability_filter) = 'http' AND target_capability = 'both')
    OR (sqlc.arg(target_capability_filter) = 'https' AND target_capability = 'both')
  );

-- name: GetProxy :one
SELECT
  id,
  scheme,
  host,
  port,
  username,
  password,
  tags,
  source,
  anonymity,
  reachability,
  target_capability,
  enabled,
  status,
  fail_count,
  success_count,
  latency_ms,
  last_checked,
  last_used,
  created_at,
  updated_at
FROM proxies
WHERE id = sqlc.arg(id);

-- name: DeleteProxy :exec
DELETE FROM proxies
WHERE id = sqlc.arg(id);

-- name: DeleteUnavailableProxies :execrows
DELETE FROM proxies
WHERE status = 'unavailable' OR enabled = 0;

-- name: SetProxyEnabled :exec
UPDATE proxies
SET enabled = sqlc.arg(enabled),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: CandidateProxies :many
SELECT
  id,
  scheme,
  host,
  port,
  username,
  password,
  tags,
  source,
  anonymity,
  reachability,
  target_capability,
  enabled,
  status,
  fail_count,
  success_count,
  latency_ms,
  last_checked,
  last_used,
  created_at,
  updated_at
FROM proxies
WHERE enabled = 1
  AND status != 'unavailable'
  AND (
    sqlc.arg(scheme_filter) = ''
    OR instr(',' || sqlc.arg(scheme_filter) || ',', ',' || scheme || ',') > 0
  )
  AND (sqlc.arg(anonymity_filter) = '' OR anonymity = sqlc.arg(anonymity_filter))
  AND (
    sqlc.arg(reachability_filter) = ''
    OR reachability = sqlc.arg(reachability_filter)
    OR (sqlc.arg(reachability_filter) = 'domestic' AND reachability = 'both')
    OR (sqlc.arg(reachability_filter) = 'foreign' AND reachability = 'both')
  )
  AND (
    sqlc.arg(target_capability_filter) = ''
    OR target_capability = sqlc.arg(target_capability_filter)
    OR (sqlc.arg(target_capability_filter) = 'http' AND target_capability = 'both')
    OR (sqlc.arg(target_capability_filter) = 'https' AND target_capability = 'both')
  )
ORDER BY
  CASE status WHEN 'available' THEN 0 ELSE 1 END,
  fail_count ASC,
  CASE WHEN latency_ms = 0 THEN 999999 ELSE latency_ms END ASC,
  COALESCE(last_used, '1970-01-01T00:00:00Z') ASC,
  id ASC
LIMIT sqlc.arg(row_limit);

-- name: SequentialCandidateProxies :many
SELECT
  id,
  scheme,
  host,
  port,
  username,
  password,
  tags,
  source,
  anonymity,
  reachability,
  target_capability,
  enabled,
  status,
  fail_count,
  success_count,
  latency_ms,
  last_checked,
  last_used,
  created_at,
  updated_at
FROM proxies
WHERE enabled = 1
  AND status != 'unavailable'
  AND (
    sqlc.arg(scheme_filter) = ''
    OR instr(',' || sqlc.arg(scheme_filter) || ',', ',' || scheme || ',') > 0
  )
  AND (sqlc.arg(anonymity_filter) = '' OR anonymity = sqlc.arg(anonymity_filter))
  AND (
    sqlc.arg(reachability_filter) = ''
    OR reachability = sqlc.arg(reachability_filter)
    OR (sqlc.arg(reachability_filter) = 'domestic' AND reachability = 'both')
    OR (sqlc.arg(reachability_filter) = 'foreign' AND reachability = 'both')
  )
  AND (
    sqlc.arg(target_capability_filter) = ''
    OR target_capability = sqlc.arg(target_capability_filter)
    OR (sqlc.arg(target_capability_filter) = 'http' AND target_capability = 'both')
    OR (sqlc.arg(target_capability_filter) = 'https' AND target_capability = 'both')
  )
ORDER BY
  COALESCE(last_used, '1970-01-01T00:00:00Z') ASC,
  id ASC
LIMIT sqlc.arg(row_limit);

-- name: MarkProxyUsed :exec
UPDATE proxies
SET last_used = sqlc.arg(last_used),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: MarkProxyCheckOK :exec
UPDATE proxies
SET status = 'available',
    fail_count = 0,
    success_count = success_count + 1,
    latency_ms = sqlc.arg(latency_ms),
    reachability = sqlc.arg(reachability),
    target_capability = sqlc.arg(target_capability),
    last_checked = sqlc.arg(last_checked),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: MarkProxyCheckFail :exec
UPDATE proxies
SET status = 'unavailable',
    fail_count = fail_count + 1,
    reachability = sqlc.arg(reachability),
    target_capability = sqlc.arg(target_capability),
    last_checked = sqlc.arg(last_checked),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteProxyAtFailThreshold :exec
DELETE FROM proxies
WHERE id = sqlc.arg(id)
  AND fail_count >= sqlc.arg(fail_threshold);

-- name: MarkProxyAnonymity :exec
UPDATE proxies
SET anonymity = sqlc.arg(anonymity),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ProxyStats :one
SELECT
  COUNT(*) AS total,
  COALESCE(SUM(CASE WHEN enabled = 1 AND status = 'available' THEN 1 ELSE 0 END), 0) AS available,
  COALESCE(SUM(CASE WHEN status = 'unavailable' THEN 1 ELSE 0 END), 0) AS unavailable,
  COALESCE(SUM(CASE WHEN enabled = 0 THEN 1 ELSE 0 END), 0) AS disabled,
  COALESCE(SUM(CASE WHEN anonymity = 'anonymous' THEN 1 ELSE 0 END), 0) AS anonymous,
  COALESCE(SUM(CASE WHEN anonymity = 'transparent' THEN 1 ELSE 0 END), 0) AS transparent,
  COALESCE(SUM(CASE WHEN anonymity = 'unknown' THEN 1 ELSE 0 END), 0) AS unknown,
  COALESCE(AVG(CASE WHEN latency_ms > 0 THEN latency_ms ELSE NULL END), 0) AS average_delay
FROM proxies;
