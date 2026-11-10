-- 公开资料与登录凭据分开维护，旧账号默认为空展示名和简介。
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD CONSTRAINT users_display_name_check
    CHECK (char_length(display_name) <= 40 AND display_name = btrim(display_name));
ALTER TABLE users ADD CONSTRAINT users_bio_check
    CHECK (char_length(bio) <= 300 AND bio = btrim(bio));
