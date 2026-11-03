CREATE TABLE controls(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,turn_id TEXT NOT NULL,generation INTEGER NOT NULL,kind TEXT NOT NULL,prompt TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,UNIQUE(turn_id,id));
CREATE INDEX controls_pending ON controls(turn_id,status,created_at);
