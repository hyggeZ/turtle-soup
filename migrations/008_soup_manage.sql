-- 题目保留原记录及关联对局，删除后从社区和新对局入口隐藏。
ALTER TABLE soups ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE soups ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS soups_visible_created_idx ON soups(created_at DESC,id DESC) WHERE deleted_at IS NULL;
