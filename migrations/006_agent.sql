-- 单人 AI 对局保存题目快照，避免作者改题后改变进行中的谜底。
CREATE TABLE IF NOT EXISTS ai_games (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    soup_id BIGINT NOT NULL REFERENCES soups(id),
    title TEXT NOT NULL,
    surface TEXT NOT NULL,
    answer TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','finished')),
    busy_op TEXT,
    busy_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ai_games_user_idx ON ai_games(user_id,id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ai_games_active_soup_idx ON ai_games(user_id,soup_id) WHERE status='active';

-- 操作 ID 保证重试不会重复调用模型；这里仅保存用户自己的单人对局内容。
CREATE TABLE IF NOT EXISTS ai_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id BIGINT NOT NULL REFERENCES ai_games(id) ON DELETE CASCADE,
    operation_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('question','restore','reveal')),
    body TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT 'v1',
    latency_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (game_id,operation_id)
);
ALTER TABLE ai_events ADD COLUMN IF NOT EXISTS prompt_version TEXT NOT NULL DEFAULT 'v1';
CREATE INDEX IF NOT EXISTS ai_events_game_idx ON ai_events(game_id,id);

-- 仅记录模型调用指标和错误类型；不保存汤底、玩家输入或模型原文。
CREATE TABLE IF NOT EXISTS ai_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id BIGINT NOT NULL REFERENCES ai_games(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('question','restore')),
    model TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    latency_ms DOUBLE PRECISION NOT NULL,
    success BOOLEAN NOT NULL,
    error_kind TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ai_attempts_created_idx ON ai_attempts(created_at DESC);

-- 按数据库日期原子计数，限制单个账号每天的模型调用量。
CREATE TABLE IF NOT EXISTS ai_daily_usage (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    usage_day DATE NOT NULL,
    calls SMALLINT NOT NULL CHECK (calls BETWEEN 1 AND 120),
    PRIMARY KEY (user_id,usage_day)
);
