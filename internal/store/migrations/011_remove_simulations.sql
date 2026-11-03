-- Retire the former product simulator, retaining unrelated CLI task history.
CREATE TEMP TABLE obsolete_tasks AS SELECT id FROM tasks WHERE adapter='demo';
CREATE TEMP TABLE obsolete_deliveries AS SELECT id FROM deliveries WHERE json_extract(state_json,'$.adapter')='demo';
CREATE TEMP TABLE obsolete_collaborations AS
 SELECT id FROM collaborations c WHERE EXISTS(SELECT 1 FROM collaboration_tasks ct JOIN obsolete_tasks t ON ct.task_id=t.id WHERE ct.collaboration_id=c.id)
 AND NOT EXISTS(SELECT 1 FROM collaboration_tasks ct WHERE ct.collaboration_id=c.id AND ct.task_id NOT IN(SELECT id FROM obsolete_tasks));
CREATE TABLE pending_cleanup(kind TEXT NOT NULL,entry_id TEXT NOT NULL,PRIMARY KEY(kind,entry_id));
INSERT INTO pending_cleanup SELECT 'workspaces',id FROM obsolete_tasks;
INSERT INTO pending_cleanup SELECT 'deliveries',id FROM obsolete_deliveries;
INSERT OR IGNORE INTO pending_cleanup SELECT 'integrations',snapshot_id FROM acceptances WHERE task_id IN(SELECT id FROM obsolete_tasks);
INSERT OR IGNORE INTO pending_cleanup SELECT 'blobs',hash FROM artifact_versions WHERE task_id IN(SELECT id FROM obsolete_tasks)
 AND hash NOT IN(SELECT hash FROM artifact_versions WHERE task_id NOT IN(SELECT id FROM obsolete_tasks));
DELETE FROM task_inputs WHERE task_id IN(SELECT id FROM obsolete_tasks) OR artifact_id IN(SELECT id FROM artifact_versions WHERE task_id IN(SELECT id FROM obsolete_tasks));
DELETE FROM artifact_versions WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM snapshots WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM acceptances WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM turn_context WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM task_context WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM manager_tasks WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM workflow_tasks WHERE task_id IN(SELECT id FROM obsolete_tasks) OR delivery_id IN(SELECT id FROM obsolete_deliveries);
DELETE FROM controls WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM idempotency_keys WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM collaboration_tasks WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM collaboration_receipts WHERE entity_id IN(SELECT id FROM obsolete_collaborations) OR entity_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM collaborations WHERE id IN(SELECT id FROM obsolete_collaborations);
DELETE FROM review_requests WHERE delivery_id IN(SELECT id FROM obsolete_deliveries);
DELETE FROM flow_actions WHERE json_extract(result_json,'$.id') IN(SELECT id FROM obsolete_deliveries);
DELETE FROM deliveries WHERE id IN(SELECT id FROM obsolete_deliveries);
DELETE FROM events WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM messages WHERE session_id IN(SELECT session_id FROM tasks WHERE id IN(SELECT id FROM obsolete_tasks));
DELETE FROM jobs WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM turns WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM sessions WHERE task_id IN(SELECT id FROM obsolete_tasks);
DELETE FROM tasks WHERE id IN(SELECT id FROM obsolete_tasks);
DROP TABLE obsolete_tasks;
DROP TABLE obsolete_deliveries;
DROP TABLE obsolete_collaborations;
