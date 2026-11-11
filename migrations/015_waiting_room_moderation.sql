-- 后台隐藏题目只处理该题仍在等待的房间，按房间 ID 锁定以维持固定锁顺序。
CREATE INDEX IF NOT EXISTS rooms_soup_waiting_idx
    ON rooms(soup_id,id) WHERE status='waiting';
