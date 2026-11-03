CREATE TABLE IF NOT EXISTS deleted_tasks(task_id TEXT PRIMARY KEY REFERENCES tasks(id),deleted_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS deleted_collaborations(collaboration_id TEXT PRIMARY KEY REFERENCES collaborations(id),deleted_at TEXT NOT NULL);
