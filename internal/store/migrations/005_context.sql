CREATE TABLE projects(id TEXT PRIMARY KEY,path TEXT NOT NULL UNIQUE,name TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE context_entries(id TEXT PRIMARY KEY,project_path TEXT NOT NULL,kind TEXT NOT NULL,title TEXT NOT NULL,content TEXT NOT NULL,previous_id TEXT NOT NULL DEFAULT '',current INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL);
CREATE INDEX context_project ON context_entries(project_path,current);
CREATE VIRTUAL TABLE context_fts USING fts5(entry_id UNINDEXED,title,content,tokenize='unicode61');
CREATE TABLE task_context(task_id TEXT PRIMARY KEY,mode TEXT NOT NULL,snapshot_json TEXT NOT NULL);
