-- 非事务执行：CREATE INDEX CONCURRENTLY 不能包在事务里。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_request_id_trgm
    ON usage_logs USING gin (request_id gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_request_id_trgm
    ON usage_logs USING gin (upstream_request_id gin_trgm_ops);
