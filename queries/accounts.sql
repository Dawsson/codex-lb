-- name: ListAccounts :many
SELECT id, email, alias, plan_type, status, routing_policy,
       security_work_authorized, workspace_id, workspace_label, seat_type,
       limit_warmup_enabled
  FROM accounts
 ORDER BY email, id;

-- name: LatestUsageByWindow :many
SELECT account_id,
       coalesce(window, 'primary') AS window_name,
       used_percent,
       reset_at,
       window_minutes,
       credits_has,
       credits_balance,
       recorded_at
  FROM (
        SELECT usage_history.*,
               row_number() OVER (
                 PARTITION BY account_id, coalesce(window, 'primary')
                 ORDER BY recorded_at DESC, id DESC
               ) AS rn
          FROM usage_history
         WHERE coalesce(window, 'primary') = sqlc.arg(window_name)
       )
 WHERE rn = 1;
