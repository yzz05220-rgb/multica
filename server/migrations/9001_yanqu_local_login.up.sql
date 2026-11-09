-- 颜趣本地补丁：用户名 + 密码登录（测试阶段替代邮箱验证码）。
-- 独立表，不改上游 user 表结构；删除本表即恢复上游行为。
CREATE TABLE IF NOT EXISTS yanqu_local_login (
    username TEXT PRIMARY KEY CHECK (username ~ '^[a-z0-9][a-z0-9_.-]{1,31}$'),
    user_id UUID NOT NULL UNIQUE REFERENCES "user"(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    disabled BOOLEAN NOT NULL DEFAULT false,
    failed_attempts INT NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
