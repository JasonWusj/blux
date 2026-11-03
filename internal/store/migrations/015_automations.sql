CREATE TABLE IF NOT EXISTS automations (
 id TEXT PRIMARY KEY,name TEXT NOT NULL,prompt TEXT NOT NULL,repository_path TEXT NOT NULL,adapter TEXT NOT NULL,
 schedule_time TEXT NOT NULL DEFAULT '',network TEXT NOT NULL DEFAULT 'restricted',
 timeout_seconds INTEGER NOT NULL,daily_limit INTEGER NOT NULL,pause_after_failures INTEGER NOT NULL,
 anchor_at TEXT NOT NULL,paused_at TEXT NOT NULL DEFAULT '',pause_reason TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS automation_runs (
 id TEXT PRIMARY KEY,automation_id TEXT NOT NULL REFERENCES automations(id),
 trigger_kind TEXT NOT NULL,dedupe_key TEXT NOT NULL UNIQUE,planned_at TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL,task_id TEXT NOT NULL DEFAULT '',turn_id TEXT NOT NULL DEFAULT '',
 rules_json TEXT NOT NULL,code TEXT NOT NULL DEFAULT '',message TEXT NOT NULL DEFAULT '',summary TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,finished_at TEXT NOT NULL DEFAULT '',notified_at TEXT NOT NULL DEFAULT '',
 seen_at TEXT NOT NULL DEFAULT '',dismissed_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS automation_run_task ON automation_runs(task_id) WHERE task_id<>'';
CREATE UNIQUE INDEX IF NOT EXISTS automation_run_turn ON automation_runs(turn_id) WHERE turn_id<>'';
CREATE INDEX IF NOT EXISTS automation_run_history ON automation_runs(automation_id,created_at);
CREATE INDEX IF NOT EXISTS automation_run_status ON automation_runs(status);
CREATE TABLE IF NOT EXISTS automation_receipts (
 scope TEXT NOT NULL,key TEXT NOT NULL,digest TEXT NOT NULL,entity_id TEXT NOT NULL,PRIMARY KEY(scope,key)
);
