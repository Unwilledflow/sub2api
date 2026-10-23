-- Add TypeSafe (Jev System One) as a first-class platform.
--
-- 1. user_platform_quotas.platform CHECK
-- 2. composite_model_routes.target_platform CHECK
--
-- TypeSafe 不是对话模型，不进入渠道监控 provider，因此 channel_monitors /
-- channel_monitor_request_templates 的约束保持不变。
--
-- 该迁移来自上游。上游没有本仓库定制的 cn / video 两个平台，直接照搬会把
-- 存量行挡在约束外（生产实测：user_platform_quotas 有 cn 635 行、video 628 行，
-- ADD CONSTRAINT 会因存量数据违反约束而失败）。因此这里是上游定义的**超集**：
-- 在上游列表基础上补回 cn、video。
--
-- DROP ... IF EXISTS 保证可重入；新约束是旧约束的超集，存量行瞬时校验通过。

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'cn', 'video',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'cline', 'command_code', 'typesafe'));

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                               'cn', 'video',
                               'kimi', 'zhipu', 'deepseek', 'minimax', 'cline', 'command_code', 'typesafe'));
