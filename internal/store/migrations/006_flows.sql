CREATE TABLE deliveries(id TEXT PRIMARY KEY,state_json TEXT NOT NULL,revision INTEGER NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE flow_templates(id TEXT NOT NULL,version INTEGER NOT NULL,state_json TEXT NOT NULL,PRIMARY KEY(id,version));
CREATE TABLE flow_actions(scope TEXT NOT NULL,key TEXT NOT NULL,digest TEXT NOT NULL,result_json TEXT NOT NULL,PRIMARY KEY(scope,key));
CREATE TABLE workflow_tasks(task_id TEXT PRIMARY KEY,delivery_id TEXT NOT NULL,generation INTEGER NOT NULL);
CREATE TABLE task_inputs(task_id TEXT NOT NULL,artifact_id TEXT NOT NULL REFERENCES artifact_versions(id),PRIMARY KEY(task_id,artifact_id));
