-- 主持回答需要按原问题判断是否已经答复；其他事件不进入此索引。
CREATE INDEX IF NOT EXISTS room_events_answer_parent_idx
ON room_events(parent_id)
WHERE kind='answer' AND parent_id IS NOT NULL;
