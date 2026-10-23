-- 只索引带上游请求标识的行；CONCURRENTLY 避免阻塞热表写入。
-- 编号从 kedaya 233 改为 260，避开本地 233_add_group_monitor_result_history.sql。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_request_id
    ON usage_logs (upstream_request_id)
    WHERE upstream_request_id IS NOT NULL;
