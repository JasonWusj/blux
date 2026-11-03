CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  prompt TEXT NOT NULL,
  status TEXT NOT NULL,
  adapter TEXT NOT NULL,
  session_id TEXT NOT NULL UNIQUE,
  current_turn_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_event_seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL UNIQUE REFERENCES tasks(id),
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS turns (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  session_id TEXT NOT NULL REFERENCES sessions(id),
  prompt TEXT NOT NULL,
  status TEXT NOT NULL,
  generation INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  ended_at TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_seq INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_turn ON turns(session_id)
  WHERE status IN ('queued', 'running');
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  turn_id TEXT NOT NULL REFERENCES turns(id),
  role TEXT NOT NULL,
  status TEXT NOT NULL,
  segments TEXT NOT NULL,
  created_seq INTEGER NOT NULL,
  position INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  schema_version INTEGER NOT NULL,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  session_id TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  generation INTEGER NOT NULL,
  seq INTEGER NOT NULL,
  type TEXT NOT NULL,
  payload TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(task_id, seq)
);
CREATE INDEX IF NOT EXISTS events_task_seq ON events(task_id, seq);
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  session_id TEXT NOT NULL,
  turn_id TEXT NOT NULL UNIQUE,
  generation INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  status TEXT NOT NULL,
  worker_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  ended_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS jobs_queue ON jobs(status, created_at, id);
CREATE TABLE IF NOT EXISTS idempotency_keys (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  digest TEXT NOT NULL,
  task_id TEXT NOT NULL,
  turn_id TEXT NOT NULL,
  PRIMARY KEY(scope, key)
);
CREATE TABLE IF NOT EXISTS workers (
  id TEXT PRIMARY KEY,
  last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_updated ON tasks(updated_at DESC, id);
