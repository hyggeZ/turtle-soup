-- 已发布题目从版本 1 开始；成功编辑正文后版本递增。
ALTER TABLE soups ADD COLUMN version BIGINT NOT NULL DEFAULT 1
    CONSTRAINT soups_version_positive CHECK (version > 0);
