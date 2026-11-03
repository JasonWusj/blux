CREATE TABLE manager_tasks(task_id TEXT PRIMARY KEY,project_path TEXT NOT NULL);
CREATE TABLE review_requests(id TEXT PRIMARY KEY,delivery_id TEXT NOT NULL,generation INTEGER NOT NULL,phase_id TEXT NOT NULL,message TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(delivery_id,generation,phase_id));
