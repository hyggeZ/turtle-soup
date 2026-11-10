-- 记录用户主动查看过的题目汤底，避免已知谜底的用户进入玩家席位。
CREATE TABLE IF NOT EXISTS soup_reveals (
    soup_id BIGINT NOT NULL REFERENCES soups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revealed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (soup_id, user_id)
);
CREATE INDEX IF NOT EXISTS soup_reveals_user_idx ON soup_reveals(user_id);

-- 房间创建时固定题目内容，日后允许作者编辑作品时也不会改变已开的对局。
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS soup_title TEXT;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS soup_surface TEXT;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS soup_answer TEXT;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS soup_genre TEXT;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS soup_difficulty SMALLINT;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS soup_horror SMALLINT;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS paused_at TIMESTAMPTZ;
ALTER TABLE rooms ADD COLUMN IF NOT EXISTS answer_published BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE game_operations ADD COLUMN IF NOT EXISTS request_hash BYTEA;
ALTER TABLE game_operations ADD COLUMN IF NOT EXISTS message TEXT;
UPDATE rooms r SET soup_title=s.title, soup_surface=s.surface, soup_answer=s.answer,
    soup_genre=s.genre, soup_difficulty=s.difficulty, soup_horror=s.horror
FROM soups s WHERE r.soup_id=s.id AND r.soup_title IS NULL;
