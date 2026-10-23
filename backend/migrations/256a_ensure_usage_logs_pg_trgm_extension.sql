-- 请求 ID 搜索是前置通配 ILIKE，需要 pg_trgm。权限不足时不阻塞启动。
DO $$
BEGIN
    BEGIN
        CREATE EXTENSION IF NOT EXISTS pg_trgm;
    EXCEPTION
        WHEN OTHERS THEN
            RAISE NOTICE 'pg_trgm extension not created: %', SQLERRM;
    END;
END
$$;
