-- 关闭未开始的房间时保留记录，并为自建房间上限计数建立索引。
ALTER TABLE rooms DROP CONSTRAINT IF EXISTS rooms_status_check;
ALTER TABLE rooms ADD CONSTRAINT rooms_status_check
    CHECK (status IN ('waiting','active','paused','finished','closed'));
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS rooms_owner_open_idx ON rooms(owner_id)
    WHERE status IN ('waiting','active','paused');
