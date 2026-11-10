-- 全站按数据库日期计数，防止批量注册绕过单账号的模型调用额度。
CREATE TABLE IF NOT EXISTS ai_global_usage (
    usage_day DATE PRIMARY KEY,
    calls INTEGER NOT NULL CHECK (calls >= 1)
);

-- 升级时承接旧版各账号已使用的额度，避免当天重新放行全站额度。
INSERT INTO ai_global_usage(usage_day,calls)
SELECT usage_day,SUM(calls)::INTEGER
FROM ai_daily_usage
GROUP BY usage_day
ON CONFLICT (usage_day) DO UPDATE
SET calls=GREATEST(ai_global_usage.calls,EXCLUDED.calls);
