-- 作者删除与后台隐藏独立记录；两者都不改写已创建对局的题目快照。
ALTER TABLE soups ADD COLUMN hidden_at TIMESTAMPTZ;
ALTER TABLE soups ADD COLUMN hidden_by BIGINT REFERENCES users(id);
ALTER TABLE soups ADD CONSTRAINT soups_hidden_pair_check
    CHECK ((hidden_at IS NULL) = (hidden_by IS NULL));
