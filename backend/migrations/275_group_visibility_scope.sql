-- 分组可见范围三档：public / hidden / restricted
--
-- 背景：groups.user_visible 是全局布尔，只能表达「所有人可见」或「所有人不可见」。
-- 需要「只让指定用户可见」时，现有 user_allowed_groups 机制只在
-- is_exclusive=true 的分组上生效（见 service/user.go CanBindGroup），公开分组的
-- 授权记录实际上被忽略——线上分组 17 已授权 34 人，却仍对全部 850 个用户可见。
--
-- 本迁移只新增一列表达「可见范围」，不改变既有行为：
--   public     = 所有非受限用户可见（等价于 current: user_visible=true）
--   hidden     = 对普通用户不可见（等价于 current: user_visible=false）
--   restricted = 仅 user_allowed_groups 中的用户可见（新语义）
--
-- visibility_scope 由现有 user_visible 回填，保证升级瞬间零行为变化；
-- 之后的判定以 visibility_scope 为准，user_visible 保留为派生镜像（兼容旧代码/前端）。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS visibility_scope VARCHAR(16);

UPDATE groups
SET visibility_scope = CASE WHEN user_visible THEN 'public' ELSE 'hidden' END
WHERE visibility_scope IS NULL;

ALTER TABLE groups
    ALTER COLUMN visibility_scope SET DEFAULT 'public';

UPDATE groups SET visibility_scope = 'public' WHERE visibility_scope IS NULL;

ALTER TABLE groups
    ALTER COLUMN visibility_scope SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'groups'::regclass
          AND conname = 'groups_visibility_scope_check'
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_visibility_scope_check
            CHECK (visibility_scope IN ('public', 'hidden', 'restricted'));
    END IF;
END $$;

COMMENT ON COLUMN groups.visibility_scope IS
    'Group visibility: public (all users) / hidden (admins only) / restricted (user_allowed_groups only)';

CREATE INDEX IF NOT EXISTS idx_groups_visibility_scope
    ON groups (visibility_scope);
