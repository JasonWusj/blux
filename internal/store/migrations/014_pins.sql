CREATE TABLE IF NOT EXISTS pins(kind TEXT NOT NULL CHECK(kind IN('task','collaboration')),entity_id TEXT NOT NULL,pinned_at TEXT NOT NULL,PRIMARY KEY(kind,entity_id));
