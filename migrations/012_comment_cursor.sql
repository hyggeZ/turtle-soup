-- 评论按题目和递减 ID 游标翻页，避免每页扫描并排序该题全部评论。
CREATE INDEX IF NOT EXISTS comments_soup_id_desc_idx ON comments(soup_id,id DESC);
