ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE IF NOT EXISTS rooms (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, code TEXT NOT NULL UNIQUE,
 owner_id BIGINT NOT NULL REFERENCES users(id), host_id BIGINT REFERENCES users(id),
 soup_id BIGINT NOT NULL REFERENCES soups(id), mode TEXT NOT NULL CHECK (mode IN ('casual','competitive')),
 max_players SMALLINT NOT NULL CHECK (max_players BETWEEN 2 AND 20), public BOOLEAN NOT NULL DEFAULT FALSE,
 status TEXT NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting','active','paused','finished')),
 turn_user_id BIGINT REFERENCES users(id), turn_deadline TIMESTAMPTZ,
 winner_id BIGINT REFERENCES users(id), created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rooms_lobby_idx ON rooms(public,status,created_at DESC);
CREATE TABLE IF NOT EXISTS room_members (
 room_id BIGINT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, user_id BIGINT NOT NULL REFERENCES users(id),
 role TEXT NOT NULL CHECK (role IN ('player','host','spectator')),
 seen_answer BOOLEAN NOT NULL DEFAULT FALSE, correct BOOLEAN NOT NULL DEFAULT FALSE,
 host_request BOOLEAN NOT NULL DEFAULT FALSE, joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (room_id,user_id)
);
CREATE INDEX IF NOT EXISTS room_members_user_idx ON room_members(user_id);
CREATE TABLE IF NOT EXISTS room_events (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, room_id BIGINT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id), kind TEXT NOT NULL CHECK (kind IN ('question','answer','restore','restore_result','answer_request','answer_result')),
 body TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '', parent_id BIGINT REFERENCES room_events(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS room_events_room_idx ON room_events(room_id,id);
CREATE TABLE IF NOT EXISTS game_operations (
 room_id BIGINT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, user_id BIGINT NOT NULL REFERENCES users(id),
 operation_id TEXT NOT NULL, event_id BIGINT REFERENCES room_events(id), PRIMARY KEY (room_id,user_id,operation_id)
);
