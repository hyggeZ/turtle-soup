-- 保留已经提交的后台操作，即使对应账号或题目日后移除也能追溯其 ID。
CREATE TABLE admin_audit_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id BIGINT NOT NULL CHECK (actor_id > 0),
    action TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id BIGINT NOT NULL CHECK (subject_id > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (subject_type = 'user' AND action IN ('user_create', 'user_password_change', 'user_disable', 'user_enable'))
        OR (subject_type = 'soup' AND action IN ('soup_hide', 'soup_restore'))
    )
);
