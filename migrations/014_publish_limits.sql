-- 发布限额按作者和时间读取最近记录，避免为老账号扫描全部历史内容。
CREATE INDEX IF NOT EXISTS soups_author_recent_idx
    ON soups(author_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS comments_author_recent_idx
    ON comments(author_id,created_at DESC,id DESC);
