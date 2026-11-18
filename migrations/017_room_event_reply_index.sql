-- 主持待办由任意子事件结束；保留专用回答索引，供提交回答和题目状态检查使用。
CREATE INDEX IF NOT EXISTS room_events_reply_parent_idx
ON room_events(parent_id)
WHERE parent_id IS NOT NULL;
