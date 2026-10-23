-- 定时测试默认保持官方行为（普通连通性测试）；打开 quality_check_enabled
-- 的计划才走降智检测（生成 + 视觉评估 + 连续退化暂停/恢复）。
-- kedaya 249 同语义，本地编号避开 245/249/255 碰撞。
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS quality_check_enabled BOOLEAN NOT NULL DEFAULT false;

-- 已存在的计划若保存的是降智检测题面（261 默认或当前 keyframes 题面），视为已经在做降智检测。
UPDATE scheduled_test_plans
SET quality_check_enabled = true
WHERE md5(prompt_text) IN (
    '15caede62ffea7c47ee2ccf2c8674e87',
    '297caf9418b914f791fe7df9ef44f58e'
);
