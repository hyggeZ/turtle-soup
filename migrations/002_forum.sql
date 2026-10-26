CREATE TABLE IF NOT EXISTS soups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    surface TEXT NOT NULL,
    answer TEXT NOT NULL,
    genre TEXT NOT NULL CHECK (genre IN ('本格', '变格')),
    difficulty SMALLINT NOT NULL CHECK (difficulty BETWEEN 1 AND 5),
    horror SMALLINT NOT NULL CHECK (horror BETWEEN 0 AND 5),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS soups_created_idx ON soups(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS soups_author_idx ON soups(author_id);

CREATE TABLE IF NOT EXISTS follows (
    follower_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, followed_id),
    CHECK (follower_id <> followed_id)
);
CREATE INDEX IF NOT EXISTS follows_followed_idx ON follows(followed_id);

CREATE TABLE IF NOT EXISTS soup_likes (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    soup_id BIGINT NOT NULL REFERENCES soups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, soup_id)
);
CREATE INDEX IF NOT EXISTS soup_likes_soup_idx ON soup_likes(soup_id);

CREATE TABLE IF NOT EXISTS soup_bookmarks (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    soup_id BIGINT NOT NULL REFERENCES soups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, soup_id)
);
CREATE INDEX IF NOT EXISTS soup_bookmarks_soup_idx ON soup_bookmarks(soup_id);

CREATE TABLE IF NOT EXISTS comments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    soup_id BIGINT NOT NULL REFERENCES soups(id) ON DELETE CASCADE,
    author_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS comments_soup_idx ON comments(soup_id, created_at, id);
