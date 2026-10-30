-- Timestamps are stored as RFC 3339 UTC text.

CREATE TABLE users (
    id         INTEGER PRIMARY KEY,
    email      TEXT    NOT NULL UNIQUE,
    handle     TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    name       TEXT    NOT NULL,
    bio        TEXT    NOT NULL DEFAULT '',
    website    TEXT    NOT NULL DEFAULT '',
    github     TEXT    NOT NULL DEFAULT '',
    social     TEXT    NOT NULL DEFAULT '',
    banned_at  TEXT,
    created_at TEXT    NOT NULL
);

CREATE TABLE otp_codes (
    id         INTEGER PRIMARY KEY,
    email      TEXT    NOT NULL,
    code_hash  TEXT    NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT    NOT NULL,
    used_at    TEXT,
    created_at TEXT    NOT NULL
);
CREATE INDEX otp_codes_email ON otp_codes (email, created_at);

CREATE TABLE sessions (
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at   TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    last_seen_at TEXT    NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE projects (
    id             INTEGER PRIMARY KEY,
    slug           TEXT    NOT NULL UNIQUE,
    owner_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    tagline        TEXT    NOT NULL,
    description    TEXT    NOT NULL,
    url            TEXT    NOT NULL,
    github_url     TEXT    NOT NULL DEFAULT '',
    video_url      TEXT    NOT NULL DEFAULT '',
    launch_date    TEXT    NOT NULL DEFAULT '', -- YYYY-MM-DD or empty
    status         TEXT    NOT NULL,
    category       TEXT    NOT NULL,
    content_lang   TEXT    NOT NULL,
    product_langs  TEXT    NOT NULL DEFAULT '', -- comma-separated codes
    looking_for    TEXT    NOT NULL DEFAULT '', -- comma-separated codes
    pricing        TEXT    NOT NULL DEFAULT '',
    is_open_source INTEGER NOT NULL DEFAULT 0,
    city           TEXT    NOT NULL DEFAULT '',
    hidden_at      TEXT,
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);
CREATE INDEX projects_owner ON projects (owner_id);
CREATE INDEX projects_created ON projects (created_at);

CREATE TABLE tags (
    id   INTEGER PRIMARY KEY,
    kind TEXT NOT NULL, -- 'tech' or 'ai'
    name TEXT NOT NULL COLLATE NOCASE,
    UNIQUE (kind, name)
);

CREATE TABLE project_tags (
    project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    tag_id     INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, tag_id)
);
CREATE INDEX project_tags_tag ON project_tags (tag_id);

CREATE TABLE likes (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    created_at TEXT    NOT NULL,
    PRIMARY KEY (user_id, project_id)
);
CREATE INDEX likes_project ON likes (project_id);

CREATE TABLE comments (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       TEXT    NOT NULL,
    hidden_at  TEXT,
    created_at TEXT    NOT NULL
);
CREATE INDEX comments_project ON comments (project_id, created_at);

-- Full-text index over projects; rowid = projects.id. Maintained by the store.
CREATE VIRTUAL TABLE projects_fts USING fts5 (
    name, tagline, description, tags,
    tokenize = 'unicode61 remove_diacritics 2'
);
