CREATE TABLE collaborations (
 id TEXT PRIMARY KEY,title TEXT NOT NULL,goal TEXT NOT NULL,repository_path TEXT NOT NULL,
 context_ids_json TEXT NOT NULL DEFAULT '[]',created_at TEXT NOT NULL
);
CREATE TABLE collaboration_tasks (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id),collaboration_id TEXT NOT NULL REFERENCES collaborations(id),created_at TEXT NOT NULL
);
CREATE INDEX collaboration_task_group ON collaboration_tasks(collaboration_id,created_at);
CREATE TABLE collaboration_receipts (
 scope TEXT NOT NULL,key TEXT NOT NULL,digest TEXT NOT NULL,entity_id TEXT NOT NULL,PRIMARY KEY(scope,key)
);
