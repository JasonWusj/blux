-- Context versions are immutable execution inputs, not replayable projections.
CREATE TABLE turn_context (
  turn_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id),
  snapshot_json TEXT NOT NULL
);
